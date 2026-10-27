package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

func sideViewApp(t *testing.T) (*App, *fakePanes) {
	t.Helper()
	a, f := panesApp(t)
	cmd := key(t, a, "enter")
	f.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"PARENT SCREEN"}, Cursor: ochazuke.Pos{X: 1, Y: 1}}
	runOnce(a, cmd)
	vim := pane{Session: a.attached.Session, Pane: daemon.Pane{ID: "side-vim", Name: "editor"}}
	term := pane{Session: a.attached.Session, Pane: daemon.Pane{ID: "side-term", Name: "scratch-shell"}}
	f.panes = append(f.panes, vim, term)
	for _, link := range []work.Companion{
		{Window: vim.ID, Parent: a.attached.ID, Kind: "vim"},
		{Window: term.ID, Parent: a.attached.ID, Kind: "term"},
	} {
		if err := a.work.PutCompanion(link); err != nil {
			t.Fatal(err)
		}
	}
	a.setPanes(f.panes, nil)
	return a, f
}

func TestSideFocusReplacementAndDetachStack(t *testing.T) {
	a, f := sideViewApp(t)
	parent := f.streams[0]
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	vim := f.streams[1]
	if a.r.Focus != sideTermID || !a.attached.ShowVim || a.attached.SideFront != "vim" {
		t.Fatalf("vim not focused: focus %s parent %+v", a.r.Focus, a.attached)
	}
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	if a.r.Focus != paneTermID || !a.attached.ShowVim || len(f.streams) != 2 {
		t.Fatalf("same side should focus parent without hiding or reopening: focus %s parent %+v streams %d", a.r.Focus, a.attached, len(f.streams))
	}
	if err := a.showSide(a.attached, "term"); err != nil {
		t.Fatal(err)
	}
	if a.r.Focus != sideTermID || !a.attached.ShowVim || !a.attached.ShowTerm || a.attached.SideFront != "term" || !vim.released || parent.released {
		t.Fatalf("replacement lost state or parent stream: focus %s parent %+v", a.r.Focus, a.attached)
	}
	press(t, a, "ctrl+a", "d")
	if a.attached.SideFront != "vim" || a.attached.ShowTerm || a.r.Focus != sideTermID || f.streams[len(f.streams)-1].pane.ID != "side-vim" {
		t.Fatalf("first close did not restore vim: focus %s parent %+v", a.r.Focus, a.attached)
	}
	press(t, a, "ctrl+a", "d")
	if a.r.Terminal(sideTermID) != nil || a.attached.ShowVim || a.r.Focus != paneTermID || parent.released {
		t.Fatalf("second close did not hide only sidebar: focus %s parent %+v", a.r.Focus, a.attached)
	}
	press(t, a, "ctrl+a", "d")
	if !parent.parked || parent.released || a.r.Terminal(paneTermID) != nil || a.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("final close did not detach parent: focus %s parked %v released %v", a.r.Focus, parent.parked, parent.released)
	}
	if len(f.killed) != 0 {
		t.Fatalf("hiding killed daemon panes: %v", f.killed)
	}
}

func TestSideRefreshFallsBackAfterExit(t *testing.T) {
	a, f := sideViewApp(t)
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	if err := a.showSide(a.attached, "term"); err != nil {
		t.Fatal(err)
	}
	f.panes = f.panes[:len(f.panes)-1] // the side terminal exited on its own
	a.setPanes(f.panes, nil)
	if a.attached.SideTerm != "" || a.attached.SideFront != "vim" || a.r.Focus != sideTermID || f.streams[len(f.streams)-1].pane.ID != "side-vim" {
		t.Fatalf("refresh failed to restore surviving side: focus %s parent %+v", a.r.Focus, a.attached)
	}
	f.panes = f.panes[:len(f.panes)-1]
	a.setPanes(f.panes, nil)
	if a.r.Terminal(sideTermID) != nil || a.r.Focus != paneTermID || a.attached.SideVim != "" {
		t.Fatalf("last side exit did not return to parent: focus %s parent %+v", a.r.Focus, a.attached)
	}
}

func TestSideLayoutAndFocusedZoomCursor(t *testing.T) {
	a, f := sideViewApp(t)
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	f.streams[1].frames <- ochazuke.TerminalFrame{Lines: []string{"EDITOR SCREEN"}, Cursor: ochazuke.Pos{X: 1, Y: 1}}
	runOnce(a, a.r.PendingCommands())
	for _, tt := range []struct {
		name          string
		width, height int
		cursor        ochazuke.Pos
	}{
		// 240 wide: the tree 72, the detail 167, so the side sits right of
		// the parent (83 + gap) inside it; the cursor is 2 in from the
		// panel's left edge plus the side's own X of 1.
		{name: "split", width: 240, height: 30, cursor: ochazuke.Pos{X: 72 + 1 + 83 + 1 + 3, Y: 2}},
		// 200 wide: the detail (139) is under sideSplit, so the side stacks
		// under the parent at the detail's width.
		{name: "stacked at 200", width: 200, height: 30, cursor: ochazuke.Pos{X: 60 + 1 + 3, Y: 17}},
		{name: "stacked detail", width: 120, height: 30, cursor: ochazuke.Pos{X: 36 + 1 + 3, Y: 17}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			block := a.renderPanes(a.r, tt.width, tt.height)
			text := ansi.Strip(block.Text)
			if !strings.Contains(text, "PARENT SCREEN") || !strings.Contains(text, "EDITOR SCREEN") {
				t.Fatalf("missing live content:\n%s", text)
			}
			if block.Cursor == nil || *block.Cursor != tt.cursor {
				t.Fatalf("cursor %v, want %+v", block.Cursor, tt.cursor)
			}
		})
	}
	press(t, a, "ctrl+a", "z")
	block := a.renderPanes(a.r, 120, 30)
	text := ansi.Strip(block.Text)
	if strings.Contains(text, "PARENT SCREEN") || !strings.Contains(text, "EDITOR SCREEN") || block.Cursor == nil || *block.Cursor != (ochazuke.Pos{X: 3, Y: 2}) {
		t.Fatalf("zoom did not render focused side with its cursor: %v\n%s", block.Cursor, text)
	}
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	block = a.renderPanes(a.r, 120, 30)
	text = ansi.Strip(block.Text)
	if strings.Contains(text, "EDITOR SCREEN") || !strings.Contains(text, "PARENT SCREEN") || block.Cursor == nil || *block.Cursor != (ochazuke.Pos{X: 3, Y: 2}) {
		t.Fatalf("zoom did not follow focus back to parent: %v\n%s", block.Cursor, text)
	}
}

func TestMainDetachReleasesSideStream(t *testing.T) {
	a, f := sideViewApp(t)
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	if err := a.closeTerminal(); err != nil {
		t.Fatal(err)
	}
	if a.r.Terminal(sideTermID) != nil || a.r.Terminal(paneTermID) != nil || !f.streams[0].released || !f.streams[1].released {
		t.Fatalf("detach left a terminal lease: parent %v side %v", f.streams[0].released, f.streams[1].released)
	}
}

func TestSessionKillRefusesLiveCompanionsWithoutDetaching(t *testing.T) {
	a, f := sideViewApp(t)
	if text := ansi.Strip(a.renderPanes(a.r, 120, 30).Text); !strings.Contains(text, "claude v t") || strings.Contains(text, "scratch-shell") {
		t.Fatalf("parent badges or companion row filtering missing:\n%s", text)
	}
	killSession := func() {
		t.Helper()
		if err := a.pv.tree.Select("session:matomeru"); err != nil {
			t.Fatal(err)
		}
		if err := a.paneList.Focus(); err != nil {
			t.Fatal(err)
		}
		press(t, a, "d", "d")
		settle(a, key(t, a, "y"))
	}
	killSession()
	if len(f.killed) != 0 || a.r.Terminal(paneTermID) == nil || f.streams[0].released {
		t.Fatalf("blocked session kill changed live parent: killed %v released %v", f.killed, f.streams[0].released)
	}
	f.panes = f.panes[:2] // both companions were closed in their applications
	a.setPanes(f.panes, nil)
	killSession()
	if len(f.killed) != 1 || f.killed[0] != "session:matomeru" || a.r.Terminal(paneTermID) != nil || !f.streams[0].released {
		t.Fatalf("session kill after companion exit: killed %v released %v", f.killed, f.streams[0].released)
	}
}

// A side's stream opens with its region's size, before the frame that
// first draws it: the lease is the side panel's body
// (PanelContentSize), side by side and stacked, and it follows a layout
// change. Before, the stream dialled at the window's own size (the
// session's whole screen) and only shrank once drawn.
func TestSideStreamOpensAtItsRegion(t *testing.T) {
	a, f := sideViewApp(t)
	// 240 wide: the detail is 167x30, the side its right 83 columns.
	a.renderPanes(a.r, 240, 30)
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	side := f.streams[len(f.streams)-1]
	if cw, ch := ochazuke.PanelContentSize(83, 30); side.lease != [2]int{cw, ch} {
		t.Fatalf("beside: side opened with lease %v, want %dx%d", side.lease, cw, ch)
	}
	// 120 wide: the detail is 83x30, the side its bottom 15 rows.
	a.renderPanes(a.r, 120, 30)
	cw, ch := ochazuke.PanelContentSize(83, 15)
	if side.lease != [2]int{cw, ch} {
		t.Fatalf("after the layout change: side lease %v, want %dx%d", side.lease, cw, ch)
	}
	if err := a.showSide(a.attached, "term"); err != nil {
		t.Fatal(err)
	}
	term := f.streams[len(f.streams)-1]
	if term.pane.ID != "side-term" || term.lease != [2]int{cw, ch} {
		t.Fatalf("stacked: side %s opened with lease %v, want %dx%d", term.pane.ID, term.lease, cw, ch)
	}
	parent := f.streams[0]
	if pw, ph := ochazuke.PanelContentSize(83, 15); parent.lease != [2]int{pw, ph - 2} {
		t.Fatalf("parent lease %v", parent.lease)
	}
}

// The side-layout setting flips the split: leader R toggles beside and
// below (from auto, the orientation not shown), saves the setting, says
// so in the footer, and the next frame sizes both terminals to their new
// regions. beside holds down to two 40-column panels.
func TestSideLayoutFlipResizesBoth(t *testing.T) {
	a, f := sideViewApp(t)
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	parent, side := f.streams[0], f.streams[1]
	want := func(label string, pw, ph, sw, sh int) {
		t.Helper()
		pc, pr := ochazuke.PanelContentSize(pw, ph)
		sc, sr := ochazuke.PanelContentSize(sw, sh)
		if parent.lease != [2]int{pc, pr - 2} || side.lease != [2]int{sc, sr} {
			t.Fatalf("%s: parent %v side %v, want %dx%d and %dx%d", label, parent.lease, side.lease, pc, pr-2, sc, sr)
		}
	}
	a.renderPanes(a.r, 240, 30) // the detail is 167x30: auto shows beside
	want("auto, wide", 83, 30, 83, 30)
	press(t, a, "ctrl+a", "R")
	if got := a.r.Settings().String(settingSideLayout); got != sideBelow {
		t.Fatalf("from auto beside, R set %q", got)
	}
	if !slices.Contains(a.m.Notices, "side layout: below") {
		t.Fatalf("footer %q", a.m.Notices)
	}
	a.renderPanes(a.r, 240, 30)
	want("below, wide", 167, 15, 167, 15)
	press(t, a, "ctrl+a", "R")
	if got := a.r.Settings().String(settingSideLayout); got != sideBeside {
		t.Fatalf("from below, R set %q", got)
	}
	a.renderPanes(a.r, 120, 30) // the detail is 83 wide: beside still splits it
	want("beside, narrow", 41, 30, 41, 30)
	if a.sideArea(2*sideBesideMin, 30).beside || !a.sideArea(2*sideBesideMin+1, 30).beside {
		t.Fatal("beside ignores its minimum")
	}
	if err := a.r.Settings().Set(settingSideLayout, sideAuto); err != nil {
		t.Fatal(err)
	}
	a.renderPanes(a.r, 120, 30)
	want("auto, narrow", 83, 15, 83, 15)
	press(t, a, "ctrl+a", "R")
	if got := a.r.Settings().String(settingSideLayout); got != sideBeside {
		t.Fatalf("from auto below, R set %q", got)
	}
}

// Leader o moves the focus between the parent and its side, wrapping,
// as tmux's prefix o does; with no side shown it is a quiet no-op.
func TestLeaderOCyclesParentAndSide(t *testing.T) {
	a, f := sideViewApp(t)
	press(t, a, "ctrl+a", "o")
	if a.r.Focus != paneTermID || len(f.streams[0].sent) != 0 {
		t.Fatalf("no side: focus %s sent %v", a.r.Focus, f.streams[0].sent)
	}
	if err := a.showSide(a.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{paneTermID, sideTermID, paneTermID} {
		press(t, a, "ctrl+a", "o")
		if a.r.Focus != want {
			t.Fatalf("focus %s, want %s", a.r.Focus, want)
		}
	}
	if len(f.streams[0].sent) != 0 || len(f.streams[1].sent) != 0 {
		t.Fatalf("o reached a pane: %v %v", f.streams[0].sent, f.streams[1].sent)
	}
}

// Leader w opens a chooser of every open side across every pane; enter
// selects the parent, attaches it, shows that side and focuses it. A side
// whose parent exited, or is not in this listing, is not offered.
func TestSideChooserJumpsToAnySide(t *testing.T) {
	a, f := sideViewApp(t)
	other := f.panes[1] // w2, not attached
	gone := pane{Session: other.Session, Pane: daemon.Pane{ID: "gone-parent", Name: "gone"}}
	dead := pane{Session: other.Session, Exited: true, Pane: daemon.Pane{ID: "dead-parent", Name: "dead"}}
	w2vim := pane{Session: other.Session, Pane: daemon.Pane{ID: "w2-vim", Name: "editor"}}
	orphan := pane{Session: other.Session, Pane: daemon.Pane{ID: "orphan-term", Name: "orphan"}}
	deadSide := pane{Session: other.Session, Pane: daemon.Pane{ID: "dead-term", Name: "dead-side"}}
	f.panes = append(f.panes, dead, w2vim, orphan, deadSide)
	for _, link := range []work.Companion{
		{Window: w2vim.ID, Parent: other.ID, Kind: "vim"},
		{Window: orphan.ID, Parent: gone.ID, Kind: "term"},
		{Window: deadSide.ID, Parent: dead.ID, Kind: "term"},
	} {
		if err := a.work.PutCompanion(link); err != nil {
			t.Fatal(err)
		}
	}
	a.setPanes(f.panes, nil)
	press(t, a, "ctrl+a", "w")
	if a.r.TopModal() != sideChooser {
		t.Fatalf("top modal %q", a.r.TopModal())
	}
	var labels []string
	for _, row := range a.r.Chooser(sideChooser).Matches() {
		labels = append(labels, row.Label)
	}
	want := []string{"side vim · " + f.panes[0].label(), "side term · " + f.panes[0].label(), "side vim · " + other.label()}
	if !slices.Equal(labels, want) {
		t.Fatalf("rows %q, want %q", labels, want)
	}
	typeText(t, a, "ochazuke")
	press(t, a, "enter")
	if a.attached.ID != other.ID || a.r.Focus != sideTermID || a.attached.SideFront != "vim" {
		t.Fatalf("attached %s focus %s front %q", a.attached.ID, a.r.Focus, a.attached.SideFront)
	}
	if row, _ := a.selectedRow(); row != other.rowID() {
		t.Fatalf("selected %s", row)
	}
	if s := f.streams[len(f.streams)-1]; s.pane.ID != w2vim.ID {
		t.Fatalf("side stream %s", s.pane.ID)
	}
}

// With no side open, leader w says so instead of opening an empty chooser.
func TestSideChooserWithNoSides(t *testing.T) {
	a, _ := panesApp(t)
	press(t, a, "ctrl+a", "w") // the page leader row: normal mode on Panes
	if a.r.TopModal() != "" || !slices.Contains(a.m.Notices, "no side panes are open") {
		t.Fatalf("modal %q notices %q", a.r.TopModal(), a.m.Notices)
	}
}

// Outside live, ctrl+a w is a Panes page row too, and the palette reaches
// the verb.
func TestSideChooserFromPageAndPalette(t *testing.T) {
	a, _ := sideViewApp(t)
	press(t, a, "ctrl+a", "d") // detach: the focus is back on the row
	if a.r.Terminal(paneTermID) != nil {
		t.Fatal("still attached")
	}
	press(t, a, "ctrl+a", "w")
	if a.r.TopModal() != sideChooser {
		t.Fatalf("page leader w: top modal %q", a.r.TopModal())
	}
	press(t, a, "esc")
	press(t, a, ":")
	typeText(t, a, "panes sides")
	press(t, a, "enter")
	if a.r.TopModal() != sideChooser {
		t.Fatalf("palette: top modal %q", a.r.TopModal())
	}
}

// The leader's hint and its ? modal name o, R and w; space after the
// leader stays unbound, so it sends the leader and itself to the pane.
func TestLeaderHintAndKeysListTheSideKeys(t *testing.T) {
	a, f := sideViewApp(t)
	var hint string
	for _, b := range a.r.Noun("terminal").Bindings["terminal"] {
		if b.Keys == "ctrl+a" {
			hint = b.Hint
		}
	}
	for _, want := range []string{"o other", "R layout", "w sides"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint %q lacks %q", hint, want)
		}
	}
	for _, b := range a.r.Noun("terminal").Bindings["leader_pending"] {
		if b.Keys == "space" || b.Keys == "ctrl+o" {
			t.Fatalf("leader %s is bound: %+v", b.Keys, b)
		}
	}
	press(t, a, "ctrl+a", "?")
	var rows []string
	for _, it := range a.r.List(leaderList).Items() {
		rows = append(rows, it.Label)
	}
	text := strings.Join(rows, "\n")
	for _, want := range []string{"ctrl+a o ", "ctrl+a R ", "ctrl+a w ", "the other pane", "side layout", "sides: jump"} {
		if !strings.Contains(text, want) {
			t.Fatalf("leader keys lack %q:\n%s", want, text)
		}
	}
	press(t, a, "esc")
	before := len(f.streams[0].sent)
	press(t, a, "ctrl+a", "space")
	if got := f.streams[0].sent[before:]; len(got) != 1 || !slices.Equal(got[0], []string{"ctrl+a", "space"}) {
		t.Fatalf("leader space sent %v", got)
	}
}
