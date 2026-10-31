package main

import (
	"strings"
	"testing"
)

// The outline gets ochazuke's tree zoom (v0.43.0) with no change: z +
// draws only the selected task's subtree under a breadcrumb line, h past
// the zoom's top steps the zoom out a level onto the parent, z 0 shows
// every level. The modal draws the tree with Tree.Block, which takes the
// breadcrumb line out of the height it is given, so no row is cut.
func TestOutlineZoomsWithABreadcrumb(t *testing.T) {
	t.Parallel()
	a := pinned(t)
	press(t, a, "T")
	if a.r.Focus != outlineNode("t1") {
		t.Fatalf("T: focus %s", a.r.Focus)
	}
	tree := a.r.Tree(outlineTree)
	press(t, a, "j", "tab") // t2 under t1
	press(t, a, "k")
	if a.r.Focus != outlineNode("t1") {
		t.Fatalf("k: focus %s", a.r.Focus)
	}
	t1, t2 := stored(t, a, "t1").Title, stored(t, a, "t2").Title
	press(t, a, "z", "+")
	if tree.Zoomed() != outlineNode("t1") || len(tree.Breadcrumb()) == 0 {
		t.Fatalf("z +: zoomed %q crumb %q", tree.Zoomed(), tree.Breadcrumb())
	}
	if tree.Height() != tree.List().Height()+1 {
		t.Fatalf("Height %d, list %d: the breadcrumb line is not counted", tree.Height(), tree.List().Height())
	}
	f := frameAt(a, 120)
	if !strings.Contains(f, t1) || !strings.Contains(f, t2) {
		t.Fatalf("zoomed frame lacks the crumb %q or the row %q:\n%s", t1, t2, f)
	}
	if a.r.Focus != outlineNode("t2") {
		t.Fatalf("z +: focus %s, want the subtree's top row", a.r.Focus)
	}
	press(t, a, "h") // one level out: onto t1, the zoom on its parent
	if tree.Zoomed() == outlineNode("t1") || a.r.Focus != outlineNode("t1") {
		t.Fatalf("h: zoomed %q focus %s", tree.Zoomed(), a.r.Focus)
	}
	press(t, a, "z", "+", "z", "0")
	if tree.Zoomed() != "" || tree.Height() != tree.List().Height() {
		t.Fatalf("z 0: zoomed %q height %d list %d", tree.Zoomed(), tree.Height(), tree.List().Height())
	}
}

// A panes section sizes itself with Tree.Height: zoomed, its need counts
// the breadcrumb line, so the rows under it keep their lines.
func TestPaneSectionNeedCountsTheBreadcrumb(t *testing.T) {
	t.Parallel()
	app, _ := panesApp(t)
	groupBy(t, app, groupProjectSessionPane)
	for _, sec := range app.pv.sections {
		parent := ""
		for _, n := range sec.nodes {
			if n.Parent != "" {
				parent = n.Parent
				break
			}
		}
		if parent == "" {
			continue
		}
		before := app.sectionNeed(sec)
		if err := sec.tree.Zoom(parent); err != nil {
			t.Fatal(err)
		}
		header := 0
		if sec.name == sectionPanes {
			header = 1
		}
		if got, want := app.sectionNeed(sec), 1+header+sec.tree.List().Height()+1; got != want {
			t.Fatalf("%s zoomed: need %d, want %d (before %d)", sec.name, got, want, before)
		}
		return
	}
	t.Fatal("no section with a group row")
}
