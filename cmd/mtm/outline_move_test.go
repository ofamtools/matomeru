package main

import (
	"errors"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// m on a task row of the outline is the Work page's m: the chooser of the
// task's project, and the choice moves the task out of the outline.
func TestOutlineMoveTaskToAnotherProject(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "O")
	if app.r.TopModal() != outlineModal || app.r.FocusID() != outlineNode("t1") {
		t.Fatalf("modal %q focus %s", app.r.TopModal(), app.r.FocusID())
	}
	press(t, app, "m")
	if app.r.Chooser(ochazuke.EditChooserKind) == nil {
		t.Fatalf("m opened %q\n%s", app.r.TopModal(), frame(app))
	}
	pickOwner(t, app, "ochaz")
	if got := stored(t, app, "t1").ProjectID; got != "ochazuke" {
		t.Fatalf("m: project %q", got)
	}
	if app.r.TopModal() != outlineModal || strings.Contains(frame(app), "write the brief") {
		t.Fatalf("the moved task stays in the outline (modal %q):\n%s", app.r.TopModal(), frame(app))
	}
}

// A task never becomes a top-level row, so a move that would spill out of
// its project's row is refused before it is made, as a quiet edge: no
// write, no error.
func TestOutlineTaskStaysUnderItsProject(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "O")
	o := app.outline
	if o == nil || o.tree.Refuse == nil {
		t.Fatal("the outline tree has no Refuse")
	}
	if err := o.tree.Refuse(ochazuke.TreeNode{ID: outlineNode("t1")}, ochazuke.TreeChange{Op: ochazuke.TreeMove, ID: outlineNode("t1"), Parent: ""}); !errors.Is(err, ochazuke.ErrTreeRefused) {
		t.Fatalf("a task moved to the top level: %v", err)
	}
	if err := o.tree.Refuse(ochazuke.TreeNode{ID: outlineNode("t1")}, ochazuke.TreeChange{Op: ochazuke.TreeMove, ID: outlineNode("t1"), Parent: outlineNode("t2")}); err != nil {
		t.Fatalf("a task moved under a task: %v", err)
	}
	before := stored(t, app, "t1")
	press(t, app, "K", "K")
	if got := stored(t, app, "t1"); got.ParentID != before.ParentID || got.Position != before.Position {
		t.Fatalf("K at the first task wrote: %+v -> %+v", before, got)
	}
}

// A archives the task under the cursor (and over a visual selection every
// selected one) out of the outline's default view; X is the verb that
// brings an archived row back (the archive filter shows one).
func TestOutlineArchiveAndRestoreTasks(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "O")
	press(t, app, "A")
	if !stored(t, app, "t1").Archived {
		t.Fatalf("A kept t1: %+v", stored(t, app, "t1"))
	}
	if strings.Contains(frame(app), "write the brief") {
		t.Fatalf("an archived task shows in the outline:\n%s", frame(app))
	}
	executeA(t, app, "task", "restore", "", ochazuke.Arguments{"id": "t1"})
	if stored(t, app, "t1").Archived {
		t.Fatal("restore kept t1 archived")
	}
	press(t, app, "g", "g", "j", "V", "j", "A")
	for _, id := range []string{"t1", "t2"} {
		if !stored(t, app, id).Archived {
			t.Fatalf("V j A: %s not archived", id)
		}
	}
}
