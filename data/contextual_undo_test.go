package data

import (
	"errors"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestUndoChangeSetOutOfOrderDisjointRecords(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	owner := core.Actor{Kind: "user", ID: "owner"}
	a, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Task A"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Task B"}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	_, starA, err := s.Star(a.Ref(), owner)
	if err != nil {
		t.Fatal(err)
	}
	_, starB, err := s.Star(b.Ref(), owner)
	if err != nil {
		t.Fatal(err)
	}

	// Undo Task A's star out of order while Task B's newer star stays active.
	undoA, err := s.UndoChangeSet(starA.Number, owner)
	if err != nil {
		t.Fatalf("UndoChangeSet(starA): %v", err)
	}
	gotA, _ := s.Get(a.Ref())
	gotB, _ := s.Get(b.Ref())
	if gotA.IsStarred() {
		t.Fatal("Task A should be unstarred after out-of-order UndoChangeSet")
	}
	if !gotB.IsStarred() {
		t.Fatal("Task B should remain starred after undoing Task A")
	}

	// Redo Task A's undo cleanly while Task B stays untouched.
	if _, err := s.RedoChangeSet(undoA.Number, owner); err != nil {
		t.Fatalf("RedoChangeSet(undoA): %v", err)
	}
	gotA, _ = s.Get(a.Ref())
	if !gotA.IsStarred() {
		t.Fatal("Task A should be starred again after RedoChangeSet")
	}

	// Legacy UndoLast still undoes the newest undoable write (starB, since starA was redone? Wait: starB > starA).
	if cs, err := s.UndoLast(owner); err != nil || cs.UndoOf != starB.Number {
		t.Fatalf("UndoLast = %+v, %v; want UndoOf=%d", cs, err, starB.Number)
	}
}

func TestUndoChangeSetRefusesConflictingNewerEdit(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	owner := core.Actor{Kind: "user", ID: "owner"}
	task, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Original"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, starCS, err := s.Star(task.Ref(), owner)
	if err != nil {
		t.Fatal(err)
	}
	_, titleCS, err := s.Patch(PatchInput{Ref: task.Ref(), Fields: map[string]any{"title": "Renamed"}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// Attempting to undo the older star while a newer title edit is active on the same record must refuse safely.
	if _, err := s.UndoChangeSet(starCS.Number, owner); !errors.Is(err, ErrUndoConflict) {
		t.Fatalf("UndoChangeSet(starCS) err = %v, want ErrUndoConflict", err)
	}
	got, _ := s.Get(task.Ref())
	if !got.IsStarred() || got.Text("title") != "Renamed" {
		t.Fatalf("record mutated despite conflict: starred=%v title=%q", got.IsStarred(), got.Text("title"))
	}

	// Once the newer title edit is undone, the older star is safely undoable.
	if _, err := s.UndoChangeSet(titleCS.Number, owner); err != nil {
		t.Fatalf("UndoChangeSet(titleCS): %v", err)
	}
	if _, err := s.UndoChangeSet(starCS.Number, owner); err != nil {
		t.Fatalf("UndoChangeSet(starCS) after clearing blocker: %v", err)
	}
	got, _ = s.Get(task.Ref())
	if got.IsStarred() || got.Text("title") != "Original" {
		t.Fatalf("record after both undos: starred=%v title=%q", got.IsStarred(), got.Text("title"))
	}
}

func TestUndoChangeSetIgnoresBookkeepingReads(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	owner := core.Actor{Kind: "user", ID: "owner"}
	viewer := core.Actor{Kind: "system", ID: "mtm.view", Label: "viewed"}
	task, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Read me"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, starCS, err := s.Star(task.Ref(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: task.Ref(), Fields: map[string]any{"readAt": WriteTime{}}}, viewer); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UndoChangeSet(starCS.Number, owner); err != nil {
		t.Fatalf("bookkeeping readAt should not block UndoChangeSet: %v", err)
	}
	got, _ := s.Get(task.Ref())
	if got.IsStarred() {
		t.Fatal("Task should be unstarred")
	}
}

func TestRedoChangeSetRefusesConflictingNewerEdit(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	owner := core.Actor{Kind: "user", ID: "owner"}
	task, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Alpha"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, starCS, err := s.Star(task.Ref(), owner)
	if err != nil {
		t.Fatal(err)
	}
	undoCS, err := s.UndoChangeSet(starCS.Number, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Patch(PatchInput{Ref: task.Ref(), Fields: map[string]any{"title": "Beta"}}, owner); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RedoChangeSet(undoCS.Number, owner); !errors.Is(err, ErrUndoConflict) || !strings.Contains(err.Error(), "modified later") {
		t.Fatalf("RedoChangeSet(undoCS) err = %v, want ErrUndoConflict", err)
	}
	got, _ := s.Get(task.Ref())
	if got.IsStarred() || got.Text("title") != "Beta" {
		t.Fatalf("record mutated despite redo conflict: starred=%v title=%q", got.IsStarred(), got.Text("title"))
	}
}
