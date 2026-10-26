package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// Scrollback is the fake's capture: the window's backs entry, else its
// screen with no history.
func (f *fakePanes) Scrollback(_ context.Context, p pane, _ int) (scrollback, error) {
	if f.err != nil {
		return scrollback{}, f.err
	}
	if b, ok := f.backs[p.ID]; ok {
		return b, nil
	}
	return scrollback{Text: f.screens[p.ID]}, nil
}

// copyApp is the panes page with w1's scrollback set: history lines then
// screen lines, and a clipboard fake.
func copyApp(t *testing.T, history, screen []string) (*App, *fakePanes, *fakeSystem) {
	t.Helper()
	app, f := panesApp(t)
	sys := &fakeSystem{}
	app.cx.sys = sys
	f.screens["w1"] = strings.Join(screen, "\n")
	f.backs = map[string]scrollback{"w1": {Text: strings.Join(append(append([]string(nil), history...), screen...), "\n"), History: len(history)}}
	settle(app, follow(app)) // the Screen tab's preview of w1
	frame(app)               // a drawn frame places the copy stop
	return app, f, sys
}

// enterCopy presses l on the selected pane row and lands the scrollback.
func enterCopyMode(t *testing.T, app *App) {
	t.Helper()
	_, cmd := app.Update(keyMsg("l"))
	if app.m.Status != "" {
		t.Fatalf("l: %s", app.m.Status)
	}
	settle(app, cmd)
	if app.r.Focus != copyID || !app.copy.active || app.r.Mode.Name != "normal" {
		t.Fatalf("after l: focus %s active %v mode %s", app.r.Focus, app.copy.active, app.r.Mode.Name)
	}
}

// at fails unless the copy cursor is at row, col.
func at(t *testing.T, app *App, keys string, row, col int) {
	t.Helper()
	b := app.copy.buf
	if b.row != row || b.col != col {
		t.Fatalf("after %s: cursor %d,%d, want %d,%d (line %q)", keys, b.row, b.col, row, col, b.lines[min(b.row, len(b.lines)-1)])
	}
}

// pressAll presses keys and runs the work each one starts (a yank's
// clipboard, a scrollback load) to completion.
func pressAll(t *testing.T, app *App, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_, cmd := app.Update(keyMsg(k))
		if app.m.Status != "" {
			t.Fatalf("key %q: %s", k, app.m.Status)
		}
		settle(app, cmd)
	}
}

// keysAt presses each key of a space-separated list, then checks the cursor.
func keysAt(t *testing.T, app *App, keys string, row, col int) {
	t.Helper()
	press(t, app, strings.Fields(keys)...)
	at(t, app, keys, row, col)
}

func TestCopyModeFromTheListIsFocusedNotLive(t *testing.T) {
	app, f, _ := copyApp(t, []string{"old line"}, []string{"$ echo hi", "hi", "$ "})
	enterCopyMode(t, app)
	b := app.copy.buf
	if !reflect.DeepEqual(b.lines, []string{"old line", "$ echo hi", "hi", "$"}) {
		t.Fatalf("buffer = %q", b.lines)
	}
	// The cursor starts on the screen's last line with text; the screen
	// stays where the preview drew it, the history above.
	at(t, app, "l", 3, 0)
	if b.top != 1 {
		t.Fatalf("top = %d, want the screen's first line", b.top)
	}
	// Keys do not reach a pane: nothing is attached.
	press(t, app, "k", "x", "d")
	if len(f.streams) != 0 || app.r.Terminal(paneTermID) != nil {
		t.Fatalf("copy mode attached: %+v", f.streams)
	}
	fr := frame(app)
	for _, want := range []string{"matomeru/claude · copy mode · 3/4", "copy mode · i live · esc list", "$ echo hi"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("frame lacks %q:\n%s", want, fr)
		}
	}
	// i goes live; ctrl+a [ comes back to copy mode on the live screen;
	// esc returns to the list and detaches.
	press(t, app, "i")
	if app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" || app.copy.active {
		t.Fatalf("after i: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
	press(t, app, "ctrl+a", "[")
	if app.r.Focus != copyID || app.r.Terminal(paneTermID) == nil || app.copy.source != paneTermID {
		t.Fatalf("after ctrl+a [: focus %s source %q", app.r.Focus, app.copy.source)
	}
	press(t, app, "esc")
	if app.r.Focus != "pane:matomeru/w1" || app.r.Terminal(paneTermID) != nil || app.copy.active || app.r.Mode.Name != "normal" {
		t.Fatalf("after esc: focus %s terminal %v", app.r.Focus, app.r.Terminal(paneTermID) != nil)
	}
	// } reaches the copy stop from the list too; q leaves like esc.
	frame(app)
	press(t, app, "}")
	if app.r.Focus != copyID || !app.copy.active {
		t.Fatalf("after }: focus %s", app.r.Focus)
	}
	press(t, app, "q")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("after q: focus %s", app.r.Focus)
	}
}

// Live in a shell that printed 200 lines, ctrl+a [ keeps the screen where
// it was; k moves into the scrollback, /word finds it, V y yanks it into
// register 0 and the clipboard; the pane stays attached and i is live.
func TestCopyModeFromLiveKeepsTheScreenAndYanksLines(t *testing.T) {
	var all []string
	for i := 1; i <= 200; i++ {
		all = append(all, fmt.Sprintf("line %03d", i))
	}
	all[50] = "line 051 needle here"
	screen := append(append([]string(nil), all[190:]...), "$ ")
	app, f, sys := copyApp(t, all[:190], screen)
	cmd := key(t, app, "enter")
	s := f.streams[0]
	s.frames <- ochazuke.TerminalFrame{Lines: screen, Cursor: ochazuke.Pos{X: 2, Y: 10}}
	runOnce(app, cmd)
	live := frame(app)
	_, cmd2 := app.Update(keyMsg("ctrl+a"))
	_, cmd2 = app.Update(keyMsg("["))
	settle(app, cmd2)
	if app.m.Status != "" || app.r.Focus != copyID {
		t.Fatalf("ctrl+a [: %s focus %s", app.m.Status, app.r.Focus)
	}
	at(t, app, "ctrl+a [", 200, 0) // the terminal cursor's line, on the prompt
	copied := frame(app)
	if got, want := bodyLines(copied), bodyLines(live); !reflect.DeepEqual(got, want) {
		t.Fatalf("the screen moved:\nlive\n%s\ncopy\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
	keysAt(t, app, "k", 199, 0)
	keysAt(t, app, "1 5 k", 184, 0)
	press(t, app, "/")
	typeText(t, app, "needle")
	press(t, app, "enter")
	at(t, app, "/needle", 50, 9)
	pressAll(t, app, "V", "j", "y")
	want := "line 051 needle here\nline 052\n"
	if y, err := app.r.ReadYank("0"); err != nil || y.Text != want || y.Shape != "line" {
		t.Fatalf("register 0 = %+v, %v", y, err)
	}
	if !reflect.DeepEqual(sys.copied, []string{want}) {
		t.Fatalf("clipboard = %q", sys.copied)
	}
	if app.r.Mode.Name != "normal" || app.r.Focus != copyID {
		t.Fatalf("after y: mode %s focus %s", app.r.Mode.Name, app.r.Focus)
	}
	// New output while copy mode is on lands in the buffer's tail.
	deliverFrame(t, app, ochazuke.TerminalFrame{Lines: append(append([]string(nil), screen[2:]...), "new output", "$ ")})
	if b := app.copy.buf; b.lines[len(b.lines)-2] != "new output" || b.row != 50 {
		t.Fatalf("after new output: tail %q, row %d", b.lines[len(b.lines)-3:], b.row)
	}
	press(t, app, "i")
	if app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" || s.released {
		t.Fatalf("after i: focus %s mode %s released %v", app.r.Focus, app.r.Mode.Name, s.released)
	}
}

// deliverFrame lands a frame on the attached terminal's running read, as
// the stream would.
func deliverFrame(t *testing.T, app *App, f ochazuke.TerminalFrame) {
	t.Helper()
	for _, j := range app.r.Jobs() {
		if j.Owner == paneTermID && j.State == ochazuke.JobRunning {
			app.Update(ochazuke.Completion{Ticket: ochazuke.Ticket{Owner: j.Owner, Job: j.Job, Slot: j.Slot}, Value: f})
			return
		}
	}
	t.Fatal("the terminal has no read running")
}

// bodyLines is the frame's panel body: the lines between the tab line and
// the panel's bottom border, right of the tree.
func bodyLines(fr string) []string {
	var out []string
	lines := strings.Split(fr, "\n")
	start := -1
	for i, l := range lines {
		if strings.Contains(l, "[Screen]") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	for _, l := range lines[start:] {
		i := strings.Index(l, "│")
		j := strings.LastIndex(l, "│")
		if i < 0 {
			i = strings.Index(l, "┃")
			j = strings.LastIndex(l, "┃")
		}
		if i < 0 || j <= i {
			break
		}
		out = append(out, strings.TrimRight(l[i+len("│"):j], " "))
	}
	return out
}

func TestCopyModeMotionsTakeCounts(t *testing.T) {
	lines := []string{
		"  alpha beta.gamma delta", // 0
		"(one [two] three)",        // 1
		"",                         // 2
		"foo-bar baz_qux  end",     // 3
		"x=1; y=2; z=3",            // 4
		"",                         // 5
		"last paragraph line",      // 6
		"zzz",                      // 7
	}
	app, _, _ := copyApp(t, nil, lines)
	enterCopyMode(t, app)
	keysAt(t, app, "g g", 0, 2) // gg lands on the first non-blank
	keysAt(t, app, "0", 0, 0)
	keysAt(t, app, "^", 0, 2)
	keysAt(t, app, "w", 0, 8)
	keysAt(t, app, "2 w", 0, 13) // beta . gamma: 'beta' -> '.' -> 'gamma'
	keysAt(t, app, "b", 0, 12)
	keysAt(t, app, "B", 0, 8)
	keysAt(t, app, "W", 0, 19)
	keysAt(t, app, "e", 0, 23)
	keysAt(t, app, "g e", 0, 17)
	keysAt(t, app, "E", 0, 23)
	keysAt(t, app, "0 3 l", 0, 3)
	keysAt(t, app, "$", 0, 23)
	keysAt(t, app, "h", 0, 22)
	keysAt(t, app, "j", 1, 16) // the goal column clamps to the line
	keysAt(t, app, "0 %", 1, 16)
	keysAt(t, app, "%", 1, 0)
	keysAt(t, app, "f t", 1, 6)
	keysAt(t, app, ";", 1, 11)
	keysAt(t, app, ",", 1, 6)
	keysAt(t, app, "t ]", 1, 8)
	keysAt(t, app, "F (", 1, 0)
	keysAt(t, app, "4 G", 3, 0)
	keysAt(t, app, "2 f a", 3, 9)
	keysAt(t, app, "T b", 3, 9)
	keysAt(t, app, "g _", 3, 19)
	keysAt(t, app, "G", 7, 0)
	keysAt(t, app, "{", 5, 0)
	keysAt(t, app, "2 {", 0, 0)
	keysAt(t, app, "}", 2, 0)
	keysAt(t, app, "2 }", 7, 0)
	keysAt(t, app, "H", 0, 2)
	keysAt(t, app, "L", 7, 0)
	keysAt(t, app, "M", 3, 0)
	keysAt(t, app, "j W", 4, 5)
	// ? searches up; a capital makes the pattern match case.
	press(t, app, "?")
	typeText(t, app, "alpha")
	press(t, app, "enter")
	at(t, app, "?alpha", 0, 2)
	press(t, app, "/")
	typeText(t, app, "a")
	press(t, app, "enter")
	keysAt(t, app, "", 0, 6)
	keysAt(t, app, "n", 0, 11)
	keysAt(t, app, "N", 0, 6)
	keysAt(t, app, "3 n", 0, 17)
	if _, err := app.r.Execute(ochazuke.Request{Verb: "search", Noun: copyNoun, Target: copyID, Args: ochazuke.Arguments{"pattern": "ALPHA"}}); err == nil || !strings.Contains(err.Error(), "pattern not found: ALPHA") {
		t.Fatalf("a capital matches case: %v", err)
	}
}

func TestCopyModeScrollsByPages(t *testing.T) {
	var lines []string
	for i := range 100 {
		lines = append(lines, fmt.Sprintf("row %d", i))
	}
	app, _, _ := copyApp(t, lines[:90], lines[90:])
	enterCopyMode(t, app)
	frame(app)
	h := app.copy.buf.height
	keysAt(t, app, "g g", 0, 0)
	press(t, app, "ctrl+d")
	if b := app.copy.buf; b.row != h/2 || b.top != h/2 {
		t.Fatalf("ctrl+d: row %d top %d, height %d", b.row, b.top, h)
	}
	press(t, app, "ctrl+u")
	if b := app.copy.buf; b.row != 0 || b.top != 0 {
		t.Fatalf("ctrl+u: row %d top %d", b.row, b.top)
	}
	press(t, app, "2", "ctrl+f")
	if b := app.copy.buf; b.top != 2*(h-2) {
		t.Fatalf("2 ctrl+f: top %d, height %d", b.top, h)
	}
	press(t, app, "ctrl+b")
	if b := app.copy.buf; b.top != h-2 {
		t.Fatalf("ctrl+b: top %d", b.top)
	}
	top := app.copy.buf.top
	press(t, app, "3", "ctrl+e")
	if b := app.copy.buf; b.top != top+3 || b.row < b.top {
		t.Fatalf("3 ctrl+e: top %d row %d", b.top, b.row)
	}
	press(t, app, "ctrl+y")
	if b := app.copy.buf; b.top != top+2 {
		t.Fatalf("ctrl+y: top %d", b.top)
	}
}

// v, V and ctrl+v each yank the right text; a block of columns 5-10 over
// three lines is three lines of six characters; o swaps the ends; $ runs a
// block to the lines' ends; a count works inside a selection.
func TestCopyModeVisualShapesYank(t *testing.T) {
	lines := []string{"0123456789abcdef", "ABCDEFGHIJKLMNOP", "qrstuvwxyzQRSTUV", "short"}
	app, _, sys := copyApp(t, nil, lines)
	enterCopyMode(t, app)
	yank := func(keys, want, shape string) {
		t.Helper()
		pressAll(t, app, strings.Fields(keys)...)
		y, err := app.r.ReadYank("0")
		if err != nil || y.Text != want || y.Shape != shape {
			t.Fatalf("%s: register 0 = %+v, %v; want %q (%s)", keys, y, err, want, shape)
		}
		if got := sys.copied[len(sys.copied)-1]; got != want {
			t.Fatalf("%s: clipboard %q", keys, got)
		}
		if app.r.Mode.Name != "normal" {
			t.Fatalf("%s: mode %s after the yank", keys, app.r.Mode.Name)
		}
	}
	yank("g g 0 5 l ctrl+v 2 j 5 l y", "56789a\nFGHIJK\nvwxyzQ", "block")
	at(t, app, "block yank", 0, 5) // the cursor goes to the block's corner
	yank("v e y", "56789abcdef", "char")
	yank("j 2 l v j o h y", "GHIJKLMNOP\nqrstuvwx", "char")
	yank("g g V j y", "0123456789abcdef\nABCDEFGHIJKLMNOP\n", "line")
	yank("g g 0 1 2 l ctrl+v 3 j $ y", "cdef\nMNOP\nSTUV\n", "block")
	yank("g g 2 Y", "0123456789abcdef\nABCDEFGHIJKLMNOP\n", "line")
	// esc ends a selection; esc again leaves copy mode.
	press(t, app, "v", "esc")
	if app.r.Mode.Name != "normal" || app.r.Focus != copyID {
		t.Fatalf("v esc: mode %s focus %s", app.r.Mode.Name, app.r.Focus)
	}
}

// The footer names copy mode's actions, innermost first, with no motions.
func TestCopyModeFooterHasNoMotions(t *testing.T) {
	app, _, _ := copyApp(t, nil, []string{"one", "two"})
	enterCopyMode(t, app)
	hints := ochazuke.BindingHints(app.r, app.r.Focus)
	for _, want := range []string{"i live", "q back", "v select", "/ search"} {
		if !strings.Contains(hints, want) {
			t.Fatalf("footer lacks %q: %s", want, hints)
		}
	}
	for _, motion := range []string{"copy.move", "copy.scroll", "copy.next", "copy.find", "ctrl+d"} {
		if strings.Contains(hints, motion) {
			t.Fatalf("footer shows the motion %q: %s", motion, hints)
		}
	}
	press(t, app, "v")
	if hints := ochazuke.BindingHints(app.r, app.r.Focus); !strings.Contains(hints, "y yank") {
		t.Fatalf("visual footer: %s", hints)
	}
}

// Copy mode works zoomed: ctrl+a z then ctrl+a [ draws the buffer over the
// page body, and i is live in the zoom again.
func TestCopyModeInZoom(t *testing.T) {
	app, f, _ := copyApp(t, []string{"history"}, []string{"$ ls", "a b c"})
	cmd := key(t, app, "enter")
	f.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"$ ls", "a b c"}, Cursor: ochazuke.Pos{X: 0, Y: 1}}
	runOnce(app, cmd)
	pressAll(t, app, "ctrl+a", "z", "ctrl+a", "[")
	if fr := frame(app); !strings.Contains(fr, "copy mode · 3/3 · zoomed") || !strings.Contains(fr, "a b c") {
		t.Fatalf("zoomed copy frame:\n%s", fr)
	}
	press(t, app, "i")
	if !app.pv.zoom || app.r.Focus != paneTermID {
		t.Fatalf("i: zoom %v focus %s", app.pv.zoom, app.r.Focus)
	}
}

// The buffer holds at most copyCap lines: the oldest history goes.
func TestCopyModeCapsTheScrollback(t *testing.T) {
	var history []string
	for i := range copyCap + 50 {
		history = append(history, fmt.Sprint(i))
	}
	app, _, _ := copyApp(t, history, []string{"$"})
	enterCopyMode(t, app)
	b := app.copy.buf
	if len(b.lines) != copyCap || b.lines[0] != "51" || b.lines[len(b.lines)-1] != "$" || b.row != copyCap-1 {
		t.Fatalf("buffer: %d lines, first %q, last %q, row %d", len(b.lines), b.lines[0], b.lines[len(b.lines)-1], b.row)
	}
}

// With a side pane open, ctrl+a [ in the side copies the side pane and the
// parent keeps its live view; i goes back to the side; ctrl+a [ in the
// parent copies the parent beside the side.
func TestCopyModeWithASidePane(t *testing.T) {
	app, f := sideViewApp(t)
	app.cx.sys = &fakeSystem{}
	if err := app.showSide(app.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	pressAll(t, app, "ctrl+a", "[")
	if app.r.Focus != copyID || app.copy.source != sideTermID || app.copy.pane.ID != "side-vim" {
		t.Fatalf("side ctrl+a [: focus %s source %q pane %s", app.r.Focus, app.copy.source, app.copy.pane.ID)
	}
	if fr := frame(app); !strings.Contains(fr, "side · ") || !strings.Contains(fr, "copy mode") || !strings.Contains(fr, "PARENT SCREEN") {
		t.Fatalf("side copy frame:\n%s", fr)
	}
	press(t, app, "i")
	if app.r.Focus != sideTermID || app.copy.active {
		t.Fatalf("i: focus %s", app.r.Focus)
	}
	if err := app.r.SetFocus(paneTermID); err != nil {
		t.Fatal(err)
	}
	pressAll(t, app, "ctrl+a", "[")
	if app.copy.source != paneTermID || app.copy.pane.ID != "w1" || app.r.Terminal(sideTermID) == nil {
		t.Fatalf("parent ctrl+a [: source %q pane %s", app.copy.source, app.copy.pane.ID)
	}
	if fr := frame(app); !strings.Contains(fr, "matomeru/claude · copy mode") || !strings.Contains(fr, "side vim") {
		t.Fatalf("parent copy frame beside the side:\n%s", fr)
	}
	if len(f.killed) != 0 {
		t.Fatalf("copy mode killed panes: %v", f.killed)
	}
}
