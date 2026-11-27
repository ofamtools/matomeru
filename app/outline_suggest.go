package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

const (
	CmdOutlineSuggestNext   = "outline.suggest.next"
	CmdOutlineSuggestPrev   = "outline.suggest.prev"
	CmdOutlineSuggestAccept = "outline.suggest.accept"

	maxOutlineSuggestions = 6
)

type outlineSuggestion struct {
	ID       string
	Title    string
	Status   string
	ChildCnt int
	IsRoot   bool
}

type outlineSuggestState struct {
	query    string
	parentID string
	kind     string
	treeID   string
	items    []outlineSuggestion
	selected int // -1 means none highlighted; enter creates a new item as usual
}

func outlineSuggestSpecs() []core.CommandSpec {
	internal := func(id, desc string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Meta: map[string]any{"internal": true}}
	}
	return []core.CommandSpec{
		internal(CmdOutlineSuggestNext, "Highlight the next auto-suggest match in inline outline edit"),
		internal(CmdOutlineSuggestPrev, "Highlight the previous auto-suggest match in inline outline edit"),
		internal(CmdOutlineSuggestAccept, "Accept the highlighted auto-suggest match in inline outline edit"),
	}
}

func outlineSuggestBindings() []core.Binding {
	m, all := ModeOutlineEdit, ""
	return []core.Binding{
		bind(m, all, "ctrl+n", CmdOutlineSuggestNext, "next suggestion"),
		bind(m, all, "ctrl+p", CmdOutlineSuggestPrev, "prev suggestion"),
		hidden(bind(m, all, "down", CmdOutlineSuggestNext, "next suggestion")),
		hidden(bind(m, all, "up", CmdOutlineSuggestPrev, "prev suggestion")),
	}
}

func (a *App) registerOutlineSuggestHandlers() {
	a.handle(CmdOutlineSuggestNext, a.outlineSuggestStep(1))
	a.handle(CmdOutlineSuggestPrev, a.outlineSuggestStep(-1))
	a.handle(CmdOutlineSuggestAccept, a.outlineSuggestAcceptCmd)
}

func (a *App) outlineAutoSuggestEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetOutlineAutoSuggest, true)
}

func (a *App) resetOutlineSuggest() {
	if a == nil || a.trees == nil {
		return
	}
	a.trees.suggest = outlineSuggestState{selected: -1}
}

func (a *App) editTreeID(meta *treeEditMeta) string {
	if meta == nil {
		return outlineTree
	}
	if meta.treeID != "" {
		return meta.treeID
	}
	if meta.isWorkTree {
		return workTable
	}
	return outlineTree
}

func (a *App) outlineSuggestCandidates(kind, parentID string) []data.TreeNode {
	allNodes, err := a.recordTreeNodes(kind)
	if err != nil || len(allNodes) == 0 {
		return nil
	}
	if parentID == "" {
		return allNodes
	}
	kids, ok := findTreeChildren(allNodes, parentID)
	if !ok {
		return nil
	}
	return kids
}

func (a *App) computeOutlineSuggestions(schema data.Schema, kind, parentID, query string) []outlineSuggestion {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	candidates := a.outlineSuggestCandidates(kind, parentID)
	if len(candidates) == 0 {
		return nil
	}
	subMatcher, _ := traits.CompileMultiTerm(q, traits.MatchSubstring, traits.FilterConfig{})
	fuzzyMatcher, _ := traits.CompileMultiTerm(q, traits.MatchFuzzy, traits.FilterConfig{})
	var exact, sub, fuzzy []outlineSuggestion
	for _, nd := range candidates {
		title := strings.TrimSpace(views.Title(schema, nd.Record))
		if title == "" {
			continue
		}
		item := outlineSuggestion{
			ID:       nd.Record.ID,
			Title:    title,
			Status:   outlinePrimaryStatus(schema, nd.Record),
			ChildCnt: len(nd.Children),
			IsRoot:   parentID == "",
		}
		switch {
		case strings.EqualFold(title, q):
			exact = append(exact, item)
		case subMatcher.Match(title):
			sub = append(sub, item)
		case fuzzyMatcher.Match(title):
			fuzzy = append(fuzzy, item)
		}
	}
	return capOutlineSuggestions(exact, sub, fuzzy)
}

func capOutlineSuggestions(groups ...[]outlineSuggestion) []outlineSuggestion {
	out := make([]outlineSuggestion, 0, maxOutlineSuggestions)
	for _, g := range groups {
		for _, item := range g {
			if len(out) >= maxOutlineSuggestions {
				return out
			}
			out = append(out, item)
		}
	}
	return out
}

func outlinePrimaryStatus(schema data.Schema, rec data.Record) string {
	if col, ok := primaryStatusColumn(schema); ok {
		if v := strings.TrimSpace(rec.Text(col.Name)); v != "" {
			return v
		}
		return col.Default
	}
	return strings.TrimSpace(rec.Text("workStatus"))
}

func (a *App) outlineSuggestQuery(tm *tree.Model, treeID string) (string, string, bool) {
	if !a.outlineAutoSuggestEnabled() || tm == nil || !tm.Editing() || treeID == featuresOutlineTree {
		return "", "", false
	}
	ed := tm.Edit()
	if ed == nil || ed.Kind == tree.EditRename {
		return "", "", false
	}
	q := strings.TrimSpace(ed.Value())
	if q == "" {
		return "", "", false
	}
	return q, ed.ParentID, true
}

func (a *App) syncOutlineSuggestions(tm *tree.Model, kind, treeID string) []outlineSuggestion {
	if a == nil || a.trees == nil {
		return nil
	}
	q, parentID, ok := a.outlineSuggestQuery(tm, treeID)
	if !ok {
		a.resetOutlineSuggest()
		return nil
	}
	st := &a.trees.suggest
	if st.query == q && st.parentID == parentID && st.kind == kind && st.treeID == treeID {
		return st.items
	}
	items := a.computeOutlineSuggestions(a.schema(kind), kind, parentID, q)
	a.trees.suggest = outlineSuggestState{
		query:    q,
		parentID: parentID,
		kind:     kind,
		treeID:   treeID,
		items:    items,
		selected: -1,
	}
	return items
}

func (a *App) outlineSuggestStep(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		tm, meta, ok := a.activeEditTree()
		if !ok {
			return core.Next(s)
		}
		items := a.syncOutlineSuggestions(tm, meta.kind, a.editTreeID(meta))
		if len(items) == 0 {
			return core.Next(s)
		}
		st := &a.trees.suggest
		if st.selected < 0 {
			if delta > 0 {
				st.selected = 0
			} else {
				st.selected = len(items) - 1
			}
		} else {
			st.selected = (st.selected + delta + len(items)) % len(items)
		}
		a.say("suggestion %d/%d: %s · enter or tab accepts", st.selected+1, len(items), items[st.selected].Title)
		return core.Next(s)
	}
}

func (a *App) outlineSuggestAcceptCmd(s core.State, _ core.Command) core.StepResult {
	if mode, accepted := a.tryAcceptOutlineSuggestion(true); accepted {
		return core.Next(withMode(s, mode))
	}
	return core.Next(s)
}

func (a *App) runOutlineEditNewlineOrSuggest() (string, bool) {
	if a.isEditingFeaturesOutline() {
		return a.featuresOutlineNewline()
	}
	if mode, accepted := a.tryAcceptOutlineSuggestion(false); accepted {
		return mode, true
	}
	return a.runOutlineEditNewline()
}

func (a *App) runOutlineEditCommitOrFeatures() (string, bool) {
	a.resetOutlineSuggest()
	if a.isEditingFeaturesOutline() {
		return a.commitFeaturesOutlineEdit()
	}
	return a.runOutlineEditCommit()
}

func (a *App) runOutlineEditCancelOrFeatures() (string, bool) {
	a.resetOutlineSuggest()
	if a.isEditingFeaturesOutline() {
		return a.cancelFeaturesOutlineEdit()
	}
	return a.runOutlineEditCancel()
}

// tryAcceptOutlineSuggestion accepts a matching suggestion during inline add.
// When defaultFirst is true (tab), the first suggestion is accepted even if the
// user has not explicitly stepped into the list; when false (enter), a
// suggestion is accepted only if explicitly highlighted via ctrl+n/ctrl+p/up/down.
func (a *App) tryAcceptOutlineSuggestion(defaultFirst bool) (string, bool) {
	tm, meta, ok := a.activeEditTree()
	if !ok {
		return "", false
	}
	treeID := a.editTreeID(meta)
	items := a.syncOutlineSuggestions(tm, meta.kind, treeID)
	if len(items) == 0 {
		return "", false
	}
	idx := a.trees.suggest.selected
	if idx < 0 {
		if !defaultFirst {
			return "", false
		}
		idx = 0
	}
	if idx >= len(items) {
		return "", false
	}
	return a.acceptOutlineSuggestion(tm, meta, treeID, items[idx])
}

func (a *App) snapshotHiddenChildrenForCleanSlate(cs *cleanSlateState, kind, rootID string) {
	if cs == nil || rootID == "" {
		return
	}
	if cs.hiddenChildren == nil {
		cs.hiddenChildren = map[string]map[string]bool{}
	}
	if _, already := cs.hiddenChildren[rootID]; already {
		return
	}
	hidden := map[string]bool{}
	if kids, ok := findTreeChildren(a.outlineSuggestCandidates(kind, ""), rootID); ok {
		for _, ch := range kids {
			hidden[ch.Record.ID] = true
		}
	}
	cs.hiddenChildren[rootID] = hidden
}

func (a *App) acceptOutlineSuggestion(tm *tree.Model, meta *treeEditMeta, treeID string, match outlineSuggestion) (string, bool) {
	ed := tm.Edit()
	wasTopLevel := ed != nil && ed.ParentID == ""
	kind := meta.kind
	retMode := meta.returnMode
	isWork := meta.isWorkTree
	tm.CancelEdit()
	a.resetOutlineSuggest()

	anchoredCleanSlate := false
	if cs := a.activeCleanSlate(kind, treeID); cs != nil && wasTopLevel {
		a.addCleanSlateRoot(kind, treeID, match.ID)
		a.snapshotHiddenChildrenForCleanSlate(cs, kind, match.ID)
		anchoredCleanSlate = true
	}

	schema, _ := a.syncTreeModel(tm, kind, treeID)
	tm.Reveal(match.ID)
	delete(tm.Folded, match.ID)
	a.saveTreeFolded(kind, treeID, tm)
	if isWork {
		a.syncWorkTreeToTable(kind, tm)
	}

	tm.StartAdd(tree.EditAddChild, a.defaultEditPrefix(schema))
	a.trees.editMeta = &treeEditMeta{
		kind:       kind,
		returnMode: retMode,
		isWorkTree: isWork,
		treeID:     treeID,
	}
	if anchoredCleanSlate {
		a.say("anchored to %s · type child · shift+tab outdents · esc exits", match.Title)
	} else {
		a.say("matched %s · type child · shift+tab outdents · esc exits", match.Title)
	}
	return ModeOutlineEdit, true
}

func outlineSuggestionRow(item outlineSuggestion, idx int, active bool) ui.ViewNode {
	marker := "  · "
	tone := ""
	if active {
		marker = "  ▸ "
		tone = "accent"
	}
	var suffix []string
	if item.Status != "" {
		suffix = append(suffix, item.Status)
	}
	if item.ChildCnt == 1 {
		suffix = append(suffix, "1 child")
	} else if item.ChildCnt > 1 {
		suffix = append(suffix, fmt.Sprintf("%d children", item.ChildCnt))
	}
	line := marker + item.Title
	if len(suffix) > 0 {
		line += "  [" + strings.Join(suffix, " · ") + "]"
	}
	return ui.ViewNode{
		ID:    fmt.Sprintf("outline.suggest.%d", idx),
		Kind:  ui.KindText,
		Tone:  tone,
		Box:   layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": line},
	}
}

func (a *App) buildOutlineSuggestionStrip(treeID string, items []outlineSuggestion, selected int) ui.ViewNode {
	headerText := fmt.Sprintf(" Existing matches (%d) · tab accept · ↑↓/ctrl+n/p + enter pick · enter new", len(items))
	rows := make([]ui.ViewNode, 0, len(items)+1)
	rows = append(rows, ui.ViewNode{
		ID:    treeID + ".suggestions.header",
		Kind:  ui.KindText,
		Tone:  "muted",
		Box:   layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": headerText},
	})
	for i, item := range items {
		active := i == selected || (selected < 0 && i == 0)
		rows = append(rows, outlineSuggestionRow(item, i, active))
	}
	return ui.ViewNode{
		ID:       treeID + ".suggestions",
		Kind:     ui.KindVStack,
		Box:      layout.Constraints{FixedH: len(rows)},
		Children: rows,
	}
}

func (a *App) attachOutlineSuggestions(panel *ui.ViewNode, tm *tree.Model, kind, treeID string) {
	if panel == nil {
		return
	}
	items := a.syncOutlineSuggestions(tm, kind, treeID)
	selected := -1
	if a.trees != nil {
		selected = a.trees.suggest.selected
	}
	if panel.Facts == nil {
		panel.Facts = map[string]any{}
	}
	panel.Facts["hasSuggestions"] = len(items) > 0
	panel.Facts["suggestionCount"] = len(items)
	panel.Facts["suggestionSelected"] = selected >= 0
	if len(items) == 0 {
		return
	}
	strip := a.buildOutlineSuggestionStrip(treeID, items, selected)
	body := panel.Children
	for i := range body {
		if body[i].Box.Flex == 0 && body[i].Box.FixedH == 0 {
			body[i].Box.Flex = 1
		}
	}
	panel.Children = []ui.ViewNode{{
		ID:       treeID + ".suggest.stack",
		Kind:     ui.KindVStack,
		Box:      layout.Constraints{Flex: 1},
		Children: append(body, strip),
	}}
}
