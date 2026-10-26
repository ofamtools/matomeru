package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
)

// The emulator replays a stream: the snapshot paints, output continues it,
// and the frame carries the cursor's cell, shape and visibility, which a
// capture cannot.
func TestLiveScreenReplaysTheStreamWithItsCursor(t *testing.T) {
	var l liveScreen
	if _, ok := l.frame(); ok {
		t.Fatal("a frame before the snapshot")
	}
	l.apply(daemon.Frame{Type: 'O', Data: []byte("lost")}) // no snapshot yet: ignored
	l.apply(daemon.Frame{Type: 'S', Cols: 20, Rows: 4, Data: []byte("$ ls\r\na  b\r\n$ ")})
	f, ok := l.frame()
	if !ok || len(f.Lines) != 4 || plain(f.Lines[0]) != "$ ls" || plain(f.Lines[1]) != "a  b" {
		t.Fatalf("snapshot frame %v %q", ok, f.Lines)
	}
	if f.Cursor != (ochazuke.Pos{X: 2, Y: 2}) || f.Hidden || f.Shape != "" {
		t.Fatalf("cursor %+v hidden %v shape %q", f.Cursor, f.Hidden, f.Shape)
	}
	l.apply(daemon.Frame{Type: 'O', Data: []byte("vim\x1b[5 q")})
	if f, _ = l.frame(); f.Cursor != (ochazuke.Pos{X: 5, Y: 2}) || f.Shape != "bar" || plain(f.Lines[2]) != "$ vim" {
		t.Fatalf("after output: cursor %+v shape %q line %q", f.Cursor, f.Shape, f.Lines[2])
	}
	l.apply(daemon.Frame{Type: 'O', Data: []byte("\x1b[?25l")})
	if f, _ = l.frame(); !f.Hidden {
		t.Fatal("cursor should be hidden")
	}
	l.apply(daemon.Frame{Type: 'R', Cols: 30, Rows: 6})
	if f, _ = l.frame(); len(f.Lines) != 6 {
		t.Fatalf("after resize %d lines", len(f.Lines))
	}
	l.close()
	if _, ok := l.frame(); ok {
		t.Fatal("a frame after close")
	}
}

func plain(line string) string { return strings.TrimRight(ansi.Strip(line), " ") }

// A live pane's cursor reaches the terminal: the frame's cell, offset by
// the panel the terminal is drawn in.
func TestLivePaneCursorReachesTheView(t *testing.T) {
	app, f := panesApp(t)
	cmd := key(t, app, "enter")
	f.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"$ PROMPT"}, Cursor: ochazuke.Pos{X: 2, Y: 0}}
	runOnce(app, cmd)
	app.m.Width, app.m.Height = 120, 30
	v := app.m.View()
	if v.Cursor == nil {
		t.Fatal("no cursor on a focused live pane")
	}
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	row := lines[v.Cursor.Y]
	i := strings.Index(row, "$ PROMPT")
	if i < 0 || ansi.StringWidth(row[:i])+2 != v.Cursor.X {
		t.Fatalf("cursor %+v not after the prompt on row %q", v.Cursor.Position, row)
	}
	if v.Cursor.Shape != tea.CursorBlock {
		t.Fatalf("shape %v", v.Cursor.Shape)
	}
}

// A parked stream is given back once the preview leaves its pane: another
// page, or another pane attached.
func TestHeldStreamDropsWhenThePreviewLeaves(t *testing.T) {
	app, f := panesApp(t)
	key(t, app, "enter")
	press(t, app, "ctrl+a", "d")
	first := f.streams[0]
	if !first.parked || first.released {
		t.Fatalf("detach: parked %v released %v", first.parked, first.released)
	}
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if !first.released || app.held.stream != nil {
		t.Fatalf("leaving the page kept the lease: released %v", first.released)
	}
	if err := app.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	key(t, app, "enter")
	press(t, app, "ctrl+a", "d")
	key(t, app, "enter")
	if second := f.streams[1]; !second.released || f.streams[2].released {
		t.Fatalf("a new attach should drop the parked stream: second released %v", second.released)
	}
}
