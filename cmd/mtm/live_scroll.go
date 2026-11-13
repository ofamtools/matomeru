package main

import (
	"strings"

	"ofam.dev/ochazuke"
)

// Scroll keys in a live pane belong to the agent that runs in it. Every
// key goes to the program through the terminal's `any` row, so a harness
// whose screen cannot scroll its own history (OMP draws inline, with no
// transcript scroll mode) gets mtm's: a row in the terminal mode, guarded
// by the pane's agent, runs terminal.scroll, which enters copy mode and
// scrolls one page back. A harness with no such row keeps the key. The
// rows are data (the user's bindings.json adds, changes or unbinds them
// for any harness); the one mechanism is the pane-agent guard.

const (
	// settingTerminalScrollKeys turns terminal.scroll off: while it is off the
	// verb is unavailable, so every scroll row stands aside and the key
	// reaches the program.
	settingTerminalScrollKeys = "terminal.scroll-keys"
	// paneAgentGuard is the guard family "pane-agent:<harness>": it holds
	// while the focused live terminal's pane runs that harness (the harness
	// row's name, as the harness picker lists it); it is unmet for a pane of
	// another harness, one that records none, and when no pane is focused.
	paneAgentGuard = "pane-agent:"
)

// scrollKeySettings is the setting of the default scroll rows.
func scrollKeySettings() []ochazuke.Setting {
	return []ochazuke.Setting{{Name: settingTerminalScrollKeys, Type: ochazuke.SettingBoolean, Default: true,
		Description: "Let mtm scroll a live pane for the agent bindings.json names (terminal.scroll rows; by default OMP's PageUp enters copy mode a page back); off sends every scroll key to the program"}}
}

// defineLiveScroll registers the verb, the guards and the default rows.
func (a *App) defineLiveScroll() {
	a.r.Define("terminal").
		Verb("scroll", a.terminalScroll, "Scroll the live pane's history in copy mode: enters it, a page (or half, line) back with dir -1 or forward with 1; the terminal.scroll-keys setting off makes it unavailable, so the key reaches the program").
		When("setting:"+settingTerminalScrollKeys).
		Mode("terminal").
		Bind("pgup", ochazuke.Do("terminal.scroll", ochazuke.Arguments{"dir": -1, "by": "page"}).Guard(paneAgentGuard+"omp").Hide()).
		Bind("shift+pgup", ochazuke.Do("terminal.scroll", ochazuke.Arguments{"dir": -1, "by": "page"}).Guard(paneAgentGuard+"omp").Hide())
	a.parameters("terminal.scroll", map[string]ochazuke.Parameter{
		"dir": {Type: "integer", Description: "-1 back into the history (default), 1 forward"},
		"by":  {Type: "string", Enum: []string{"page", "half", "line"}, Description: "How far: a page (default), half a page or a line"},
	})
	a.defineAgentGuards()
}

// defineAgentGuards registers pane-agent:<name> for every harness of the
// registry (the built-ins and harnesses.json); a reload registers the new
// names. ochazuke resolves a guard by its exact name, so the family is
// enumerated from data rather than parsed.
func (a *App) defineAgentGuards() {
	if a.harnesses == nil {
		return
	}
	for _, name := range a.harnesses.Names() {
		a.r.DefineGuard(paneAgentGuard+name, func(_ *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
			return a.paneAgentIs(ev.Focus, name)
		})
	}
}

// paneAgentIs reports whether the live terminal id shows a pane whose
// harness row is named name.
func (a *App) paneAgentIs(id, name string) bool {
	var p pane
	switch id {
	case paneTermID:
		p = a.attached
	case sideTermID:
		p, _ = a.r.Instances[sideTermID].Data.(pane)
	default:
		return false
	}
	if p.ID == "" {
		return false
	}
	h, ok := paneHarness(a.harnesses, p)
	return ok && strings.EqualFold(h.Name, name)
}

// scrollSteps is how many lines a copy-mode scroll by moves in a view h
// rows tall.
func scrollSteps(by string, h int) int {
	h = max(1, h)
	return map[string]int{"half": max(1, h/2), "page": max(1, h-2), "line": 1}[by]
}

// terminalScroll is terminal.scroll: from a live pane, copy mode with the
// view scrolled by a page once the scrollback has loaded; in copy mode,
// the scroll itself.
func (a *App) terminalScroll(ctx ochazuke.Ctx) ochazuke.Result {
	by := ochazuke.StringArg(ctx.Args, "by", "page")
	dir := ochazuke.IntArg(ctx.Args, "dir", -1)
	if c := a.copy; c.active {
		c.buf.scroll(scrollSteps(by, c.buf.height)*dir, by != "line")
		return ochazuke.Done(nil)
	}
	if err := a.enterTerminalCopy(); err != nil {
		return ochazuke.Failed(err)
	}
	a.copy.pending = &copyScroll{by: by, dir: dir}
	return ochazuke.Done(nil)
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
	c.buf.scroll(scrollSteps(p.by, c.buf.height)*p.dir, p.by != "line")
}
