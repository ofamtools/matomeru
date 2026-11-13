package main

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// projectsOn focuses the Work page's project row id.
func projectsOn(t *testing.T, a *App, id string) {
	t.Helper()
	toWork(t, a)
	if err := a.projectList.Select(id); err != nil {
		t.Fatal(err)
	}
	if err := a.projectList.Focus(); err != nil {
		t.Fatal(err)
	}
}

// set, rename and the edit wrap write the props; an unknown harness is
// written with a notice (record first); one undo reverts the last write.
func TestProjectSetRenameAndUndo(t *testing.T) {
	t.Parallel()
	a := open(t)
	start, _ := a.work.Project("matomeru") // a new project holds the palette tone it was given
	executeA(t, a, "project", "set", "", ochazuke.Arguments{"id": "matomeru", "props": map[string]any{"@label": "Matomeru"}})
	executeA(t, a, "project", "set", "", ochazuke.Arguments{"id": "matomeru", "props": map[string]any{"color": "blue", "preset": "fast"}})
	p, _ := a.work.Project("matomeru")
	if p.Name != "Matomeru" || p.Theme.Color != "blue" || p.Settings.Preset != "fast" || p.Theme.Glyph != "◆" {
		t.Fatalf("after rename and set: %+v", p)
	}
	executeA(t, a, "project", "set", "", ochazuke.Arguments{"id": "matomeru", "props": map[string]any{"harness": "bogusharness"}})
	if p, _ := a.work.Project("matomeru"); p.Settings.Harness != "bogusharness" {
		t.Fatalf("harness: %+v", p.Settings)
	}
	if !strings.Contains(a.m.Status+frame(a), "unknown harness bogusharness") {
		t.Fatalf("no notice for the unknown harness:\n%s", frame(a))
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if p, _ := a.work.Project("matomeru"); p.Settings.Harness != "" || p.Theme.Color != "blue" {
		t.Fatalf("undo of the harness: %+v", p)
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if p, _ := a.work.Project("matomeru"); p.Theme.Color != start.Theme.Color || p.Settings.Preset != "" || p.Name != "Matomeru" {
		t.Fatalf("undo of the set: %+v", p)
	}
	// project edit with arguments is the standard set of those given (and
	// undoes as one).
	executeA(t, a, "project", "edit", "matomeru", ochazuke.Arguments{"glyph": "★"})
	if p, _ := a.work.Project("matomeru"); p.Theme.Glyph != "★" || p.Name != "Matomeru" {
		t.Fatalf("edit --glyph: %+v", p)
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if p, _ := a.work.Project("matomeru"); p.Theme.Glyph != "◆" {
		t.Fatalf("undo of edit: %+v", p)
	}
}

// From the CLI: project new wraps create (dir attached as the default) and
// answers the stored project; create, duplicate, get and list are the
// standard verbs; undoing a create deletes the project.
func TestProjectStandardVerbsFromTheCLI(t *testing.T) {
	t.Parallel()
	a, _ := cliApp(t)
	dir := t.TempDir()
	var made struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		DefaultDirectory string `json:"default_directory"`
	}
	if err := json.Unmarshal([]byte(mustCLI(t, a, "project", "new", "fresh", "--dir", dir)), &made); err != nil {
		t.Fatal(err)
	}
	if made.Name != "fresh" || made.DefaultDirectory != dir {
		t.Fatalf("project new: %+v", made)
	}
	var thing map[string]any
	cliJSON(t, a, &thing, "project", "get", made.ID)
	if thing["name"] != "fresh" || thing["dir"] != dir {
		t.Fatalf("get: %v", thing)
	}
	cliJSON(t, a, &thing, "project", "create", "--name", "other", "--color", "red")
	other := thing["id"].(string)
	if p, err := a.work.Project(other); err != nil || p.Name != "other" || p.Theme.Color != "red" || p.Status != "open" {
		t.Fatalf("create: %+v %v", p, err)
	}
	cliJSON(t, a, &thing, "project", "create", "--from", made.ID, `name="fresh copy"`)
	if p, err := a.work.Project(thing["id"].(string)); err != nil || p.Name != "fresh copy" || p.DefaultTree != dir {
		t.Fatalf("duplicate: %+v %v", p, err)
	}
	var rows []map[string]any
	cliRows(t, a, &rows, "project", "list")
	for _, id := range []string{made.ID, other} {
		found := false
		for _, row := range rows {
			found = found || row["id"] == id
		}
		if !found {
			t.Fatalf("created project %s absent from list: %v", id, rows)
		}
	}
	b := open(t)
	created, _ := executeA(t, b, "project", "create", "", ochazuke.Arguments{"name": "short-lived"}).(map[string]any)
	executeA(t, b, "history-entry", "undo", "", nil)
	if _, err := b.work.Project(created["id"].(string)); err == nil {
		t.Fatal("undo of create kept the project")
	}
}

// Targeted keys on a project row: i opens the editor on the props' fields
// and q goes back to the row; the palette's rename opens on the old name;
// d d archives first; the archived filter lets the owner delete the retained row.
func TestProjectKeysEditRenameDelete(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.m.Width, a.m.Height = 100, 30
	projectsOn(t, a, "matomeru")
	press(t, a, "i")
	if a.editor == nil || a.editor.Record.ID != "matomeru" {
		t.Fatal("i did not open the editor")
	}
	for _, want := range []string{"color", "glyph", "harness", "preset"} {
		if !strings.Contains(frame(a), want) {
			t.Fatalf("editor lacks %q:\n%s", want, frame(a))
		}
	}
	press(t, a, "esc", "q")
	if sel, ok := a.projectList.Selected(); a.editor != nil || !ok || sel.ID != "matomeru" || a.r.FocusID() != "matomeru" {
		t.Fatalf("q: editor %v, focus %q", a.editor != nil, a.r.FocusID())
	}
	press(t, a, ":")
	typeText(t, a, "edit --field @label")
	press(t, a, "enter")
	if editing(a) == nil || !strings.Contains(frame(a), "matomeru") {
		t.Fatalf("modal %q\n%s", a.r.TopModal(), frame(a))
	}
	typeText(t, a, "Mato")
	press(t, a, "enter")
	if p, _ := a.work.Project("matomeru"); p.Name != "Mato" {
		t.Fatalf("palette rename: %q\n%s", p.Name, frame(a))
	}
	projectsOn(t, a, "ochazuke")
	press(t, a, "d", "d")
	if p, err := a.work.Project("ochazuke"); err != nil || !p.Archived {
		t.Fatalf("first d d did not retain an archived project: %+v %v", p, err)
	}
	executeA(t, a, "ochazuke.collection", "filter-saved", "projects", ochazuke.Arguments{"name": "archived"})
	projectsOn(t, a, "ochazuke")
	press(t, a, "d", "d")
	if fr := frame(a); a.r.TopModal() == "" {
		t.Fatalf("d d: modal %q\n%s", a.r.TopModal(), fr)
	}
	press(t, a, "y")
	if _, err := a.work.Project("ochazuke"); err == nil {
		t.Fatal("y kept the project")
	}
}
