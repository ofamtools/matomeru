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
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/hints"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

const hintsActionPick hints.Action = HintsActionPick

var hintsCycleOrder = []hints.Action{
	hints.ActionDefault,
	hints.ActionSecondary,
	hints.ActionAlt,
	hints.ActionMeta,
}

type hintsState struct {
	session       *hints.Session
	paneID        string
	focusJump     bool
	screenOverlay bool
	focusStack    []focusHintFrame
	returnMode    string
	returnFocus   string
	returnPage    string
	inScratch     bool
	lines         []string
}

func (a *App) screenHintsActive() bool {
	return a != nil && a.panes != nil && a.panes.hints != nil && a.panes.hints.screenOverlay && a.panes.hints.session != nil && a.panes.hints.session.Active()
}

func (a *App) screenVisibleLines(s core.State) []string {
	w, h := 120, 36
	if a.Shell != nil && a.Shell.W > 0 && a.Shell.H > 0 {
		w, h = a.Shell.W, a.Shell.H
	}
	r := ui.NewRenderer(0)
	frame, _ := r.Render(a.View(s), a.theme(), s.ActiveFrame().FocusID, w, h)
	rawLines := strings.Split(frame.ANSI, "\n")
	plain := make([]string, len(rawLines))
	for i, ln := range rawLines {
		plain[i] = hintsStripSGR.ReplaceAllString(ln, "")
	}
	return plain
}

func (a *App) screenHintsOverlay(w, h int) ui.ViewNode {
	if w <= 0 {
		w = 120
	}
	if h <= 0 {
		h = 36
	}
	lines := a.hintsRenderLines(a.panes.hints.lines, w, h)
	ov := modal.BuildOverlay(modal.Frame{
		ID:        "screen.hints",
		WidthPct:  100,
		HeightPct: 100,
	}, ui.ViewNode{
		ID:    "screen.hints.raw",
		Kind:  ui.KindRaw,
		Props: map[string]any{"lines": lines},
	})
	ov.ID = "screen.hints.overlay"
	return ov
}

func (a *App) hintsModifierActionsEnabled() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetHintsModifierActions, true)
}

func hintActionFromName(name string) (hints.Action, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case HintsActionCopy:
		return hints.ActionDefault, true
	case HintsActionInsert:
		return hints.ActionSecondary, true
	case HintsActionOpen:
		return hints.ActionAlt, true
	case HintsActionReveal:
		return hints.ActionMeta, true
	case HintsActionPick:
		return hintsActionPick, true
	default:
		return "", false
	}
}

func hintActionName(a hints.Action) string {
	switch a {
	case hints.ActionSecondary:
		return HintsActionInsert
	case hints.ActionAlt:
		return HintsActionOpen
	case hints.ActionMeta:
		return HintsActionReveal
	case hintsActionPick:
		return HintsActionPick
	default:
		return HintsActionCopy
	}
}

func hintActionVerbLabel(a hints.Action) string {
	switch a {
	case hints.ActionSecondary:
		return "inserts"
	case hints.ActionAlt:
		return "opens"
	case hints.ActionMeta:
		return "reveals"
	case hintsActionPick:
		return "picks action"
	default:
		return "copies"
	}
}

func hintsActionLegend(modifiers bool) []hints.ActionLegend {
	if modifiers {
		return []hints.ActionLegend{
			{Key: "a-z", Label: HintsActionCopy, Action: hints.ActionDefault},
			{Key: "Shift", Label: HintsActionInsert, Action: hints.ActionSecondary},
			{Key: "ctrl", Label: HintsActionOpen, Action: hints.ActionAlt},
			{Key: "alt", Label: HintsActionReveal, Action: hints.ActionMeta},
			{Key: "tab", Label: "cycle"},
			{Key: "esc", Label: "cancel"},
		}
	}
	return []hints.ActionLegend{
		{Label: HintsActionCopy, Action: hints.ActionDefault},
		{Label: HintsActionInsert, Action: hints.ActionSecondary},
		{Label: HintsActionOpen, Action: hints.ActionAlt},
		{Label: HintsActionReveal, Action: hints.ActionMeta},
		{Key: "tab", Label: "cycle"},
		{Key: "esc", Label: "cancel"},
	}
}

func hintsStatusLine(sess *hints.Session) string {
	if sess == nil {
		return "hints"
	}
	act := sess.ActiveAction()
	count := len(sess.Remaining())
	if sess.ModifiersEnabled() {
		if act == hints.ActionDefault {
			return fmt.Sprintf("hints (%d) · lowercase copies · ctrl opens · Shift inserts · alt reveals · tab cycles · esc/q cancels", count)
		}
		return fmt.Sprintf("hints (%d) [%s] · lowercase %s · ctrl opens · Shift inserts · alt reveals · tab cycles · esc/q cancels",
			count, hintActionName(act), hintActionVerbLabel(act))
	}
	if act == hints.ActionDefault {
		return fmt.Sprintf("hints (%d) · lowercase copies · tab cycles · esc/q cancels", count)
	}
	return fmt.Sprintf("hints (%d) [%s] · lowercase %s · tab cycles · esc/q cancels",
		count, hintActionName(act), hintActionVerbLabel(act))
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

// paneHints activates universal hint mode using contextitems.ExtractVisibleMatches
// and ochazuke/widgets/hints. When focused on a live terminal pane it overlays
// the pane screen and records matches into context items; on any other screen
// (Show pages, Markdown readers, Work tables, Project Home, modals) it extracts
// visible paths, URLs, and PR refs from the rendered frame and overlays hints
// in place without switching spaces.
func (a *App) paneHints(s core.State, c core.Command) core.StepResult {
	pane, hasPane := a.termPane(s)
	inScratch := a.activeScratch() != nil
	usePane := hasPane && (inScratch || s.ActivePage == SpacePanes || a.activeChildPaneID(s) != "")

	var lines []string
	var paneID string
	if usePane {
		paneID = pane.ID
		lines = a.paneVisibleLines(*pane, s.ActiveFrame().Mode == ModeTerminal)
	} else {
		lines = a.screenVisibleLines(s)
	}
	matches := contextitems.ExtractVisibleMatches(lines)
	if usePane {
		a.recordVisibleMatches(*pane, matches)
	}

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

	actionName := strings.TrimSpace(c.Arg("action"))
	if actionName == "" {
		actionName = a.hintsDefaultAction()
	}
	defaultAct, ok := hintActionFromName(actionName)
	if !ok {
		defaultAct, _ = hintActionFromName(a.hintsDefaultAction())
	}
	mods := a.hintsModifierActionsEnabled()
	sess := hints.NewSession(
		targets,
		hints.WithDefaultAction(defaultAct),
		hints.WithModifiers(mods),
		hints.WithActionLegend(hintsActionLegend(mods)),
	)

	curFrame := s.ActiveFrame()
	a.panes.hints = &hintsState{
		session:       sess,
		paneID:        paneID,
		screenOverlay: !usePane,
		returnMode:    curFrame.Mode,
		returnFocus:   curFrame.FocusID,
		returnPage:    s.ActivePage,
		inScratch:     inScratch,
		lines:         lines,
	}

	if !usePane || inScratch || len(s.Modals) > 0 {
		s = withMode(s, ModeHints)
	} else {
		targetSpace := SpacePanes
		if isProjectTab(s.ActivePage) {
			targetSpace = s.ActivePage
		}
		targetFocus := siteScreen
		if curFrame.FocusID == sideScreen {
			targetFocus = sideScreen
		} else {
			a.panes.activePage[pane.ID] = views.ScreenPageID
		}
		s.ActivePage = targetSpace
		s = withPage(s, targetSpace, targetFocus, ModeHints)
	}
	a.viewMemo.gen++
	a.say("%s", hintsStatusLine(sess))
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

// applyTerminalHints overlays active hint badges onto screen when a terminal
// hints session targets paneID, returning optional panel badges and the live
// hints status bar node (nil when inactive or targeting another pane).
func (a *App) applyTerminalHints(paneID string, screen *ui.ViewNode, shownLines []string, w, h int, barID string) ([]ui.Badge, *ui.ViewNode) {
	if a == nil || a.panes == nil || a.panes.hints == nil || a.panes.hints.focusJump {
		return nil, nil
	}
	sess := a.panes.hints.session
	if sess == nil || !sess.Active() || a.panes.hints.paneID != paneID {
		return nil, nil
	}
	if screen != nil {
		if screen.Props == nil {
			screen.Props = map[string]any{}
		}
		screen.Props["lines"] = a.hintsRenderLines(shownLines, w, h)
		delete(screen.Props, "cursor")
	}
	bar := sess.View(barID)
	badges := []ui.Badge{{
		Text: fmt.Sprintf("hints %d · %s", len(sess.Remaining()), hintActionName(sess.ActiveAction())),
		Tone: "accent",
	}}
	return badges, &bar
}

func (a *App) exitHints(s core.State) core.State {
	hs := a.panes.hints
	a.panes.hints = nil
	a.viewMemo.gen++
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
	if len(s.Modals) > 0 {
		return withMode(s, retMode)
	}
	if hs.focusJump || hs.screenOverlay {
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

func (a *App) cycleHintsActionDir(s core.State, dir int) (core.StepResult, bool) {
	hs := a.panes.hints
	if hs == nil || hs.focusJump || hs.session == nil || !hs.session.Active() {
		return core.StepResult{}, false
	}
	next, _ := traits.Cycle(hintsCycleOrder, hs.session.ActiveAction(), dir, true)
	hs.session.SetAction(next)
	a.viewMemo.gen++
	a.say("%s", hintsStatusLine(hs.session))
	return core.Next(s), true
}

func (a *App) hintsActionCycle(s core.State, c core.Command) core.StepResult {
	if res, ok := a.cycleHintsActionDir(s, parseCycleDir(c.Args)); ok {
		return res
	}
	return core.Next(s)
}

func (a *App) hintsActionSet(s core.State, c core.Command) core.StepResult {
	hs := a.panes.hints
	if hs == nil || hs.focusJump || hs.session == nil || !hs.session.Active() {
		a.say("no active terminal hints session")
		return core.Next(s)
	}
	raw := strings.TrimSpace(c.Arg("action"))
	act, ok := hintActionFromName(raw)
	if !ok {
		a.say("unknown hint action %q (want copy, insert, open, reveal, or pick)", raw)
		return core.Next(s)
	}
	hs.session.SetAction(act)
	a.viewMemo.gen++
	a.say("%s", hintsStatusLine(hs.session))
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

	var res hints.Result
	if key != "" {
		res = hs.session.HandleKey(key)
	} else if r, sz := utf8.DecodeRuneInString(text); text != "" && sz == len(text) && r != utf8.RuneError {
		res = hs.session.HandleRune(r, false)
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
		} else if hs.session.Prefix() == "" {
			a.say("%s", hintsStatusLine(hs.session))
		} else {
			a.say("hints [%s]: %s_ (%d remaining)", hintActionName(hs.session.ActiveAction()), hs.session.Prefix(), len(hs.session.Remaining()))
		}
		return core.Next(s)
	case hints.StatusNoMatch:
		a.say("no hint matches %q · esc/q cancels", key)
		return core.Next(s)
	case hints.StatusMatched:
		target := res.Target
		paneID := hs.paneID
		if hs.focusJump {
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
		return a.executeHintAction(s, hs, pane, *target, res.Action)
	}
	return core.Next(s)
}

func (a *App) executeHintAction(s core.State, hs *hintsState, pane *data.Record, target hints.Target, action hints.Action) core.StepResult {
	switch action {
	case hintsActionPick:
		return a.openHintActionPicker(s, hs, pane, target)
	case hints.ActionMeta:
		return a.revealContextValue(s, target.Kind, target.Value, pane)
	case hints.ActionAlt:
		return a.systemOpenTarget(s, contextTargetPath(target.Kind, target.Value, pane))
	case hints.ActionSecondary:
		return core.Next(a.insertHintTarget(s, hs, pane, target.Kind, target.Value))
	default:
		a.yank(target.Value, target.Kind)
		a.pushToast("Copied "+clip(target.Value, 40), toast.Ok)
		return core.Next(s)
	}
}

func (a *App) openHintActionPicker(s core.State, hs *hintsState, pane *data.Record, target hints.Target) core.StepResult {
	resolvedPath := contextTargetPath(target.Kind, target.Value, pane)
	items := []picker.Item{
		{
			ID:       HintsActionCopy,
			Title:    "Copy",
			Subtitle: clip(target.Value, 64),
			Badges:   []ui.Badge{{Text: "a-z", Tone: "muted"}},
			Keywords: []string{"copy", "yank", "clipboard"},
			Data:     hints.ActionDefault,
		},
		{
			ID:       HintsActionInsert,
			Title:    "Paste in input",
			Subtitle: "Insert into active input or terminal",
			Badges:   []ui.Badge{{Text: "Shift", Tone: "muted"}},
			Keywords: []string{"paste", "insert", "input", "type"},
			Data:     hints.ActionSecondary,
		},
		{
			ID:       HintsActionOpen,
			Title:    "Open",
			Subtitle: clip(resolvedPath, 64),
			Badges:   []ui.Badge{{Text: "ctrl", Tone: "muted"}},
			Keywords: []string{"open", "launch", "browser"},
			Data:     hints.ActionAlt,
		},
		{
			ID:       HintsActionReveal,
			Title:    "Reveal in Finder",
			Subtitle: "open -R " + clip(resolvedPath, 56),
			Badges:   []ui.Badge{{Text: "alt", Tone: "muted"}},
			Keywords: []string{"reveal", "finder", "folder", "dir"},
			Data:     hints.ActionMeta,
		},
	}
	pm := picker.New("Hint Action · "+clip(target.Value, 40), items)
	pm.Placeholder = "choose action · enter runs · esc cancels"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "hints.action.picker",
		Scope:    "hints.action",
		PageSize: 4,
		OnConfirm: func(picked picker.Item) modal.Result {
			act, _ := picked.Data.(hints.Action)
			a.afterModal(func(st core.State) core.State {
				return a.executeHintAction(st, hs, pane, target, act).State
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("pick hint action for %s · enter runs · esc cancels", clip(target.Value, 44))
	return core.Next(a.pushModal(s, m))
}

type hintInsertable interface {
	InsertHintText(text string) (label string, ok bool)
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

func (a *App) insertIntoTopModal(s core.State, top modal.Modal, value string) (core.State, string, bool) {
	if top == nil {
		return s, "", false
	}
	if custom, ok := top.(hintInsertable); ok {
		if label, inserted := custom.InsertHintText(value); inserted {
			return s, label, true
		}
		return s, "", false
	}
	if fm := formModelOfModal(top); fm != nil {
		if fm.Editable() {
			fm.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": value}})
			return s, "form", true
		}
		return s, "", false
	}
	insertVia := func(cmdID, label string) (core.State, string, bool) {
		res := top.Handle(core.Command{ID: cmdID, Args: map[string]any{"text": value}})
		return a.applyModalResult(s, res).State, label, true
	}
	switch m := top.(type) {
	case *richComposerModal:
		return insertVia(CmdComposerInsert, "composer")
	case *modal.CommandLineModal:
		return insertVia(commandline.CmdInsert, "command line")
	case *modal.PickerModal:
		if m.Model() != nil && m.Model().Filtering {
			return insertVia(modal.CmdPickerInsert, "filter")
		}
	case *modal.PaletteModal:
		if pal := m.Model(); pal != nil {
			if pal.FocusForm && pal.ActiveForm != nil && pal.ActiveForm.Editable() {
				pal.ActiveForm.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": value}})
				return s, "form", true
			}
			if !pal.FocusForm && pal.Picker != nil && pal.Picker.Filtering {
				return insertVia(modal.CmdPickerInsert, "filter")
			}
		}
	case *modal.PathPickerModal:
		if m.Model() != nil && m.Model().Filtering() {
			return insertVia(modal.CmdPickerInsert, "filter")
		}
	}
	return s, "", false
}

// insertHintTarget inserts a matched hint value into the currently active
// input field (modal input, inline filter/outline edit, or active terminal pane),
// falling back to copying to the clipboard when no input target is active.
func (a *App) insertHintTarget(s core.State, hs *hintsState, pane *data.Record, kind, value string) core.State {
	if value == "" {
		return s
	}
	notifyInsert := func(label string) {
		a.viewMemo.gen++
		a.say("inserted %s into %s", clip(value, 44), label)
		a.pushToast("Inserted into "+label+": "+clip(value, 36), toast.Ok)
	}

	// 1. Active modal input.
	if a.modals != nil {
		if nextState, label, ok := a.insertIntoTopModal(s, a.modals.Top(), value); ok {
			notifyInsert(label)
			return nextState
		}
	}

	// 2. Inline view inputs (filter bar or inline outline edit).
	if hs != nil {
		switch hs.returnMode {
		case ModeFilter:
			s = a.filterEdit(s, core.Command{ID: CmdFilterInsert, Args: map[string]any{"text": value}}).State
			notifyInsert("filter")
			return s
		case ModeOutlineEdit:
			s = a.outlineEditInsert(s, core.Command{ID: CmdOutlineEditInsert, Args: map[string]any{"text": value}}).State
			notifyInsert("outline")
			return s
		case ModeFeaturesNote:
			a.featuresNoteInsertText(value)
			notifyInsert("features note")
			return s
		case ModeThreadsCompose:
			a.threadsComposeInsertText(value)
			notifyInsert("thread reply")
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
		frame = buildFocusHintFlatFrame(tree, solved, len(s.Modals) == 0)
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
