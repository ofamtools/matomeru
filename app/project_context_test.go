package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestProjectContextReviewAndFullTableControls(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver
	add := func(label, source string, fields map[string]any) *data.Record {
		t.Helper()
		fields["title"], fields["value"] = label, "https://example.org/"+label
		fields["source"], fields["task"] = source, f.subTask.Ref()
		rec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindContext, Fields: fields}, core.Actor{Kind: "agent", ID: "worker"})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	response := add("final-response", "response", map[string]any{})
	repeated := add("final-response", "response", map[string]any{})
	curated := add("curated-link", "curated", map[string]any{"official": true})
	add("legacy-screen", "screen", map[string]any{})

	d.Keys("P")
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"context")
	d.Keys("space")
	frame := d.Frame()
	root := a.View(a.State())
	for _, id := range []string{response.ID, repeated.ID, curated.ID} {
		if root.Find(projectNavNodePrefix+"context."+id) != nil {
			t.Fatalf("new item %s leaked into the expanded Project Nav", id)
		}
	}
	if !strings.Contains(frame, "New Items for Review") {
		t.Fatal("Context section did not show the review block")
	}

	selectProjectSection(t, f, "context")
	if a.Focus() != projectContextTable {
		t.Fatalf("Context section entry focus = %q", a.Focus())
	}
	d.Keys("/")
	d.Type("final-response")
	d.Keys("enter", "o", "g", "b", ")")
	d.Type("kind")
	d.Keys("enter")
	full := a.nav.tables[projectContextTable]
	if len(full.Filtered()) != 1 || full.SortField == "" || full.GroupField != "kind" {
		t.Fatalf("full Context table controls did not apply: filtered=%d sort=%q group=%q", len(full.Filtered()), full.SortField, full.GroupField)
	}
	// The main table must keep its space even when the terminal is short.
	height := 18
	measured := ui.Measure(a.View(a.State()), ui.DefaultDarkTheme(), a.Focus(), 120, height)
	if review, full := measured.Rects[projectContextReviewTable], measured.Rects[projectContextTable]; full.H <= review.H || full.Y <= review.Y || full.H < 3 {
		t.Fatalf("short terminal table geometry: review=%+v full=%+v", review, full)
	}

	d.Keys("shift+tab")
	if a.Focus() != projectContextReviewTable {
		t.Fatalf("shift+tab did not reach review table: focus=%q", a.Focus())
	}
	review := a.nav.tables[projectContextReviewTable]
	if len(review.Rows) != 3 {
		t.Fatalf("review has %d rows, want both response records and curated item", len(review.Rows))
	}
	d.Keys("r", "r", "r")
	for _, rec := range []*data.Record{response, repeated, curated} {
		got, err := a.Store.Get(rec.Ref())
		if err != nil || got.IsUnreadBy("readAt") {
			t.Fatalf("mark read did not acknowledge %s: err=%v", rec.ID, err)
		}
	}
	root = a.View(a.State())
	if root.Find(projectContextReviewTable) != nil || a.Focus() != projectContextTable {
		t.Fatalf("acknowledgement left the review block or stale focus %q", a.Focus())
	}

	add("another-response", "response", map[string]any{})
	a.cfg.userSettings[SetProjectContextReview] = false
	a.reloadSettings()
	a.invalidateProjectUnreadCaches("")
	root = a.View(a.State())
	if root.Find(projectContextReviewTable) != nil || root.Find(projectContextTable) == nil {
		t.Fatal("project.contextReview opt-out did not retain the full Context table")
	}
}
