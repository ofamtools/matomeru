package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

type cli struct {
	t  *testing.T
	db string
}

// ok runs mtm with --db and returns its stdout; the command must succeed.
func (c cli) ok(args ...string) string {
	c.t.Helper()
	var out, errs bytes.Buffer
	if code := run(append([]string{"--db", c.db}, args...), &out, &errs); code != 0 {
		c.t.Fatalf("mtm %s: exit %d: %s", strings.Join(args, " "), code, errs.String())
	}
	return out.String()
}

func (c cli) fails(args ...string) string {
	c.t.Helper()
	var out, errs bytes.Buffer
	if code := run(append([]string{"--db", c.db}, args...), &out, &errs); code == 0 {
		c.t.Fatalf("mtm %s succeeded: %s", strings.Join(args, " "), out.String())
	}
	return errs.String()
}

func decode[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	return v
}

type record struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id"`
	Fields     map[string]any `json:"fields"`
	StarStatus string         `json:"starStatus"`
}

func TestRecordCommands(t *testing.T) {
	c := cli{t, filepath.Join(t.TempDir(), "sub", "mtm.db")}
	made := decode[record](t, c.ok("record", "create", "task", "--title", "Write the CLI", "--field", "priority=P1", "--field=workStatus=active"))
	if made.Kind != "task" || made.ID == "" || made.Fields["title"] != "Write the CLI" || made.Fields["priority"] != "P1" {
		t.Fatalf("created %+v", made)
	}
	child := decode[record](t, c.ok("record", "create", "task", "--title", "Parse flags", "--field", "parent="+made.ID))
	if parent, _ := child.Fields["parent"].(map[string]any); parent["kind"] != "task" || parent["id"] != made.ID {
		t.Fatalf("parent = %v", child.Fields["parent"])
	}
	run := decode[record](t, c.ok("record", "create", "run", "--title", "gate", "--field", "exitCode=2"))
	if run.Fields["exitCode"] != float64(2) {
		t.Fatalf("exitCode = %#v, want a number", run.Fields["exitCode"])
	}
	// An extension's title is its name.
	ext := decode[record](t, c.ok("record", "create", "extension", "--title", "o-git"))
	if ext.Fields["name"] != "o-git" {
		t.Fatalf("extension fields = %v", ext.Fields)
	}

	if got := decode[record](t, c.ok("record", "get", "task", made.ID)); got.ID != made.ID {
		t.Fatalf("get = %+v", got)
	}
	if all := decode[[]record](t, c.ok("record", "list", "task")); len(all) != 2 {
		t.Fatalf("list = %v", all)
	}
	for mode, query := range map[string]string{"substring": "parse", "fuzzy": "prsflg", "regex": "^parse f"} {
		got := decode[[]record](t, c.ok("record", "list", "task", "--query", query, "--mode", mode))
		if len(got) != 1 || got[0].ID != child.ID {
			t.Errorf("--mode %s --query %s = %v", mode, query, got)
		}
	}
	if got := decode[[]record](t, c.ok("record", "list", "task", "--starred")); len(got) != 0 {
		t.Fatalf("--starred = %v", got)
	}
	table := c.ok("record", "list", "task", "--format", "table")
	if !strings.Contains(table, "ID") || !strings.Contains(table, "TITLE") || !strings.Contains(table, "Write the CLI") || !strings.Contains(table, "P1") {
		t.Fatalf("table:\n%s", table)
	}

	// Undo takes back the CLI's last change: the extension, then the run.
	c.ok("record", "undo")
	c.fails("record", "get", "extension", ext.ID)
	c.ok("record", "undo")
	c.fails("record", "get", "run", run.ID)
	if all := decode[[]record](t, c.ok("record", "list", "task")); len(all) != 2 {
		t.Fatalf("after two undos the tasks are %v", all)
	}

	for _, bad := range [][]string{
		{"record", "list"}, {"record", "get", "task"}, {"record", "list", "task", "--mode", "exact"},
		{"record", "list", "task", "--mode", "regex", "--query", "["}, {"record", "create", "run", "--field", "exitCode=two"},
		{"record", "frobnicate"}, {"record", "list", "task", "--format", "xml"}, {"--nope"},
	} {
		c.fails(bad...)
	}
}

func TestMCPCommands(t *testing.T) {
	c := cli{t, filepath.Join(t.TempDir(), "mtm.db")}
	tools := decode[[]struct {
		Name        string         `json:"name"`
		InputSchema map[string]any `json:"inputSchema"`
	}](t, c.ok("mcp", "tools"))
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s has no input schema", tool.Name)
		}
	}
	if got := strings.Join(names, " "); got != "ui.create_page ui.update_page ui.ask ui.list_pages" {
		t.Fatalf("tools = %s", got)
	}

	page := decode[struct{ ID, Slug string }](t, c.ok("mcp", "call", "ui.create_page",
		`{"pane": "w1", "title": "Plan", "root": {"kind": "markdown", "props": {"markdown": "# Plan"}}}`))
	if page.ID == "" || page.Slug != "plan" {
		t.Fatalf("created page %+v", page)
	}
	c.ok("mcp", "call", "ui.ask", `{"pane": "w1", "title": "Ship?", "choices": [{"id": "yes"}, {"id": "no"}]}`)
	list := decode[struct {
		Pages []struct {
			Slug      string `json:"slug"`
			Unread    bool   `json:"unread"`
			Questions []struct {
				Title string `json:"title"`
			} `json:"questions"`
		} `json:"pages"`
	}](t, c.ok("mcp", "call", "ui.list_pages", `{"pane": "w1"}`))
	if len(list.Pages) != 2 || list.Pages[1].Slug != "questions" || !list.Pages[1].Unread || list.Pages[1].Questions[0].Title != "Ship?" {
		t.Fatalf("pages = %+v", list.Pages)
	}
	// The pages are records like any other.
	if recs := decode[[]record](t, c.ok("record", "list", "ui.page")); len(recs) != 2 {
		t.Fatalf("ui.page records = %v", recs)
	}
	c.fails("mcp", "call", "ui.nope", "{}")
	c.fails("mcp", "call", "ui.create_page", `{"title": "x", "bogus": 1}`)
	c.fails("mcp", "call")
}

func TestSeedDemoFlag(t *testing.T) {
	c := cli{t, filepath.Join(t.TempDir(), "mtm.db")}
	first := decode[[]record](t, c.ok("--seed-demo", "record", "list", "extension"))
	again := decode[[]record](t, c.ok("--seed-demo", "record", "list", "extension"))
	if len(first) != 3 || len(again) != 3 {
		t.Fatalf("extensions after seeding: %d, then %d", len(first), len(again))
	}
}

func TestSmokeAndHelp(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"--smoke"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "smoke: all checks passed") {
		t.Fatalf("--smoke: exit %d\n%s\n%s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "render") || !strings.Contains(out.String(), "cache") {
		t.Fatalf("--smoke printed no frame stats:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"--help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "mtm record list KIND") {
		t.Fatalf("--help: exit %d\n%s", code, out.String())
	}
}
