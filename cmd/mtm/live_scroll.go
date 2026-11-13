package main

import (
	"ofam.dev/ochazuke"
)

// Scroll keys in a live pane follow the pane's harness, as data: the
// harness row's "scroll" field (harness.ScrollCopy or ScrollForward; empty
// forwards). One set of rows in the terminal mode calls terminal.scroll;
// the verb reads the focused pane's harness row. copy enters mtm's copy
// mode a page back (OMP draws inline and has no transcript scroll of its
// own); forward sends the key to the program as the terminal's `any` row
// does. A harnesses.json row sets it for any harness, also through
// "extends". No guard: the pane's data decides.
//
// The shift rows carry always: they enter copy mode a page back from any
// live pane whatever its harness says, unless the program is on the
// alternate screen (which has no history; TUIOS forwards there too). The
// setting terminal.shift-scroll turns that off.

// settingTerminalScrollKeys is the opt-out: off, terminal.scroll forwards
// every key whatever the harness says.
const settingTerminalScrollKeys = "terminal.scroll-keys"

// settingTerminalShiftScroll is the opt-out of the always rows.
const settingTerminalShiftScroll = "terminal.shift-scroll"

// scrollKeySettings is the setting of the scroll rows.
func scrollKeySettings() []ochazuke.Setting {
	return []ochazuke.Setting{{Name: settingTerminalScrollKeys, Type: ochazuke.SettingBoolean, Default: true,
		Description: "Let a live pane's PageUp follow its harness's scroll field (omp: copy mode a page back; the others pass the key to the program); off passes every scroll key to the program"},
		{Name: settingTerminalShiftScroll, Type: ochazuke.SettingBoolean, Default: true,
			Description: "shift+PageUp in a live pane opens copy mode a page back whatever its harness says, as TUIOS does (a pane on the alternate screen still gets the key); off leaves shift+PageUp to the harness's scroll field like PageUp"}}
}

// defineLiveScroll registers terminal.scroll and the live-terminal rows.
func (a *App) defineLiveScroll() {
	row := func(dir int, always bool) ochazuke.Binding {
		return ochazuke.Do("terminal.scroll", ochazuke.Arguments{"dir": dir, "by": "page", "always": always}).FromKey("keys").Hide()
	}
	a.r.Define("terminal").
		Verb("scroll", a.terminalScroll, "Scroll a live pane by its harness's scroll field: copy enters copy mode a page back (dir -1; forward from live does nothing), forward sends the key (keys) to the program; the terminal.scroll-keys setting off always sends it").
		Mode("terminal").
		Bind("pgup", row(-1, false)).
		Bind("shift+pgup", row(-1, true)).
		Bind("pgdown", row(1, false)).
		Bind("shift+pgdown", row(1, true))
	a.parameters("terminal.scroll", map[string]ochazuke.Parameter{
		"dir":    {Type: "integer", Description: "-1 back into the history (default), 1 forward"},
		"by":     {Type: "string", Enum: []string{"page", "half", "line"}, Description: "How far: a page (default), half a page or a line"},
		"always": {Type: "boolean", Description: "Enter copy mode whatever the harness's scroll field says (the terminal.shift-scroll setting allows it; not on the alternate screen)"},
		"keys":   {Type: "string", Description: "The key that was pressed: sent to the program when the pane's harness forwards"},
	})
}

// liveScrollEntersCopy reports whether a scroll key up in the focused live
// terminal enters copy mode: the harness's scroll field with
// terminal.scroll-keys on, or an always row with terminal.shift-scroll on
// and the program off the alternate screen.
func (a *App) liveScrollEntersCopy(always bool) bool {
	id := a.r.FocusID()
	if a.r.Settings().Bool(settingTerminalScrollKeys) && a.paneScrollsInCopy(id) {
		return true
	}
	if t := a.r.Terminal(id); always && t != nil && a.r.Settings().Bool(settingTerminalShiftScroll) {
		return !t.Frame().AltScreen
	}
	return false
}

// paneScrollsInCopy reports whether the scroll keys of the pane the live
// terminal id shows enter copy mode: its harness row's scroll field (a
// pane that records no harness forwards).
func (a *App) paneScrollsInCopy(id string) bool {
	var p pane
	switch id {
	case paneTermID:
		p = a.attached
	case sideTermID:
		p, _ = a.r.Instances[sideTermID].Data.(pane)
	}
	if p.ID == "" {
		return false
	}
	h, _ := paneHarness(a.harnesses, p)
	return h.ScrollEntersCopy()
}

// scrollUnit is a unit of copy-mode scrolling (the by parameter).
type scrollUnit string

const (
	unitHalf scrollUnit = "half"
	unitPage scrollUnit = "page"
	unitLine scrollUnit = "line"
)

// scrollSpan is what a unit moves: its lines in a view h rows tall, and
// whether the cursor goes with the view (a line scroll leaves it).
type scrollSpan struct {
	lines  func(h int) int
	cursor bool
}

// scrollUnits is every unit by names.
var scrollUnits = map[scrollUnit]scrollSpan{
	unitHalf: {func(h int) int { return max(1, h/2) }, true},
	unitPage: {func(h int) int { return max(1, h-2) }, true},
	unitLine: {func(int) int { return 1 }, false},
}

// scrollSteps is how many lines a copy-mode scroll by moves in a view h
// rows tall (none for a unit that does not exist).
func scrollSteps(by string, h int) int {
	if span := scrollUnits[scrollUnit(by)]; span.lines != nil {
		return span.lines(max(1, h))
	}
	return 0
}

// scrollBy scrolls the copy view by dir units of by.
func (b *copyBuf) scrollBy(by string, dir int) {
	b.scroll(scrollSteps(by, b.height)*dir, scrollUnits[scrollUnit(by)].cursor)
}

// terminalScroll is terminal.scroll. In copy mode it scrolls the view. From
// a live pane whose harness says copy (and the setting is on) it enters
// copy mode and scrolls once the scrollback has loaded; scrolling forward
// from live has nowhere to go, so that key is forwarded. Otherwise the key
// goes to the program.
func (a *App) terminalScroll(ctx ochazuke.Ctx) ochazuke.Result {
	by := ochazuke.StringArg(ctx.Args, "by", "page")
	dir := ochazuke.IntArg(ctx.Args, "dir", -1)
	if c := a.copy; c.active {
		before := c.buf.place()
		c.buf.scrollBy(by, dir)
		return resultError(a.copyScrolled(before, by, dir))
	}
	if dir < 0 && a.liveScrollEntersCopy(boolArg(ctx.Args, "always", false)) {
		if err := a.enterTerminalCopy(); err != nil {
			return ochazuke.Failed(err)
		}
		a.copy.pending = &copyScroll{by: by, dir: dir}
		return ochazuke.Done(nil)
	}
	keys := ochazuke.StringArg(ctx.Args, "keys", "")
	if keys == "" {
		return ochazuke.Done(nil)
	}
	_, err := a.r.Execute(ochazuke.Request{Verb: "send", Noun: "terminal", Args: ochazuke.Arguments{"keys": keys}})
	return resultError(err)
}

// copyScroll is a scroll that waits for the scrollback of the copy mode a
// live pane just entered.
type copyScroll struct {
	by  string
	dir int
}

// takePendingScroll runs the scroll terminal.scroll left for the history.
func (c *copyMode) takePendingScroll() {
	p := c.pending
	if p == nil {
		return
	}
	c.pending = nil
	c.buf.scrollBy(p.by, p.dir)
}
