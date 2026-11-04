package main

import (
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// d d then p is a move: the task comes back under its own id with its
// status, easy and private flags and its notes, at the new slot; u takes
// the put back (the task is deleted again, the note detached); a second p
// makes a copy with a new id.
func TestOutlineDeleteThenPutMovesTheTask(t *testing.T) {
	t.Parallel()
	a, task, note := outlineWithKept(t, work.StatusBlocked, true, true)
	press(t, a, "d", "d", "p") // the cursor falls on t2; p puts after it
	got, err := a.work.Task("t1")
	if err != nil {
		t.Fatalf("p did not restore t1: %v", err)
	}
	if got.Status != task.Status || !got.Easy || !got.Private || got.Created != task.Created {
		t.Fatalf("restored %+v, was %+v", got, task)
	}
	if n, _ := a.work.Note(note.ID); n.TaskID != "t1" {
		t.Fatalf("note not re-attached: %+v", n)
	}
	var order []string
	for _, x := range tasksOf(t, a, "matomeru") {
		order = append(order, x.ID)
	}
	if len(order) != 2 || order[0] != "t2" || order[1] != "t1" {
		t.Fatalf("order %v, want t2 then t1", order)
	}
	press(t, a, "u")
	if _, err := a.work.Task("t1"); err == nil {
		t.Fatal("u kept the put task")
	}
	if len(tasksOf(t, a, "matomeru")) != 1 {
		t.Fatalf("u left %+v", tasksOf(t, a, "matomeru"))
	}
	press(t, a, "ctrl+r")
	if got, err := a.work.Task("t1"); err != nil || got.Status != task.Status {
		t.Fatalf("redo: %+v %v", got, err)
	}
	// The memory is used up: another p copies with a new id.
	press(t, a, "p")
	if n := len(tasksOf(t, a, "matomeru")); n != 3 {
		t.Fatalf("second p: %d tasks", n)
	}
}

// p and P on a project row (or the one root of a project's outline, or
// the no project row) put the rows in it as tasks, first; they never make a
// project or land on no project.
func TestOutlinePutOnAProjectRowPutsTasksInIt(t *testing.T) {
	t.Parallel()
	for _, row := range []string{projectNode("ochazuke"), projectNode("")} {
		for _, key := range []string{"p", "P"} {
			a := allOutline(t, outlineNode("t1"))
			press(t, a, "y", "y")
			focusRow(t, a, row)
			press(t, a, key)
			if made := newProjects(t, a); len(made) != 0 {
				t.Fatalf("%s on %s made projects %+v", key, row, made)
			}
			want := projectOfRoot(row)
			tasks, err := a.work.Tasks("")
			if err != nil {
				t.Fatal(err)
			}
			var found []work.Task
			for _, x := range tasks {
				if x.Title == "write the brief" && x.ID != "t1" {
					found = append(found, x)
				}
			}
			if len(found) != 1 || found[0].ProjectID != want || found[0].ParentID != "" {
				t.Fatalf("%s on %s: put %+v, want one top task of %q", key, row, found, want)
			}
			if got := a.r.Tree(outlineTree).Nodes(); len(got) == 0 {
				t.Fatal("outline empty")
			}
		}
	}
	// The one root of a project's outline.
	a := open(t)
	press(t, a, "j", "O")
	focusRow(t, a, projectNode("matomeru"))
	press(t, a, "y", "y") // a yank of the root: the project and its tasks
	focusRow(t, a, outlineNode("t1"))
	press(t, a, "y", "y")
	focusRow(t, a, projectNode("matomeru"))
	press(t, a, "p")
	if made := newProjects(t, a); len(made) != 0 {
		t.Fatalf("p on the root made projects %+v", made)
	}
	if got := rootRows(a); len(got) != 1 {
		t.Fatalf("roots %v", got)
	}
	if kids := tasksOf(t, a, "matomeru"); len(kids) != 3 {
		t.Fatalf("p on the root: %d tasks, want 3", len(kids))
	}
}

// p with rows in the outline but none shown (a filter matching nothing)
// says so and adds nothing.
func TestOutlinePutWithNothingShownRefuses(t *testing.T) {
	t.Parallel()
	a := allOutline(t, outlineNode("t1"))
	press(t, a, "y", "y", "/")
	typeText(t, a, "zzzz")
	press(t, a, "enter")
	before := len(tasksOf(t, a, "matomeru"))
	press(t, a, "p")
	if got := len(tasksOf(t, a, "matomeru")); got != before {
		t.Fatalf("p added %d tasks with nothing shown", got-before)
	}
	if made := newProjects(t, a); len(made) != 0 {
		t.Fatalf("p made projects %+v", made)
	}
}

// Undoing an add that made a project with tasks (its rows go tasks first,
// then the project) deletes them all from the store: screen and store
// agree. A task delete made while its project's row goes (not an undo) is
// still left to the project's own delete.
func TestOutlineUndoOfAProjectWithTasksLeavesNothing(t *testing.T) {
	t.Parallel()
	a := allOutline(t, projectNode("ochazuke"))
	o := a.outline
	if err := a.work.PutProject(work.Project{ID: "px", Name: "px", Status: "open", Created: work.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutTask(work.Task{ID: "tx", ProjectID: "px", Title: "in px", Position: 1, Status: work.StatusTodo}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	// The rows are gone from the tree, as the library's undo leaves them.
	for _, c := range []ochazuke.TreeChange{
		{Op: ochazuke.TreeDelete, ID: outlineNode("tx"), Undone: true},
		{Op: ochazuke.TreeDelete, ID: projectNode("px"), Undone: true},
	} {
		if err := a.writeOutline(o, c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.work.Task("tx"); err == nil {
		t.Fatal("the undone task is still stored")
	}
	if p, err := a.work.Project("px"); err == nil {
		t.Fatalf("the undone project is still stored: %+v", p)
	}
}
