package views

import (
	"fmt"
	"testing"

	"ofam.dev/ochazuke/ui"
)

func homeKindGroup(key, kind string, n int) ProjectHomeKindGroup {
	g := ProjectHomeKindGroup{Key: key, Kind: kind, Title: key, Count: n}
	for i := range n {
		g.Items = append(g.Items, ProjectHomeItem{ID: fmt.Sprintf("%s%s.%d", ProjectHomeItemPrefix, key, i), Title: fmt.Sprintf("%s %d", kind, i)})
	}
	return g
}

func TestProjectHomeLayoutCapsCardsAndOmitsEmptyStarred(t *testing.T) {
	root := ProjectHomeView(ProjectHomeSpec{Interactive: true, Layout: &ProjectHomeLayout{
		Starred:  []ProjectHomeKindGroup{homeKindGroup("tasks", "task", 0)},
		Lists:    []ProjectHomeKindGroup{homeKindGroup("tasks", "task", 12), homeKindGroup("notes", "note", 2), homeKindGroup("pages", "page", 0)},
		CardMaxH: 5,
	}})
	if root.Find(ProjectHomeStarredGridID) != nil {
		t.Fatal("starred grid drawn with no starred items")
	}
	if root.Find(ProjectHomeCardPrefix+"pages") != nil {
		t.Fatal("empty type drew a card")
	}
	for _, c := range []struct {
		key    string
		viewH  int
		capped bool
		count  int
	}{{"tasks", 5, true, 12}, {"notes", 2, false, 2}} {
		scroll := root.Find(ProjectHomeCardPrefix + c.key + ".scroll")
		if scroll == nil || scroll.Kind != ui.KindScroll || scroll.Box.FixedH != c.viewH {
			t.Fatalf("%s scroll = %+v, want KindScroll FixedH %d", c.key, scroll, c.viewH)
		}
		if scroll.Facts["capped"] != c.capped || scroll.Facts["count"] != c.count {
			t.Fatalf("%s facts = %v", c.key, scroll.Facts)
		}
		first := root.Find(fmt.Sprintf("%s%s.0", ProjectHomeItemPrefix, c.key))
		last := root.Find(fmt.Sprintf("%s%s.%d", ProjectHomeItemPrefix, c.key, c.count-1))
		if first.Facts["atTop"] != true || first.Facts["atBottom"] == true || last.Facts["atBottom"] != true || !last.Focusable {
			t.Fatalf("%s edge facts: first=%v last=%v", c.key, first.Facts, last.Facts)
		}
	}
}

func TestProjectHomeLayoutStarredGridColumns(t *testing.T) {
	starred := []ProjectHomeKindGroup{
		homeKindGroup("s.tasks", "task", 2), homeKindGroup("s.panes", "pane", 1),
		homeKindGroup("s.lanes", "lane", 0), homeKindGroup("s.notes", "note", 1), homeKindGroup("s.pages", "page", 3),
	}
	root := ProjectHomeView(ProjectHomeSpec{Interactive: true, Layout: &ProjectHomeLayout{Starred: starred}})
	grid := root.Find(ProjectHomeStarredGridID + ".grid")
	if grid == nil || grid.Kind != ui.KindGrid || grid.Cols != ProjectHomeStarredColumns {
		t.Fatalf("grid = %+v, want %d-column KindGrid", grid, ProjectHomeStarredColumns)
	}
	if len(grid.Children) != 4 {
		t.Fatalf("grid cells = %d, want 4 non-empty kinds", len(grid.Children))
	}
	if panel := root.Find(ProjectHomeStarredGridID); panel.Facts["count"] != 7 {
		t.Fatalf("starred count fact = %v, want 7", panel.Facts["count"])
	}
}
