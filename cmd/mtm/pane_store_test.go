package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// pane get answers its props from the listing; the agent's settings are
// read-only (set refuses them; set-model changes them in the session).
func TestPaneGetAndReadOnlySettings(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	thing, _ := executeA(t, a, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
	if thing["name"] != "claude" || thing["session"] != "matomeru" || thing["display"] != "matomeru/claude" {
		t.Fatalf("get: %v", thing)
	}
	_, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": "w1", "props": map[string]any{"model": "opus"}}})
	if err == nil {
		t.Fatal("set model was written")
	}
	rows, _ := ochazuke.AnswerRows(executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true})).([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("list: %v", rows)
	}
}

// * on a pane row stars it through the standard set, and undo takes the
// star back.
func TestPaneStarKeyAndUndo(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	row := "pane:matomeru/w1"
	if err := a.selectPaneRow(row); err != nil {
		t.Fatal(err)
	}
	press(t, a, "*")
	if stars, _ := a.work.PaneStars(); len(stars) != 1 {
		t.Fatalf("* stars: %v", stars)
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if stars, _ := a.work.PaneStars(); len(stars) != 0 {
		t.Fatalf("undo kept the star: %v", stars)
	}
}

// pane rename NAME is the standard set of name: the daemon renames the
// window in a job named pane.set; undo renames it back.
func TestPaneRenameIsSetAndUndoes(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	fr := &fakeRenamer{panes: f}
	a.renamer = fr
	row := "pane:matomeru/w1"
	executeA(t, a, "pane", "set", row, ochazuke.Arguments{"props": map[string]any{"@label": "fixer"}})
	settle(a, a.r.PendingCommands())
	if len(jobsOf(a, "pane.set")) != 1 {
		t.Fatalf("jobs: %+v", a.r.Jobs())
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	settle(a, a.r.PendingCommands())
	if got := strings.Join(fr.calls, "; "); got != "window matomeru/w1 fixer; window matomeru/w1 claude" {
		t.Fatalf("calls %s", got)
	}
}

// m on a pane row opens the project chooser (choose-move), whose pick is
// the standard set of @owner; unassociate sets it to no project.
func TestPaneMoveKeyAndUnassociate(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	row := "pane:matomeru/w1"
	if err := a.selectPaneRow(row); err != nil {
		t.Fatal(err)
	}
	press(t, a, "m")
	if a.r.Chooser(ochazuke.EditChooserKind) == nil {
		t.Fatalf("m opened %q", a.r.TopModal())
	}
	pickOwner(t, a, "ochaz")
	if o, _ := a.work.PaneOwner("w1", ""); o.ProjectID != "ochazuke" {
		t.Fatalf("m: %+v", o)
	}
	executeA(t, a, "pane", "unassociate", "", ochazuke.Arguments{"id": row})
	if o, _ := a.work.PaneOwner("w1", ""); o.ProjectID != "" || o.TaskID != "" {
		t.Fatalf("unassociate: %+v", o)
	}
}
