package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/hints"
	"ofam.dev/ochazuke/widgets/modal"
)

// CmdContentHints activates Content/Action Hints (distinct from Focus Hints `g f`):
// scans all visible rendered text on the current screen (Show pages, Markdown
// pages, tables/trees, and live terminals) for file paths, URLs, PR refs,
// Matomeru record IDs, and inline code spans, with multi-action activation.
const CmdContentHints = "hints.content"

func contentHintsSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdContentHints,
			Description: "Show content action hints over visible paths, URLs, record IDs, and code spans on screen (copy, paste, open, reveal, or pick)",
			Params: []core.ParamSpec{{
				Name:        "action",
				Type:        core.ParamString,
				Enum:        []string{HintsActionCopy, HintsActionInsert, HintsActionOpen, HintsActionReveal, HintsActionPick},
				Default:     HintsActionCopy,
				Description: "Action performed when activating a lowercase hint label (copy, insert, open, reveal, or pick)",
			}},
		},
	}
}

func contentHintsBindings() []core.Binding {
	return []core.Binding{
		bind(ModeGlobal, "", ",", CmdContentHints, "content hints"),
		with(bind(ModeHints, "", "space", CmdHintsActionSet, "pick action menu"), map[string]any{"action": HintsActionPick}),
	}
}

func (a *App) registerContentHintsHandlers() {
	a.handle(CmdContentHints, a.contentHints)
}

func contentHintsStatusLine(sess *hints.Session) string {
	if sess == nil {
		return "content hints"
	}
	act := sess.ActiveAction()
	count := len(sess.Remaining())
	if sess.ModifiersEnabled() {
		if act == hints.ActionDefault {
			return fmt.Sprintf("content hints (%d) · a-z copies · ctrl opens · Shift pastes · alt reveals · space picks · tab cycles · esc cancels", count)
		}
		return fmt.Sprintf("content hints (%d) [%s] · a-z %s · ctrl opens · Shift pastes · alt reveals · space picks · tab cycles · esc cancels",
			count, hintActionName(act), hintActionVerbLabel(act))
	}
	if act == hints.ActionDefault {
		return fmt.Sprintf("content hints (%d) · a-z copies · space picks · tab cycles · esc cancels", count)
	}
	return fmt.Sprintf("content hints (%d) [%s] · a-z %s · space picks · tab cycles · esc cancels",
		count, hintActionName(act), hintActionVerbLabel(act))
}

func (a *App) visibleTerminalForContentHints(s core.State) (*data.Record, bool) {
	if len(s.Modals) > 0 && a.activeScratch() == nil {
		return nil, false
	}
	pane, hasPane := a.termPane(s)
	if !hasPane || pane == nil {
		return nil, false
	}
	if a.activeScratch() != nil || s.ActiveFrame().Mode == ModeTerminal {
		return pane, true
	}
	root := a.View(s)
	if root.Find(siteScreen) == nil {
		return nil, false
	}
	activePg := a.panes.activePage[pane.ID]
	if activePg != "" && activePg != views.ScreenPageID {
		return nil, false
	}
	return pane, true
}

func (a *App) resolveHintDefaultAction(c core.Command) hints.Action {
	actionName := strings.TrimSpace(c.Arg("action"))
	if actionName == "" {
		actionName = a.hintsDefaultAction()
	}
	act, ok := hintActionFromName(actionName)
	if !ok {
		act, _ = hintActionFromName(a.hintsDefaultAction())
	}
	return act
}

func buildHintTargets(matches []contextitems.Match) []hints.Target {
	targets := make([]hints.Target, len(matches))
	for i, m := range matches {
		targets[i] = hints.Target{
			ID:    fmt.Sprintf("h-%d", i),
			Rect:  layout.Rect{X: m.StartCol, Y: m.Row, W: max(1, m.EndCol-m.StartCol), H: 1},
			Kind:  m.Kind,
			Value: m.Value,
		}
	}
	return targets
}

// contentHints activates full-screen Content/Action Hints over whatever is
// currently visible: on a visible live terminal it scans the terminal screen
// (and falls back to the full rendered screen if the terminal has no matches),
// and on Show pages, Markdown pages, tables, trees, and modals it scans the
// entire rendered screen for paths, URLs, PR refs, record IDs, and code spans.
func (a *App) contentHints(s core.State, c core.Command) core.StepResult {
	pane, usePane := a.visibleTerminalForContentHints(s)
	inScratch := a.activeScratch() != nil

	var lines []string
	var matches []contextitems.Match
	paneID := ""
	if usePane {
		paneID = pane.ID
		lines = a.paneVisibleLines(*pane, s.ActiveFrame().Mode == ModeTerminal)
		matches = contextitems.ExtractContentMatches(lines)
		a.recordVisibleMatches(*pane, matches)
	}
	if len(matches) == 0 {
		usePane = false
		lines = a.screenVisibleLines(s)
		matches = contextitems.ExtractContentMatches(lines)
		matches = augmentViewCodeMatches(a.View(s), lines, matches)
	}
	if len(matches) == 0 {
		a.say("no actionable content (paths, URLs, IDs, code) visible on screen")
		return core.Next(s)
	}

	mods := a.hintsModifierActionsEnabled()
	sess := hints.NewSession(
		buildHintTargets(matches),
		hints.WithDefaultAction(a.resolveHintDefaultAction(c)),
		hints.WithModifiers(mods),
		hints.WithActionLegend(hintsActionLegend(mods)),
	)

	curFrame := s.ActiveFrame()
	a.panes.hints = &hintsState{
		session:       sess,
		paneID:        paneID,
		contentMode:   true,
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
		targetFocus := siteScreen
		if curFrame.FocusID == sideScreen {
			targetFocus = sideScreen
		}
		s = withPage(s, s.ActivePage, targetFocus, ModeHints)
	}
	a.viewMemo.gen++
	a.say("%s", contentHintsStatusLine(sess))
	return core.Next(s)
}

func (a *App) buildScreenHintsOverlay(w, h int) ui.ViewNode {
	if w <= 0 {
		w = 120
	}
	if h <= 0 {
		h = 36
	}
	bodyH := max(h-1, 1)
	lines := a.hintsRenderLines(a.panes.hints.lines, w, bodyH)
	bar := a.panes.hints.session.View("screen.hints.bar")
	bar.Box = layout.Constraints{FixedH: 1}
	ov := modal.BuildOverlay(modal.Frame{
		ID:        "screen.hints",
		WidthPct:  100,
		HeightPct: 100,
	}, ui.ViewNode{
		ID:   "screen.hints.body",
		Kind: ui.KindVStack,
		Children: []ui.ViewNode{
			{
				ID:    "screen.hints.raw",
				Kind:  ui.KindRaw,
				Box:   layout.Constraints{Flex: 1},
				Props: map[string]any{"lines": lines},
			},
			bar,
		},
	})
	ov.ID = "screen.hints.overlay"
	return ov
}

// tryOpenHintRecordRef opens a Matomeru record Show page directly when the
// picked content hint is a stored record reference (e.g. "task:abc123" or
// "mtm://note/xyz") instead of shelling out to the OS opener.
func (a *App) tryOpenHintRecordRef(s core.State, kind, value string) (core.State, bool) {
	if kind != contextitems.KindRef || a.Store == nil {
		return s, false
	}
	ref, ok := parseHintRecordRef(value)
	if !ok {
		return s, false
	}
	if _, err := a.Store.Get(ref); err != nil {
		return s, false
	}
	return a.show(s, ref), true
}

func parseHintRecordRef(raw string) (core.Ref, bool) {
	s := strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(s, "mtm://"); ok {
		parts := strings.Split(strings.Trim(rest, "/"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return core.Ref{Kind: normalizeHintRefKind(parts[0]), ID: parts[1]}, true
		}
		return core.Ref{}, false
	}
	k, id, ok := strings.Cut(s, ":")
	if !ok || k == "" || id == "" {
		return core.Ref{}, false
	}
	return core.Ref{Kind: normalizeHintRefKind(k), ID: id}, true
}

func normalizeHintRefKind(k string) string {
	low := strings.ToLower(strings.TrimSpace(k))
	if low == "doc" {
		return data.KindDocument
	}
	return low
}

// augmentViewCodeMatches locates visible inline code spans and fenced code
// blocks extracted from raw KindMarkdown / md.code ViewNodes on the rendered
// screen lines (where the Markdown renderer stripped literal backticks).
func augmentViewCodeMatches(root ui.ViewNode, plainLines []string, existing []contextitems.Match) []contextitems.Match {
	snippets := collectViewCodeSnippets(root)
	if len(snippets) == 0 || len(plainLines) == 0 {
		return existing
	}
	out := slices.Clone(existing)
	for _, code := range snippets {
		firstLine, _, _ := strings.Cut(code, "\n")
		needle := strings.TrimSpace(firstLine)
		if len([]rune(needle)) < 2 {
			continue
		}
		if m, ok := locateSnippetOnLines(plainLines, needle, code); ok {
			out = mergeContentCodeMatch(out, m)
		}
	}
	slices.SortFunc(out, func(a, b contextitems.Match) int {
		if c := cmp.Compare(a.Row, b.Row); c != 0 {
			return c
		}
		return cmp.Compare(a.StartCol, b.StartCol)
	})
	return out
}

func extractNodeCodeSnippets(n ui.ViewNode, add func(string)) {
	if n.Kind == ui.KindMarkdown {
		if md, ok := n.Props["markdown"].(string); ok && md != "" {
			for _, it := range contextitems.Extract(md, contextitems.Options{}) {
				if it.Kind == contextitems.KindCode {
					add(it.Value)
				}
			}
		}
		return
	}
	if n.Kind == ui.KindText && n.Tone == "md.code" {
		if txt, ok := n.Props["text"].(string); ok {
			add(txt)
		}
	}
}

func collectViewCodeSnippets(root ui.ViewNode) []string {
	var out []string
	seen := map[string]bool{}
	add := func(val string) {
		v := strings.TrimSpace(val)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	var walk func(n ui.ViewNode)
	walk = func(n ui.ViewNode) {
		extractNodeCodeSnippets(n, add)
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(searchContentRoot(root))
	return out
}

func locateSnippetOnLines(lines []string, needle, fullValue string) (contextitems.Match, bool) {
	for r, ln := range lines {
		idx := strings.Index(ln, needle)
		if idx < 0 {
			continue
		}
		startCol := utf8.RuneCountInString(ln[:idx])
		endCol := startCol + utf8.RuneCountInString(needle)
		return contextitems.Match{
			Row:      r,
			StartCol: startCol,
			EndCol:   endCol,
			Kind:     contextitems.KindCode,
			Value:    fullValue,
		}, true
	}
	return contextitems.Match{}, false
}

func mergeContentCodeMatch(matches []contextitems.Match, cand contextitems.Match) []contextitems.Match {
	kept := matches[:0]
	for _, m := range matches {
		if m.Row != cand.Row {
			kept = append(kept, m)
			continue
		}
		// Drop partial sub-matches strictly inside the code span (e.g. "./" inside "go test ./...").
		if m.StartCol >= cand.StartCol && m.EndCol <= cand.EndCol {
			continue
		}
		if m.StartCol < cand.EndCol && m.EndCol > cand.StartCol {
			return matches
		}
		kept = append(kept, m)
	}
	return append(kept, cand)
}
