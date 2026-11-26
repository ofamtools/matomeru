package app

import (
	"slices"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// TestFormSubmitIsOneUndoStep edits a field and the star, archive and snooze
// statuses in one Edit form submit: it writes one change set, and one undo
// takes every part of it back.
func TestFormSubmitIsOneUndoStep(t *testing.T) {
	a, d := demo(t)
	d.Keys("2")
	task := titled(t, a, data.KindTask, "ochazuke-v2")
	d.Keys("g", "g", "e")
	rf := a.activeRecordForm()
	if rf == nil || rf.ref == nil || *rf.ref != task.Ref() {
		t.Fatalf("edit form = %+v", rf)
	}
	fm := rf.Model()
	for _, name := range []string{data.ColStarStatus, data.ColArchiveStatus, data.ColSnoozedUntil} {
		if !slices.ContainsFunc(fm.Specs, func(sp core.ParamSpec) bool { return sp.Name == name }) {
			t.Fatalf("edit form does not show %s", name)
		}
	}
	before, _ := a.Store.LatestChangeSetNumber()
	values := map[string]any{}
	for k, v := range fm.Values {
		values[k] = v
	}
	values["title"] = "ochazuke-v3"
	values[data.ColStarStatus] = data.StarStarred
	values[data.ColArchiveStatus] = data.ArchiveArchived
	values[data.ColSnoozedUntil] = "2030-01-02 09:00"
	if !a.submitForm(rf, values) {
		t.Fatalf("submit refused: %q", a.Status())
	}
	after, _ := a.Store.LatestChangeSetNumber()
	if after != before+1 {
		t.Fatalf("submit wrote %d change sets, want 1", after-before)
	}
	cur, _ := a.Store.Get(task.Ref())
	want, _ := parseSnoozeTime("2030-01-02 09:00")
	if cur.Text("title") != "ochazuke-v3" || cur.StarStatus != data.StarStarred || cur.ArchiveStatus != data.ArchiveArchived ||
		cur.SnoozedUntil == nil || !cur.SnoozedUntil.Equal(want) {
		t.Fatalf("after submit = %+v", cur)
	}
	if _, err := a.Store.UndoLast(User); err != nil {
		t.Fatal(err)
	}
	cur, _ = a.Store.Get(task.Ref())
	if cur.Text("title") != "ochazuke-v2" || cur.StarStatus != task.StarStatus || cur.ArchiveStatus != task.ArchiveStatus ||
		!sameTime(cur.SnoozedUntil, task.SnoozedUntil) {
		t.Fatalf("one undo left = %+v", cur)
	}
}

func sameTime(x, y *time.Time) bool {
	return (x == nil && y == nil) || (x != nil && y != nil && x.Equal(*y))
}
