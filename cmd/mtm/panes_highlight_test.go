package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var reverseVideo = regexp.MustCompile(`\x1b\[(?:[0-9]+;)*7(?:;[0-9]+)*m`)

// standouts are the sidebar rows drawn in reverse video (mono theme), one
// per section list that marks a cursor.
func standouts(t *testing.T, a *App) []string {
	t.Helper()
	frameWH(a, 160, 40)
	var rows []string
	for _, line := range strings.Split(a.sectionsBlock(50, 35).Text, "\n") {
		if reverseVideo.MatchString(line) {
			rows = append(rows, strings.TrimSpace(ansi.Strip(line)))
		}
	}
	return rows
}

// oneStandout fails unless exactly one sidebar row stands out and it names want.
func oneStandout(t *testing.T, a *App, want, ctx string) {
	t.Helper()
	rows := standouts(t, a)
	if len(rows) != 1 || !strings.Contains(rows[0], want) {
		t.Fatalf("%s: want exactly one standout naming %q, got %q", ctx, want, rows)
	}
}

func highlightApp(t *testing.T) (*App, []pane) {
	t.Helper()
	a, panes := sectionsApp(t)
	a.m.Appearance.Theme = "mono"
	return a, panes
}

// Gaps A and C: the leader's next/previous pane in a live terminal moves
// the one standout with the attached pane, across sections, and the
// stepped-to pane is the displayed row.
func TestPaneHighlightLeaderCycleMovesStandout(t *testing.T) {
	t.Parallel()
	a, panes := highlightApp(t)
	if err := a.r.SetFocus("pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	if err := a.attachLive(panes[4], "pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	oneStandout(t, a, "oldest", "attached")
	order := append([]string(nil), a.pv.view.order...)
	names := map[string]string{"pane:s/wwait": "waiter", "pane:s/werr": "errs", "pane:s/wdone": "finisher", "pane:s/wnew": "newest", "pane:s/wold": "oldest", "pane:s/widle": "idler", "pane:s/wterm": "term"}
	for _, step := range []string{"j", "k", "k", "k", "k", "k", "k", "j", "j"} {
		i := indexOf(order, a.attached.rowID())
		delta := 1
		if step == "k" {
			delta = -1
		}
		if i+delta < 0 || i+delta >= len(order) {
			t.Fatalf("test walks off the list at %d of %v", i, order)
		}
		want := order[i+delta]
		press(t, a, "ctrl+a", step)
		if a.r.CurrentMode().Name != "terminal" || a.r.FocusID() != paneTermID {
			t.Fatalf("%s: focus=%q mode=%q", step, a.r.FocusID(), a.r.CurrentMode().Name)
		}
		if got := a.displayedRow(); got != want {
			t.Fatalf("%s: displayed %q, want %q", step, got, want)
		}
		if got, _ := a.selectedRow(); got != want {
			t.Fatalf("%s: sidebar selects %q, want %q", step, got, want)
		}
		if name, ok := names[want]; ok {
			oneStandout(t, a, name, "after leader "+step)
		}
		a.Update(nil)
		if name, ok := names[want]; ok {
			oneStandout(t, a, name, "after leader "+step+" and an update")
		}
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// Gap A, the other leader attach rows: leader W (waiting) steps to a pane
// in another section and the standout follows, as for leader j and k.
func TestPaneHighlightLeaderWaitingMovesStandout(t *testing.T) {
	t.Parallel()
	a, panes := highlightApp(t)
	if err := a.r.SetFocus("pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	if err := a.attachLive(panes[4], "pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "ctrl+a", "W")
	if got := a.displayedRow(); got != "pane:s/wwait" {
		t.Fatalf("leader W displayed %q", got)
	}
	oneStandout(t, a, "waiter", "after leader W")
}

// Gap B: with the focus in the side pane, the standout stays on the
// parent; an orphaned side (parent gone from the listing) is closed with
// the parent's live view, leaving the sidebar on a real row, never none.
func TestPaneHighlightSideFocusMarksParent(t *testing.T) {
	t.Parallel()
	a, f := sideViewApp(t)
	a.m.Appearance.Theme = "mono"
	parent := a.attached.rowID()
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	if a.r.FocusID() != sideTermID {
		t.Fatalf("focus %q", a.r.FocusID())
	}
	a.Update(nil)
	if got := a.displayedRow(); got != parent {
		t.Fatalf("side focused: displayed %q, want parent %q", got, parent)
	}
	if got, _ := a.selectedRow(); got != parent {
		t.Fatalf("side focused: sidebar selects %q, want parent %q", got, parent)
	}
	rows := standouts(t, a)
	if len(rows) != 1 {
		t.Fatalf("side focused: standouts %q", rows)
	}
	// The parent exits: no listing holds it, and its side goes with it.
	var left []pane
	for _, p := range f.panes {
		if p.rowID() != parent {
			left = append(left, p)
		}
	}
	f.panes = left
	a.setPanes(left, nil)
	a.Update(nil)
	row, _ := a.selectedRow()
	if row == "" || a.sectionOf(row) < 0 {
		t.Fatalf("orphaned side: sidebar selects %q (no row)", row)
	}
	if rows := standouts(t, a); a.r.Page == panesPage && len(rows) != 1 {
		t.Fatalf("orphaned side: standouts %q", rows)
	}
}

// Gaps C and D: browsing, the focused list's cursor is the one standout
// (the other lists keep none), j and k spill across the lists in drawing
// order, and a list entered by a focus move shows its cursor at once and
// answers j and k.
func TestPaneHighlightBrowsingKeepsOneCursor(t *testing.T) {
	t.Parallel()
	a, _ := highlightApp(t)
	if err := a.r.SetFocus("pane:s/widle"); err != nil {
		t.Fatal(err)
	}
	oneStandout(t, a, "idler", "focused middle row")
	walk := func(key string, want ...string) {
		t.Helper()
		for _, name := range want {
			press(t, a, key)
			oneStandout(t, a, name, "after "+key)
			if row, _ := a.selectedRow(); a.displayedRow() != row {
				t.Fatalf("displayed %q != selected %q", a.displayedRow(), row)
			}
		}
	}
	walk("j", "term", "oldest", "newest")
	walk("k", "oldest", "term", "idler", "unassociated", "PR 1")
	// Entering each list by a focus move shows that list's cursor and
	// leaves the others bare, whatever each remembers.
	for i, s := range a.pv.sections {
		if s.tree.List().Len() == 0 {
			continue
		}
		if err := a.r.SetFocus(s.listID()); err != nil {
			t.Fatal(err)
		}
		a.Update(nil)
		if rows := standouts(t, a); len(rows) != 1 {
			t.Fatalf("section %d focused: standouts %q", i, rows)
		}
		before, _ := a.selectedRow()
		press(t, a, "j")
		after, _ := a.selectedRow()
		if after == before {
			t.Fatalf("section %d: j did not move off %q", i, before)
		}
		if rows := standouts(t, a); len(rows) != 1 {
			t.Fatalf("section %d after j: standouts %q", i, rows)
		}
	}
}

// Gap D opt-out: panes.remembered-highlights=true brings back every
// list's remembered cursor while browsing.
func TestPaneHighlightRememberedOptOutKeepsAllCursors(t *testing.T) {
	t.Parallel()
	a, _ := highlightApp(t)
	if err := a.r.SetFocus("pane:s/widle"); err != nil {
		t.Fatal(err)
	}
	setting(t, a, settingRememberedHighlights, true)
	if rows := standouts(t, a); len(rows) != 3 {
		t.Fatalf("opt-out standouts %q, want one per list", rows)
	}
}
