package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/hints"
)

// Live hints, as tuios has them: ctrl+a f in an attached pane (the main
// pane or its side) labels every item on the pane's visible screen (URLs,
// PRs, paths, branches, commits) with a short letter label drawn over the
// live pane, which is dimmed. Typing a label copies the item, ctrl and
// the label opens it, shift (a capital) types it into the pane; esc or
// ctrl+c cancels, backspace takes the last letter back, and every other
// key is swallowed, so nothing reaches the pane while the labels are up.
// The labels are a terminal overlay (ochazuke's Terminal.SetOverlay) plus
// the hints mode, a passthrough base mode (no capture pass, so ctrl+c and
// every ctrl+letter reach the rows). Leaving the mode clears the overlay;
// so does a focus change while it is up (hintsUpdate).

const hintsMode = "hints"

// liveHints is one hints session: the terminal it labels and what is on
// its screen.
type liveHints struct {
	term    string              // the terminal instance labelled
	spans   []contextitems.Span // every occurrence, in reading order
	labels  []string            // one per span; the same item shares a label
	matcher *hints.Matcher
}

// hintActions name the hint verb's action argument.
var hintActions = map[string]hints.Action{"copy": hints.Copy, "type": hints.Type, "open": hints.Open}

// defineLiveHints adds the hints verbs and rows to the terminal noun.
func (a *App) defineLiveHints() {
	a.r.DefineMode(hintsMode, false) // a base mode: it lasts across a label's letters
	_ = a.r.DefinePassthrough(hintsMode)
	hint := func(action string) ochazuke.Binding {
		return ochazuke.Do("terminal.hint", ochazuke.Arguments{"action": action}).FromPayload("label", ochazuke.SourceLetter).Hinted(action)
	}
	// A terminal speaking the kitty keyboard protocol sends shift+a, not A.
	var shifted []string
	for c := 'a'; c <= 'z'; c++ {
		shifted = append(shifted, "shift+"+string(c))
	}
	a.r.Define("terminal").
		Verb("hints", a.startHints, "Label the items on the live pane's screen (URLs, PRs, paths, branches, commits): a label copies, ctrl+label opens, a capital types it into the pane").
		Verb("hint", a.pressHint, "Take one letter of a label; a whole label copies, opens or types its item and leaves hints").
		Verb("hints-erase", a.eraseHint, "Take back the last letter typed").
		Verb("hints-cancel", a.cancelHints, "Clear the labels and go back to the live pane").
		Verb("hints-ignore", func(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done(nil) }, "Swallow a key while the labels are up").
		Mode("leader_pending").
		Bind("f", ochazuke.Do("terminal.hints").Hinted("hints")).
		Mode(hintsMode).
		Pattern(ochazuke.Alpha, hint("copy")).
		Pattern(ochazuke.Upper, hint("type")).
		Pattern("one-of:"+strings.Join(shifted, " "), hint("type").Hide()).
		Pattern(ochazuke.CtrlAlpha, hint("open")).
		Bind("backspace", ochazuke.Do("terminal.hints-erase").Hide()).
		Bind("esc", ochazuke.Do("terminal.hints-cancel").Hinted("cancel")).
		Bind("ctrl+c", ochazuke.Do("terminal.hints-cancel").Hide()).
		Pattern(ochazuke.Paste, ochazuke.Do("terminal.hints-ignore").Hide()).
		Pattern(ochazuke.Any, ochazuke.Do("terminal.hints-ignore").Hide())
	a.parameters("terminal.hint", map[string]ochazuke.Parameter{
		"label":  {Type: "string", Description: "One letter of a label"},
		"action": {Type: "string", Enum: []string{"copy", "type", "open"}, Description: "What a whole label does: copy, type into the pane, or open"},
	})
}

// hintTerminal is the terminal instance a hints verb acts on: the one it
// ran at, else the focus.
func (a *App) hintTerminal(ctx ochazuke.Ctx) (*ochazuke.Terminal, error) {
	if ctx.Instance != nil {
		if t := a.r.Terminal(ctx.Instance.ID); t != nil {
			return t, nil
		}
	}
	if t := a.r.Terminal(a.r.Focus); t != nil {
		return t, nil
	}
	return nil, errors.New(a.r.TerminalLeader() + " f works in a live pane")
}

// terminalPane is the pane a terminal instance streams: the side's pane
// is its Data, the main terminal's is the attached pane. Another terminal
// has none (and no fallback to the attached pane).
func (a *App) terminalPane(id string) (pane, bool) {
	inst := a.r.Instances[id]
	if inst == nil {
		return pane{}, false
	}
	if p, ok := inst.Data.(pane); ok && p.ID != "" {
		return p, true
	}
	if id == paneTermID && a.attached.ID != "" {
		return a.attached, true
	}
	return pane{}, false
}

// startHints is terminal.hints (ctrl+a f): find the items on the visible
// screen, label them nearest the cursor first, draw the labels and enter
// hints mode. No screen yet, or nothing on it, says so and stays live.
func (a *App) startHints(ctx ochazuke.Ctx) ochazuke.Result {
	term, err := a.hintTerminal(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	frame := term.Frame()
	if len(frame.Lines) == 0 {
		ctx.Status("hints: the pane has drawn nothing yet")
		return ochazuke.Done(nil)
	}
	// The emulator pads every row to the pane's columns, so the widest row
	// is the width soft-wrapped rows fill; the drawn size is the fallback.
	width := 0
	for _, line := range frame.Lines {
		width = max(width, ansi.StringWidth(line))
	}
	if width <= 0 {
		width, _ = term.Size()
	}
	var src contextitems.Source
	if p, ok := a.terminalPane(term.ID()); ok {
		src = contextSource(p, "screen")
	}
	spans := contextitems.ExtractSpans(frame.Lines, contextitems.SpanOptions{Width: width, Source: src, Stat: contextitems.CachedStat()})
	if len(spans) == 0 {
		ctx.Status("hints: nothing to label on the screen")
		return ochazuke.Done(nil)
	}
	targets := make([]hints.Target, len(spans))
	for i, s := range spans {
		at := s.Parts[0]
		targets[i] = hints.Target{Text: s.Raw, Key: s.Key, Distance: hints.Distance(at.Row, at.Col, frame.Cursor.Y, frame.Cursor.X, width)}
	}
	labels := hints.Assign(targets, hints.DefaultAlphabet)
	a.hinting = &liveHints{term: term.ID(), spans: spans, labels: labels, matcher: hints.NewMatcher(labels)}
	a.drawHints(term)
	if err := a.r.SetMode(hintsMode); err != nil {
		a.endHints()
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(len(spans))
}

// drawHints sets the overlay: the pane dimmed and, for every occurrence
// whose label still starts with the letters typed, the label over the
// occurrence's first cells.
func (a *App) drawHints(term *ochazuke.Terminal) {
	h := a.hinting
	style := ochazuke.NewStyles(*a.m.Appearance).Accent.Bold(true).Reverse(true)
	o := &ochazuke.TerminalOverlay{Dim: true}
	typed := h.matcher.Typed()
	for i, s := range h.spans {
		if !strings.HasPrefix(h.labels[i], typed) {
			continue
		}
		at := s.Parts[0]
		o.Marks = append(o.Marks, ochazuke.OverlayMark{Row: at.Row, Col: at.Col, Text: h.labels[i], Style: style})
	}
	term.SetOverlay(o)
}

// endHints clears the overlay and the session and returns to terminal
// mode when hints mode is on.
func (a *App) endHints() {
	if a.hinting != nil {
		if term := a.r.Terminal(a.hinting.term); term != nil {
			term.SetOverlay(nil)
		}
		a.hinting = nil
	}
	if a.r.Mode.Name == hintsMode {
		_ = a.r.SetMode("terminal")
	}
}

// hintsUpdate runs after every message: labels left behind by a focus
// change (the CLI, a closed side, a page switch) are cleared. A modal over
// hints mode keeps them; closing it returns to hints mode with them.
func (a *App) hintsUpdate() {
	h := a.hinting
	if h == nil {
		return
	}
	if a.r.Terminal(h.term) == nil {
		a.hinting = nil // the instance went, and its overlay with it
		return
	}
	if a.r.TopModal() != "" {
		return
	}
	if a.r.Focus != h.term || a.r.Mode.Name != hintsMode {
		a.endHints()
	}
}

// cancelHints is terminal.hints-cancel (esc, ctrl+c).
func (a *App) cancelHints(ochazuke.Ctx) ochazuke.Result {
	a.endHints()
	return ochazuke.Done(nil)
}

// eraseHint is terminal.hints-erase (backspace).
func (a *App) eraseHint(ctx ochazuke.Ctx) ochazuke.Result {
	if a.hinting == nil {
		a.endHints()
		return ochazuke.Done(nil)
	}
	a.hinting.matcher.Backspace()
	if term := a.r.Terminal(a.hinting.term); term != nil {
		a.drawHints(term)
	}
	return ochazuke.Done(a.hinting.matcher.Typed())
}

// pressHint is terminal.hint: one letter of a label. A letter that starts
// no label is ignored; a prefix narrows the labels shown; a whole label
// acts with the strongest modifier typed on any of its letters (open over
// type over copy) and leaves hints mode.
func (a *App) pressHint(ctx ochazuke.Ctx) ochazuke.Result {
	h := a.hinting
	if h == nil {
		a.endHints()
		return ochazuke.Done(nil)
	}
	letter := []rune(ochazuke.StringArg(ctx.Args, "label", ""))
	if len(letter) != 1 {
		return ochazuke.Done(nil)
	}
	res := h.matcher.Press(letter[0], hintActions[ochazuke.StringArg(ctx.Args, "action", "copy")])
	switch res.State {
	case hints.NoMatch:
		return ochazuke.Done(nil)
	case hints.Prefix:
		if term := a.r.Terminal(h.term); term != nil {
			a.drawHints(term)
		}
		return ochazuke.Done(h.matcher.Typed())
	}
	span, term := h.spans[res.Index], h.term
	a.endHints()
	// Copy and type use the text as it reads on the screen (Raw: ./main.go,
	// a wrapped URL joined); open uses the resolved item (a path made
	// absolute against the pane's folder, a PR as its URL).
	switch res.Action {
	case hints.Type:
		_, err := a.r.Execute(ochazuke.Request{Noun: "terminal", Verb: "paste", Args: ochazuke.Arguments{"id": term, "text": span.Raw}})
		return resultError(err)
	case hints.Open:
		return a.openHint(ctx, span.Item)
	default:
		return a.copyText(span.Raw)
	}
}

// openHint opens an item with the default app, off the loop, as the
// Context page's open does; a branch or commit has nothing to open.
func (a *App) openHint(ctx ochazuke.Ctx, it contextitems.Item) ochazuke.Result {
	switch it.Kind {
	case "file", "dir", "link", "pr":
	default:
		ctx.Status(fmt.Sprintf("a %s has nothing to open; its label copies it", it.Kind))
		return ochazuke.Done(nil)
	}
	sys, target := a.cx.sys, contextitems.Target(it)
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return target, sys.Open(c, target)
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err == nil {
			ctx.Notify("opened " + oneLine(target))
		}
	})
}
