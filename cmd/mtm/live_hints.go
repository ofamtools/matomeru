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
// commits) on the part of the screen drawn now of every live pane drawn
// now (the pane it ran in, and the other of the main pane and its side
// when the page draws both: not zoomed, not in copy mode) with one set of
// short letter labels drawn over the panes, which are dimmed. f on a pane
// row of the Panes page does the same for what the Screen tab draws for
// that row: the live panes when they are drawn, else the row's captured
// screen (the preview), the focus staying on the row. Typing a label
// copies the item, ctrl and the label opens it, shift (a capital) types
// it into the pane it is on (an item on both live panes: the one hints ran
// in, the main pane from the row; from the preview, the row's pane, sent
// through the daemon without attaching it); esc, q or ctrl+c cancels,
// backspace takes the last letter back, and every other key is swallowed,
// so nothing reaches a pane or the list while the labels are up. The
// labels over a live pane are a terminal overlay (ochazuke's
// Terminal.SetOverlay); over the preview, the same overlay shape drawn by
// mtm's Screen tab (hint_overlay.go). The mode is hints, a passthrough
// base mode (no capture pass, so ctrl+c and every ctrl+letter reach the
// rows), whose rows are bound on both the terminal and the pane noun
// (hintRows), so they answer wherever the session runs. The focus stays
// where it ran. Leaving the mode clears every overlay; so does a focus
// change, or a labelled terminal going, while they are up (hintsUpdate).

const hintsMode = "hints"

// hintsAlphabet is the label alphabet: ochazuke's default without q,
// which closes and goes back in mtm (keys.go) and so cancels hints.
var hintsAlphabet = strings.ReplaceAll(hints.DefaultAlphabet, "q", "")

// hintScreen is one labelled screen: a live terminal, or the preview of a
// pane row (term nil), and the pane a capital types into.
type hintScreen struct {
	term *ochazuke.Terminal
	pane pane
	has  bool // pane is known
	// The preview's drawn window (screenWindow) and its overlay, which the
	// Screen tab draws while the session lasts.
	win     screenWindow
	overlay *ochazuke.TerminalOverlay
}

// liveHints is one hints session: where it runs, the screens it labels and
// what is on them.
type liveHints struct {
	home    string        // the focus that keeps the session: a terminal, or a pane row
	row     string        // the pane row whose preview is labelled, "" for live panes
	screens []*hintScreen // every screen labelled, the one it ran in first
	spans   []contextitems.Span
	in      []int    // per span, its screen's index in screens
	labels  []string // one per span; the same item shares a label
	// pieces, per span, is the text drawn in each of its parts: a link
	// wrapped over rows is highlighted on every piece (drawHints).
	pieces  [][]string
	matcher *hints.Matcher
}

// screenWindow is what the Screen tab drew of a row's preview last frame:
// the lines as drawn, which of the preview's lines they are (from top,
// shown of them; a position marker may follow) and the panel's width.
type screenWindow struct {
	row         string
	text        string
	top, shown  int
	width       int
	previewText string // the preview the window was cut from
}

// otherScreen offsets the distances of every screen but the one hints
// ran in, so its items take the shortest labels first.
const otherScreen = 1 << 24

// hintActions name the hint verb's action argument.
var hintActions = map[string]hints.Action{"copy": hints.Copy, "type": hints.Type, "open": hints.Open}

// defineLiveHints adds the hints verbs, ctrl+a f on the terminal noun and
// f on a pane row, and the hints mode's rows on both nouns.
func (a *App) defineLiveHints() {
	a.r.DefineMode(hintsMode, false) // a base mode: it lasts across a label's letters
	_ = a.r.DefinePassthrough(hintsMode)
	// The session's verbs stay on the terminal noun (whose operations the
	// history never records, as it never records keys typed into a pane);
	// a pane row's rows run them too, at the row.
	a.r.Define("terminal").
		Verb("hints", a.startHints, "Label the items on the live panes' screens (URLs, PRs, paths, branches, commits): a label copies, ctrl+label opens, a capital types it into its pane").
		Verb("hint", a.pressHint, "Take one letter of a label; a whole label copies, opens or types its item and leaves hints").
		Verb("hints-erase", a.eraseHint, "Take back the last letter typed").
		Verb("hints-cancel", a.cancelHints, "Clear the labels and go back to the pane or the row").
		Verb("hints-ignore", func(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done(nil) }, "Swallow a key while the labels are up").
		Mode("leader_pending").
		Bind("f", ochazuke.Do("terminal.hints").Hinted("hints"))
	a.r.Define("pane").
		Verb("hints", a.startRowHints, "Label the items on the Screen tab (the live panes, else the captured screen) without leaving the row, switching to it from another tab: a label copies, ctrl+label opens, a capital types it into its pane").
		Mode("normal").
		Bind("f", ochazuke.Do("pane.hints").Hinted("hints"))
	a.hintRows(a.r.Define("terminal").Mode(hintsMode))
	a.hintRows(a.r.Define("pane").Mode(hintsMode))
	a.parameters("terminal.hint", map[string]ochazuke.Parameter{
		"label":  {Type: "string", Description: "One letter of a label"},
		"action": {Type: "string", Enum: []string{"copy", "type", "open"}, Description: "What a whole label does: copy, type into the pane, or open"},
	})
}

// hintRows binds the hints mode's rows on a noun's mode table: one set of
// rows, bound where a session's focus can be (a terminal, a pane row).
func (a *App) hintRows(m *ochazuke.ModeBuilder) {
	hint := func(action string) ochazuke.Binding {
		return ochazuke.Do("terminal.hint", ochazuke.Arguments{"action": action}).FromPayload("label", ochazuke.SourceLetter).Hinted(action)
	}
	// A terminal speaking the kitty keyboard protocol sends shift+a, not A.
	var shifted []string
	for c := 'a'; c <= 'z'; c++ {
		shifted = append(shifted, "shift+"+string(c))
	}
	m.Pattern(ochazuke.Alpha, hint("copy")).
		Pattern(ochazuke.Upper, hint("type")).
		Pattern("one-of:"+strings.Join(shifted, " "), hint("type").Hide()).
		Pattern(ochazuke.CtrlAlpha, hint("open")).
		Bind("backspace", ochazuke.Do("terminal.hints-erase").Hide()).
		Bind("esc", ochazuke.Do("terminal.hints-cancel").Hinted("cancel")).
		Bind("q", ochazuke.Do("terminal.hints-cancel").Hide()).
		Bind("ctrl+c", ochazuke.Do("terminal.hints-cancel").Hide()).
		Pattern(ochazuke.Paste, ochazuke.Do("terminal.hints-ignore").Hide()).
		Pattern(ochazuke.Any, ochazuke.Do("terminal.hints-ignore").Hide())
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

// terminalScreens are the hint screens of terminals.
func (a *App) terminalScreens(terms []*ochazuke.Terminal) []*hintScreen {
	out := make([]*hintScreen, len(terms))
	for i, t := range terms {
		p, ok := a.terminalPane(t.ID())
		out[i] = &hintScreen{term: t, pane: p, has: ok}
	}
	return out
}

// startHints is terminal.hints (ctrl+a f): label the live panes drawn,
// the terminal it ran in first, and keep the focus there.
func (a *App) startHints(ctx ochazuke.Ctx) ochazuke.Result {
	term, err := a.hintTerminal(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.beginHints(ctx, &liveHints{home: term.ID()}, a.terminalScreens(a.hintTerminals(term)))
}

// startRowHints is pane.hints (f on a pane row): label what the Screen
// tab draws, keeping the focus on the row: the live panes when they are
// drawn (main first), else the row's preview as the last frame drew it.
// From another tab it switches to Screen and labels once the capture is
// in. No screen yet says so and stays put.
func (a *App) startRowHints(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil || ctx.Instance.Noun != "pane" {
		return ochazuke.Failed(errors.New("f works on a pane row"))
	}
	row := ctx.Instance.ID
	switched, err := a.toScreen(row)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if switched && a.r.Terminal(paneTermID) == nil {
		p, err := a.paneAt(ctx.Instance)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return a.capturePreview(p, func(ctx ochazuke.Ctx) {
			// A frame lays out the Screen tab, so the window labelled is
			// the one drawn; "nothing to label" is a status.
			a.View()
			_ = a.rowHints(ctx, row)
		})
	}
	return a.rowHints(ctx, row)
}

// rowHints labels the Screen tab of row as shown.
func (a *App) rowHints(ctx ochazuke.Ctx, row string) ochazuke.Result {
	h := &liveHints{home: row}
	if main := a.r.Terminal(paneTermID); main != nil && a.liveShown() && !a.copyShown(paneTermID) {
		return a.beginHints(ctx, h, a.terminalScreens(a.hintTerminals(main)))
	}
	win := a.pv.screen
	if a.pv.tab != tabScreen || a.preview.Pane != row || win.row != row || win.shown == 0 {
		ctx.Info("hints: no screen for this pane yet")
		return ochazuke.Done(nil)
	}
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	h.row = row
	return a.beginHints(ctx, h, []*hintScreen{{pane: p, has: true, win: win}})
}

// hintText is one screen as hints reads it: every line of the screen,
// the cursor, and the window of it that is drawn (rows from top, at most
// rows of them and cols columns; 0 is no bound).
type hintText struct {
	lines           []string
	cursor          ochazuke.Pos
	top, rows, cols int
	// padded reports rows padded to the pane's columns (an emulator's
	// frame), so a row that fills them continues on the next; a capture's
	// rows are not, and are never joined.
	padded bool
}

// text is the screen's text and drawn window. A terminal draws the top
// left of its frame at the size it was last told to fill (Terminal.Size),
// which is smaller than the frame while the pane has not taken the size
// mtm draws it at (another client holds it larger, a layout just
// changed); the preview draws the window the Screen tab cut last frame.
func (s *hintScreen) text() hintText {
	if s.term != nil {
		frame := s.term.Frame()
		cols, rows := s.term.Size()
		return hintText{lines: frame.Lines, cursor: frame.Cursor, rows: rows, cols: cols, padded: true}
	}
	lines := strings.Split(s.win.previewText, "\n")
	if s.win.previewText == "" {
		lines = nil
	}
	// No cursor in a capture: the nearest items are at the bottom of the
	// window, where a prompt is.
	return hintText{lines: lines, cursor: ochazuke.Pos{Y: s.win.top + s.win.shown - 1}, top: s.win.top, rows: s.win.shown, cols: s.win.width}
}

// shows reports whether a cell of the screen is drawn.
func (t hintText) shows(row, col int) bool {
	if row < t.top || t.rows > 0 && row >= t.top+t.rows {
		return false
	}
	return t.cols <= 0 || col < t.cols
}

// beginHints finds the items drawn on the screens, labels them as one set
// (the same item on two screens shares a label) nearest the cursor of the
// first screen first, draws the labels and enters hints mode. No screen
// drawn yet, or nothing on any, says so and stays put; a second screen
// with nothing on it says so beside the labels.
func (a *App) beginHints(ctx ochazuke.Ctx, h *liveHints, screens []*hintScreen) ochazuke.Result {
	var targets []hints.Target
	drawn := false
	var empty []string
	for _, sc := range screens {
		st := sc.text()
		if len(st.lines) == 0 {
			continue
		}
		drawn = true
		// The emulator pads every row to the pane's columns, so the widest
		// row is the width soft-wrapped rows fill; the drawn size is the
		// fallback. A capture's rows end at their text: the widest is not
		// the pane's width, so nothing is joined (a wrapped URL there is
		// labelled by its first row's part).
		width := 0
		for _, line := range st.lines {
			width = max(width, ansi.StringWidth(line))
		}
		if width <= 0 {
			width = st.cols
		}
		join := width
		if !st.padded {
			join = 0
		}
		var src contextitems.Source
		if sc.has {
			src = contextSource(sc.pane, "screen")
		}
		var spans []contextitems.Span
		for _, s := range contextitems.ExtractSpans(st.lines, contextitems.SpanOptions{Width: join, Source: src, Stat: contextitems.CachedStat()}) {
			// An item whose first cell is not drawn gets no label: there
			// would be nowhere to show it.
			if at := s.Parts[0]; st.shows(at.Row, at.Col) {
				h.pieces = append(h.pieces, spanPieceTexts(st.lines, s.Parts))
				s.Parts = shiftParts(s.Parts, st.top)
				spans = append(spans, s)
			}
		}
		if len(spans) == 0 {
			empty = append(empty, screenName(sc))
			continue
		}
		offset := otherScreen * len(h.screens)
		for _, s := range spans {
			at := s.Parts[0]
			targets = append(targets, hints.Target{Text: s.Raw, Key: s.Key, Distance: offset + hints.Distance(at.Row, at.Col, st.cursor.Y-st.top, st.cursor.X, width)})
			h.in = append(h.in, len(h.screens))
		}
		h.spans = append(h.spans, spans...)
		h.screens = append(h.screens, sc)
	}
	if !drawn {
		ctx.Info("hints: the pane has drawn nothing yet")
		return ochazuke.Done(nil)
	}
	if len(h.spans) == 0 {
		ctx.Info("hints: nothing to label on the screen")
		return ochazuke.Done(nil)
	}
	h.labels = hints.Assign(targets, hintsAlphabet)
	h.matcher = hints.NewMatcher(h.labels)
	a.hinting = h
	a.drawHints()
	if err := a.r.SetMode(hintsMode); err != nil {
		a.endHints()
		return ochazuke.Failed(err)
	}
	if len(empty) > 0 {
		ctx.Info("hints: nothing to label on the " + strings.Join(empty, " or the "))
	}
	return ochazuke.Done(len(h.spans))
}

// screenName names a screen in a status line.
func screenName(sc *hintScreen) string {
	switch {
	case sc.term == nil:
		return "screen"
	case sc.term.ID() == sideTermID:
		return "side"
	}
	return "pane"
}

// spanPieceTexts is the plain text lines draw in each of parts.
func spanPieceTexts(lines []string, parts []contextitems.Cell) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		if p.Row >= 0 && p.Row < len(lines) {
			out[i] = ansi.Strip(ansi.Cut(lines[p.Row], p.Col, p.End))
		}
	}
	return out
}

// shiftParts moves a span's cells up by top rows: a preview's rows are
// counted from the window's first line, as it is drawn.
func shiftParts(parts []contextitems.Cell, top int) []contextitems.Cell {
	if top == 0 {
		return parts
	}
	out := make([]contextitems.Cell, len(parts))
	for i, c := range parts {
		c.Row -= top
		out[i] = c
	}
	return out
}

// drawHints sets every labelled screen's overlay: the screen dimmed and,
// for every occurrence on it whose label still starts with the letters
// typed, the label over the occurrence's first cells. An occurrence
// joined from pieces on several rows (a wrapped link) has every piece
// highlighted, its one label over the first.
func (a *App) drawHints() {
	h := a.hinting
	styles := ochazuke.NewStyles(*a.m.Appearance)
	style := styles.Accent.Bold(true).Reverse(true)
	piece := styles.Accent.Underline(true)
	overlays := make([]*ochazuke.TerminalOverlay, len(h.screens))
	for i := range overlays {
		overlays[i] = &ochazuke.TerminalOverlay{Dim: true}
	}
	typed := h.matcher.Typed()
	var labels []*ochazuke.TerminalOverlay
	var marks []ochazuke.OverlayMark
	for i, s := range h.spans {
		if !strings.HasPrefix(h.labels[i], typed) {
			continue
		}
		o := overlays[h.in[i]]
		if len(s.Parts) > 1 && i < len(h.pieces) {
			for j, p := range s.Parts {
				if text := h.pieces[i][j]; text != "" {
					o.Marks = append(o.Marks, ochazuke.OverlayMark{Row: p.Row, Col: p.Col, Text: text, Style: piece})
				}
			}
		}
		at := s.Parts[0]
		labels = append(labels, o)
		marks = append(marks, ochazuke.OverlayMark{Row: at.Row, Col: at.Col, Text: h.labels[i], Style: style})
	}
	// Labels go over every highlight.
	for i, o := range labels {
		o.Marks = append(o.Marks, marks[i])
	}
	for i, sc := range h.screens {
		switch {
		case sc.term == nil:
			sc.overlay = overlays[i]
		case a.r.Terminal(sc.term.ID()) == sc.term:
			sc.term.SetOverlay(overlays[i])
		}
	}
}

// endHints clears every overlay the session set (on the terminals still
// there) and the session, and returns to the mode of its home when hints
// mode is on: terminal in a pane, normal on a row.
func (a *App) endHints() {
	home := ""
	if h := a.hinting; h != nil {
		for _, sc := range h.screens {
			if sc.term != nil && a.r.Terminal(sc.term.ID()) == sc.term {
				sc.term.SetOverlay(nil)
			}
		}
		home = h.home
		a.hinting = nil
	}
	if a.r.Mode.Name == hintsMode {
		mode := "normal"
		if a.r.Terminal(a.r.Focus) != nil || home != "" && a.r.Terminal(home) != nil {
			mode = "terminal"
		}
		_ = a.r.SetMode(mode)
	}
}

// previewHints is the overlay of a preview session on row, nil when none
// is up: the Screen tab draws it over the window it froze.
func (a *App) previewHints(row string) *hintScreen {
	h := a.hinting
	if h == nil || h.row == "" || h.row != row || len(h.screens) == 0 || h.screens[0].term != nil {
		return nil
	}
	return h.screens[0]
}

// hintsUpdate runs after every message: labels left behind by a focus
// change (the CLI, a closed side, a page switch), by a labelled terminal
// that went or was replaced (a side swapped by a listing), or by the
// preview's row leaving the Screen tab, are cleared, on every labelled
// terminal still there. A modal over hints mode keeps them; closing it
// returns to hints mode with them.
func (a *App) hintsUpdate() {
	h := a.hinting
	if h == nil {
		return
	}
	for _, sc := range h.screens {
		if sc.term != nil && a.r.Terminal(sc.term.ID()) != sc.term {
			a.endHints()
			return
		}
	}
	if a.r.Instances[h.home] == nil || h.row != "" && (a.r.Page != panesPage || a.pv.tab != tabScreen) {
		a.endHints()
		return
	}
	if a.r.TopModal() != "" {
		return
	}
	if a.r.Focus != h.home || a.r.Mode.Name != hintsMode {
		a.endHints()
	}
}

// cancelHints is terminal.hints-cancel (esc, q, ctrl+c).
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
	// screen hints ran in first (from a row, the main pane): an item on
	// both screens types into that one, an item on one screen only into
	// its own.
	span, sc := h.spans[res.Index], h.screens[h.in[res.Index]]
	a.endHints()
	// Copy and type use the text as it reads on the screen (Raw: ./main.go,
	// a wrapped URL joined); open uses the resolved item (a path made
	// absolute against the pane's folder, a PR as its URL).
	switch res.Action {
	case hints.Type:
		if sc.term == nil {
			return a.sendText(sc.pane, span.Raw)
		}
		_, err := a.r.Execute(ochazuke.Request{Noun: "terminal", Verb: "paste", Args: ochazuke.Arguments{"id": sc.term.ID(), "text": span.Raw}})
		return resultError(err)
	case hints.Open:
		return a.openHint(ctx, span.Item)
	default:
		return a.copyText(span.Raw)
	}
}

// sendText types text into a pane that is not attached, as a bracketed
// paste through the daemon (the stream's Paste does the same for an
// attached one), off the loop.
func (a *App) sendText(p pane, text string) ochazuke.Result {
	d := a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return nil, d.PasteText(c, p, text)
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err == nil {
			ctx.Notify("typed " + oneLine(text) + " into " + p.label())
		}
	})
}

// openHint opens an item with the default app, off the loop, as the
// Context page's open does; a branch or commit has nothing to open.
func (a *App) openHint(ctx ochazuke.Ctx, it contextitems.Item) ochazuke.Result {
	switch it.Kind {
	case "file", "dir", "link", "pr":
	default:
		ctx.Info(fmt.Sprintf("a %s has nothing to open; its label copies it", it.Kind))
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
