package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// setting sets one setting or fails the test.
func setting(t *testing.T, a *App, name string, value any) {
	t.Helper()
	if err := a.r.Settings().Set(name, value); err != nil {
		t.Fatal(err)
	}
}

// frameMargin is the frame's left margin.
const frameMargin = "  "

// leftColumn is the Panes page's left side in a side-by-side frame of
// width 120: the lines under the "panes (N)" head down to the footer
// rule, cut to the tree's width.
func leftColumn(t *testing.T, fr string) []string {
	t.Helper()
	lines := strings.Split(fr, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, frameMargin)
	}
	head, end := -1, -1
	for i, l := range lines {
		if head < 0 && strings.HasPrefix(l, "panes (") {
			head = i
		}
		if head >= 0 && i > head && strings.HasPrefix(l, "────") {
			end = i
			break
		}
	}
	if head < 0 || end < 0 {
		t.Fatalf("no left column:\n%s", fr)
	}
	out := make([]string, 0, end-head-1)
	for _, l := range lines[head+1 : end] {
		r := []rune(l)
		out = append(out, strings.TrimRight(string(r[:min(len(r), treeWidth(120))]), " "))
	}
	return out
}

// panelSpan is the first and last line of the panel titled title in col.
func panelSpan(t *testing.T, col []string, title string) (int, int) {
	t.Helper()
	top := slices.IndexFunc(col, func(l string) bool {
		return (strings.HasPrefix(l, "╭") || strings.HasPrefix(l, "┏")) && strings.Contains(l, title)
	})
	if top < 0 {
		t.Fatalf("no panel %q:\n%s", title, strings.Join(col, "\n"))
	}
	for i := top + 1; i < len(col); i++ {
		if strings.HasPrefix(col[i], "╰") || strings.HasPrefix(col[i], "┗") {
			return top, i
		}
	}
	t.Fatalf("panel %q has no bottom:\n%s", title, strings.Join(col, "\n"))
	return 0, 0
}

func TestPanelHeights(t *testing.T) {
	for _, c := range []struct {
		name    string
		height  int
		content [3]int
		hide    bool
		want    [3]int
	}{
		{"min 6 each, the middle takes the rest", 23, [3]int{4, 2, 2}, false, [3]int{6, 11, 6}},
		{"a panel grows with its rows", 40, [3]int{7, 3, 2}, false, [3]int{9, 25, 6}},
		{"up to the cap, then it scrolls", 60, [3]int{30, 3, 30}, false, [3]int{10, 40, 10}},
		{"empty panels keep their lines", 40, [3]int{0, 3, 0}, false, [3]int{6, 28, 6}},
		{"empty panels hide", 40, [3]int{0, 3, 0}, true, [3]int{0, 40, 0}},
		{"a short page shrinks the panels for a third", 15, [3]int{4, 9, 4}, false, [3]int{5, 5, 5}},
		{"a tiny page shrinks them to a row", 7, [3]int{4, 9, 4}, false, [3]int{3, 1, 3}},
		{"a middle that needs little leaves the panels be", 15, [3]int{4, 2, 4}, false, [3]int{6, 3, 6}},
	} {
		if got := panelHeights(c.height, c.content, defaultPanelMin, defaultPanelMax, c.hide); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The panels layout at 24, 40 and 60 lines: Needs you is a panel at the
// top and Working a panel on the column's last lines, each at least 6
// lines, the panes list filling the lines between (goldens in testdata).
func TestPanesPanelsLayout(t *testing.T) {
	a, _ := sectionsApp(t)
	if err := a.r.SetFocus("pane:s/widle"); err != nil {
		t.Fatal(err)
	}
	for _, height := range []int{24, 40, 60} {
		fr := frameWH(a, 120, height)
		col := leftColumn(t, fr)
		qTop, qEnd := panelSpan(t, col, "Needs you (4)")
		wTop, wEnd := panelSpan(t, col, "Working (2)")
		if qTop != 0 || qEnd-qTop+1 < defaultPanelMin {
			t.Fatalf("%d lines: Needs you spans %d..%d:\n%s", height, qTop, qEnd, fr)
		}
		if wEnd != len(col)-1 || wEnd-wTop+1 < defaultPanelMin {
			t.Fatalf("%d lines: Working spans %d..%d of %d:\n%s", height, wTop, wEnd, len(col), fr)
		}
		middle := strings.Join(col[qEnd+1:wTop], "\n")
		for _, want := range []string{"pane", "idler", "term"} {
			if !strings.Contains(middle, want) {
				t.Fatalf("%d lines: the middle lacks %q:\n%s", height, want, fr)
			}
		}
		if got := wTop - qEnd - 1; got != height-6-2*defaultPanelMin-1 {
			t.Fatalf("%d lines: the middle is %d lines:\n%s", height, got, fr)
		}
		golden(t, fmt.Sprintf("panes-panels-%d.golden", height), fr)
	}
}

// golden compares fr with testdata/name (go test -update rewrites it).
func golden(t *testing.T, name, fr string) {
	t.Helper()
	file := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.WriteFile(file, []byte(fr), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if fr != string(want) {
		t.Fatalf("frame differs from %s:\n%s", file, fr)
	}
}

// A panel grows with its rows up to panes.panel-max and then scrolls,
// saying how many rows it hides; a count changing under the min does not
// move the rest of the column.
func TestPanesPanelsGrowToTheCap(t *testing.T) {
	a, panes := sectionsApp(t)
	if err := a.r.SetFocus("pane:s/widle"); err != nil {
		t.Fatal(err)
	}
	before := leftColumn(t, frameWH(a, 120, 40))
	_, wEnd0 := panelSpan(t, before, "Working (2)")
	wTop0, _ := panelSpan(t, before, "Working (2)")
	panes[3].AgentState = "idle" // one working fewer: the panel keeps its lines
	a.setPanes(panes, nil)
	col := leftColumn(t, frameWH(a, 120, 40))
	if top, end := panelSpan(t, col, "Working (1)"); top != wTop0 || end != wEnd0 {
		t.Fatalf("Working moved from %d..%d to %d..%d", wTop0, wEnd0, top, end)
	}
	for i := range 12 {
		panes = append(panes, pane{Session: "s", SessionID: "s", Harness: "claude", StateAt: int64(100 + i),
			Pane: daemon.Pane{ID: fmt.Sprintf("wb%02d", i), Name: fmt.Sprintf("busy%02d", i), AgentState: "working"}})
	}
	a.setPanes(panes, nil)
	col = leftColumn(t, frameWH(a, 120, 40))
	top, end := panelSpan(t, col, "Working (13)")
	if end-top+1 != defaultPanelMax || !strings.Contains(col[top], "more") {
		t.Fatalf("Working spans %d..%d:\n%s", top, end, strings.Join(col, "\n"))
	}
	setting(t, a, settingPanelMax, 14)
	col = leftColumn(t, frameWH(a, 120, 40))
	if top, end := panelSpan(t, col, "Working (13)"); end-top+1 != 14 {
		t.Fatalf("panel-max 14: Working spans %d..%d", top, end)
	}
}

// panes.empty-panels: show draws an empty panel with its empty-state
// line; hide gives its lines to the panes list.
func TestPanesPanelsEmpty(t *testing.T) {
	a, f := panesApp(t)
	_ = f
	col := leftColumn(t, frameWH(a, 120, 30))
	qTop, qEnd := panelSpan(t, col, "Needs you (0)")
	if qEnd-qTop+1 != defaultPanelMin || !strings.Contains(col[qTop+1], "Nothing needs you") {
		t.Fatalf("empty Needs you:\n%s", strings.Join(col, "\n"))
	}
	if _, wEnd := panelSpan(t, col, "Working (0)"); wEnd != len(col)-1 || !strings.Contains(strings.Join(col, "\n"), "No agent is working") {
		t.Fatalf("empty Working:\n%s", strings.Join(col, "\n"))
	}
	setting(t, a, settingEmptyPanels, emptyHide)
	fr := frameWH(a, 120, 30)
	if strings.Contains(fr, "Needs you") || strings.Contains(fr, "Working (") {
		t.Fatalf("hidden empty panels drawn:\n%s", fr)
	}
	setting(t, a, settingEmptyPanels, emptyShow)
	setting(t, a, settingPanesLayout, layoutStacked)
	if fr := frameWH(a, 120, 30); strings.Contains(fr, "Needs you") || strings.Contains(fr, "╭─ Working") {
		t.Fatalf("stacked draws panels:\n%s", fr)
	}
}

// The focused list's panel draws the heavy accent border; the others the
// plain one.
func TestPanesPanelsBorderFollowsTheFocus(t *testing.T) {
	a, _ := sectionsApp(t)
	if err := a.r.SetFocus("pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	col := leftColumn(t, frameWH(a, 120, 30))
	wTop, _ := panelSpan(t, col, "Working (2)")
	qTop, _ := panelSpan(t, col, "Needs you (4)")
	if !strings.Contains(col[wTop+1], "┃") || strings.Contains(col[qTop+1], "┃") {
		t.Fatalf("focused Working:\n%s", strings.Join(col, "\n"))
	}
}

// j and k spill across the panels and the list between them in drawing
// order; } walks the stops top to bottom.
func TestPanesPanelsFocusWalk(t *testing.T) {
	a, _ := sectionsApp(t)
	frameWH(a, 120, 40)
	queue, middle, working := sectionIDs(a, 0), sectionIDs(a, 1), sectionIDs(a, 2)
	if err := a.r.SetFocus(queue[0]); err != nil {
		t.Fatal(err)
	}
	var walk []string
	for range len(queue) + len(a.pv.sections[1].tree.Nodes()) + len(working) - 1 {
		press(t, a, "j")
		frameWH(a, 120, 40)
		walk = append(walk, a.r.Focus)
	}
	if walk[len(queue)-1] != a.pv.sections[1].tree.Nodes()[0].ID {
		t.Fatalf("j off Needs you: %v", walk)
	}
	if last := walk[len(walk)-1]; last != working[len(working)-1] {
		t.Fatalf("j walk ends on %s: %v", last, walk)
	}
	if !slices.Contains(walk, middle[0]) || !slices.Contains(walk, working[0]) {
		t.Fatalf("walk %v", walk)
	}
	press(t, a, "k")
	if a.r.Focus != working[0] {
		t.Fatalf("k: %s", a.r.Focus)
	}
}

// g n, g w and g p put the focus on the Needs you, Working and Panes
// lists (panes.focus-section); again on the same list it stays; an empty
// list says so and the focus stays; from another page it opens Panes.
func TestPanesFocusSectionKeys(t *testing.T) {
	a, panes := sectionsApp(t)
	frameWH(a, 120, 40)
	press(t, a, "g", "w")
	if a.r.Focus != "pane:s/wold" {
		t.Fatalf("g w: focus %s", a.r.Focus)
	}
	press(t, a, "j", "g", "w") // again: it stays on Working's selected row
	if a.r.Focus != "pane:s/wnew" {
		t.Fatalf("g w again: focus %s", a.r.Focus)
	}
	press(t, a, "g", "n")
	if a.r.Focus != "pane:s/wwait" {
		t.Fatalf("g n: focus %s", a.r.Focus)
	}
	press(t, a, "g", "p")
	if a.r.Focus != "pane:s/widle" {
		t.Fatalf("g p: focus %s", a.r.Focus)
	}
	if row, _ := a.selectedRow(); row != "pane:s/widle" {
		t.Fatalf("the detail follows %s", row)
	}
	// Nothing working: g w says so, the focus stays.
	for i := range panes {
		if panes[i].AgentState == "working" {
			panes[i].AgentState = "idle"
		}
	}
	a.setPanes(panes, nil)
	v, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "focus-section", Args: ochazuke.Arguments{"section": sectionWorking}})
	if err != nil || v != "No agent is working" || a.r.Focus != "pane:s/widle" {
		t.Fatalf("empty Working: %v %v focus %s", v, err, a.r.Focus)
	}
	// From another page, the verb opens Panes.
	if err := a.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "focus-section", Args: ochazuke.Arguments{"section": sectionQueue}}); err != nil {
		t.Fatal(err)
	}
	if a.r.Page != panesPage || a.r.Focus != "pane:s/wwait" {
		t.Fatalf("from Work: page %s focus %s", a.r.Page, a.r.Focus)
	}
	// The keys are binding rows on the page, so hints and help read them.
	if k := a.keyFor(a.r.Focus, "panes.focus-section"); k == "" {
		t.Fatal("no key for panes.focus-section")
	}
}

// With panes.sticky-rows off (the default) a row changing section moves
// at once, and the cursor goes with it, so the detail stays on its pane.
func TestPanesRowsMoveWithoutSticky(t *testing.T) {
	a, panes := sectionsApp(t)
	row := "pane:s/wold"
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	panes[4].AgentState = "idle"
	a.setPanes(panes, nil)
	if slices.Contains(sectionIDs(a, 2), row) || !slices.Contains(sectionIDs(a, 1), row) {
		t.Fatalf("working %v middle %v", sectionIDs(a, 2), sectionIDs(a, 1))
	}
	if a.r.Focus != row {
		t.Fatalf("focus %s, want it on the moved row", a.r.Focus)
	}
	if got, _ := a.selectedRow(); got != row {
		t.Fatalf("detail follows %s", got)
	}
	// It starts working again: back to Working, the cursor with it.
	panes[4].AgentState = "working"
	a.setPanes(panes, nil)
	if !slices.Contains(sectionIDs(a, 2), row) || a.r.Focus != row {
		t.Fatalf("working %v focus %s", sectionIDs(a, 2), a.r.Focus)
	}
}

// The lock the owner saw, with panes.sticky-rows on: a row held in
// Working while its pane was attached live stayed there for as long as it
// was attached, since the attached pane stays the active list's selection
// and the cursor never "moved off" it. A row is held only while the
// cursor is on one of the lists.
func TestPanesStickyRowsHoldOnlyUnderTheCursor(t *testing.T) {
	a, panes := sectionsApp(t)
	setting(t, a, settingStickyRows, true)
	row := "pane:s/wold"
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	settle(a, a.r.PendingCommands())
	if a.attached.ID != "wold" || a.r.Terminal(a.r.Focus) == nil {
		t.Fatalf("attached %q focus %s", a.attached.ID, a.r.Focus)
	}
	panes[4].AgentState = "idle"
	a.setPanes(panes, nil)
	if slices.Contains(sectionIDs(a, 2), row) {
		t.Fatalf("live pane held in Working: %v", sectionIDs(a, 2))
	}
	if got, _ := a.selectedRow(); got != row {
		t.Fatalf("selection %s, want the attached pane's row", got)
	}
}

// panes.working-states is data: a state word tuios does not use by
// default ("active") puts a pane in Working once the setting lists it,
// and its glyph and tone follow.
func TestPanesWorkingStatesSetting(t *testing.T) {
	a, panes := sectionsApp(t)
	if err := a.r.SetFocus("pane:s/wterm"); err != nil {
		t.Fatal(err)
	}
	panes[5].AgentState = "active"
	a.setPanes(panes, nil)
	if slices.Contains(sectionIDs(a, 2), "pane:s/widle") {
		t.Fatalf("active in Working by default: %v", sectionIDs(a, 2))
	}
	setting(t, a, settingWorkingStates, []string{"working", "active"})
	if !slices.Contains(sectionIDs(a, 2), "pane:s/widle") {
		t.Fatalf("active not in Working: %v", sectionIDs(a, 2))
	}
	p := a.panes[5]
	if !p.working() || stateGlyph(p) != "●" {
		t.Fatalf("working %v glyph %q", p.working(), stateGlyph(p))
	}
	// Waiting words too: "blocked" (OSC 7501's word) as needs input.
	setting(t, a, settingWaitingStates, []string{"needs_input", "blocked"})
	panes[5].AgentState = "blocked"
	a.setPanes(panes, nil)
	if !a.panes[5].needsInput() || stateGlyph(a.panes[5]) != "?" {
		t.Fatal("blocked does not read as needs input")
	}
	// The default words are tuios's.
	d := pane{Harness: "claude", Pane: daemon.Pane{AgentState: "working"}}
	if !d.working() || (pane{Pane: daemon.Pane{AgentState: "active"}}).working() {
		t.Fatal("defaults")
	}
}

// A cleared session, against the fake daemon: the agent works, its
// session is cleared (tuios reports it idle, a new session id, a reset
// state time), then it works again. It leaves Working at once, comes
// back placed by its new state time, and never keeps the old place.
func TestPanesClearedSessionLeavesWorking(t *testing.T) {
	a, f := panesApp(t)
	f.panes = []pane{
		{Session: "s", SessionID: "s", Harness: "claude", AgentSession: "old", StateAt: 100, Pane: daemon.Pane{ID: "wa", Name: "alpha", AgentState: "working"}},
		{Session: "s", SessionID: "s", Harness: "claude", StateAt: 200, Pane: daemon.Pane{ID: "wb", Name: "beta", AgentState: "working"}},
		{Session: "s", SessionID: "s", Pane: daemon.Pane{ID: "wt", Name: "term"}},
	}
	reload(t, a)
	if err := a.r.SetFocus("pane:s/wa"); err != nil {
		t.Fatal(err)
	}
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:s/wa", "pane:s/wb"}) {
		t.Fatalf("working %v", got)
	}
	// /clear: tuios answers idle with a new session and a fresh time.
	f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].StateAt = "idle", "new", 300
	reload(t, a)
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:s/wb"}) {
		t.Fatalf("after the clear: working %v", got)
	}
	if !strings.Contains(strings.Join(leftColumn(t, frameWH(a, 120, 40)), "\n"), "alpha") {
		t.Fatal("alpha vanished")
	}
	// It works again: placed by its new time, after beta.
	f.panes[0].AgentState, f.panes[0].StateAt = "working", 400
	reload(t, a)
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:s/wb", "pane:s/wa"}) {
		t.Fatalf("working again %v", got)
	}
	// A state time reset while it stays working keeps its place (stable
	// order); with panes.stable-order off it sorts by the new time.
	f.panes[0].StateAt = 50
	reload(t, a)
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:s/wb", "pane:s/wa"}) {
		t.Fatalf("stable: %v", got)
	}
	setting(t, a, settingStableOrder, false)
	if got := sectionIDs(a, 2); !slices.Equal(got, []string{"pane:s/wa", "pane:s/wb"}) {
		t.Fatalf("unstable: %v", got)
	}
}

// The narrow layout (the list above the detail) keeps the panels and a
// third of its lines for the panes list.
func TestPanesPanelsNarrow(t *testing.T) {
	a, _ := sectionsApp(t)
	for _, width := range []int{60, 80} {
		fr := frameWH(a, width, 40)
		for _, want := range []string{"Needs you (4)", "Working (2)", "idler"} {
			if !strings.Contains(fr, want) {
				t.Fatalf("%d columns lack %q:\n%s", width, want, fr)
			}
		}
		for _, l := range strings.Split(fr, "\n") {
			if ansi.StringWidth(l) > width {
				t.Fatalf("%d columns: a line is %d wide:\n%s", width, ansi.StringWidth(l), fr)
			}
		}
	}
}
