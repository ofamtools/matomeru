package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

// ModeSearch is the non-destructive in-page search typing mode (distinct from
// ModeFilter which narrows/hides rows in filterable collections).
const ModeSearch = "search"

// Commands for universal non-destructive in-page search.
const (
	CmdSearchStart     = "search.start"
	CmdSearchInsert    = "search.insert"
	CmdSearchBackspace = "search.backspace"
	CmdSearchAccept    = "search.accept"
	CmdSearchCancel    = "search.cancel"
	CmdSearchNext      = "search.next"
	CmdSearchPrev      = "search.prev"
	CmdSearchClear     = "search.clear"
	CmdSearchModeCycle = "search.mode.cycle"
)

type pageSearchMatch struct {
	NodeID   string
	FocusID  string
	ScrollID string
	RowIdx   int
	RowID    string
	LineIdx  int
	Kind     string // "text", "markdown", "raw", "table", "tree", "heading"
	Snippet  string
}

type pageSearchState struct {
	query      string
	prevQuery  string
	mode       traits.MatchMode
	errMsg     string
	matches    []pageSearchMatch
	index      int
	returnMode string
	line       input.Line
}

func screenSearchSpecs() []core.CommandSpec {
	textParam := []core.ParamSpec{{Name: "text", Type: core.ParamString}}
	return []core.CommandSpec{
		spec(CmdSearchStart, "Search visible lines and blocks on the current page without hiding non-matching items"),
		{ID: CmdSearchInsert, Description: "Type into the in-page search bar", Params: textParam, Meta: map[string]any{"internal": true}},
		{ID: CmdSearchBackspace, Description: "Erase before cursor in the in-page search bar", Meta: map[string]any{"internal": true}},
		{ID: CmdSearchAccept, Description: "Keep active in-page search highlights and return to normal navigation", Meta: map[string]any{"internal": true}},
		{ID: CmdSearchCancel, Description: "Cancel in-page search edit and restore previous search", Meta: map[string]any{"internal": true}},
		spec(CmdSearchNext, "Jump to the next in-page search match"),
		spec(CmdSearchPrev, "Jump to the previous in-page search match"),
		spec(CmdSearchClear, "Clear the active in-page search query and highlights"),
		spec(CmdSearchModeCycle, "Cycle how in-page search matches: substring, fuzzy, regex"),
	}
}

func screenSearchBindings() []core.Binding {
	all := ""
	return []core.Binding{
		bind(ModeGlobal, all, "\\", CmdSearchStart, "search page"),
		when(bind(ModeNormal, all, "n", CmdSearchNext, "next search match"), "facts.hasSearch && !facts.hasFilter"),
		when(bind(ModeNormal, all, "N", CmdSearchPrev, "prev search match"), "facts.hasSearch && !facts.hasFilter"),
		when(bind(ModeNormal, all, "esc", CmdSearchClear, "clear search"), "facts.hasSearch && !facts.visualActive && !(facts.markedCount > 0)"),
		when(bind(ModePage, all, "n", CmdSearchNext, "next search match"), "facts.hasSearch"),
		when(bind(ModePage, all, "N", CmdSearchPrev, "prev search match"), "facts.hasSearch"),
		when(bind(ModeReader, all, "n", CmdSearchNext, "next search match"), "facts.hasSearch"),
		when(bind(ModeReader, all, "N", CmdSearchPrev, "prev search match"), "facts.hasSearch"),

		{Mode: ModeSearch, Class: core.ClassPrintable, Command: CmdSearchInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeSearch, Class: core.ClassPaste, Command: CmdSearchInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeSearch, all, "backspace", CmdSearchBackspace, "erase"),
		hidden(bind(ModeSearch, all, "shift+backspace", CmdSearchBackspace, "erase")),
		bind(ModeSearch, all, "enter", CmdSearchAccept, "keep search"),
		bind(ModeSearch, all, "esc", CmdSearchCancel, "cancel"),
		bind(ModeSearch, all, "ctrl+x", CmdSearchClear, "clear"),
		bind(ModeSearch, all, "tab", CmdSearchModeCycle, "match mode"),
		bind(ModeSearch, all, "down", CmdSearchNext, "next match"),
		bind(ModeSearch, all, "up", CmdSearchPrev, "prev match"),
		hidden(bind(ModeSearch, all, "ctrl+n", CmdSearchNext, "next match")),
		hidden(bind(ModeSearch, all, "ctrl+p", CmdSearchPrev, "prev match")),
	}
}

func (a *App) registerScreenSearchHandlers() {
	a.handle(CmdSearchStart, a.searchStart)
	a.handle(CmdSearchInsert, a.searchEdit)
	a.handle(CmdSearchBackspace, a.searchEdit)
	a.handle(CmdSearchAccept, a.searchAccept)
	a.handle(CmdSearchCancel, a.searchCancel)
	a.handle(CmdSearchNext, a.searchStep(1))
	a.handle(CmdSearchPrev, a.searchStep(-1))
	a.handle(CmdSearchClear, a.searchClear)
	a.handle(CmdSearchModeCycle, a.searchModeCycle)
}

func (a *App) searchHighlightEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetSearchHighlight, true)
}

func (a *App) pageSearch() *pageSearchState {
	if a == nil || a.nav == nil {
		return nil
	}
	if a.nav.pageSearch == nil {
		a.nav.pageSearch = &pageSearchState{mode: a.matchMode()}
	}
	return a.nav.pageSearch
}

func (a *App) hasPageSearch() bool {
	return a != nil && a.nav != nil && a.nav.pageSearch != nil && strings.TrimSpace(a.nav.pageSearch.query) != ""
}

func (a *App) pageSearchViewKey(s core.State) string {
	if a == nil || a.nav == nil || a.nav.pageSearch == nil {
		return ""
	}
	ps := a.nav.pageSearch
	typing := a.Engine.Mode(s) == ModeSearch
	if !typing && strings.TrimSpace(ps.query) == "" {
		return ""
	}
	return fmt.Sprintf("%q:%s:%d:%d:%d:%t:%t",
		ps.query, ps.mode, ps.index, len(ps.matches), ps.line.Cursor, typing, a.searchHighlightEnabled())
}

func (a *App) pageSearchStatusBadge() string {
	if !a.hasPageSearch() {
		return ""
	}
	ps := a.nav.pageSearch
	return fmt.Sprintf("\\%s (%s)", ps.query, ps.countLabel())
}

func (ps *pageSearchState) effectiveMode(def func() table.MatchMode) traits.MatchMode {
	if ps.mode != "" {
		return ps.mode
	}
	if def != nil {
		return def()
	}
	return traits.MatchSubstring
}

func (ps *pageSearchState) countLabel() string {
	if len(ps.matches) == 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d", ps.index+1, len(ps.matches))
}

func (a *App) searchStart(s core.State, _ core.Command) core.StepResult {
	ps := a.pageSearch()
	curMode := a.Engine.Mode(s)
	if curMode == "" || curMode == ModeSearch {
		curMode = ModeNormal
	}
	ps.returnMode = curMode
	ps.prevQuery = ps.query
	ps.line.Set(ps.query)
	a.recomputePageSearch(s)
	a.viewMemo.gen++
	a.say("search page (does not hide items) · enter keeps · esc cancels · ↑/↓ matches · tab mode")
	return core.Next(withMode(s, ModeSearch))
}

func (a *App) searchEdit(s core.State, c core.Command) core.StepResult {
	ps := a.pageSearch()
	ps.line.Sync(ps.query)
	switch {
	case c.ID == CmdSearchInsert:
		ps.line.Insert(c.Arg("text"))
	case c.ID == CmdSearchBackspace:
		ps.line.HandleCommand(core.Command{ID: input.CmdBackspace})
	case input.IsReadlineCommand(c.ID):
		ps.line.HandleCommand(c)
	}
	ps.query = ps.line.Value()
	ps.index = 0
	s = a.recomputeAndJumpPageSearch(s)
	a.viewMemo.gen++
	return core.Next(s)
}

func (a *App) screenSearchReadline(s core.State, c core.Command) core.StepResult {
	return a.searchEdit(s, c)
}

func (a *App) searchAccept(s core.State, _ core.Command) core.StepResult {
	ps := a.pageSearch()
	retMode := ps.returnMode
	if retMode == "" {
		retMode = ModeNormal
	}
	if q := strings.TrimSpace(ps.query); q != "" {
		a.nav.Registers.SetSpecial(core.RegSearch, q)
		a.recomputePageSearch(s)
		a.say("search %q · %s (n/N next/prev · esc clears)", q, ps.countLabel())
	} else {
		ps.matches = nil
		ps.index = 0
		a.say("")
	}
	a.viewMemo.gen++
	return core.Next(withMode(s, retMode))
}

func (a *App) searchCancel(s core.State, _ core.Command) core.StepResult {
	ps := a.pageSearch()
	retMode := ps.returnMode
	if retMode == "" {
		retMode = ModeNormal
	}
	ps.query = ps.prevQuery
	ps.line.Set(ps.query)
	a.recomputePageSearch(s)
	a.say("")
	a.viewMemo.gen++
	return core.Next(withMode(s, retMode))
}

func (a *App) searchClear(s core.State, _ core.Command) core.StepResult {
	ps := a.pageSearch()
	ps.query = ""
	ps.prevQuery = ""
	ps.errMsg = ""
	ps.matches = nil
	ps.index = 0
	ps.line.Set("")
	a.viewMemo.gen++
	if a.Engine.Mode(s) != ModeSearch {
		a.say("search cleared")
	}
	return core.Next(s)
}

func (a *App) searchModeCycle(s core.State, _ core.Command) core.StepResult {
	ps := a.pageSearch()
	ps.mode = traits.CycleMatchMode(ps.effectiveMode(a.matchMode), traits.FilterConfig{})
	ps.index = 0
	s = a.recomputeAndJumpPageSearch(s)
	a.say("search matches by %s · %s", ps.mode, ps.countLabel())
	a.viewMemo.gen++
	return core.Next(s)
}

func (a *App) searchStep(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		ps := a.pageSearch()
		if strings.TrimSpace(ps.query) == "" {
			a.say("no active search (press \\ to search)")
			return core.Next(s)
		}
		a.recomputePageSearch(s)
		if len(ps.matches) == 0 {
			a.say("search %q · 0/0 (no matches)", ps.query)
			return core.Next(s)
		}
		ps.index = cycleWrapIndex(ps.index, len(ps.matches), delta)
		s = a.jumpToPageSearchMatch(s, ps.matches[ps.index])
		a.say("search %q · %s", ps.query, ps.countLabel())
		a.viewMemo.gen++
		return core.Next(s)
	}
}

func (a *App) recomputePageSearch(s core.State) {
	ps := a.pageSearch()
	q := strings.TrimSpace(ps.query)
	if q == "" {
		ps.errMsg = ""
		ps.matches = nil
		ps.index = 0
		return
	}
	mm, errMsg := traits.CompileMultiTerm(q, ps.effectiveMode(a.matchMode), traits.FilterConfig{})
	ps.errMsg = errMsg
	if errMsg != "" {
		ps.matches = nil
		ps.index = 0
		return
	}
	root := a.View(s)
	ps.matches = collectPageSearchMatches(root, mm)
	if len(ps.matches) == 0 {
		ps.index = 0
	} else if ps.index >= len(ps.matches) {
		ps.index = 0
	}
}

func (a *App) recomputeAndJumpPageSearch(s core.State) core.State {
	a.recomputePageSearch(s)
	ps := a.pageSearch()
	if len(ps.matches) == 0 {
		if ps.errMsg != "" {
			a.say("%s", formatFilterErrorLabel(ps.errMsg))
		} else if strings.TrimSpace(ps.query) != "" {
			a.say("search %q · 0/0", ps.query)
		}
		return s
	}
	s = a.jumpToPageSearchMatch(s, ps.matches[ps.index])
	a.say("search %q · %s", ps.query, ps.countLabel())
	return s
}

func (a *App) selectTreeSearchMatch(s core.State, m pageSearchMatch) {
	tm := a.treeModelForSearch(s, m.NodeID)
	if tm == nil {
		return
	}
	if m.RowID != "" {
		tm.SelectedID = m.RowID
		return
	}
	if vis := tm.Flatten(); m.RowIdx >= 0 && m.RowIdx < len(vis) {
		tm.SelectedID = vis[m.RowIdx].ID
	}
}

func (a *App) jumpToPageSearchMatch(s core.State, m pageSearchMatch) core.State {
	if m.Kind == "table" {
		if tbl := a.nav.tables[m.NodeID]; tbl != nil && m.RowIdx >= 0 && m.RowIdx < len(tbl.Items()) {
			tbl.SelectedIdx = m.RowIdx
			tbl.Focused()
		}
	}
	if m.Kind == "tree" {
		a.selectTreeSearchMatch(s, m)
	}
	if m.FocusID != "" && len(s.Modals) == 0 {
		s = withFocus(s, m.FocusID)
	}
	if m.LineIdx > 0 {
		a.scrollToSearchMatch(s, m)
	}
	return s
}

func (a *App) treeModelForSearch(s core.State, nodeID string) *tree.Model {
	if tm, ok := a.projectSectionTree(nodeID); ok {
		return tm
	}
	if tm, _, _, _, ok := a.activeTree(withFocus(s, nodeID)); ok {
		return tm
	}
	return nil
}

func (a *App) scrollToSearchMatch(s core.State, m pageSearchMatch) {
	if slot := a.showScrollSlot(m.ScrollID); slot != nil {
		off := max(m.LineIdx-2, 0)
		*slot = &off
		return
	}
	if m.ScrollID == views.PageScrollID {
		if pageID := a.activeSitePageIDFor(s); pageID != "" {
			a.setPageScrollOffset(pageID, max(m.LineIdx-2, 0))
		}
	}
}

// --- ViewNode tree match scanning -----------------------------------------------

func searchContentRoot(root ui.ViewNode) ui.ViewNode {
	// If a modal overlay is open (not a toast/hints overlay), search inside the top modal.
	for i := len(root.Children) - 1; i >= 0; i-- {
		ch := root.Children[i]
		if ch.Kind == ui.KindOverlay && !strings.Contains(ch.ID, "toast") && !strings.Contains(ch.ID, "hints") {
			return ch
		}
	}
	// Otherwise search inside the active tab's content (excluding chrome.sep).
	for _, ch := range root.Children {
		if ch.Kind == ui.KindTabs {
			for _, tab := range ch.Children {
				if len(tab.Children) > 0 {
					return tab
				}
			}
		}
	}
	return root
}

func collectPageSearchMatches(root ui.ViewNode, mm traits.MultiMatcher) []pageSearchMatch {
	target := searchContentRoot(root)
	var out []pageSearchMatch
	walkSearchNode(target, mm, "", "", &out)
	return out
}

func collectNodeOwnSearchMatches(n ui.ViewNode, mm traits.MultiMatcher, focusID, scrollID string, out *[]pageSearchMatch) {
	switch n.Kind {
	case ui.KindText:
		collectTextNodeMatches(n, mm, focusID, scrollID, out)
	case ui.KindMarkdown:
		collectMarkdownNodeMatches(n, mm, focusID, scrollID, out)
	case ui.KindRaw:
		collectRawNodeMatches(n, mm, focusID, scrollID, out)
	case ui.KindTable:
		collectTableNodeMatches(n, mm, out)
	case ui.KindTree:
		collectTreeNodeMatches(n, mm, out)
	}
}

func walkSearchNode(n ui.ViewNode, mm traits.MultiMatcher, focusID, scrollID string, out *[]pageSearchMatch) {
	if n.ID == "chrome.sep" || (n.Kind == ui.KindOverlay && strings.Contains(n.ID, "toast")) {
		return
	}
	if n.Focusable && n.ID != "" {
		focusID = n.ID
	}
	if n.Kind == ui.KindScroll && n.ID != "" {
		scrollID = n.ID
	}
	before := len(*out)
	collectNodeOwnSearchMatches(n, mm, focusID, scrollID, out)
	for _, ch := range n.Children {
		walkSearchNode(ch, mm, focusID, scrollID, out)
	}
	if len(*out) == before && strings.TrimSpace(n.Title) != "" && mm.Match(n.Title) {
		*out = append(*out, pageSearchMatch{
			NodeID:   n.ID,
			FocusID:  focusID,
			ScrollID: scrollID,
			Kind:     "heading",
			Snippet:  n.Title,
		})
	}
}

func collectTextNodeMatches(n ui.ViewNode, mm traits.MultiMatcher, focusID, scrollID string, out *[]pageSearchMatch) {
	raw, _ := n.Props["text"].(string)
	if strings.TrimSpace(raw) == "" {
		return
	}
	for i, ln := range strings.Split(raw, "\n") {
		if strings.TrimSpace(ln) != "" && mm.Match(ln) {
			*out = append(*out, pageSearchMatch{
				NodeID:   n.ID,
				FocusID:  focusID,
				ScrollID: scrollID,
				LineIdx:  i,
				Kind:     "text",
				Snippet:  strings.TrimSpace(ln),
			})
		}
	}
}

func collectMarkdownNodeMatches(n ui.ViewNode, mm traits.MultiMatcher, focusID, scrollID string, out *[]pageSearchMatch) {
	md, _ := n.Props["markdown"].(string)
	if strings.TrimSpace(md) == "" {
		return
	}
	for i, ln := range strings.Split(md, "\n") {
		if strings.TrimSpace(ln) != "" && mm.Match(ln) {
			*out = append(*out, pageSearchMatch{
				NodeID:   n.ID,
				FocusID:  focusID,
				ScrollID: scrollID,
				LineIdx:  i,
				Kind:     "markdown",
				Snippet:  strings.TrimSpace(ln),
			})
		}
	}
}

func collectRawNodeMatches(n ui.ViewNode, mm traits.MultiMatcher, focusID, scrollID string, out *[]pageSearchMatch) {
	lines, _ := n.Props["lines"].([]string)
	for i, raw := range lines {
		plain := hintsStripSGR.ReplaceAllString(raw, "")
		if strings.TrimSpace(plain) != "" && mm.Match(plain) {
			*out = append(*out, pageSearchMatch{
				NodeID:   n.ID,
				FocusID:  focusID,
				ScrollID: scrollID,
				LineIdx:  i,
				Kind:     "raw",
				Snippet:  strings.TrimSpace(plain),
			})
		}
	}
}

func collectTableNodeMatches(n ui.ViewNode, mm traits.MultiMatcher, out *[]pageSearchMatch) {
	rows, _ := n.Props["rows"].([]any)
	for i, r := range rows {
		joined := tableRowSearchText(r)
		if joined != "" && mm.Match(joined) {
			*out = append(*out, pageSearchMatch{
				NodeID:  n.ID,
				FocusID: n.ID,
				RowIdx:  i,
				Kind:    "table",
				Snippet: joined,
			})
		}
	}
}

func tableCellSearchText(c any) string {
	if runs, ok := ui.DecodeInlineRuns(c); ok {
		var b strings.Builder
		for _, r := range runs {
			b.WriteString(r.Text)
		}
		return strings.TrimSpace(b.String())
	}
	return strings.TrimSpace(fmt.Sprint(c))
}

func tableCellsSearchText(cells []any) string {
	parts := make([]string, 0, len(cells))
	for _, c := range cells {
		if s := tableCellSearchText(c); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

func tableRowSearchText(row any) string {
	switch cells := row.(type) {
	case ui.TableRowSpec:
		return tableCellsSearchText(cells.Cells)
	case *ui.TableRowSpec:
		if cells != nil {
			return tableCellsSearchText(cells.Cells)
		}
	case []any:
		return tableCellsSearchText(cells)
	case map[string]any:
		if raw, ok := cells["cells"].([]any); ok {
			return tableCellsSearchText(raw)
		}
		if s, ok := cells["title"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func collectTreeNodeMatches(n ui.ViewNode, mm traits.MultiMatcher, out *[]pageSearchMatch) {
	if uiNodes, ok := n.Props["nodes"].([]ui.TreeNode); ok {
		rowIdx := 0
		walkTypedTreeSearchNodes(uiNodes, n.ID, mm, &rowIdx, out)
		return
	}
	nodes, _ := n.Props["nodes"].([]any)
	walkUITreeSearchNodes(nodes, n.ID, mm, out)
}

func walkTypedTreeSearchNodes(nodes []ui.TreeNode, treeID string, mm traits.MultiMatcher, rowIdx *int, out *[]pageSearchMatch) {
	for _, nd := range nodes {
		idx := *rowIdx
		*rowIdx++
		if strings.TrimSpace(nd.Label) != "" && mm.Match(nd.Label) {
			*out = append(*out, pageSearchMatch{
				NodeID:  treeID,
				FocusID: treeID,
				RowIdx:  idx,
				Kind:    "tree",
				Snippet: strings.TrimSpace(nd.Label),
			})
		}
		if len(nd.Children) > 0 && (nd.Expanded == nil || *nd.Expanded) {
			walkTypedTreeSearchNodes(nd.Children, treeID, mm, rowIdx, out)
		}
	}
}

func walkUITreeSearchNodes(nodes []any, treeID string, mm traits.MultiMatcher, out *[]pageSearchMatch) {
	for _, item := range nodes {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		lbl, _ := m["label"].(string)
		id, _ := m["id"].(string)
		if strings.TrimSpace(lbl) != "" && mm.Match(lbl) {
			*out = append(*out, pageSearchMatch{
				NodeID:  treeID,
				FocusID: treeID,
				RowID:   id,
				Kind:    "tree",
				Snippet: strings.TrimSpace(lbl),
			})
		}
		if kids, ok := m["children"].([]any); ok && len(kids) > 0 {
			walkUITreeSearchNodes(kids, treeID, mm, out)
		}
	}
}

// --- Bottom Search Bar & Inline Highlighting ------------------------------------

func (a *App) activeBottomBarNode(s core.State, root ui.ViewNode, w int) (ui.ViewNode, bool) {
	if bar, ok := a.searchBarNode(s, w); ok {
		return bar, true
	}
	return a.filterBarNode(s, root, w)
}

func (a *App) searchBarNode(s core.State, w int) (ui.ViewNode, bool) {
	if a.Engine.Mode(s) != ModeSearch {
		return ui.ViewNode{}, false
	}
	ps := a.pageSearch()
	var (
		sb    strings.Builder
		runes int
		spans []ui.ToneSpan
	)
	write := func(seg, tone string) {
		if seg == "" {
			return
		}
		n := len([]rune(seg))
		if tone != "" {
			spans = append(spans, ui.ToneSpan{From: runes, To: runes + n, Tone: tone})
		}
		sb.WriteString(seg)
		runes += n
	}

	write(" ", "")
	write("search ", "muted")
	write("\\", "accent")

	q := ps.query
	ps.line.Sync(q)
	rs := []rune(q)
	ci := min(max(ps.line.Cursor, 0), len(rs))
	shownQuery := string(rs[:ci]) + "█" + string(rs[ci:])

	queryTone := ""
	if ps.errMsg != "" {
		queryTone = "error"
	}
	write(shownQuery, queryTone)
	write("  ["+string(ps.effectiveMode(a.matchMode))+"]", "accent")
	if strings.TrimSpace(q) != "" && ps.errMsg == "" {
		countTone := "accent"
		if len(ps.matches) == 0 {
			countTone = "warning"
		}
		write("  "+ps.countLabel(), countTone)
	}
	if ps.errMsg != "" {
		write("  "+formatFilterErrorLabel(ps.errMsg), "error")
	} else {
		write("    enter keep · esc cancel · ctrl+x clear · tab mode · ↑/↓ match", "muted")
	}

	clipped := clip(sb.String(), w)
	props := map[string]any{"text": clipped}
	if clamped := clampToneSpans(spans, len([]rune(clipped))); len(clamped) > 0 {
		props["spans"] = clamped
	}
	return ui.ViewNode{
		ID:    "hints",
		Kind:  ui.KindText,
		Box:   layout.Constraints{FixedH: 1},
		Props: props,
	}, true
}

func (a *App) applyPageSearchHighlights(root *ui.ViewNode) {
	if root == nil || !a.searchHighlightEnabled() || !a.hasPageSearch() {
		return
	}
	ps := a.nav.pageSearch
	if ps.errMsg != "" || len(ps.matches) == 0 {
		return
	}
	active := ps.matches[min(ps.index, len(ps.matches)-1)]
	matchedNodes := make(map[string]bool, len(ps.matches))
	for _, m := range ps.matches {
		if m.NodeID != "" {
			matchedNodes[m.NodeID] = true
		}
	}
	highlightSearchTree(root, ps.query, active, matchedNodes)
}

func highlightSearchTree(n *ui.ViewNode, query string, active pageSearchMatch, matched map[string]bool) {
	if n.ID == "chrome.sep" || n.ID == "hints" || n.ID == "status" {
		return
	}
	if n.Kind == ui.KindText && matched[n.ID] {
		highlightTextNode(n, query, n.ID == active.NodeID)
	}
	for i := range n.Children {
		highlightSearchTree(&n.Children[i], query, active, matched)
	}
}

func highlightTextNode(n *ui.ViewNode, query string, isActive bool) {
	raw, _ := n.Props["text"].(string)
	if raw == "" {
		return
	}
	spans := findCaseFoldRuneSpans(raw, query)
	if len(spans) == 0 {
		return
	}
	tone := "match"
	if isActive {
		tone = "warning"
	}
	props := maps.Clone(n.Props)
	if props == nil {
		props = map[string]any{}
	}
	var merged []ui.ToneSpan
	if existing, ok := props["spans"].([]ui.ToneSpan); ok {
		merged = slices.Clone(existing)
	}
	for _, sp := range spans {
		merged = append(merged, ui.ToneSpan{From: sp[0], To: sp[1], Tone: tone})
	}
	props["spans"] = merged
	n.Props = props
}

func findCaseFoldRuneSpans(text, query string) [][2]int {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return nil
	}
	hay := []rune(text)
	var out [][2]int
	for _, term := range terms {
		needle := []rune(strings.Trim(term, `"`))
		if len(needle) == 0 || len(needle) > len(hay) {
			continue
		}
		for i := 0; i <= len(hay)-len(needle); i++ {
			if runesEqualFold(hay[i:i+len(needle)], needle) {
				out = append(out, [2]int{i, i + len(needle)})
				i += len(needle) - 1
			}
		}
	}
	return out
}

func runesEqualFold(a, b []rune) bool {
	for i := range a {
		if unicode.ToLower(a[i]) != unicode.ToLower(b[i]) {
			return false
		}
	}
	return true
}
