package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// Copy mode (plans/mtm-copy-mode.md): the pane focused but not live. The
// Screen tab shows the pane's scrollback and screen as a read-only buffer
// (copyBuf) with Vim's motions, search, the three visual shapes and a yank
// into the registers and the clipboard. It is entered by moving the focus
// onto the pane from the list (l on a pane row, } or tab) or with ctrl+a [
// while live; i or enter goes live, esc or q returns to the entry state. The
// pane keeps running: while it is attached the buffer's tail is the live
// emulator's screen, and the scrollback above it reloads off the loop.

const (
	copyID       = "panes/copy"
	copyNoun     = "copy"
	copyFindMode = "copy_find_pending"
	// copyCap is the most lines the buffer holds, history and screen.
	copyCap = 10000
)

// copyMode is the copy instance's component and the panes page's copy
// state: the buffer, the pane it reads and where its screen comes from.
type copyMode struct {
	buf        copyBuf
	active     bool
	pane       pane
	source     string   // the terminal whose screen is the buffer's tail; "" reads the capture's
	returnLive string   // terminal focused at entry; empty when entered from the list
	history    []string // the capture's history, capped
	screen     []string
	base       int  // the daemon's line number of the buffer's first line
	loaded     bool // a scrollback capture has landed since entering
	frame      string
	err        string            // why the last scrollback load failed
	r          *ochazuke.Runtime // asked for a mode's selection shape
}

// The delivery contract: the copy instance's only work is copy.load, whose
// completion lands through its Then.
func (c *copyMode) HandleMessage(tea.Msg) bool { return false }
func (c *copyMode) PendingCommand() tea.Cmd    { return nil }
func (c *copyMode) Close()                     {}

// ModeChanged anchors a selection where the cursor is when a selection
// mode starts from outside one; switching shapes keeps the anchor. Modes
// are judged by their selection shape, never their names (SC-05).
func (c *copyMode) ModeChanged(from, to string) {
	if c.r.SelectionShape(to) != "" && c.r.SelectionShape(from) == "" {
		c.buf.anchor = copyPos{c.buf.row, c.buf.col}
	}
	if c.r.SelectionShape(to) != ochazuke.SelectionBlock {
		c.buf.blockEnd = false
	}
}

// scrollbackReader is the daemon's scrollback capture (tuios.Scrollback).
type scrollbackReader interface {
	Scrollback(ctx context.Context, p pane, from int) (scrollback, error)
}

// plainLines is a screen as plain text, trailing blanks cut.
func plainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(ansi.Strip(l), " \r")
	}
	return out
}

// rebuild joins history and screen under the cap; shift is how many lines
// now sit above the old first line.
func (c *copyMode) rebuild(shift int) {
	lines := append(append([]string(nil), c.history...), c.screen...)
	if len(lines) == 0 {
		lines = []string{""}
	}
	c.buf.setLines(lines, shift)
}

// apply takes a scrollback capture: its history above the screen, and its
// screen unless an attached terminal supplies a live one.
func (c *copyMode) apply(back scrollback) {
	lines := strings.Split(strings.TrimRight(back.Text, "\n"), "\n")
	lines = plainLines(lines)
	hist := min(max(0, back.History), len(lines))
	history, screen := lines[:hist], lines[hist:]
	if c.source == "" {
		c.screen = screen
	}
	if drop := len(history) + len(c.screen) - copyCap; drop > 0 {
		history = history[min(drop, len(history)):]
	}
	base := back.History - len(history)
	old := c.base
	if !c.loaded {
		old = back.History // the buffer was the screen alone
	}
	c.history, c.base, c.loaded = history, base, true
	c.rebuild(old - base)
}

// followSource reads the attached terminal's screen into the buffer's tail
// when it changed, and reports the change: lines that scrolled off the
// screen are in the daemon's history, which the caller reloads.
func (c *copyMode) followSource(r *ochazuke.Runtime) bool {
	t := r.Terminal(c.source)
	if c.source == "" || t == nil {
		return false
	}
	f := t.Frame()
	text := strings.Join(f.Lines, "\n")
	if text == c.frame {
		return false
	}
	c.frame, c.screen = text, plainLines(f.Lines)
	c.rebuild(0)
	return true
}

func (a *App) defineCopy() {
	r := a.r
	for _, mode := range []string{"visual", "visual-line", "visual-block", "visual_count_pending"} {
		if !r.Modes[mode] {
			_ = r.DefineMode(mode, mode == "visual_count_pending")
		}
	}
	_ = r.DefineMode(copyFindMode, true)
	a.copy = &copyMode{buf: copyBuf{goal: -1}, r: r}
	r.Define(copyNoun).Focusable().
		Verb("move", a.copyOp(func(c *copyMode, r *ochazuke.Runtime, args ochazuke.Arguments) error {
			return c.buf.move(ochazuke.StringArg(args, "motion", ""), ochazuke.IntArg(args, "steps", 1), ochazuke.IntArg(args, "line", 0), r.SelectionShape(r.BaseMode()) == ochazuke.SelectionBlock)
		}), "Move the copy cursor (Vim motions: left, word-forward, line-end, first-line, screen-top, paragraph-forward, match, ...)").Motion().
		Verb("find", a.copyOp(func(c *copyMode, _ *ochazuke.Runtime, args ochazuke.Arguments) error {
			char, err := keyChar(ochazuke.StringArg(args, "char", ""))
			if err != nil {
				return err
			}
			kind := ochazuke.StringArg(args, "kind", "f")
			c.buf.find = copyFind{kind: kind, char: char}
			return c.buf.findChar(kind, char, ochazuke.IntArg(args, "steps", 1), false)
		}), "Move to a character on the line: f and t forward, F and T back (t and T stop beside it)").Motion().
		Verb("repeat", a.copyOp(func(c *copyMode, _ *ochazuke.Runtime, args ochazuke.Arguments) error {
			return c.buf.repeatFind(boolArg(args, "reverse", false), ochazuke.IntArg(args, "steps", 1))
		}), "Repeat the last f, F, t or T (reverse: the other way)").Motion().
		Verb("scroll", a.copyOp(func(c *copyMode, _ *ochazuke.Runtime, args ochazuke.Arguments) error {
			h := max(1, c.buf.height)
			n := map[string]int{"half": max(1, h/2), "page": max(1, h-2), "line": 1}[ochazuke.StringArg(args, "by", "line")]
			lines := n * ochazuke.IntArg(args, "steps", 1) * ochazuke.IntArg(args, "dir", 1)
			c.buf.scroll(lines, ochazuke.StringArg(args, "by", "line") != "line")
			return nil
		}), "Scroll the copy view by half a page, a page or a line (dir 1 down, -1 up)").Motion().
		Verb("next", a.copyOp(func(c *copyMode, _ *ochazuke.Runtime, args ochazuke.Arguments) error {
			return c.buf.searchFrom(c.buf.search, c.buf.back != boolArg(args, "reverse", false), ochazuke.IntArg(args, "steps", 1))
		}), "Move to the next match of the last search (reverse: the previous)").Motion().
		Verb("search", a.copySearch, "Search the copy buffer: down, or up with back; a lowercase pattern ignores case").
		Verb("select", a.copyOp(func(_ *copyMode, r *ochazuke.Runtime, args ochazuke.Arguments) error {
			shape := ochazuke.StringArg(args, "shape", ochazuke.SelectionChar)
			mode := r.SelectionMode(shape)
			if !slices.Contains([]string{ochazuke.SelectionChar, ochazuke.SelectionLine, ochazuke.SelectionBlock}, shape) || mode == "" {
				return errors.New("shape must be char, line or block")
			}
			if r.BaseMode() == mode {
				mode = "normal"
			}
			return r.SetMode(mode)
		}), "Start, switch or end a selection: char (v), line (V) or block (ctrl+v)").
		Verb("swap", a.copyOp(func(c *copyMode, _ *ochazuke.Runtime, _ ochazuke.Arguments) error {
			b := &c.buf
			b.anchor, b.row, b.col = copyPos{b.row, b.col}, b.anchor.row, b.anchor.col
			b.goal = -1
			b.clamp()
			return nil
		}), "Exchange the selection's ends").
		Verb("yank", a.copyYank, "Yank the selection (or count lines) into the registers and the clipboard; copy mode stays").
		Verb("live", a.copyLive, "Go live: keys go to the pane again").
		Verb("leave", a.copyLeave, "Return to the entry state: live terminal or pane list").
		Verb("load", a.copyLoad, "Read the pane's scrollback into the copy buffer").Quiet()
	a.parameters(copyNoun+".move", map[string]ochazuke.Parameter{"motion": {Type: "string", Required: true}, "steps": {Type: "integer"}, "line": {Type: "integer"}})
	a.parameters(copyNoun+".find", map[string]ochazuke.Parameter{"kind": {Type: "string", Required: true, Enum: []string{"f", "F", "t", "T"}}, "char": {Type: "string", Required: true}, "steps": {Type: "integer"}})
	a.parameters(copyNoun+".repeat", map[string]ochazuke.Parameter{"reverse": {Type: "boolean"}, "steps": {Type: "integer"}})
	a.parameters(copyNoun+".next", map[string]ochazuke.Parameter{"reverse": {Type: "boolean"}, "steps": {Type: "integer"}})
	a.parameters(copyNoun+".yank", map[string]ochazuke.Parameter{"lines": {Type: "integer", Description: "Lines from the cursor when nothing is selected (default 1)"}})
	a.parameters(copyNoun+".search", map[string]ochazuke.Parameter{"back": {Type: "boolean"}, "pattern": {Type: "string", Description: "Search for this at once instead of asking"}})
	a.parameters(copyNoun+".select", map[string]ochazuke.Parameter{"shape": {Type: "string", Required: true, Enum: []string{"char", "line", "block"}}})
	a.parameters(copyNoun+".scroll", map[string]ochazuke.Parameter{"by": {Type: "string", Required: true, Enum: []string{"half", "page", "line"}}, "dir": {Type: "integer"}, "steps": {Type: "integer"}})

	mv := func(motion string) ochazuke.Binding {
		return ochazuke.Do(copyNoun+".move", ochazuke.Arguments{"motion": motion}).FromPayload("steps", ochazuke.SourceCount)
	}
	line := func(motion string) ochazuke.Binding {
		return ochazuke.Do(copyNoun+".move", ochazuke.Arguments{"motion": motion}).FromPayload("line", ochazuke.SourceLine)
	}
	scroll := func(by string, dir int) ochazuke.Binding {
		return ochazuke.Do(copyNoun+".scroll", ochazuke.Arguments{"by": by, "dir": dir}).FromPayload("steps", ochazuke.SourceCount)
	}
	find := func(kind string) ochazuke.Binding {
		return ochazuke.Do("mode.enter", ochazuke.Arguments{"mode": copyFindMode, "op": kind}).FromPayload("count", ochazuke.SourceCount).Hide()
	}
	// The motions answer in normal and in every visual shape.
	motions := []struct {
		keys string
		row  ochazuke.Binding
	}{
		{"h", mv("left")}, {"left", mv("left")}, {"l", mv("right")}, {"right", mv("right")},
		{"j", mv("down")}, {"down", mv("down")}, {"k", mv("up")}, {"up", mv("up")},
		{"w", mv("word-forward")}, {"W", mv("WORD-forward")}, {"b", mv("word-backward")}, {"B", mv("WORD-backward")},
		{"e", mv("word-end")}, {"E", mv("WORD-end")}, {"g e", mv("word-end-backward")}, {"g E", mv("WORD-end-backward")},
		{"0", mv("line-start")}, {"^", mv("first-blank")}, {"$", mv("line-end")}, {"g _", mv("last-blank")},
		{"g g", line("first-line")}, {"G", line("last-line")},
		{"H", mv("screen-top")}, {"M", mv("screen-middle")}, {"L", mv("screen-bottom")},
		{"%", mv("match")}, {"}", mv("paragraph-forward")}, {"{", mv("paragraph-backward")},
		{"f", find("f")}, {"F", find("F")}, {"t", find("t")}, {"T", find("T")},
		{";", ochazuke.Do(copyNoun+".repeat").FromPayload("steps", ochazuke.SourceCount)},
		{",", ochazuke.Do(copyNoun+".repeat", ochazuke.Arguments{"reverse": true}).FromPayload("steps", ochazuke.SourceCount)},
		{"ctrl+d", scroll("half", 1)}, {"ctrl+u", scroll("half", -1)},
		{"ctrl+f", scroll("page", 1)}, {"ctrl+b", scroll("page", -1)},
		{"ctrl+e", scroll("line", 1)}, {"ctrl+y", scroll("line", -1)},
		{"n", ochazuke.Do(copyNoun+".next").FromPayload("steps", ochazuke.SourceCount)},
		{"N", ochazuke.Do(copyNoun+".next", ochazuke.Arguments{"reverse": true}).FromPayload("steps", ochazuke.SourceCount)},
		{"/", ochazuke.Do(copyNoun + ".search").Hinted("search")},
		{"?", ochazuke.Do(copyNoun+".search", ochazuke.Arguments{"back": true}).Hide()},
	}
	selects := func(m *ochazuke.ModeBuilder) *ochazuke.ModeBuilder {
		return m.Bind("v", ochazuke.Do(copyNoun+".select", ochazuke.Arguments{"shape": "char"}).Hinted("select")).
			Bind("V", ochazuke.Do(copyNoun+".select", ochazuke.Arguments{"shape": "line"}).Hide()).
			Bind("ctrl+v", ochazuke.Do(copyNoun+".select", ochazuke.Arguments{"shape": "block"}).Hide())
	}
	noun := r.Define(copyNoun)
	normal := noun.Mode("normal")
	visual := noun.Mode("visual")
	// The footer reads v select · / search · i live · q back (y yank in a
	// selection): those rows come first, the motions after them.
	selects(normal)
	selects(visual).
		Bind("y", ochazuke.Do(copyNoun+".yank").Hinted("yank")).
		Bind("o", ochazuke.Do(copyNoun+".swap").Hinted("other end")).
		Bind("esc", ochazuke.Do("mode.set", ochazuke.Arguments{"mode": "normal"}).Hinted("end selection")).
		Bind("q", ochazuke.Do("mode.set", ochazuke.Arguments{"mode": "normal"}).Hide()).
		// A count in a selection (3j): the app's count rows answer in normal only.
		Pattern("digit-0", ochazuke.Do("mode.enter", ochazuke.Arguments{"mode": "visual_count_pending"}).FromPayload("count", ochazuke.SourceDigit).Hide())
	for _, m := range motions {
		normal.Bind(m.keys, m.row)
		visual.Bind(m.keys, m.row)
	}
	// y y and Y yank count lines.
	normal.Bind("i", ochazuke.Do(copyNoun+".live").Hinted("live")).
		Bind("enter", ochazuke.Do(copyNoun+".live").Hide()).
		Bind("q", ochazuke.Do(copyNoun+".leave").Hinted("back")).
		Bind("esc", ochazuke.Do(copyNoun+".leave").Hide()).
		Bind("y y", ochazuke.Do(copyNoun+".yank").FromPayload("lines", ochazuke.SourceCount).Hide()).
		Bind("Y", ochazuke.Do(copyNoun+".yank").FromPayload("lines", ochazuke.SourceCount).Hide())
	noun.Mode("visual-line").Extends("visual")
	noun.Mode("visual-block").Extends("visual")
	noun.Mode("count_pending").Extends("normal")
	noun.Mode("visual_count_pending").Extends("visual").
		Bind("0", ochazuke.Do("count.append", ochazuke.Arguments{"digit": 0})).
		Pattern("digit", ochazuke.Do("count.append").FromPayload("digit", ochazuke.SourceDigit)).
		Bind("esc", ochazuke.Do("mode.leave")).
		Pattern("any", ochazuke.Binding{Request: ochazuke.Request{Verb: "leave", Noun: "mode"}, Replay: true})
	noun.Mode(copyFindMode).
		Bind("esc", ochazuke.Do("mode.leave")).
		Pattern("any", ochazuke.Do(copyNoun+".find").FromKey("char").FromPayload("kind", ochazuke.SourceOp).FromPayload("steps", ochazuke.SourceCount).Hide())
	// l on a pane row focuses the pane in copy mode while the Screen tab
	// shows (on Turns it focuses the turns, turns.go); ctrl+a [ from live.
	r.Define("pane").Verb("copy", a.paneCopy, "Focus the pane without going live: copy mode (motions, search, select, yank)").
		Mode("normal").Bind("l", ochazuke.Do("pane.copy").Guard("pane-tab-screen").Hinted("copy mode"))
	r.DefineGuard("pane-tab-screen", func(*ochazuke.Runtime, ochazuke.KeyEvent) bool { return a.pv.tab == tabScreen })
	r.Define("terminal").Verb("copy", a.terminalCopy, "Leave live for copy mode on this pane: the screen stays, scrollback above").
		Mode("leader_pending").Bind("[", ochazuke.Do("terminal.copy").Hinted("copy mode"))
}

// keyChar is the character a key types (space included) for f and t.
func keyChar(key string) (rune, error) {
	if key == "space" {
		return ' ', nil
	}
	if r := []rune(key); len(r) == 1 {
		return r[0], nil
	}
	return 0, fmt.Errorf("%s is not a character", key)
}

// copyOp is a copy verb that runs on the loop on the active buffer.
func (a *App) copyOp(fn func(*copyMode, *ochazuke.Runtime, ochazuke.Arguments) error) func(*ochazuke.Runtime, ochazuke.Context, ochazuke.Arguments) (any, error) {
	return func(r *ochazuke.Runtime, _ ochazuke.Context, args ochazuke.Arguments) (any, error) {
		c := a.copy
		if !c.active {
			return nil, errors.New("copy mode is not on")
		}
		if err := fn(c, r, args); err != nil {
			return nil, err
		}
		return c.snapshot(), nil
	}
}

// snapshot is copy mode's state for the CLI and tests.
func (c *copyMode) snapshot() map[string]any {
	return map[string]any{"pane": c.pane.label(), "row": c.buf.row, "col": c.buf.col, "top": c.buf.top, "lines": len(c.buf.lines)}
}

// addCopy creates the copy instance on the panes page; it is a stop only
// while the Screen tab shows a pane or copy mode is on, as the frame
// places it only then (parentDetailBlock; ochazuke skips an undrawn stop).
func (a *App) addCopy() {
	inst := a.r.Instance(copyID, ochazuke.Parent(panesPage), ochazuke.Kind(copyNoun))
	inst.Component = a.copy
}

// enterCopy opens copy mode on p: the screen from the source terminal (the
// live emulator) or the last capture, the cursor on the terminal's cursor
// (else the last line with text), then the scrollback loads off the loop.
func (a *App) enterCopy(p pane, source string) error {
	c := a.copy
	c.active, c.pane, c.source, c.loaded, c.err = true, p, source, false, ""
	c.history, c.screen, c.frame, c.base = nil, nil, "", 0
	c.returnLive = ""
	keep := c.buf
	c.buf = copyBuf{goal: -1, search: keep.search, back: keep.back, find: keep.find}
	cursor := copyPos{-1, 0}
	if t := a.r.Terminal(source); source != "" && t != nil {
		f := t.Frame()
		c.frame, c.screen = strings.Join(f.Lines, "\n"), plainLines(f.Lines)
		cursor = copyPos{f.Cursor.Y, f.Cursor.X}
	} else if a.preview.Pane == p.rowID() {
		c.screen = plainLines(strings.Split(strings.TrimRight(a.preview.Text, "\n"), "\n"))
	}
	if cursor.row < 0 {
		cursor.row = 0
		for i, l := range c.screen {
			if strings.TrimSpace(l) != "" {
				cursor.row = i
			}
		}
	}
	c.buf.height = max(keep.height, len(c.screen))
	c.rebuild(0)
	c.buf.top, c.buf.row, c.buf.col = 0, cursor.row, cursor.col
	c.buf.clamp()
	a.pv.tab = tabScreen
	if a.r.Focus != copyID {
		if err := a.r.SetFocus(copyID); err != nil {
			c.active = false
			return err
		}
	}
	_, err := a.r.Execute(ochazuke.Request{Verb: "load", Noun: copyNoun, Target: copyID})
	return err
}

// paneCopy is pane.copy (l on a pane row on Screen; the palette from any
// tab, which switches to Screen and enters once the capture is in).
func (a *App) paneCopy(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	source := ""
	if a.r.Terminal(paneTermID) != nil && a.attached.ID == p.ID {
		source = paneTermID
	}
	switched, err := a.toScreen(ctx.Instance.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if switched && source == "" {
		return a.capturePreview(p, func(ctx ochazuke.Ctx) {
			if err := a.enterCopy(p, ""); err != nil {
				ctx.Error(err.Error())
			}
		})
	}
	return resultError(a.enterCopy(p, source))
}

// terminalCopy is terminal.copy (ctrl+a [): copy mode on the focused
// terminal's pane, which stays attached.
func (a *App) terminalCopy(ctx ochazuke.Ctx) ochazuke.Result {
	id := a.r.Focus
	if a.r.Terminal(id) == nil {
		id = paneTermID
	}
	if a.r.Terminal(id) == nil {
		return ochazuke.Failed(errors.New("no pane is attached"))
	}
	p := a.attached
	if id == sideTermID {
		p, _ = a.r.Instances[sideTermID].Data.(pane)
	}
	if err := a.enterCopy(p, id); err != nil {
		return ochazuke.Failed(err)
	}
	a.copy.returnLive = id
	return ochazuke.Done(nil)
}

// copyLoad is copy.load: the pane's scrollback, off the loop; a newer load
// replaces an older one.
func (a *App) copyLoad(ochazuke.Ctx) ochazuke.Result {
	c := a.copy
	d, ok := a.panesd.(scrollbackReader)
	if !ok || !c.active {
		return ochazuke.Done(nil)
	}
	p := c.pane
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		return d.Scrollback(ctx, p, 0)
	}).Replacing("load").Then(func(_ ochazuke.Ctx, v any, err error) {
		if !c.active || c.pane.ID != p.ID || c.pane.Session != p.Session {
			return
		}
		if err != nil {
			c.err = err.Error()
			return
		}
		c.err = ""
		if back, ok := v.(scrollback); ok {
			c.apply(back)
		}
	})
}

// copySearch is copy.search (/ and ?): the prompt asks for the pattern,
// which then runs from the cursor; pattern runs it at once.
func (a *App) copySearch(ctx ochazuke.Ctx) ochazuke.Result {
	c := a.copy
	if !c.active {
		return ochazuke.Failed(errors.New("copy mode is not on"))
	}
	back := boolArg(ctx.Args, "back", false)
	run := func(text string) error {
		if text == "" {
			return nil
		}
		c.buf.search, c.buf.back = text, back
		return c.buf.searchFrom(text, back, 1)
	}
	if text := ochazuke.StringArg(ctx.Args, "pattern", ""); text != "" {
		return resultError(run(text))
	}
	label := "/"
	if back {
		label = "?"
	}
	return resultError(a.prompt("Search", label+" search the pane's scrollback", "", run))
}

// copyYank is copy.yank: the selection (y in a visual shape) or count
// lines from the cursor (y y, Y) into the registers, as the editor's yank
// files it, then onto the clipboard off the loop (OSC 52 when no clipboard
// program serves the session). The selection ends; copy mode stays.
func (a *App) copyYank(ctx ochazuke.Ctx) ochazuke.Result {
	c := a.copy
	if !c.active {
		return ochazuke.Failed(errors.New("copy mode is not on"))
	}
	b := &c.buf
	shape := a.r.SelectionShape(a.r.BaseMode())
	var y ochazuke.Yank
	start := copyPos{b.row, b.col}
	if shape != "" {
		y.Text, y.Shape, y.Width = b.selection(shape)
		top, _, left, _ := b.blockBounds()
		switch shape {
		case ochazuke.SelectionBlock:
			start = copyPos{top, left}
		case ochazuke.SelectionLine:
			start = copyPos{top, b.col}
		default:
			if b.anchor.row < start.row || b.anchor.row == start.row && b.anchor.col < start.col {
				start = b.anchor
			}
		}
	} else {
		n := max(1, ochazuke.IntArg(ctx.Args, "lines", 1))
		end := min(b.last(), b.row+n-1)
		y.Text, y.Shape = strings.Join(b.lines[b.row:end+1], "\n")+"\n", "line"
	}
	if err := a.r.StoreYank(y, "", false); err != nil {
		return ochazuke.Failed(err)
	}
	if shape != "" {
		if err := a.r.SetMode("normal"); err != nil {
			return ochazuke.Failed(err)
		}
	}
	b.row, b.col, b.goal = start.row, start.col, -1
	b.clamp()
	text, sys := y.Text, a.cx.sys
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		return sys.Copy(ctx, text)
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if osc, _ := v.(bool); err == nil && osc {
			if err := a.copyOSC52(text); err != nil {
				ctx.Error(err.Error())
			}
		}
	})
}

// copyLive is copy.live (i, enter): back to the attached terminal, or
// attach the pane live.
func (a *App) copyLive(ochazuke.Ctx) ochazuke.Result {
	c := a.copy
	if !c.active {
		return ochazuke.Failed(errors.New("copy mode is not on"))
	}
	if c.source != "" && a.r.Terminal(c.source) != nil {
		return resultError(a.r.SetFocus(c.source))
	}
	_, err := a.r.Execute(ochazuke.Request{Verb: "attach", Noun: "pane", Target: c.pane.rowID()})
	return resultError(err)
}

// copyLeave is copy.leave (esc, q): restore the live terminal when entered
// from live, otherwise return to the pane's row.
func (a *App) copyLeave(ochazuke.Ctx) ochazuke.Result {
	c := a.copy
	if c.returnLive != "" && a.r.Terminal(c.returnLive) != nil {
		return resultError(a.r.SetFocus(c.returnLive))
	}
	row := c.pane.rowID()
	c.active = false
	if a.r.Terminal(paneTermID) != nil {
		if _, err := a.r.Execute(ochazuke.Request{Verb: "detach", Noun: "terminal", Args: ochazuke.Arguments{"id": paneTermID}}); err != nil {
			return ochazuke.Failed(err)
		}
	}
	if a.r.Instances[row] == nil {
		return resultError(a.r.SetFocus(paneListID))
	}
	if err := a.selectPaneRow(row); err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.r.SetFocus(row))
}

// copyUpdate runs after every message: focus that arrived on the copy
// stop (} or tab from the list) starts copy mode on the selected pane,
// focus that left ends it, the live screen follows into the buffer and the
// refresh timer reloads the scrollback.
func (a *App) copyUpdate(msg tea.Msg) {
	c := a.copy
	if c.active && a.r.BaseFocus() != copyID {
		c.active = false
	}
	if !c.active && a.r.Focus == copyID {
		_, inst := a.selectedRow()
		p, err := a.paneAt(inst)
		if err == nil {
			source := ""
			if a.r.Terminal(paneTermID) != nil && a.attached.ID == p.ID {
				source = paneTermID
			}
			err = a.enterCopy(p, source)
		}
		if err != nil {
			a.r.UI().Error(err.Error())
			_ = a.r.SetFocus(paneListID)
			return
		}
	}
	if !c.active {
		return
	}
	changed := c.followSource(a.r)
	_, tick := msg.(refreshDue)
	if (changed || tick && a.r.Page == panesPage) && !a.copyLoading() {
		if _, err := a.r.Execute(ochazuke.Request{Verb: "load", Noun: copyNoun, Target: copyID}); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
}

// copyLoading reports a scrollback load in flight; a busy pane's frames do
// not stack loads (each would cancel the last before it lands).
func (a *App) copyLoading() bool {
	for _, j := range a.r.Jobs() {
		if j.Owner == copyID && j.State == ochazuke.JobRunning {
			return true
		}
	}
	return false
}

// copyShown reports whether copy mode draws in place of the view of the
// terminal source ("" or paneTermID is the parent's detail).
func (a *App) copyShown(source string) bool {
	c := a.copy
	if !c.active {
		return false
	}
	if source == sideTermID {
		return c.source == sideTermID
	}
	return c.source != sideTermID
}

// copyTitle is the panel title in copy mode: the pane, the mode, the
// cursor's line and a failed load.
func (a *App) copyTitle() string {
	c := a.copy
	title := fmt.Sprintf("%s · copy mode · %d/%d", c.pane.label(), c.buf.row+1, len(c.buf.lines))
	if c.err != "" {
		title += ": " + c.err
	}
	return title
}

// copyBlock draws the buffer into width x height cells around the cursor,
// the selection in reverse video, and reports the cursor.
func (a *App) copyBlock(width, height int) ochazuke.Block {
	a.r.Placed(copyID)
	c := a.copy
	b := &c.buf
	width, height = max(1, width), max(1, height)
	b.height = height
	b.show()
	shape := a.r.SelectionShape(a.r.BaseMode())
	cur := b.line(b.row)
	cursorX := ansi.StringWidth(string(cur[:min(b.col, len(cur))]))
	left := max(0, cursorX-width+1)
	rows := make([]string, height)
	for i := range rows {
		r := b.top + i
		if r >= len(b.lines) {
			rows[i] = strings.Repeat(" ", width)
			continue
		}
		l := b.line(r)
		lo, hi, sel := b.selected(shape, r)
		var out strings.Builder
		cells := 0
		for col := left; cells < width; col++ {
			ch := " "
			if col < len(l) {
				ch = string(l[col])
			} else if !sel || col >= hi {
				break
			}
			on := sel && col >= lo && col < hi
			if on {
				out.WriteString("\x1b[7m")
			}
			out.WriteString(ch)
			if on {
				out.WriteString("\x1b[27m")
			}
			cells += max(1, ansi.StringWidth(ch))
		}
		rows[i] = out.String() + strings.Repeat(" ", max(0, width-cells))
	}
	return ochazuke.Block{Text: strings.Join(rows, "\n"), Cursor: &ochazuke.Pos{X: cursorX - left, Y: b.row - b.top}}
}

// copyTabLine is the tab line in copy mode: the tabs, the mode and its
// way out.
func (a *App) copyTabLine() string {
	parts := make([]string, len(tabNames))
	for i, name := range tabNames {
		if i == tabScreen {
			name = "[" + name + "]"
		}
		parts[i] = name
	}
	back := "list"
	if a.copy.returnLive != "" {
		back = "live"
	}
	return strings.Join(parts, " ") + " · copy mode · i live · esc " + back
}

// boolArg reads a boolean argument (true, or "true" from the CLI).
func boolArg(args ochazuke.Arguments, name string, fallback bool) bool {
	switch v := args[name].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return fallback
}
