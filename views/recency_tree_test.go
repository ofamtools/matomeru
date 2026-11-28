package views

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/widgets/tree"
)

func TestColorByRecencyAndFlagsOnLeft(t *testing.T) {
	// Pure bucket checks use a pinned mid-afternoon (local) reference so a run
	// just after midnight never sees now-10m fall on yesterday.
	refNow := time.Date(2026, 11, 27, 15, 0, 0, 0, time.Local)
	if got := RecencyBucket(refNow.Add(-10*time.Minute), refNow); got != "today" {
		t.Fatalf("today bucket = %q", got)
	}
	if got := RecencyBucket(refNow.Add(-3*24*time.Hour), refNow); got != "this_week" {
		t.Fatalf("this_week bucket = %q", got)
	}
	if got := RecencyBucket(refNow.Add(-10*24*time.Hour), refNow); got != "last_week" {
		t.Fatalf("last_week bucket = %q", got)
	}
	if got := RecencyBucket(refNow.Add(-60*24*time.Hour), refNow); got != "older" {
		t.Fatalf("older bucket = %q", got)
	}

	now := time.Now()
	w := build(t)
	schema, _ := w.store.Registry().Get(data.KindTask)
	rec := data.Record{
		Kind:      data.KindTask,
		ID:        "t1",
		Fields:    map[string]any{"title": "Recent task"},
		CreatedAt: now.Add(-10 * 24 * time.Hour),
		UpdatedAt: now.Add(-5 * time.Minute),
	}

	// Default StyleOptions: Flags placed on the left edge (col 0 CellIcons) and cell-level recency coloring.
	rec.StarStatus = data.StarStarred
	tbl := TableFromRecordsWith(schema, []data.Record{rec}, DefaultStyleOptions())
	if len(tbl.Columns) == 0 || tbl.Columns[len(tbl.Columns)-1].Key == FlagsField {
		t.Fatalf("expected no far-right Flags column when FlagsOnLeft=true, got %+v", tbl.Columns)
	}
	if !strings.Contains(tbl.Rows[0].CellIcons[tbl.Columns[0].Key], "★") {
		t.Fatalf("expected star flag in left-edge CellIcons[%s], got %+v", tbl.Columns[0].Key, tbl.Rows[0].CellIcons)
	}
	if tbl.Rows[0].CellTones["updatedAt"] != "accent" {
		t.Fatalf("expected today updatedAt tone 'accent', got %q", tbl.Rows[0].CellTones["updatedAt"])
	}
	if tbl.Rows[0].CellTones["createdAt"] != "warning" {
		t.Fatalf("expected last_week createdAt tone 'warning', got %q", tbl.Rows[0].CellTones["createdAt"])
	}

	// Opt-out FlagsOnLeft=false puts Flags at right end; ColorByRecency=off disables recency tones.
	legacyOpts := DefaultStyleOptions()
	legacyOpts.FlagsOnLeft = false
	legacyOpts.ColorByRecency = RecencyOff
	tblOff := TableFromRecordsWith(schema, []data.Record{rec}, legacyOpts)
	if tblOff.Columns[len(tblOff.Columns)-1].Key != FlagsField {
		t.Fatalf("expected Flags at last column when FlagsOnLeft=false, got %+v", tblOff.Columns)
	}
	if tblOff.Rows[0].CellTones["updatedAt"] != "" {
		t.Fatalf("expected no updatedAt recency tone when ColorByRecency=off, got %q", tblOff.Rows[0].CellTones["updatedAt"])
	}
}

func TestStickyTreeBreadcrumbs(t *testing.T) {
	tm := tree.New([]tree.Node{
		{
			ID:    "root",
			Label: "Root Project",
			Children: []tree.Node{
				{
					ID:    "parent",
					Label: "Wave 2 Chrome",
					Children: []tree.Node{
						{ID: "child1", Label: "Item 1"},
						{ID: "child2", Label: "Item 2"},
						{ID: "child3", Label: "Item 3"},
					},
				},
			},
		},
	})
	tm.PageSize = 2
	tm.SelectedID = "child3"
	tm.Offset = 0
	if got := StickyAncestorChain(tm); len(got) != 0 {
		t.Fatalf("expected no sticky breadcrumbs at Offset=0, got %v", got)
	}

	// Scroll past Root Project (row 0) and Wave 2 Chrome (row 1).
	tm.Offset = 2
	got := StickyAncestorChain(tm)
	if strings.Join(got, " > ") != "Root Project > Wave 2 Chrome" {
		t.Fatalf("sticky breadcrumbs = %v, want [Root Project, Wave 2 Chrome]", got)
	}

	schema := data.Schema{Kind: "item", Title: "Item", Plural: "Items"}
	withSticky := TreeViewWith(schema, tm, "panel", "t", ScopeIndex, true)
	if withSticky.Find("t.sticky") == nil {
		t.Fatal("expected t.sticky breadcrumb node when stickyBreadcrumbs=true")
	}
	withoutSticky := TreeViewWith(schema, tm, "panel", "t", ScopeIndex, false)
	if withoutSticky.Find("t.sticky") != nil {
		t.Fatal("expected no t.sticky breadcrumb node when stickyBreadcrumbs=false")
	}
}
