package main

import (
	"testing"

	"ofam.dev/matomeru/internal/work"
)

// task t1 with a status, flags and a note, in the outline, focused.
func outlineWithKept(t *testing.T, status string, easy, private bool) (*App, work.Task, work.Note) {
	t.Helper()
	a := open(t)
	task, err := a.work.Task("t1")
	if err != nil {
		t.Fatal(err)
	}
	task.Status, task.Easy, task.Private = status, easy, private
	if err := a.work.PutTask(task); err != nil {
		t.Fatal(err)
	}
	task, _ = a.work.Task("t1")
	if task.Status != status || task.Easy != easy || task.Private != private {
		t.Fatalf("setup lost the task's fields: %+v", task)
	}
	note, err := a.work.AddNote("matomeru", "t1", "keep this")
	if err != nil {
		t.Fatal(err)
	}
	press(t, a, "j", "O")
	focusRow(t, a, outlineNode("t1"))
	return a, task, note
}

// wantKept checks the task is back as it was, its note on it again.
func wantKept(t *testing.T, a *App, task work.Task, note work.Note) {
	t.Helper()
	got, err := a.work.Task(task.ID)
	if err != nil {
		t.Fatalf("task not restored: %v", err)
	}
	if got.Status != task.Status || got.Easy != task.Easy || got.Private != task.Private || got.Title != task.Title ||
		got.ProjectID != task.ProjectID || got.Created != task.Created || got.Position != task.Position {
		t.Fatalf("restored %+v, was %+v", got, task)
	}
	n, err := a.work.Note(note.ID)
	if err != nil || n.TaskID != task.ID || n.FormerTaskID != note.FormerTaskID {
		t.Fatalf("note after undo: %+v %v", n, err)
	}
}

// d d then u, and erasing the line then esc then u, bring the task back
// as it was: status, easy, private and its notes, not a blank todo.
func TestOutlineUndoDeleteRestoresTheTask(t *testing.T) {
	t.Parallel()
	for _, status := range []string{work.StatusBlocked, work.StatusActive, work.StatusDone} {
		for _, keys := range [][]string{{"d", "d"}, {"i", "backspace", "esc"}} {
			a, task, note := outlineWithKept(t, status, status == work.StatusBlocked, status == work.StatusActive)
			press(t, a, keys...)
			if _, err := a.work.Task("t1"); err == nil {
				t.Fatalf("%s %v kept the task", status, keys)
			}
			if n, _ := a.work.Note(note.ID); n.TaskID != "" {
				t.Fatalf("%s %v: note still on the deleted task: %+v", status, keys, n)
			}
			press(t, a, "u")
			wantKept(t, a, task, note)
		}
	}
}
