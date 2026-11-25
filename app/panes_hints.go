package app

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/hints"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/toast"
)

type hintsState struct {
	session       *hints.Session
	paneID        string
	focusJump     bool
	focusStack    []focusHintFrame
	returnMode    string
	returnFocus   string
	returnPage    string
	inScratch     bool
	pendingOpen   bool
	pendingReveal bool
	lines         []string
}

func (a *App) hintsModifierActions() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetHintsModifierActions, true)
}

func hintsHasCandidatePrefix(sess *hints.Session, nextPrefix string) bool {
	if sess == nil {
		return false
	}
	for _, t := range sess.Remaining() {
		if strings.HasPrefix(t.Label, nextPrefix) {
			return true
		}
	}
	return false
}

var hintsStripSGR = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// paneVisibleLines returns the visible terminal lines for pane (either from the
// live PTY frame or from the synthetic terminal buffer).
func (a *App) paneVisibleLines(pane data.Record, live bool) []string {
	if a.panes.Host != nil {
		if f, ok := a.panes.Host.Snapshot(pane.ID); ok && len(f.Lines) > 0 {
			a.rt.live.frames[pane.ID] = f
			if cv, inCopy := a.panes.Term.Copy(pane.ID); inCopy {
				lines, _ := copyLines(cv)
				return lines
			}
			return slices.Clone(f.Lines)
		}
	}
	if f, ok := a.rt.live.frames[pane.ID]; ok && len(f.Lines) > 0 {
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
	inScratch := a.activeScratch() != nil
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
	a.panes.hints = &hintsState{
		session:     sess,
		paneID:      pane.ID,
		returnMode:  curFrame.Mode,
		returnFocus: curFrame.FocusID,
		returnPage:  s.ActivePage,
		inScratch:   inScratch,
		lines:       lines,
	}

	if inScratch || len(s.Modals) > 0 {
		s = withMode(s, ModeHints)
	} else {
		a.panes.activePage[pane.ID] = views.ScreenPageID
		s.ActivePage = SpacePanes
		s = withPage(s, SpacePanes, siteScreen, ModeHints)
	}
	if a.hintsModifierActions() {
		a.say("hints (%d) · lowercase copies · ctrl opens · Shift inserts · alt reveals · esc/q cancels", len(targets))
	} else {
		a.say("hints (%d) · lowercase copies · esc/q cancels", len(targets))
	}
	return core.Next(s)
}

// hintsRenderLines dims baseLines and composites the active hints.Session badges
// over the terminal screen using hints.RenderOverlay.
func (a *App) hintsRenderLines(baseLines []string, w, h int) []string {
	if a.panes.hints == nil || !a.panes.hints.session.Active() {
		return baseLines
	}
	lines := baseLines
	if len(a.panes.hints.lines) > 0 {
		lines = a.panes.hints.lines
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
	overlaid := hints.RenderOverlay(strings.Join(dimmed, "\n"), w, h, a.panes.hints.session, style)
	return strings.Split(overlaid, "\n")
}

func (a *App) exitHints(s core.State) core.State {
	hs := a.panes.hints
	a.panes.hints = nil
	a.viewMemo.gen++
	if hs == nil {
		return s
	}
	hs.pendingOpen = false
	hs.pendingReveal = false
	if hs.session != nil {
		hs.session.Cancel()
	}
	retMode := hs.returnMode
	if retMode == "" {
		retMode = ModeNormal
	}
	if len(s.Modals) > 0 {
		return withMode(s, retMode)
	}
	if hs.focusJump {
		page := hs.returnPage
		if page == "" {
			page = s.ActivePage
		}
		return withPage(s, page, hs.returnFocus, retMode)
	}
	if hs.inScratch {
		return withMode(s, retMode)
	}
	retPage := hs.returnPage
	if retPage == "" {
		retPage = SpacePanes
	}
	retFocus := hs.returnFocus
	if retFocus == "" {
		retFocus = siteScreen
	}
	s.ActivePage = retPage
	return withPage(s, retPage, retFocus, retMode)
}

func (a *App) hintsCancel(s core.State, _ core.Command) core.StepResult {
	s = a.exitHints(s)
	a.say("hints cancelled")
	return core.Next(s)
}

func (a *App) hintsKey(s core.State, c core.Command) core.StepResult {
	if a.panes.hints == nil || a.panes.hints.session == nil || !a.panes.hints.session.Active() {
		return core.Next(a.exitHints(s))
	}
	hs := a.panes.hints
	key := c.Arg("key")
	text := c.Arg("text")

	// In hierarchical focus-hints Level 2+, backspace on an empty prefix steps
	// back out to the parent level and enter stops drilling to focus the panel.
	if hs.focusJump && len(hs.focusStack) > 1 {
		switch key {
		case "backspace", "ctrl+h":
			if hs.session.Prefix() == "" {
				hs.focusStack = hs.focusStack[:len(hs.focusStack)-1]
				parent := &hs.focusStack[len(hs.focusStack)-1]
				parent.session.Reset()
				hs.session = parent.session
				a.viewMemo.gen++
				a.say("%s", focusHintsLegend(hs))
				return core.Next(s)
			}
		case "enter":
			cands := hs.session.Remaining()
			if len(cands) == 0 {
				cands = hs.session.Targets()
			}
			if len(cands) > 0 {
				chosen := cands[0]
				for _, cand := range cands {
					if cand.ID == hs.returnFocus {
						chosen = cand
						break
					}
				}
				s = a.exitHints(s)
				s = withFocus(s, chosen.ID)
				a.say("focused %s", focusTargetDisplayName(chosen))
				return core.Next(s)
			}
		}
	}

	modifiers := !hs.focusJump && a.hintsModifierActions()
	var res hints.Result
	var revealMatch, openMatch bool

	if !hs.focusJump && strings.HasPrefix(key, "alt+") {
		rest := key[len("alt+"):]
		if r, sz := utf8.DecodeRuneInString(rest); rest != "" && sz == len(rest) && r != utf8.RuneError {
			res = hs.session.HandleKey(string(unicode.ToLower(r)))
			if modifiers {
				switch res.Status {
				case hints.StatusActive:
					hs.pendingReveal = true
					hs.pendingOpen = false
				case hints.StatusMatched:
					revealMatch = true
				}
			}
		} else {
			res = hints.Result{Status: hints.StatusNoMatch, Handled: true}
		}
	} else if modifiers && key == "ctrl+h" && hintsHasCandidatePrefix(hs.session, hs.session.Prefix()+"h") {
		res = hs.session.HandleRune('h', false)
		switch res.Status {
		case hints.StatusActive:
			hs.pendingOpen = true
			hs.pendingReveal = false
		case hints.StatusMatched:
			openMatch = true
		}
	} else if key != "" {
		res = hs.session.HandleKey(key)
		if (key == "backspace" || key == "ctrl+h") && hs.session.Prefix() == "" {
			hs.pendingReveal = false
			hs.pendingOpen = false
		} else if modifiers {
			if strings.HasPrefix(key, "ctrl+") && key != "ctrl+h" && key != "ctrl+c" {
				hs.pendingReveal = false
				hs.pendingOpen = false
			} else if r, sz := utf8.DecodeRuneInString(key); sz == len(key) && r != utf8.RuneError && unicode.IsUpper(r) {
				hs.pendingReveal = false
				hs.pendingOpen = false
			}
		}
	} else if r, sz := utf8.DecodeRuneInString(text); text != "" && sz == len(text) && r != utf8.RuneError {
		res = hs.session.HandleRune(r, false)
		if modifiers && unicode.IsUpper(r) {
			hs.pendingReveal = false
			hs.pendingOpen = false
		}
	}
	switch res.Status {
	case hints.StatusCancelled:
		s = a.exitHints(s)
		a.say("hints cancelled")
		return core.Next(s)
	case hints.StatusActive:
		a.viewMemo.gen++
		if hs.focusJump {
			a.say("%s", focusHintsLegend(hs))
		} else {
			a.say("hints: %s_ (%d remaining)", hs.session.Prefix(), len(hs.session.Remaining()))
		}
		return core.Next(s)
	case hints.StatusNoMatch:
		a.say("no hint matches %q · esc/q cancels", key)
		return core.Next(s)
	case hints.StatusMatched:
		target := res.Target
		paneID := hs.paneID
		focusJump := hs.focusJump
		if focusJump {
			if target == nil {
				return core.Next(a.exitHints(s))
			}
			var top focusHintFrame
			if len(hs.focusStack) > 0 {
				top = hs.focusStack[len(hs.focusStack)-1]
			}
			if spaceID, isTab := top.tabSpaceByID[target.ID]; isTab {
				s = a.exitHints(s)
				s = a.openSpace(s, spaceID)
				a.viewMemo.gen++
				a.say("switched to %s", focusTargetDisplayName(*target))
				return core.Next(s)
			}
			if children, isPanel := top.childrenByPanel[target.ID]; isPanel && len(children) > 0 {
				if len(children) == 1 {
					child := children[0]
					s = a.exitHints(s)
					s = withFocus(s, child.ID)
					a.say("focused %s", focusTargetDisplayName(child))
					return core.Next(s)
				}
				fresh := slices.Clone(children)
				for i := range fresh {
					fresh[i].Label = ""
				}
				childSess := hints.NewSession(fresh)
				hs.focusStack = append(hs.focusStack, focusHintFrame{
					session:    childSess,
					panelID:    target.ID,
					panelTitle: target.Value,
				})
				hs.session = childSess
				a.viewMemo.gen++
				a.say("%s", focusHintsLegend(hs))
				return core.Next(s)
			}
			s = a.exitHints(s)
			s = withFocus(s, target.ID)
			a.say("focused %s", focusTargetDisplayName(*target))
			return core.Next(s)
		}
		pendingReveal := hs.pendingReveal
		pendingOpen := hs.pendingOpen
		s = a.exitHints(s)
		if target == nil {
			return core.Next(s)
		}
		var pane *data.Record
		if paneID != "" {
			if rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID}); err == nil {
				pane = rec
			}
		}
		if !a.hintsModifierActions() {
			a.yank(target.Value, target.Kind)
			a.pushToast("Copied "+clip(target.Value, 40), toast.Ok)
			return core.Next(s)
		}
		switch {
		case revealMatch || pendingReveal:
			return a.revealContextValue(s, target.Kind, target.Value, pane)
		case openMatch || pendingOpen || res.Action == hints.ActionAlt:
			return a.systemOpenTarget(s, contextTargetPath(target.Kind, target.Value, pane))
		case res.Action == hints.ActionSecondary:
			return core.Next(a.insertHintTarget(s, hs, pane, target.Kind, target.Value))
		default:
			a.yank(target.Value, target.Kind)
			a.pushToast("Copied "+clip(target.Value, 40), toast.Ok)
			return core.Next(s)
		}
	}
	return core.Next(s)
}

func formModelOfModal(m modal.Modal) *form.Model {
	switch fm := m.(type) {
	case *modal.FormModal:
		return fm.Model()
	case *recordFormModal:
		if fm.FormModal != nil {
			return fm.FormModal.Model()
		}
	case *answerFormModal:
		if fm.FormModal != nil {
			return fm.FormModal.Model()
		}
	}
	return nil
}

// insertHintTarget inserts a Shift-matched hint value into the currently active
// input field (modal input, inline filter/outline edit, or active terminal pane),
// falling back to copying to the clipboard when no input target is active.
func (a *App) insertHintTarget(s core.State, hs *hintsState, pane *data.Record, kind, value string) core.State {
	if value == "" {
		return s
	}
	// 1. Active modal input.
	if a.modals != nil {
		if top := a.modals.Top(); top != nil {
			switch m := top.(type) {
			case *richComposerModal:
				res := m.Handle(core.Command{ID: CmdComposerInsert, Args: map[string]any{"text": value}})
				s = a.applyModalResult(s, res).State
				a.viewMemo.gen++
				a.say("inserted %s into composer", clip(value, 44))
				a.pushToast("Inserted into composer: "+clip(value, 36), toast.Ok)
				return s
			case *modal.FormModal, *recordFormModal, *answerFormModal:
				if fm := formModelOfModal(m); fm != nil && fm.Editable() {
					fm.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": value}})
					a.viewMemo.gen++
					a.say("inserted %s into form", clip(value, 44))
					a.pushToast("Inserted into form: "+clip(value, 36), toast.Ok)
					return s
				}
			case *modal.CommandLineModal:
				res := m.Handle(core.Command{ID: commandline.CmdInsert, Args: map[string]any{"text": value}})
				s = a.applyModalResult(s, res).State
				a.viewMemo.gen++
				a.say("inserted %s into command line", clip(value, 44))
				a.pushToast("Inserted into command line: "+clip(value, 36), toast.Ok)
				return s
			case *modal.PickerModal:
				if m.Model() != nil && m.Model().Filtering {
					res := m.Handle(core.Command{ID: modal.CmdPickerInsert, Args: map[string]any{"text": value}})
					s = a.applyModalResult(s, res).State
					a.viewMemo.gen++
					a.say("inserted %s into filter", clip(value, 44))
					a.pushToast("Inserted into filter: "+clip(value, 36), toast.Ok)
					return s
				}
			case *modal.PaletteModal:
				if pal := m.Model(); pal != nil {
					if pal.FocusForm && pal.ActiveForm != nil && pal.ActiveForm.Editable() {
						pal.ActiveForm.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": value}})
						a.viewMemo.gen++
						a.say("inserted %s into form", clip(value, 44))
						a.pushToast("Inserted into form: "+clip(value, 36), toast.Ok)
						return s
					}
					if !pal.FocusForm && pal.Picker != nil && pal.Picker.Filtering {
						res := m.Handle(core.Command{ID: modal.CmdPickerInsert, Args: map[string]any{"text": value}})
						s = a.applyModalResult(s, res).State
						a.viewMemo.gen++
						a.say("inserted %s into filter", clip(value, 44))
						a.pushToast("Inserted into filter: "+clip(value, 36), toast.Ok)
						return s
					}
				}
			case *modal.PathPickerModal:
				if m.Model() != nil && m.Model().Filtering() {
					res := m.Handle(core.Command{ID: modal.CmdPickerInsert, Args: map[string]any{"text": value}})
					s = a.applyModalResult(s, res).State
					a.viewMemo.gen++
					a.say("inserted %s into filter", clip(value, 44))
					a.pushToast("Inserted into filter: "+clip(value, 36), toast.Ok)
					return s
				}
			}
		}
	}

	// 2. Inline view inputs (filter bar or inline outline edit).
	if hs != nil {
		switch hs.returnMode {
		case ModeFilter:
			s = a.filterEdit(s, core.Command{ID: CmdFilterInsert, Args: map[string]any{"text": value}}).State
			a.viewMemo.gen++
			a.say("inserted %s into filter", clip(value, 44))
			a.pushToast("Inserted into filter: "+clip(value, 36), toast.Ok)
			return s
		case ModeOutlineEdit:
			s = a.outlineEditInsert(s, core.Command{ID: CmdOutlineEditInsert, Args: map[string]any{"text": value}}).State
			a.viewMemo.gen++
			a.say("inserted %s into outline", clip(value, 44))
			a.pushToast("Inserted into outline: "+clip(value, 36), toast.Ok)
			return s
		}
	}

	// 3. Main input for current view (active terminal pane / scratch / side split).
	targetPane := pane
	if targetPane == nil {
		if tp, ok := a.termPane(s); ok {
			targetPane = tp
		}
	}
	if targetPane != nil {
		a.insertTextIntoPane(targetPane, value)
		return s
	}

	// 4. Graceful fallback when view has no active/main input.
	a.yank(value, kind)
	a.pushToast("No active input · copied "+clip(value, 32), toast.Info)
	return s
}

// insertTextIntoPane types/inserts text directly into pane (live PTY or synthetic).
func (a *App) insertTextIntoPane(pane *data.Record, text string) {
	if pane == nil || text == "" {
		return
	}
	if a.panes.Host != nil && a.hasSession(pane.ID) {
		if err := a.panes.Host.Write(pane.ID, []byte(text)); err != nil {
			a.fail("insert into pane", err)
			return
		}
	} else {
		a.termOf(pane.ID).typed += text
	}
	a.say("inserted %s into %s", clip(text, 44), views.Title(a.schema(data.KindPane), *pane))
}

// --- Focus jump hints (ochazuke/widgets/hints over layout.Solved.FocusOrder) -----

// focusHints activates focus-jump hint mode over layout.Solved.FocusOrder using
// the same shared ochazuke/widgets/hints engine and ModeHints as terminal hints.
func (a *App) focusHints(s core.State, _ core.Command) core.StepResult {
	tree := a.View(s)
	solved := s.Layout
	if len(solved.FocusOrder) == 0 && a.Shell != nil {
		solved = a.Shell.State.Layout
	}
	if len(solved.FocusOrder) == 0 {
		w, h := 120, 36
		if a.Shell != nil && a.Shell.W > 0 && a.Shell.H > 0 {
			w, h = a.Shell.W, a.Shell.H
		}
		_, solved = ui.Render(tree, a.theme(), s.ActiveFrame().FocusID, w, h)
	}
	var frame focusHintFrame
	if a.focusHintsDrilldown() {
		frame = buildFocusHintRootFrame(tree, solved, len(s.Modals) == 0)
	} else {
		frame = focusHintFrame{session: hints.NewSession(focusTargetsFromView(tree, solved))}
	}
	if frame.session == nil || !frame.session.Active() {
		a.say("no focusable regions on screen")
		return core.Next(s)
	}
	curFrame := s.ActiveFrame()
	a.panes.hints = &hintsState{
		session:     frame.session,
		focusJump:   true,
		focusStack:  []focusHintFrame{frame},
		returnMode:  curFrame.Mode,
		returnFocus: curFrame.FocusID,
		returnPage:  s.ActivePage,
	}
	a.viewMemo.gen++
	a.say("%s", focusHintsLegend(a.panes.hints))
	return core.Next(withMode(s, ModeHints))
}
