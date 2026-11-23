package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNounCLILifecycle(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "mtm.db")
	c := cli{t: t, db: db}

	// Task create, list (--field, --format table), edit, star/unstar, archive/restore, snooze/unsnooze, delete, undo/redo.
	t1 := decode[record](t, c.ok("task", "create", "--title", "Ship Wave C", "--field", "priority=P1", "--field", "workStatus=active"))
	if t1.Kind != "task" || t1.Fields["title"] != "Ship Wave C" || t1.Fields["priority"] != "P1" {
		t.Fatalf("task create = %+v", t1)
	}
	t2 := decode[record](t, c.ok("tasks", "add", "--title", "Write tests", "--field", "priority=P2"))
	if t2.Kind != "task" {
		t.Fatalf("tasks add = %+v", t2)
	}

	// Filter by --field k=v on list.
	p1s := decode[[]record](t, c.ok("task", "list", "--field", "priority=P1"))
	if len(p1s) != 1 || p1s[0].ID != t1.ID {
		t.Fatalf("task list --field priority=P1 = %+v", p1s)
	}
	table := c.ok("tasks", "list", "--format", "table")
	if !strings.Contains(table, "Ship Wave C") || !strings.Contains(table, "Write tests") {
		t.Fatalf("tasks list table:\n%s", table)
	}

	// Edit task with --body - from stdin (maps to task.description).
	cStdin := cli{t: t, db: db, stdin: "Detailed checklist\nfrom stdin"}
	edited := decode[record](t, cStdin.ok("task", "edit", t1.ID, "--title", "Ship Wave C Now", "--body", "-"))
	if edited.Fields["title"] != "Ship Wave C Now" || edited.Fields["description"] != "Detailed checklist\nfrom stdin" {
		t.Fatalf("task edit = %+v", edited)
	}

	// Star / unstar / archive / restore / snooze / unsnooze.
	starred := decode[record](t, c.ok("task", "star", t1.ID))
	if starred.StarStatus != "starred" {
		t.Fatalf("star = %+v", starred)
	}
	if list := decode[[]record](t, c.ok("task", "list", "--starred")); len(list) != 1 {
		t.Fatalf("starred list = %+v", list)
	}
	unstarred := decode[record](t, c.ok("task", "unstar", t1.ID))
	if unstarred.StarStatus != "unstarred" {
		t.Fatalf("unstar = %+v", unstarred)
	}
	c.ok("task", "archive", t2.ID)
	if list := decode[[]record](t, c.ok("task", "list")); len(list) != 1 {
		t.Fatalf("list after archive = %+v", list)
	}
	if list := decode[[]record](t, c.ok("task", "list", "--archived")); len(list) != 2 {
		t.Fatalf("list --archived = %+v", list)
	}
	c.ok("task", "restore", t2.ID)
	c.ok("task", "snooze", t1.ID, "24h")
	c.ok("task", "unsnooze", t1.ID)

	// Note with stdin body and --managed, plus relate/unrelate to task.
	noteCLI := cli{t: t, db: db, stdin: "# Meeting Notes\n- Ship it\n"}
	note := decode[record](t, noteCLI.ok("note", "create", "--title", "Standup", "--body", "-", "--managed"))
	notePath, _ := note.Fields["path"].(string)
	if notePath == "" || !strings.Contains(notePath, filepath.Join("files", "notes")) {
		t.Fatalf("managed note path = %q", notePath)
	}
	if raw, err := os.ReadFile(notePath); err != nil || string(raw) != "# Meeting Notes\n- Ship it\n" {
		t.Fatalf("managed note file = %q err=%v", string(raw), err)
	}
	c.ok("note", "relate", note.ID, "about", "task:"+t1.ID)
	c.ok("note", "unrelate", note.ID, "about", "task:"+t1.ID)

	// Document with body and no path defaults to managed file.
	docCLI := cli{t: t, db: db, stdin: "# Architecture\n"}
	doc := decode[record](t, docCLI.ok("docs", "create", "--title", "Arch", "--body", "-"))
	docPath, _ := doc.Fields["filePath"].(string)
	if docPath == "" || !strings.Contains(docPath, filepath.Join("files", "documents")) {
		t.Fatalf("managed doc filePath = %q", docPath)
	}

	// Delete + undo + redo shortcuts.
	c.ok("task", "delete", t2.ID)
	c.fails("task", "get", t2.ID)
	c.ok("undo")
	if got := decode[record](t, c.ok("task", "get", t2.ID)); got.ID != t2.ID {
		t.Fatalf("after undo get = %+v", got)
	}
	c.ok("redo")
	c.fails("task", "get", t2.ID)
}

func TestBindingAndExtensionCLI(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MTM_CONFIG_DIR", cfgDir)
	c := cli{t: t, db: filepath.Join(dir, "mtm.db")}

	// binding list (table & json) + --all
	tbl := c.ok("binding", "list")
	if !strings.HasPrefix(tbl, "MODE") || !strings.Contains(tbl, "recents.pick") {
		t.Fatalf("binding list table:\n%s", tbl)
	}
	some := decode[[]map[string]any](t, c.ok("binding", "list", "--json"))
	all := decode[[]map[string]any](t, c.ok("binding", "list", "--all", "--json"))
	if len(some) == 0 || len(all) <= len(some) {
		t.Fatalf("binding list counts: visible=%d all=%d", len(some), len(all))
	}

	// binding check passes with 0 errors on default bindings.
	chk := c.ok("binding", "check")
	if !strings.Contains(chk, "0 errors") {
		t.Fatalf("binding check output: %s", chk)
	}
	rep := decode[struct {
		Total  int `json:"total"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}](t, c.ok("binding", "check", "--json"))
	if rep.Total == 0 || len(rep.Errors) != 0 {
		t.Fatalf("binding check json = %+v", rep)
	}

	// Invalid user binding causes binding check to fail.
	if err := os.WriteFile(filepath.Join(cfgDir, "bindings.json"), []byte(`[{"mode":"bogus","keys":["q"],"command":"nope.cmd"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	c.fails("binding", "check")

	// extension discover & extension run against a fake tool in a temp dir.
	extDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(extDir, "o-csv")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'o-csv 1.0'; exit 0; fi\necho \"ran:$*\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	found := decode[[]struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}](t, c.ok("extension", "discover", "--dir", extDir))
	hasCSV := false
	for _, r := range found {
		if r.Name == "o-csv" && r.Status == "healthy" {
			hasCSV = true
		}
	}
	if !hasCSV {
		t.Fatalf("extension discover did not find healthy o-csv: %+v", found)
	}
	ran := decode[record](t, c.ok("extension", "run", "o-csv", "--flag", "hello"))
	if ran.Kind != "run" || !strings.Contains(ran.Fields["output"].(string), "ran:--flag hello") {
		t.Fatalf("extension run = %+v", ran)
	}
}

func TestOdenUniquePrefixAndRichHelp(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MTM_CONFIG_DIR", cfgDir)
	c := cli{t: t, db: filepath.Join(dir, "mtm.db")}

	// Unique prefix dispatch: "t c" -> task create, "t l" -> task list, "bi ch" -> binding check, "ins ac" -> inspect actions.
	made := decode[record](t, c.ok("t", "c", "--title", "Prefix Task"))
	if made.Kind != "task" || made.Fields["title"] != "Prefix Task" {
		t.Fatalf("t c = %+v", made)
	}
	listed := decode[[]record](t, c.ok("t", "l"))
	if len(listed) != 1 || listed[0].ID != made.ID {
		t.Fatalf("t l = %+v", listed)
	}
	chk := c.ok("bi", "ch")
	if !strings.Contains(chk, "0 errors") {
		t.Fatalf("bi ch = %q", chk)
	}
	if tbl := c.ok("ins", "ac"); !strings.HasPrefix(tbl, "ACTION") {
		t.Fatalf("ins ac = %q", tbl)
	}

	// Rich oden help & which.
	if h := c.ok("help"); !strings.Contains(h, "Human commands:") || !strings.Contains(h, "task") {
		t.Fatalf("help = %q", h)
	}
	if ha := c.ok("help", "--all"); !strings.Contains(ha, "Machine commands") || !strings.Contains(ha, "record") {
		t.Fatalf("help --all = %q", ha)
	}
	if th := c.ok("task", "--help"); !strings.Contains(th, "usage: mtm task") || !strings.Contains(th, "list") || !strings.Contains(th, "--query") {
		t.Fatalf("task --help = %q", th)
	}
	if w := c.ok("which", "task"); !strings.Contains(w, "task inline") {
		t.Fatalf("which task = %q", w)
	}
}
