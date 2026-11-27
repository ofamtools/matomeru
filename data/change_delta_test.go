package data

import (
	"fmt"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func TestChangedKindsSinceTracksKindsRelationsAndUndo(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	actor := core.Actor{Kind: "user", ID: "owner"}

	proj, cs0, err := s.Create(CreateInput{
		Kind:   KindTask,
		Fields: map[string]any{"title": "Alpha"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if s.LocalWriteSeq() == 0 || s.LastWrittenRev() != cs0.Number {
		t.Fatalf("LocalWriteSeq=%d LastWrittenRev=%d, want >0 and %d", s.LocalWriteSeq(), s.LastWrittenRev(), cs0.Number)
	}

	// No changes since cs0.
	empty, err := s.ChangedKindsSince(cs0.Number)
	if err != nil || empty.FullRebuild || empty.HasRelation || len(empty.Kinds) != 0 {
		t.Fatalf("empty delta = %+v, err=%v", empty, err)
	}

	// Creating a note only marks KindNote (no relation).
	note, cs1, err := s.Create(CreateInput{
		Kind:   KindNote,
		Fields: map[string]any{"title": "Note 1", "body": "hello", "task": proj.Ref()},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	d1, err := s.ChangedKindsSince(cs0.Number)
	if err != nil || d1.FullRebuild || d1.HasRelation || !d1.Kinds[KindNote] || d1.Kinds[KindContext] {
		t.Fatalf("note delta = %+v, err=%v", d1, err)
	}

	// Adding a relation sets HasRelation and records both endpoint kinds.
	_, cs2, err := s.Relate(RelateInput{From: note.Ref(), Kind: RelAbout, To: proj.Ref()}, actor)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := s.ChangedKindsSince(cs1.Number)
	if err != nil || d2.FullRebuild || !d2.HasRelation || !d2.Kinds[KindNote] || !d2.Kinds[KindTask] {
		t.Fatalf("relate delta = %+v, err=%v", d2, err)
	}

	// Undo triggers FullRebuild.
	if _, err := s.Undo(cs2.Number, actor); err != nil {
		t.Fatal(err)
	}
	dUndo, err := s.ChangedKindsSince(cs2.Number)
	if err != nil || !dUndo.FullRebuild {
		t.Fatalf("undo delta = %+v, err=%v, want FullRebuild=true", dUndo, err)
	}
}

func TestProjectUnreadCandidatesSkipsHistoricalRecords(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	actor := core.Actor{Kind: "user", ID: "owner"}

	proj, _, err := s.Create(CreateInput{
		Kind:   KindTask,
		Fields: map[string]any{"title": "Beta"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	var firstOld *Record
	for i := range 250 {
		rec, _, err := s.Create(CreateInput{
			Kind: KindContext,
			Fields: map[string]any{
				"title":       fmt.Sprintf("Old %d", i),
				"value":       fmt.Sprintf("https://example.com/old/%d", i),
				"contextKind": "url",
				"task":        proj.Ref(),
			},
		}, actor)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstOld = rec
		}
	}

	// Mark cutoff right now; then update firstOld with readAt and add 2 post-cutoff items.
	cutoff := time.Now().UTC().Add(time.Millisecond)
	time.Sleep(2 * time.Millisecond)

	if _, _, err := s.Patch(PatchInput{
		Ref:    firstOld.Ref(),
		Fields: map[string]any{"readAt": WriteTime{}},
	}, actor); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, _, err := s.Create(CreateInput{
			Kind: KindContext,
			Fields: map[string]any{
				"title":       fmt.Sprintf("New %d", i),
				"value":       fmt.Sprintf("https://example.com/new/%d", i),
				"contextKind": "url",
				"task":        proj.Ref(),
			},
		}, actor); err != nil {
			t.Fatal(err)
		}
	}

	scope, err := s.ProjectScope(proj.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	cands, err := s.ProjectUnreadCandidates(scope, KindContext, cutoff.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("ProjectUnreadCandidates returned %d records, want 3 (1 readAt + 2 post-cutoff)", len(cands))
	}
}
