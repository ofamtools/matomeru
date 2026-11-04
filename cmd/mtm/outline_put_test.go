package main

import (
	"testing"

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
