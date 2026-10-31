package main

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func TestScopeRefreshHidesNotesDraftsAndLateCaptures(t *testing.T) {
	t.Parallel()
	a := open(t)
	for _, n := range []work.Note{{ID: "na", ProjectID: "matomeru", TaskID: "t1", Text: "A note"}, {ID: "nb", ProjectID: "ochazuke", Text: "B secret"}, {ID: "nu", Text: "scratch secret"}} {
		if err := a.work.PutNote(n); err != nil {
			t.Fatal(err)
		}
	}
	f := a.panesd.(*fakePanes)
	a.setPanes(f.panes, nil)
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	rv, err := a.work.AddReview(work.Review{ProjectID: "matomeru", Session: "matomeru", Window: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	if a.noteList.Len() != 1 {
		t.Fatalf("notes=%+v", a.noteList.Items())
	}
	if got := a.detailNotes("matomeru", "t1"); !strings.Contains(got, "A note") || strings.Contains(got, "secret") {
		t.Fatal(got)
	}
	executeA(t, a, "pane", "preview", f.panes[0].rowID(), nil)
	executeA(t, a, "review", "open", rv.ID, nil)
	pending := a.r.PendingCommands()
	if err := a.beginEdit(editRecord{Noun: "note", ID: a.noteList.Items()[0].ID, ProjectID: "matomeru", TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "ochazuke"})
	if a.editor != nil || a.opened != "" || string(a.answer.Text) != "" {
		t.Fatal("old detail retained")
	}
	// Keep all records visible before delivering: ownership checks alone would accept stale jobs.
	executeA(t, a, "scope", "all", "work", nil)
	settle(a, pending)
	saved, err := a.work.Review(rv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.preview.Text != "" || saved.Output != "" || a.captureErr != "" {
		t.Fatalf("late data: %+v %+v", a.preview, saved)
	}
	if a.noteList.Len() != 3 || a.pin != "ochazuke" {
		t.Fatalf("all=%+v pin=%s", a.noteList.Items(), a.pin)
	}
	executeA(t, a, "scope", "all", "work", nil)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "review", "open", rv.ID, nil)
	settle(a, a.r.PendingCommands())
	saved, err = a.work.Review(rv.ID)
	if err != nil || saved.Output != "" || a.captureErr != "pane outside pin; showing saved output" {
		t.Fatalf("reassigned pane captured: %+v %v %s", saved, err, a.captureErr)
	}
}

func TestDeletionConfirmsCountsAndKeepsNote(t *testing.T) {
	t.Parallel()
	a := open(t)
	note, err := a.work.AddNote("matomeru", "t1", "survives")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.drafts.Save("note", "draft-note", editRecord{Noun: "note", ID: "draft-note", New: true, ProjectID: "matomeru", TaskID: "t1", Text: "unfinished note"}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	// Unconfirmed: the standard delete asks first, its store's ConfirmFor
	// naming what it orphans.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "delete", Args: ochazuke.Arguments{"id": "t1"}}); err != nil {
		t.Fatal(err)
	}
	modal := a.r.Instances[a.r.TopModal()]
	if modal == nil || !strings.Contains(modal.Payload.Prompt, "1 notes") || !strings.Contains(modal.Payload.Prompt, "0 pane owners") ||
		!strings.Contains(modal.Payload.Prompt, "Directories and panes are untouched.") {
		t.Fatalf("confirmation=%+v", modal)
	}
	if _, err := a.work.Task("t1"); err != nil {
		t.Fatal("deleted before confirmation")
	}
	executeA(t, a, "confirm", "accept", modal.ID, nil)
	n, err := a.work.Note(note.ID)
	if err != nil || n.Text != "survives" || n.TaskID != "" || n.FormerTaskID != "t1" || n.ProjectID != "matomeru" {
		t.Fatalf("note=%+v err=%v", n, err)
	}
	var draft editRecord
	if err := a.drafts.Load("note", "draft-note", &draft); err != nil {
		t.Fatal(err)
	}
	saved, err := a.saveNoteRecord(draft, ochazuke.Record{"text": draft.Text})
	if err != nil || saved.TaskID != "" || saved.FormerTaskID != "t1" || saved.Text != "unfinished note" {
		t.Fatalf("saved draft=%+v err=%v", saved, err)
	}
	draft.New = false
	if err := a.drafts.Save("note", draft.ID, draft); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "delete", "matomeru", nil)
	if a.pin != "" {
		t.Fatalf("deleted project remains pinned: %s", a.pin)
	}
	if err := a.drafts.Load("note", "draft-note", &draft); err != nil {
		t.Fatal(err)
	}
	if draft.ProjectID != "" || draft.FormerProjectID != "matomeru" {
		t.Fatalf("draft owner=%+v", draft)
	}
}

// task.delete is the standard delete: undo puts the task back under its
// key with its props (the record only: what the delete orphaned stays
// orphaned), and the old confirm-delete name still deletes.
func TestTaskDeleteIsStandardAndUndoes(t *testing.T) {
	t.Parallel()
	a := open(t)
	before, err := a.work.Task("t1")
	if err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "task", "delete", "", ochazuke.Arguments{"id": "t1"})
	if _, err := a.work.Task("t1"); err == nil {
		t.Fatal("delete kept the task")
	}
	executeA(t, a, "history", "undo", "", nil)
	after, err := a.work.Task("t1")
	if err != nil || after.Title != before.Title || after.ProjectID != before.ProjectID || after.Status != before.Status {
		t.Fatalf("undo: %+v %v, want %+v", after, err, before)
	}
	executeA(t, a, "task", "confirm-delete", "", ochazuke.Arguments{"id": "t1"})
	if _, err := a.work.Task("t1"); err == nil {
		t.Fatal("confirm-delete kept the task")
	}
}
