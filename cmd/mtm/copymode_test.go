package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// Scrollback is the fake's capture: the window's backs entry, else its
// screen with no history.
func (f *fakePanes) Scrollback(_ context.Context, p pane, _ int) (scrollback, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return scrollback{}, f.err
	}
	if b, ok := f.backs[p.ID]; ok {
		return b, nil
	}
	return scrollback{Text: f.screens[p.ID]}, nil
}

// ScrollbackWindow cuts the fake's capture as the daemon does: the last Tail
// lines (trailing blank rows are not lines), or the Count lines before
// Before. It counts the windows it was asked for.
func (f *fakePanes) ScrollbackWindow(ctx context.Context, p pane, w scrollWindow) (scrollback, error) {
	back, err := f.Scrollback(ctx, p, 0)
	if err != nil {
		return back, err
	}
	f.mu.Lock()
	f.windows = append(f.windows, w)
	f.mu.Unlock()
	lines := strings.Split(strings.TrimSuffix(back.Text, "\n"), "\n")
	if w.Count > 0 {
		start := max(0, w.Before-w.Count)
		return scrollback{Text: strings.Join(lines[start:w.Before], "\n") + "\n", History: back.History, From: start, Want: w.Before - start}, nil
	}
	body := lines
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	from := max(0, len(body)-w.Tail)
	return scrollback{Text: strings.Join(body[from:], "\n"), History: back.History, From: from}, nil
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
	if app.r.FocusID() != copyID || !app.copy.active || app.r.CurrentMode().Name != "copy" {
		t.Fatalf("after l: focus %s active %v mode %s", app.r.FocusID(), app.copy.active, app.r.CurrentMode().Name)
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
	t.Parallel()
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
	if !strings.Contains(fr, "$ echo hi") {
		t.Fatalf("copy frame lacks pane output:\n%s", fr)
	}
	// i goes live; ctrl+a [ enters copy mode; esc restores live without detaching.
	press(t, app, "i")
	if app.r.FocusID() != paneTermID || app.r.CurrentMode().Name != "terminal" || app.copy.active {
		t.Fatalf("after i: focus %s mode %s", app.r.FocusID(), app.r.CurrentMode().Name)
	}
	press(t, app, "ctrl+a", "[")
	if app.r.FocusID() != copyID || app.r.Terminal(paneTermID) == nil || app.copy.source != paneTermID {
		t.Fatalf("after ctrl+a [: focus %s source %q", app.r.FocusID(), app.copy.source)
	}
	press(t, app, "esc")
	if app.r.FocusID() != paneTermID || app.r.Terminal(paneTermID) == nil || app.copy.active || app.r.CurrentMode().Name != "terminal" || f.streams[0].released {
		t.Fatalf("after esc: focus %s terminal %v", app.r.FocusID(), app.r.Terminal(paneTermID) != nil)
	}
	press(t, app, "ctrl+a", "[", "q")
	if app.r.FocusID() != paneTermID || app.copy.active || f.streams[0].released {
		t.Fatalf("after q: focus %s active %v released %v", app.r.FocusID(), app.copy.active, f.streams[0].released)
	}
	press(t, app, "ctrl+a", "d")
	// } reaches the copy stop from the list too; q leaves like esc.
	frame(app)
	press(t, app, "}")
	if app.r.FocusID() != copyID || !app.copy.active {
		t.Fatalf("after }: focus %s", app.r.FocusID())
	}
	press(t, app, "q")
	if app.r.FocusID() != "pane:matomeru/w1" {
		t.Fatalf("after q: focus %s", app.r.FocusID())
	}
}

func TestCopyModeCursorVisibleBeforeSelection(t *testing.T) {
	t.Parallel()
	app, _, _ := copyApp(t, nil, []string{"alpha", "", "omega"})
	enterCopyMode(t, app)
	press(t, app, "g", "g")
	view := frameView(app)
	if view.Cursor == nil {
		t.Fatal("copy mode has no cursor before selecting")
	}
	x, y := view.Cursor.X, view.Cursor.Y
	press(t, app, "l")
	view = frameView(app)
	if view.Cursor == nil || view.Cursor.X != x+1 || view.Cursor.Y != y {
		t.Fatalf("copy cursor did not follow l: %+v", view.Cursor)
	}
	press(t, app, "j")
	view = frameView(app)
	if view.Cursor == nil || view.Cursor.X != x || view.Cursor.Y != y+1 {
		t.Fatalf("copy cursor missing on empty line: %+v", view.Cursor)
	}
	press(t, app, "v")
	if view = frameView(app); view.Cursor == nil {
		t.Fatal("copy selection lost its endpoint cursor")
	}
}

// Live in a shell that printed 200 lines, ctrl+a [ keeps the screen where
// it was; k moves into the scrollback, /word finds it, V y yanks it into
// register 0 and the clipboard; the pane stays attached and i is live.
func TestCopyModeFromLiveKeepsTheScreenAndYanksLines(t *testing.T) {
	t.Parallel()
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
	if app.m.Status != "" || app.r.FocusID() != copyID {
		t.Fatalf("ctrl+a [: %s focus %s", app.m.Status, app.r.FocusID())
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
	if app.r.CurrentMode().Name != "copy" || app.r.FocusID() != copyID {
		t.Fatalf("after y: mode %s focus %s", app.r.CurrentMode().Name, app.r.FocusID())
	}
	// New output while copy mode is on lands in the buffer's tail.
	deliverFrame(t, app, ochazuke.TerminalFrame{Lines: append(append([]string(nil), screen[2:]...), "new output", "$ ")})
	if b := app.copy.buf; b.lines[len(b.lines)-2] != "new output" || b.row != 50 {
		t.Fatalf("after new output: tail %q, row %d", b.lines[len(b.lines)-3:], b.row)
	}
	press(t, app, "i")
	if app.r.FocusID() != paneTermID || app.r.CurrentMode().Name != "terminal" || s.released {
		t.Fatalf("after i: focus %s mode %s released %v", app.r.FocusID(), app.r.CurrentMode().Name, s.released)
	}
}

// deliverFrame lands a frame on the attached terminal's running read, as
// the stream would.
func deliverFrame(t *testing.T, app *App, f ochazuke.TerminalFrame) {
	t.Helper()
	for _, j := range app.r.Jobs() {
		if j.Owner == paneTermID && j.Status == ochazuke.JobRunning {
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
	t.Parallel()
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
	keysAt(t, app, "2 }", 7, 2) // past the last paragraph: the last character, as Vim's }
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
	t.Parallel()
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
	// pgdown and pgup are ctrl+f and ctrl+b, in copy mode.
	press(t, app, "pgdown")
	if b := app.copy.buf; b.top != 2*(h-2) {
		t.Fatalf("pgdown: top %d, height %d", b.top, h)
	}
	press(t, app, "pgup")
	if b := app.copy.buf; b.top != h-2 {
		t.Fatalf("pgup: top %d", b.top)
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
	t.Parallel()
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
		if app.r.CurrentMode().Name != "copy" {
			t.Fatalf("%s: mode %s after the yank", keys, app.r.CurrentMode().Name)
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
	if app.r.CurrentMode().Name != "copy" || app.r.FocusID() != copyID {
		t.Fatalf("v esc: mode %s focus %s", app.r.CurrentMode().Name, app.r.FocusID())
	}
}

// Copy mode reads a mode's selection shape, never its name (SC-05): a mode
// of another name declared line-shaped selects and yanks whole lines, and
// visual-line marked as a block selects a rectangle.
func TestCopyModeSelectionFollowsTheShape(t *testing.T) {
	t.Parallel()
	lines := []string{"0123456789", "abcdefghij", "ABCDEFGHIJ"}
	app, _, _ := copyApp(t, nil, lines)
	enterCopyMode(t, app)
	if err := app.r.DefineMode("pick-lines", false, ochazuke.ModeExtends("copy")); err != nil {
		t.Fatal(err)
	}
	if err := app.r.DefineSelection("pick-lines", ochazuke.SelectionLine); err != nil {
		t.Fatal(err)
	}
	pressAll(t, app, "g", "g", "0", "2", "l")
	if err := app.r.SetMode("pick-lines"); err != nil {
		t.Fatal(err)
	}
	pressAll(t, app, "j")
	if _, err := app.r.Execute(ochazuke.Request{Noun: copyNoun, Verb: "yank", Target: copyID}); err != nil {
		t.Fatal(err)
	}
	if y, _ := app.r.ReadYank("0"); y.Text != "0123456789\nabcdefghij\n" || y.Shape != "line" {
		t.Fatalf("pick-lines yank: %+v", y)
	}
	if err := app.r.DefineSelection("visual-line", ochazuke.SelectionBlock); err != nil {
		t.Fatal(err)
	}
	pressAll(t, app, "g", "g", "0", "2", "l")
	if err := app.r.SetMode("visual-line"); err != nil {
		t.Fatal(err)
	}
	pressAll(t, app, "j", "l", "y")
	if y, _ := app.r.ReadYank("0"); y.Text != "23\ncd" || y.Shape != "block" {
		t.Fatalf("visual-line as a block: %+v", y)
	}
}

// The footer names copy mode's actions, innermost first, with no motions.
func TestCopyModeFooterHasNoMotions(t *testing.T) {
	t.Parallel()
	app, _, _ := copyApp(t, nil, []string{"one", "two"})
	enterCopyMode(t, app)
	hints := ochazuke.BindingHints(app.r, app.r.FocusID())
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
	if hints := ochazuke.BindingHints(app.r, app.r.FocusID()); !strings.Contains(hints, "y yank") {
		t.Fatalf("visual footer: %s", hints)
	}
}

// Copy mode works zoomed: ctrl+a z then ctrl+a [ draws the buffer over the
// page body, and i is live in the zoom again.
func TestCopyModeInZoom(t *testing.T) {
	t.Parallel()
	app, f, _ := copyApp(t, []string{"history"}, []string{"$ ls", "a b c"})
	cmd := key(t, app, "enter")
	f.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"$ ls", "a b c"}, Cursor: ochazuke.Pos{X: 0, Y: 1}}
	runOnce(app, cmd)
	pressAll(t, app, "ctrl+a", "z", "ctrl+a", "[")
	if fr := frame(app); !strings.Contains(fr, "copy mode · 3/3 · zoomed") || !strings.Contains(fr, "a b c") {
		t.Fatalf("zoomed copy frame:\n%s", fr)
	}
	press(t, app, "esc")
	if !app.pv.zoom || app.r.FocusID() != paneTermID {
		t.Fatalf("esc: zoom %v focus %s", app.pv.zoom, app.r.FocusID())
	}
}

// The buffer holds at most copy.max-lines lines: the oldest history goes,
// and older lines stop loading at the cap.
func TestCopyModeCapsTheScrollback(t *testing.T) {
	t.Parallel()
	var history []string
	for i := range 1050 {
		history = append(history, fmt.Sprint(i))
	}
	app, f, _ := copyApp(t, history, []string{"$"})
	for name, v := range map[string]int{settingCopyMaxLines: 300, settingCopyChunk: 200} {
		if err := app.r.Settings().Set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	enterCopyMode(t, app)
	b := app.copy.buf
	if len(b.lines) != 200 || b.lines[0] != "851" || b.lines[len(b.lines)-1] != "$" || b.row != 199 {
		t.Fatalf("first chunk: %d lines, first %q, last %q, row %d", len(b.lines), b.lines[0], b.lines[len(b.lines)-1], b.row)
	}
	// Scrolling up loads the chunk before it, but only up to the cap.
	pressAll(t, app, "g", "g")
	settleCopyLoads(t, app)
	b = app.copy.buf
	if len(b.lines) != 300 || b.lines[0] != "751" || b.lines[len(b.lines)-1] != "$" {
		t.Fatalf("at the cap: %d lines, first %q, last %q", len(b.lines), b.lines[0], b.lines[len(b.lines)-1])
	}
	loads := len(f.windows)
	pressAll(t, app, "g", "g")
	settleCopyLoads(t, app)
	if len(f.windows) != loads {
		t.Fatalf("a read past the cap: %v", f.windows[loads:])
	}
}

// settleCopyLoads lets the loads copy mode starts as the view nears the top
// of the loaded lines run to completion.
func settleCopyLoads(t *testing.T, app *App) {
	t.Helper()
	for range 5 {
		_, cmd := app.Update(ochazuke.JobLingerMsg{})
		settle(app, cmd)
	}
}

// Opening copy mode reads one chunk of a long scrollback, not all of it; the
// older chunks load as the cursor reaches the top of what is loaded, each
// bounded by copy.chunk-lines, and the lines join up with none missing.
func TestCopyModeLoadsOlderLinesInChunks(t *testing.T) {
	t.Parallel()
	var history []string
	for i := range 5000 {
		history = append(history, fmt.Sprint(i))
	}
	app, f, _ := copyApp(t, history, []string{"$"})
	if err := app.r.Settings().Set(settingCopyChunk, 1000); err != nil {
		t.Fatal(err)
	}
	enterCopyMode(t, app)
	if len(f.windows) != 1 || f.windows[0] != (scrollWindow{Tail: 1000, Styled: true}) {
		t.Fatalf("entering read %v, want one tail chunk", f.windows)
	}
	if b := app.copy.buf; len(b.lines) != 1000 || b.lines[0] != "4001" || app.copy.base != 4001 {
		t.Fatalf("first chunk: %d lines from %q, base %d", len(b.lines), b.lines[0], app.copy.base)
	}
	// The cursor is at the bottom: nothing older is read.
	settleCopyLoads(t, app)
	if len(f.windows) != 1 {
		t.Fatalf("read without scrolling: %v", f.windows)
	}
	// Scrolling to the top of the loaded lines reads the chunk before them.
	for range 6 {
		pressAll(t, app, "g", "g")
		settleCopyLoads(t, app)
	}
	for _, w := range f.windows[1:] {
		if w.Count > 1000 || w.Count < 1 {
			t.Fatalf("an older read is not one chunk: %v", w)
		}
	}
	b := app.copy.buf
	if len(b.lines) != 5001 || app.copy.base != 0 {
		t.Fatalf("after scrolling to the top: %d lines, base %d", len(b.lines), app.copy.base)
	}
	for i, want := range history {
		if b.lines[i] != want {
			t.Fatalf("line %d is %q, want %q", i, b.lines[i], want)
		}
	}
	// Everything is loaded: no further reads.
	n := len(f.windows)
	pressAll(t, app, "g", "g")
	settleCopyLoads(t, app)
	if len(f.windows) != n {
		t.Fatalf("read with nothing older: %v", f.windows[n:])
	}
}

// With a side pane open, ctrl+a [ in the side copies the side pane and the
// parent keeps its live view; i goes back to the side; ctrl+a [ in the
// parent copies the parent beside the side.
func TestCopyModeWithASidePane(t *testing.T) {
	t.Parallel()
	app, f := sideViewApp(t)
	app.cx.sys = &fakeSystem{}
	if err := app.showSide(app.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	pressAll(t, app, "ctrl+a", "[")
	if app.r.FocusID() != copyID || app.copy.source != sideTermID || app.copy.pane.ID != "side-vim" {
		t.Fatalf("side ctrl+a [: focus %s source %q pane %s", app.r.FocusID(), app.copy.source, app.copy.pane.ID)
	}
	if fr := frame(app); !strings.Contains(fr, "side · ") || !strings.Contains(fr, "copy mode") || !strings.Contains(fr, "PARENT SCREEN") {
		t.Fatalf("side copy frame:\n%s", fr)
	}
	press(t, app, "esc")
	if app.r.FocusID() != sideTermID || app.copy.active {
		t.Fatalf("i: focus %s", app.r.FocusID())
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

// Each way of ending a selection stays in copy mode, with the cursor
// ready for another motion; a second Escape leaves for the pane list.
func TestCopyModeSelectionReturnsToCopy(t *testing.T) {
	t.Parallel()
	for _, shape := range []struct{ key, mode string }{
		{"v", "visual"}, {"V", "visual-line"}, {"ctrl+v", "visual-block"},
	} {
		for _, end := range []string{shape.key, "esc", "q", "y"} {
			t.Run(shape.mode+"/"+end, func(t *testing.T) {
				t.Parallel()
				app, _, _ := copyApp(t, nil, []string{"alpha beta", "second line", "last line"})
				enterCopyMode(t, app)
				pressAll(t, app, "g", "g", shape.key)
				if app.r.CurrentMode().Name != shape.mode {
					t.Fatalf("%s: mode %s", shape.key, app.r.CurrentMode().Name)
				}
				pressAll(t, app, "2", "j", end)
				if app.r.CurrentMode().Name != "copy" || !app.copy.active || app.r.FocusID() != copyID {
					t.Fatalf("%s: mode %s active %v focus %s", end, app.r.CurrentMode().Name, app.copy.active, app.r.FocusID())
				}
				pressAll(t, app, "esc")
				if app.r.CurrentMode().Name != "normal" || app.copy.active || app.r.FocusID() != "pane:matomeru/w1" {
					t.Fatalf("esc out: mode %s active %v focus %s", app.r.CurrentMode().Name, app.copy.active, app.r.FocusID())
				}
			})
		}
	}
}

func TestCopyModeInheritsNormalAndRestoresFocusModes(t *testing.T) {
	t.Parallel()
	app, _, _ := copyApp(t, nil, []string{"alpha beta", "second line"})
	enterCopyMode(t, app)
	// The palette is an app normal row, inherited by the declared mode.
	pressAll(t, app, ":")
	if len(app.r.Modals()) == 0 {
		t.Fatal("normal palette binding did not answer in copy")
	}
	pressAll(t, app, "esc", "q")
	if app.r.FocusID() != copyID || app.r.CurrentMode().Name != "copy" || !app.copy.active {
		t.Fatalf("palette return: focus %s mode %s active %v", app.r.FocusID(), app.r.CurrentMode().Name, app.copy.active)
	}
	// Pending find and count modes consume their key back into copy.
	pressAll(t, app, "g", "g", "0", "f", "b")
	at(t, app, "fb", 0, 6)
	if app.r.CurrentMode().Name != "copy" {
		t.Fatalf("find return: mode %s", app.r.CurrentMode().Name)
	}
	pressAll(t, app, "2", "h")
	at(t, app, "2h", 0, 4)
	if app.r.CurrentMode().Name != "copy" {
		t.Fatalf("count return: mode %s", app.r.CurrentMode().Name)
	}
	// A normal page binding still answers and leaving the stop ends copy.
	pressAll(t, app, "ctrl+j")
	if app.r.Page == panesPage || app.r.CurrentMode().Name != "normal" || app.copy.active {
		t.Fatalf("page away: page %s mode %s active %v", app.r.Page, app.r.CurrentMode().Name, app.copy.active)
	}
}

// Copy mode's / follows search.case as every search does (R5.17): smart
// by default (a capital matches case), ignore and sensitive as set.
func TestCopyModeSearchFollowsSearchCase(t *testing.T) {
	t.Parallel()
	app, _, _ := copyApp(t, nil, []string{"Alpha one", "alpha two", "ALPHA three"})
	enterCopyMode(t, app)
	keysAt(t, app, "g g", 0, 0)
	search := func(pattern string) error {
		_, err := app.r.Execute(ochazuke.Request{Verb: "search", Noun: copyNoun, Target: copyID, Args: ochazuke.Arguments{"pattern": pattern}})
		return err
	}
	if err := search("alpha"); err != nil {
		t.Fatal(err)
	}
	at(t, app, "smart: lower case ignores case", 1, 0)
	keysAt(t, app, "n", 2, 0)
	if err := search("Alpha"); err != nil {
		t.Fatal(err)
	}
	at(t, app, "smart: a capital matches case", 0, 0)
	if err := app.r.Settings().Set(ochazuke.SearchCaseSetting, ochazuke.SearchCaseSensitive); err != nil {
		t.Fatal(err)
	}
	if err := search("alpha"); err != nil {
		t.Fatal(err)
	}
	at(t, app, "sensitive", 1, 0)
	keysAt(t, app, "n", 1, 0)
	if err := app.r.Settings().Set(ochazuke.SearchCaseSetting, ochazuke.SearchCaseIgnore); err != nil {
		t.Fatal(err)
	}
	if err := search("ALPHA"); err != nil {
		t.Fatal(err)
	}
	at(t, app, "ignore: a capital still ignores case", 2, 0)
}

func TestLeaderLCopyReturnsToFocusedSide(t *testing.T) {
	t.Parallel()
	a, _ := sideViewApp(t)
	if err := a.showSide(a.attached, "term"); err != nil {
		t.Fatal(err)
	}
	side := a.r.Instances[sideTermID].Data.(pane)
	press(t, a, "ctrl+a", "l")
	if a.r.CurrentMode().Name != "copy" || a.copy.pane.ID != side.ID {
		t.Fatalf("copied %s in %s", a.copy.pane.ID, a.r.CurrentMode().Name)
	}
	press(t, a, "esc")
	if a.r.FocusID() != sideTermID || a.r.CurrentMode().Name != "terminal" || a.r.Terminal(sideTermID) == nil {
		t.Fatalf("lost side: %s %s", a.r.FocusID(), a.r.CurrentMode().Name)
	}
}

// A page drawn under a modal keeps what it drew before the modal opened:
// ochazuke no longer swaps the focus and mode while drawing, so a renderer
// reads the drawn focus and mode (shownFocused, DrawnBaseMode, StylesAt),
// not the live ones, which are the modal's. Copy mode with a visual
// selection under the help modal still has its focus, its mode, its
// selection and its focused border.
func TestPageUnderModalKeepsItsFocusAndSelection(t *testing.T) {
	t.Parallel()
	app, _, _ := copyApp(t, nil, []string{"0123456789", "abcdefghij", "ABCDEFGHIJ"})
	enterCopyMode(t, app)
	pressAll(t, app, "g", "g", "v", "l")
	before := app.copyBlock(40, 6).Text
	panel := rawFrame(app)
	if app.r.DrawnBaseMode(copyID) != "visual" || !app.shownFocused(copyID) {
		t.Fatalf("before the modal: mode %q focus %q", app.r.DrawnBaseMode(copyID), app.r.DrawnFocus(copyID))
	}
	pressAll(t, app, "?")
	if len(app.r.Modals()) == 0 {
		t.Fatalf("? opened no modal (focus %s mode %s)", app.r.FocusID(), app.r.CurrentMode().Name)
	}
	if app.r.FocusID() == copyID {
		t.Fatalf("the modal did not take the live focus")
	}
	if got := app.r.DrawnBaseMode(copyID); got != "visual" {
		t.Fatalf("drawn mode under the modal %q, want visual", got)
	}
	if !app.shownFocused(copyID) {
		t.Fatalf("the copy view lost its drawn focus under the modal")
	}
	if after := app.copyBlock(40, 6).Text; after != before {
		t.Fatalf("selection under the modal changed:\n%q\n%q", before, after)
	}
	if app.pageStyles().ChromeLevel() != app.m.StylesAt(app.r.Page).ChromeLevel() {
		t.Fatal("page styles are not the drawn ones")
	}
	// The page under the modal still draws the copy view.
	if with := rawFrame(app); !strings.Contains(ansi.Strip(with), "abcdefghij") {
		t.Fatalf("the copy view is gone under the modal:\n%s", with)
	}
	_ = panel
}

// A paragraph ends at an empty line only, as in Vim and in ochazuke's
// motion table: a line holding only a tab does not (copy mode used to stop there; plainLines already empties a line of spaces).
func TestCopyModeParagraphsEndAtEmptyLinesOnly(t *testing.T) {
	t.Parallel()
	app, _, _ := copyApp(t, nil, []string{"one", "\t", "two", "", "three"})
	enterCopyMode(t, app)
	keysAt(t, app, "g g", 0, 0)
	keysAt(t, app, "}", 3, 0)
	keysAt(t, app, "{", 0, 0)
}

// copy.colors on draws the pane's styles under the selection; off draws
// plain text. A yank is plain text either way.
func TestCopyModeColors(t *testing.T) {
	t.Parallel()
	red := "\x1b[31m"
	for _, on := range []bool{true, false} {
		app, _, _ := copyApp(t, nil, []string{red + "red line" + "\x1b[0m", "plain"})
		if err := app.r.Settings().Set(settingCopyColors, on); err != nil {
			t.Fatal(err)
		}
		enterCopyMode(t, app)
		pressAll(t, app, "g", "g", "0", "v", "3", "l")
		block := app.copyBlock(20, 4).Text
		if got := strings.Contains(block, red); got != on {
			t.Fatalf("colors %v: red in the drawn copy view = %v: %q", on, got, block)
		}
		if !strings.Contains(block, "\x1b[7m") {
			t.Fatalf("colors %v: selection not drawn in reverse: %q", on, block)
		}
		if ansi.Strip(block) != strings.Join([]string{"red line" + strings.Repeat(" ", 12), "plain" + strings.Repeat(" ", 15), strings.Repeat(" ", 20), strings.Repeat(" ", 20)}, "\n") {
			t.Fatalf("colors %v: text changed: %q", on, ansi.Strip(block))
		}
		pressAll(t, app, "y")
		if y, err := app.r.ReadYank("0"); err != nil || y.Text != "red " {
			t.Fatalf("colors %v: yank %+v, %v", on, y, err)
		}
	}
}
