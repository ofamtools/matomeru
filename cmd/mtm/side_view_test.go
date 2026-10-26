package main

import (
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
		key(t, a, "x")
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
