package app

import (
	"testing"

	"ofam.dev/ochazuke/core"
)

func setEdgeSpill(a *App, on bool) {
	a.cfg.userSettings[SetEdgeFocusSpill] = on
	a.reloadSettings()
	a.applySettings()
}

// j at the last row of a table stays with nav.edgeFocusSpill off and moves
// focus to the next region drawn with it on.
func TestEdgeSpillTable(t *testing.T) {
	for _, on := range []bool{false, true} {
		a, d := configured(t, t.TempDir())
		setEdgeSpill(a, on)
		d.Frame()
		if a.Focus() != "panes.rail.table" {
			t.Fatalf("start focus %q", a.Focus())
		}
		d.Keys("G")
		d.Keys("j")
		want := "panes.rail.table"
		if on {
			want = "site.toc.screen"
		}
		if a.Focus() != want {
			t.Fatalf("spill=%v: j at the table's bottom focused %q, want %q", on, a.Focus(), want)
		}
	}
}

// A tree at its last row reports the same edge to the same spill decision.
func TestEdgeSpillTree(t *testing.T) {
	for _, on := range []bool{false, true} {
		a, d := configured(t, t.TempDir())
		setEdgeSpill(a, on)
		d.Keys("2")
		d.Frame()
		tm, move, ok := a.motionTree(a.State(), a.Focus())
		if !ok || tm == nil {
			t.Fatalf("no work tree at %q", a.Focus())
		}
		move(core.Command{ID: "motion.bottom"})
		before := tm.SelectedID
		move(core.Command{ID: "motion.down"})
		if got := a.treeEdgeSpill(tm, before, core.Command{ID: "motion.down"}); got != on {
			t.Fatalf("spill=%v: tree bottom spilled=%v", on, got)
		}
	}
}
