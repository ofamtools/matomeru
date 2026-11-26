package data

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func specs(names ...string) []core.ParamSpec {
	out := make([]core.ParamSpec, len(names))
	for i, n := range names {
		out[i] = core.ParamSpec{Name: n}
	}
	return out
}

func TestFormSubmissionFieldDiff(t *testing.T) {
	before := &Record{Kind: KindTask, ID: "t1", ChangeSetNumber: 7, StarStatus: StarUnstarred, ArchiveStatus: ArchiveActive,
		Fields: map[string]any{"title": "a", "description": "gone soon", "priority": "P1", "estimate": int64(3)}}
	sub := FormSubmission{
		Before: before,
		// description is visible but missing: cleared. priority is not visible: untouched.
		Submitted:     map[string]any{"title": "b", "estimate": 3},
		VisibleFields: specs("title", "description", "estimate"),
	}
	want := map[string]any{"title": "b", "description": nil}
	if got := sub.FieldDiff(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FieldDiff = %v, want %v", got, want)
	}
	if !sub.HasChanges() {
		t.Fatal("HasChanges = false")
	}
	in := sub.PatchInput()
	if in.Ref != before.Ref() || in.IfRevision != 7 || !reflect.DeepEqual(in.Fields, want) {
		t.Fatalf("PatchInput = %+v", in)
	}
	same := FormSubmission{Before: before, Submitted: map[string]any{"title": "a"}, VisibleFields: specs("title", ColStarStatus)}
	if same.HasChanges() {
		t.Fatalf("unchanged submit has changes: %+v", same.PatchInput())
	}
}

func TestFormSubmissionStatusOverlay(t *testing.T) {
	old := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	until := old.Add(time.Hour)
	before := &Record{Kind: KindTask, ID: "t1", StarStatus: StarUnstarred, ArchiveStatus: ArchiveActive, SnoozedUntil: &old,
		Fields: map[string]any{"title": "a"}}
	sub := FormSubmission{
		Before:        before,
		Submitted:     map[string]any{"title": "a", ColStarStatus: StarStarred, ColArchiveStatus: ArchiveArchived},
		VisibleFields: specs("title", ColStarStatus, ColArchiveStatus, ColSnoozedUntil),
		SnoozeUntil:   &until,
	}
	want := map[string]any{ColStarStatus: StarStarred, ColArchiveStatus: ArchiveArchived, ColSnoozedUntil: until.Format(TimeFormat)}
	if got := sub.PatchInput().Fields; !reflect.DeepEqual(got, want) {
		t.Fatalf("PatchInput fields = %v, want %v", got, want)
	}
	// A visible snooze with no time clears it; hidden statuses are never touched.
	sub.SnoozeUntil, sub.VisibleFields = nil, specs("title", ColSnoozedUntil)
	if got := sub.PatchInput().Fields; !reflect.DeepEqual(got, map[string]any{ColSnoozedUntil: nil}) {
		t.Fatalf("clear snooze fields = %v", got)
	}

	// A new record: statuses leave the create and become its overlay.
	create := FormSubmission{Schema: Schema{Kind: KindTask}, Submitted: map[string]any{"title": "n", ColStarStatus: StarStarred},
		Defaults: map[string]any{"priority": "P2", "title": "default"}}
	if in := create.CreateInput(); in.Kind != KindTask || !reflect.DeepEqual(in.Fields, map[string]any{"title": "n", "priority": "P2"}) {
		t.Fatalf("CreateInput = %+v", in)
	}
	ref := core.Ref{Kind: KindTask, ID: "x"}
	if ov := create.CreateOverlay(ref); ov.Ref != ref || !reflect.DeepEqual(ov.Fields, map[string]any{ColStarStatus: StarStarred}) {
		t.Fatalf("CreateOverlay = %+v", ov)
	}
}

func TestFormSubmissionCommitsOneUndo(t *testing.T) {
	s := openTest(t)
	rec := create(t, s, KindTask, map[string]any{"title": "a"})
	until := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	sub := FormSubmission{Before: rec, Submitted: map[string]any{"title": "b", ColStarStatus: StarStarred, ColArchiveStatus: ArchiveArchived},
		VisibleFields: specs("title", ColStarStatus, ColArchiveStatus, ColSnoozedUntil), SnoozeUntil: &until}
	got, cs, err := s.Patch(sub.PatchInput(), owner)
	if err != nil || cs == nil {
		t.Fatalf("Patch: %v %v", cs, err)
	}
	if got.Text("title") != "b" || got.StarStatus != StarStarred || got.ArchiveStatus != ArchiveArchived || got.SnoozedUntil == nil || !got.SnoozedUntil.Equal(until) {
		t.Fatalf("patched = %+v", got)
	}
	if _, err := s.UndoLast(owner); err != nil {
		t.Fatal(err)
	}
	back, _ := s.Get(rec.Ref())
	if back.Text("title") != "a" || back.StarStatus != StarUnstarred || back.ArchiveStatus != ArchiveActive || back.SnoozedUntil != nil {
		t.Fatalf("one undo left = %+v", back)
	}
}

func TestPatchManyMatchesPatch(t *testing.T) {
	s := openTest(t)
	one := create(t, s, KindTask, map[string]any{"title": "one"})
	many := create(t, s, KindTask, map[string]any{"title": "many"})
	stale := int64(999) // a revision neither record is at
	_, single, err := s.Patch(PatchInput{Ref: one.Ref(), IfRevision: stale, Fields: map[string]any{"title": "x", ColStarStatus: StarStarred}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.PatchMany([]PatchInput{{Ref: many.Ref(), IfRevision: stale, Fields: map[string]any{"title": "x", ColStarStatus: StarStarred}}}, "", "", owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Conflicts) != 1 || len(batch.Conflicts) != 1 || single.Conflicts[0].Kind != batch.Conflicts[0].Kind ||
		!reflect.DeepEqual(single.Conflicts[0].Fields, batch.Conflicts[0].Fields) || len(single.Changes) != len(batch.Changes) {
		t.Fatalf("Patch %+v and PatchMany %+v differ", single, batch)
	}
	for i := range single.Changes {
		if a, b := single.Changes[i], batch.Changes[i]; a.Op != b.Op || a.Field != b.Field || !reflect.DeepEqual(a.New, b.New) {
			t.Fatalf("change %d: %+v vs %+v", i, a, b)
		}
	}
}

func TestPatchRollsBackOnFailure(t *testing.T) {
	s := openTest(t)
	rec := create(t, s, KindTask, map[string]any{"title": "kept"})
	// "a" sorts before "z", so "a" is applied before "z" fails.
	if _, _, err := s.Patch(PatchInput{Ref: rec.Ref(), Fields: map[string]any{"a": "partial", "z": func() {}}}, owner); err == nil {
		t.Fatal("Patch with an unstorable value succeeded")
	}
	missing := core.Ref{Kind: KindTask, ID: "missing"}
	if _, err := s.PatchMany([]PatchInput{{Ref: rec.Ref(), Fields: map[string]any{"title": "partial"}}, {Ref: missing, Fields: map[string]any{"title": "x"}}}, "", "", owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PatchMany with a missing record: %v", err)
	}
	got, _ := s.Get(rec.Ref())
	if got.Text("title") != "kept" || got.ChangeSetNumber != rec.ChangeSetNumber {
		t.Fatalf("failed patches wrote: %+v", got)
	}
	if _, has := got.Fields["a"]; has {
		t.Fatalf("partial field written: %v", got.Fields)
	}
}
