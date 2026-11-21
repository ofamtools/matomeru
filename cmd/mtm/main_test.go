package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cli struct {
	t     *testing.T
	db    string
	stdin string
}

// ok runs mtm with --db and returns its stdout; the command must succeed.
func (c cli) ok(args ...string) string {
	c.t.Helper()
	var out, errs bytes.Buffer
	if code := run(append([]string{"--db", c.db}, args...), strings.NewReader(c.stdin), &out, &errs); code != 0 {
		c.t.Fatalf("mtm %s: exit %d: %s", strings.Join(args, " "), code, errs.String())
	}
	return out.String()
}

func (c cli) fails(args ...string) string {
	c.t.Helper()
	var out, errs bytes.Buffer
	if code := run(append([]string{"--db", c.db}, args...), strings.NewReader(c.stdin), &out, &errs); code == 0 {
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
	c := cli{t: t, db: filepath.Join(t.TempDir(), "sub", "mtm.db")}
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
	c := cli{t: t, db: filepath.Join(t.TempDir(), "mtm.db")}
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
	c := cli{t: t, db: filepath.Join(t.TempDir(), "mtm.db")}
	first := decode[[]record](t, c.ok("--seed-demo", "record", "list", "extension"))
	again := decode[[]record](t, c.ok("--seed-demo", "record", "list", "extension"))
	if len(first) != 3 || len(again) != 3 {
		t.Fatalf("extensions after seeding: %d, then %d", len(first), len(again))
	}
}

func TestSmokeAndHelp(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"--smoke"}, strings.NewReader(""), &out, &errs); code != 0 || !strings.Contains(out.String(), "smoke: all checks passed") {
		t.Fatalf("--smoke: exit %d\n%s\n%s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "render") || !strings.Contains(out.String(), "cache") {
		t.Fatalf("--smoke printed no frame stats:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"--help"}, strings.NewReader(""), &out, &errs); code != 0 || !strings.Contains(out.String(), "mtm record list KIND") {
		t.Fatalf("--help: exit %d\n%s", code, out.String())
	}
}

// "mtm mcp serve" is an MCP server over the same store as the rest of the CLI.
func TestMCPServe(t *testing.T) {
	c := cli{t: t, db: filepath.Join(t.TempDir(), "mtm.db")}
	c.stdin = strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ui.create_page","arguments":{"pane":"w1","title":"Served","root":{"kind":"markdown","props":{"markdown":"# hi"}}}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
	}, "\n") + "\n"
	type reply struct {
		ID     int `json:"id"`
		Result struct {
			ServerInfo struct{ Name string }   `json:"serverInfo"`
			Tools      []struct{ Name string } `json:"tools"`
			IsError    bool                    `json:"isError"`
		} `json:"result"`
		Error *struct{ Code int } `json:"error"`
	}
	replies := map[int]reply{}
	for _, line := range strings.Split(strings.TrimSpace(c.ok("mcp", "serve")), "\n") {
		r := decode[reply](t, line)
		replies[r.ID] = r
	}
	if len(replies) != 4 { // the notification gets no reply
		t.Fatalf("replies = %+v", replies)
	}
	if replies[1].Result.ServerInfo.Name != "mtm" {
		t.Errorf("initialize = %+v", replies[1])
	}
	var names []string
	for _, tool := range replies[2].Result.Tools {
		names = append(names, tool.Name)
	}
	if list := strings.Join(names, " "); !strings.Contains(list, "ui.create_page") || !strings.Contains(list, "ui.list_pages") || len(names) <= 4 {
		t.Errorf("tools/list has no Open UI and data tools: %s", list)
	}
	if replies[3].Error != nil || replies[3].Result.IsError {
		t.Errorf("tools/call = %+v", replies[3])
	}
	if replies[4].Error == nil || replies[4].Error.Code != -32601 {
		t.Errorf("an unknown method = %+v", replies[4])
	}
	// What the server wrote is in the store the CLI reads.
	pages := decode[[]record](t, c.ok("record", "list", "ui.page"))
	if len(pages) != 1 || pages[0].Fields["title"] != "Served" {
		t.Fatalf("pages = %v", pages)
	}
	c.fails("mcp", "serve", "extra")
}

// "mtm migrate" is a dry run unless --apply is given, and only reads v1.
func TestMigrate(t *testing.T) {
	dir := t.TempDir()
	v1 := filepath.Join(dir, "v1", "mtm.db")
	if err := os.MkdirAll(filepath.Dir(v1), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", v1) // the driver comes with the data package
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, row TEXT)`,
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, row TEXT)`,
		`CREATE TABLE notes (id TEXT PRIMARY KEY, row TEXT)`,
		`INSERT INTO projects VALUES ('p1', '{"id":"p1","name":"alpha","repo":"/src/alpha","status":"open","created":1000}')`,
		`INSERT INTO tasks VALUES ('t1', '{"id":"t1","parent_id":"","project_id":"p1","title":"top","status":"active","created":1100}')`,
		`INSERT INTO notes VALUES ('n1', '{"id":"n1","text":"a note","task_id":"t1","project_id":"p1","created":1}')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		raw, err := os.ReadFile(v1)
		if err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(filepath.Dir(v1))
		info, _ := os.Stat(v1)
		return fmt.Sprintf("%x %s %d files", sha256.Sum256(raw), info.ModTime(), len(entries))
	}
	before := state()

	type counts struct{ Total, Created, Unchanged int }
	type report struct {
		DryRun, Applied        bool
		Projects, Tasks, Notes counts
		Writes                 int
	}
	c := cli{t: t, db: filepath.Join(dir, "v2", "mtm.db")}
	for _, args := range [][]string{{"migrate", "--from", v1}, {"migrate", "--from", v1, "--dry-run"}} {
		rep := decode[report](t, c.ok(args...))
		if !rep.DryRun || rep.Applied || rep.Writes != 0 || rep.Projects.Created != 1 || rep.Tasks.Created != 1 || rep.Notes.Created != 1 {
			t.Fatalf("%v = %+v", args, rep)
		}
		if _, err := os.Stat(c.db); !os.IsNotExist(err) {
			t.Fatalf("%v created the v2 store (%v)", args, err)
		}
	}

	rep := decode[report](t, c.ok("migrate", "--from="+v1, "--apply"))
	if rep.DryRun || !rep.Applied || rep.Writes == 0 || rep.Projects.Created != 1 || rep.Tasks.Created != 1 {
		t.Fatalf("--apply = %+v", rep)
	}
	if tasks := decode[[]record](t, c.ok("record", "list", "task")); len(tasks) != 2 {
		t.Fatalf("tasks after --apply = %v", tasks)
	}
	// With a v2 store a dry run tells what an apply would still change: nothing.
	rep = decode[report](t, c.ok("migrate", "--from", v1))
	if !rep.DryRun || rep.Writes != 0 || rep.Projects.Unchanged != 1 || rep.Tasks.Unchanged != 1 || rep.Projects.Created != 0 {
		t.Fatalf("dry run after --apply = %+v", rep)
	}
	if after := state(); after != before {
		t.Fatalf("the v1 database changed:\n%s\n%s", before, after)
	}

	c.fails("migrate")
	c.fails("migrate", "--from", v1, "--dry-run", "--apply")
	c.fails("migrate", "--from", c.db, "--apply")
	c.fails("migrate", "--from", filepath.Join(dir, "missing.db"))
	c.fails("record", "list", "task", "--apply")
}
