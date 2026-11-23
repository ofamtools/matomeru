package app

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/widgets/hints"
)

type hintsState struct {
	session     *hints.Session
	paneID      string
	returnMode  string
	returnFocus string
	inScratch   bool
	lines       []string
}

var hintsStripSGR = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// paneVisibleLines returns the visible terminal lines for pane (either from the
// live PTY frame or from the synthetic terminal buffer).
func (a *App) paneVisibleLines(pane data.Record, live bool) []string {
	if a.Host != nil {
		if f, ok := a.Host.Snapshot(pane.ID); ok && len(f.Lines) > 0 {
			a.live.frames[pane.ID] = f
			if cv, inCopy := a.Term.Copy(pane.ID); inCopy {
				lines, _ := copyLines(cv)
				return lines
			}
			return slices.Clone(f.Lines)
		}
	}
	if f, ok := a.live.frames[pane.ID]; ok && len(f.Lines) > 0 {
		return slices.Clone(f.Lines)
	}
	return a.termLines(pane, live)
}

// paneHints activates live terminal hint mode using contextitems.ExtractVisibleMatches
// and ochazuke/widgets/hints.
func (a *App) paneHints(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	inScratch := topModal(s) == modalScratch
	lines := a.paneVisibleLines(*pane, s.ActiveFrame().Mode == ModeTerminal)
	matches := contextitems.ExtractVisibleMatches(lines)
	a.recordVisibleMatches(*pane, matches)

	if len(matches) == 0 {
		a.say("no URLs, paths or PR refs visible on screen")
		return core.Next(s)
	}

	targets := make([]hints.Target, len(matches))
	for i, m := range matches {
		targets[i] = hints.Target{
			ID:    fmt.Sprintf("h-%d", i),
			Rect:  layout.Rect{X: m.StartCol, Y: m.Row, W: max(1, m.EndCol-m.StartCol), H: 1},
			Kind:  m.Kind,
			Value: m.Value,
		}
	}
	sess := hints.NewSession(targets)
	curFrame := s.ActiveFrame()
	a.hints = &hintsState{
		session:     sess,
		paneID:      pane.ID,
		returnMode:  curFrame.Mode,
		returnFocus: curFrame.FocusID,
		inScratch:   inScratch,
		lines:       lines,
	}

	if !inScratch {
		a.activePage[pane.ID] = views.ScreenPageID
		s.ActivePage = SpacePanes
		s = withPage(s, SpacePanes, siteScreen, ModeHints)
	} else {
		s = withMode(s, ModeHints)
	}
	a.say("hints (%d) · lowercase copies · ctrl opens · UPPERCASE types · esc/q cancels", len(targets))
	return core.Next(s)
}

// hintsRenderLines dims baseLines and composites the active hints.Session badges
// over the terminal screen using hints.RenderOverlay.
func (a *App) hintsRenderLines(baseLines []string, w, h int) []string {
	if a.hints == nil || !a.hints.session.Active() {
		return baseLines
	}
	lines := baseLines
	if len(a.hints.lines) > 0 {
		lines = a.hints.lines
	}
	if w <= 0 {
		w = 80
	}
	if h < len(lines) {
		h = len(lines)
	}
	if h <= 0 {
		h = 24
	}
	dimmed := make([]string, len(lines))
	for i, raw := range lines {
		plain := hintsStripSGR.ReplaceAllString(raw, "")
		dimmed[i] = "\x1b[2m" + plain + "\x1b[0m"
	}
	style := hints.OverlayStyleFromTheme(a.theme())
	overlaid := hints.RenderOverlay(strings.Join(dimmed, "\n"), w, h, a.hints.session, style)
	return strings.Split(overlaid, "\n")
}

func (a *App) exitHints(s core.State) core.State {
	hs := a.hints
	a.hints = nil
	if hs == nil {
		return s
	}
	if hs.session != nil {
		hs.session.Cancel()
	}
	retMode := hs.returnMode
	if retMode == "" {
		retMode = ModeNormal
	}
	if hs.inScratch {
		return withMode(s, retMode)
	}
	retFocus := hs.returnFocus
	if retFocus == "" {
		retFocus = siteScreen
	}
	return withPage(s, SpacePanes, retFocus, retMode)
}

func (a *App) hintsCancel(s core.State, _ core.Command) core.StepResult {
	s = a.exitHints(s)
	a.say("hints cancelled")
	return core.Next(s)
}

func (a *App) hintsKey(s core.State, c core.Command) core.StepResult {
	if a.hints == nil || a.hints.session == nil || !a.hints.session.Active() {
		return core.Next(a.exitHints(s))
	}
	key := c.Arg("key")
	text := c.Arg("text")
	var res hints.Result
	if key != "" {
		res = a.hints.session.HandleKey(key)
	} else if r, sz := utf8.DecodeRuneInString(text); text != "" && sz == len(text) && r != utf8.RuneError {
		res = a.hints.session.HandleRune(r, false)
	}
	switch res.Status {
	case hints.StatusCancelled:
		s = a.exitHints(s)
		a.say("hints cancelled")
		return core.Next(s)
	case hints.StatusActive:
		a.say("hints: %s_ (%d remaining)", a.hints.session.Prefix(), len(a.hints.session.Remaining()))
		return core.Next(s)
	case hints.StatusNoMatch:
		a.say("no hint matches %q · esc/q cancels", key)
		return core.Next(s)
	case hints.StatusMatched:
		target := res.Target
		paneID := a.hints.paneID
		s = a.exitHints(s)
		if target == nil {
			return core.Next(s)
		}
		var pane *data.Record
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID}); err == nil {
			pane = rec
		}
		switch res.Action {
		case hints.ActionAlt:
			// ctrl+label opens the target via handlers.
			return a.openContextValue(s, target.Kind, target.Value, pane)
		case hints.ActionSecondary:
			// UPPERCASE label types/inserts the target into the pane.
			a.insertTextIntoPane(pane, target.Value)
			return core.Next(s)
		default:
			// lowercase label copies the target to clipboard + registers.
			a.yank(target.Value, target.Kind)
			return core.Next(s)
		}
	}
	return core.Next(s)
}

// insertTextIntoPane types/inserts text directly into pane (live PTY or synthetic).
func (a *App) insertTextIntoPane(pane *data.Record, text string) {
	if pane == nil || text == "" {
		return
	}
	if a.Host != nil && a.hasSession(pane.ID) {
		if err := a.Host.Write(pane.ID, []byte(text)); err != nil {
			a.fail("insert into pane", err)
			return
		}
	} else {
		a.termOf(pane.ID).typed += text
	}
	a.say("inserted %s into %s", clip(text, 44), views.Title(a.schema(data.KindPane), *pane))
}
