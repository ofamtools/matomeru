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
// pane or its side) labels every item (URLs, PRs, paths, branches,
// commits) on the visible screen of every live pane drawn now (the pane it
// ran in, and the other of the main pane and its side when the page draws
// both: not zoomed, not in copy mode) with one set of short letter labels
// drawn over the live panes, which are dimmed. Typing a label copies the item, ctrl and
// the label opens it, shift (a capital) types it into the pane; esc or
// ctrl+c cancels, backspace takes the last letter back, and every other
// key is swallowed, so nothing reaches the pane while the labels are up.
// The labels are a terminal overlay (ochazuke's Terminal.SetOverlay) plus
// the hints mode, a passthrough base mode (no capture pass, so ctrl+c and
// every ctrl+letter reach the rows). The focus stays on the terminal it
// ran in, so the keys answer there and reach neither pane. Leaving the
// mode clears every overlay; so does a focus change, or either labelled
// terminal going, while they are up (hintsUpdate).

const hintsMode = "hints"

// liveHints is one hints session: the terminals it labels and what is on
// their screens.
type liveHints struct {
	term    string               // the terminal it ran in, which keeps the focus
	screens []*ochazuke.Terminal // every terminal labelled, term first
	spans   []contextitems.Span  // every occurrence: term's in reading order, then the next screen's
	in      []int                // per span, its terminal's index in screens
	labels  []string             // one per span; the same item shares a label
	matcher *hints.Matcher
}

// otherScreen offsets the distances of every terminal but the one hints
// ran in, so its items take the shortest labels first.
const otherScreen = 1 << 24

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
		Verb("hints", a.startHints, "Label the items on the live panes' screens (URLs, PRs, paths, branches, commits): a label copies, ctrl+label opens, a capital types it into its pane").
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

// hintTerminals are the terminals a hints session from term labels: term
// first, then the other of the main pane and its side when the page draws
// it live beside term. Zoomed, the page draws only the focused one; a
// terminal in copy mode draws its copy buffer, not the live screen, so it
// is left out; on another tab the live panes are not drawn (liveShown).
// ochazuke records what the last frame placed (Page.drawn, the focus jumps
// read it) but does not export it, so this follows mtm's own layout.
func (a *App) hintTerminals(term *ochazuke.Terminal) []*ochazuke.Terminal {
	out := []*ochazuke.Terminal{term}
	other := ""
	switch term.ID() {
	case paneTermID:
		other = sideTermID
	case sideTermID:
		other = paneTermID
	}
	if other == "" || a.r.Page != panesPage || !a.liveShown() || a.pv.zoom || a.copyShown(other) {
		return out
	}
	if t := a.r.Terminal(other); t != nil {
		out = append(out, t)
	}
	return out
}

// startHints is terminal.hints (ctrl+a f): find the items on the visible
// screens, label them as one set (the same item in both panes shares a
// label) nearest the cursor of the terminal it ran in first, draw the
// labels and enter hints mode. No screen yet, or nothing on any, says so
// and stays live.
func (a *App) startHints(ctx ochazuke.Ctx) ochazuke.Result {
	term, err := a.hintTerminal(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	h := &liveHints{term: term.ID()}
	var targets []hints.Target
	drawn := false
	for _, t := range a.hintTerminals(term) {
		frame := t.Frame()
		if len(frame.Lines) == 0 {
			continue
		}
		drawn = true
		// The emulator pads every row to the pane's columns, so the widest
		// row is the width soft-wrapped rows fill; the drawn size is the
		// fallback.
		width := 0
		for _, line := range frame.Lines {
			width = max(width, ansi.StringWidth(line))
		}
		if width <= 0 {
			width, _ = t.Size()
		}
		var src contextitems.Source
		if p, ok := a.terminalPane(t.ID()); ok {
			src = contextSource(p, "screen")
		}
		spans := contextitems.ExtractSpans(frame.Lines, contextitems.SpanOptions{Width: width, Source: src, Stat: contextitems.CachedStat()})
		if len(spans) == 0 {
			continue
		}
		offset := otherScreen * len(h.screens)
		for _, s := range spans {
			at := s.Parts[0]
			targets = append(targets, hints.Target{Text: s.Raw, Key: s.Key, Distance: offset + hints.Distance(at.Row, at.Col, frame.Cursor.Y, frame.Cursor.X, width)})
			h.in = append(h.in, len(h.screens))
		}
		h.spans = append(h.spans, spans...)
		h.screens = append(h.screens, t)
	}
	if !drawn {
		ctx.Status("hints: the pane has drawn nothing yet")
		return ochazuke.Done(nil)
	}
	if len(h.spans) == 0 {
		ctx.Status("hints: nothing to label on the screen")
		return ochazuke.Done(nil)
	}
	h.labels = hints.Assign(targets, hints.DefaultAlphabet)
	h.matcher = hints.NewMatcher(h.labels)
	a.hinting = h
	a.drawHints()
	if err := a.r.SetMode(hintsMode); err != nil {
		a.endHints()
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(len(h.spans))
}

// drawHints sets every labelled terminal's overlay: the pane dimmed and,
// for every occurrence on it whose label still starts with the letters
// typed, the label over the occurrence's first cells.
func (a *App) drawHints() {
	h := a.hinting
	style := ochazuke.NewStyles(*a.m.Appearance).Accent.Bold(true).Reverse(true)
	overlays := make([]*ochazuke.TerminalOverlay, len(h.screens))
	for i := range overlays {
		overlays[i] = &ochazuke.TerminalOverlay{Dim: true}
	}
	typed := h.matcher.Typed()
	for i, s := range h.spans {
		if !strings.HasPrefix(h.labels[i], typed) {
			continue
		}
		at := s.Parts[0]
		o := overlays[h.in[i]]
		o.Marks = append(o.Marks, ochazuke.OverlayMark{Row: at.Row, Col: at.Col, Text: h.labels[i], Style: style})
	}
	for i, t := range h.screens {
		if a.r.Terminal(t.ID()) == t {
			t.SetOverlay(overlays[i])
		}
	}
}

// endHints clears every overlay the session set (on the terminals still
// there) and the session, and returns to terminal mode when hints mode is
// on.
func (a *App) endHints() {
	if h := a.hinting; h != nil {
		for _, t := range h.screens {
			if a.r.Terminal(t.ID()) == t {
				t.SetOverlay(nil)
			}
		}
		a.hinting = nil
	}
	if a.r.Mode.Name == hintsMode {
		_ = a.r.SetMode("terminal")
	}
}

// hintsUpdate runs after every message: labels left behind by a focus
// change (the CLI, a closed side, a page switch), or by a labelled
// terminal that went or was replaced (a side swapped by a listing), are
// cleared, on every labelled terminal still there. A modal over hints
// mode keeps them; closing it returns to hints mode with them.
func (a *App) hintsUpdate() {
	h := a.hinting
	if h == nil {
		return
	}
	for _, t := range h.screens {
		if a.r.Terminal(t.ID()) != t {
			a.endHints()
			return
		}
	}
	if a.r.Terminal(h.term) == nil {
		a.endHints()
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
	a.drawHints()
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
		a.drawHints()
		return ochazuke.Done(h.matcher.Typed())
	}
	// Index is the label's first occurrence, and the spans list the
	// terminal hints ran in first: an item on both screens types into
	// that one, an item on one screen only into its own.
	span, term := h.spans[res.Index], h.screens[h.in[res.Index]].ID()
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
