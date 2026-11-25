package app

import (
	"fmt"
	"maps"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

type treeEditMeta struct {
	kind       string
	returnMode string
	isWorkTree bool
}

type cleanSlateState struct {
	active bool
	roots  map[string]bool
}

// treeController owns the universal schema-driven tree state for 2 Work
// (per-kind tree models and layout overrides), the T Outline overlay, and
// per-project embedded Project Page outlines, sharing per-kind fold maps and
// expanded note threads across views.
type treeController struct {
	work              map[string]*tree.Model
	outline           *tree.Model
	projectTrees      map[string]*tree.Model
	activeProjectID   string
	folded            map[string]map[string]bool
	foldInitialized   map[string]bool
	layoutOverride    map[string]string
	workTableRowID    map[string]string
	editMeta          *treeEditMeta
	outlineCleanSlate cleanSlateState
	workCleanSlate    map[string]*cleanSlateState
	expandedThreads   map[string]bool
}

func newTreeController() *treeController {
	return &treeController{
		work:            map[string]*tree.Model{},
		projectTrees:    map[string]*tree.Model{},
		folded:          map[string]map[string]bool{},
		foldInitialized: map[string]bool{},
		layoutOverride:  map[string]string{},
		workTableRowID:  map[string]string{},
		workCleanSlate:  map[string]*cleanSlateState{},
		expandedThreads: map[string]bool{},
	}
}

func (tc *treeController) workModel(kind string) *tree.Model {
	if tc.work == nil {
		tc.work = map[string]*tree.Model{}
	}
	tm := tc.work[kind]
	if tm == nil {
		tm = tree.New(nil)
		tc.work[kind] = tm
	}
	return tm
}

func (tc *treeController) outlineModel() *tree.Model {
	if tc.outline == nil {
		tc.outline = tree.New(nil)
	}
	return tc.outline
}

func (tc *treeController) projectModel(projectID string) *tree.Model {
	if tc.projectTrees == nil {
		tc.projectTrees = map[string]*tree.Model{}
	}
	projectID = strings.TrimSpace(projectID)
	tm := tc.projectTrees[projectID]
	if tm == nil {
		tm = tree.New(nil)
		tc.projectTrees[projectID] = tm
	}
	return tm
}

func (tc *treeController) projectIDForModel(tm *tree.Model) string {
	if tc == nil || tm == nil {
		return ""
	}
	if tc.activeProjectID != "" && tc.projectTrees[tc.activeProjectID] == tm {
		return tc.activeProjectID
	}
	for id, m := range tc.projectTrees {
		if m == tm {
			return id
		}
	}
	return ""
}

func (tc *treeController) editingProjectModel() (*tree.Model, string) {
	if tc == nil {
		return nil, ""
	}
	if tc.activeProjectID != "" {
		if tm := tc.projectTrees[tc.activeProjectID]; tm != nil && tm.Editing() {
			return tm, tc.activeProjectID
		}
	}
	for id, tm := range tc.projectTrees {
		if tm != nil && tm.Editing() {
			return tm, id
		}
	}
	return nil, ""
}

func (tc *treeController) clearEdit() {
	if tc == nil || tc.editMeta == nil {
		return
	}
	if tc.editMeta.isWorkTree {
		if tm := tc.work[tc.editMeta.kind]; tm != nil {
			tm.CancelEdit()
		}
	} else {
		if tm := tc.outline; tm != nil {
			tm.CancelEdit()
		}
		for _, tm := range tc.projectTrees {
			if tm != nil && tm.Editing() {
				tm.CancelEdit()
			}
		}
	}
	tc.editMeta = nil
}

func (tc *treeController) editing() bool {
	if tc == nil || tc.editMeta == nil {
		return false
	}
	if tc.editMeta.isWorkTree {
		tm := tc.work[tc.editMeta.kind]
		return tm != nil && tm.Editing()
	}
	if tm, _ := tc.editingProjectModel(); tm != nil {
		return true
	}
	return tc.outline != nil && tc.outline.Editing()
}

func (tc *treeController) editingOutline() bool {
	return tc != nil && tc.editMeta != nil && !tc.editMeta.isWorkTree && tc.outline != nil && tc.outline.Editing()
}

func (tc *treeController) activeEdit() *tree.EditState {
	if tc == nil || tc.editMeta == nil {
		return nil
	}
	if tc.editMeta.isWorkTree {
		if tm := tc.work[tc.editMeta.kind]; tm != nil {
			return tm.Edit()
		}
		return nil
	}
	if tm, _ := tc.editingProjectModel(); tm != nil {
		return tm.Edit()
	}
	if tc.outline != nil {
		return tc.outline.Edit()
	}
	return nil
}

func (tc *treeController) workFocusedIndex(kind string) int {
	if tc == nil {
		return -1
	}
	tm := tc.work[kind]
	if tm == nil {
		return -1
	}
	if row, ok := tm.Focused(); ok {
		return row.Index
	}
	return -1
}

func (a *App) scrollOff() int {
	if a.cfg.Settings == nil {
		return 5
	}
	return max(0, a.cfg.Settings.Int(SetScrollOff, 5))
}

func (a *App) treeViewportHeight(treeID string) int {
	if a.Shell != nil {
		if r, ok := a.Shell.State.Layout.InnerRects[treeID]; ok && r.H > 0 {
			return r.H
		}
		if a.Shell.H > 6 {
			return a.Shell.H - 6
		}
	}
	return 20
}

// workLayoutFor returns the effective layout ("tree" or "table") for kind in
// 2 Work, consulting schema.EffectiveHierarchy, per-kind g t overrides, and
// the work.hierarchyLayout setting.
func (a *App) workLayoutFor(kind string) string {
	h := a.schema(kind).EffectiveHierarchy()
	if h == nil {
		return WorkTasksLayoutTable
	}
	if a.trees != nil {
		if ov := a.trees.layoutOverride[kind]; ov == WorkTasksLayoutTree || ov == WorkTasksLayoutTable {
			return ov
		}
	}
	switch a.hierarchyLayout() {
	case WorkHierarchyLayoutTable:
		return WorkTasksLayoutTable
	case WorkHierarchyLayoutTree:
		return WorkTasksLayoutTree
	default:
		if h.DefaultView == WorkTasksLayoutTable {
			return WorkTasksLayoutTable
		}
		return WorkTasksLayoutTree
	}
}

func (a *App) workTasksLayout() string {
	return a.workLayoutFor(data.KindTask)
}

// inWorkTree reports whether 2 Work is currently displaying a schema-driven
// hierarchy tree (true when the browsed noun resolves to tree layout and no
// table filter, sort, or group is active).
func (a *App) inWorkTree(s core.State) bool {
	if s.ActivePage != SpaceWork || a.Modal() != "" || a.nav.histPos >= 0 {
		return false
	}
	if a.workLayoutFor(a.workKind()) != WorkTasksLayoutTree {
		return false
	}
	if tbl := a.nav.tables[workTable]; tbl != nil && (tbl.Query != "" || tbl.SortField != "" || tbl.GroupField != "") {
		return false
	}
	return true
}

func (a *App) recordTreeNodes(kind string) ([]data.TreeNode, error) {
	if a.isExternalKind(kind) {
		recs, _ := a.externalRecords(kind)
		parentField, orderField := "", ""
		if h := a.schema(kind).EffectiveHierarchy(); h != nil {
			parentField, orderField = h.ParentField, h.OrderField
		}
		return data.BuildTree(recs, parentField, orderField, a.nav.showArchived), nil
	}
	return a.recordTreeNodesWithAnimating(kind)
}

func (a *App) outlineStartCollapsed() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetOutlineStartCollapsed, true)
}

func collectCollapsibleRecordIDs(nodes []data.TreeNode) []string {
	var ids []string
	var walk func(ns []data.TreeNode)
	walk = func(ns []data.TreeNode) {
		for _, nd := range ns {
			if len(nd.Children) > 0 && nd.Record.ID != "" {
				ids = append(ids, nd.Record.ID)
			}
			walk(nd.Children)
		}
	}
	walk(nodes)
	return ids
}

func (a *App) saveTreeFolded(kind, treeID string, tm *tree.Model) {
	if a.trees == nil || tm == nil {
		return
	}
	projID := ""
	if kind == data.KindTask {
		projID = a.trees.projectIDForModel(tm)
	}
	if !a.isCleanSlateActive(kind, treeID) && projID == "" && treeID != projectOutlineTree {
		a.trees.folded[kind] = tm.Folded
		return
	}
	// Preserve hidden trees' fold state while reflecting any fold changes
	// made to visible nodes (excluding a pinned project hoist root).
	dst := a.trees.folded[kind]
	if dst == nil {
		dst = map[string]bool{}
		a.trees.folded[kind] = dst
	}
	var walk func(ns []tree.Node)
	walk = func(ns []tree.Node) {
		for _, nd := range ns {
			if nd.ID != "" && nd.ID != projID {
				if tm.Folded[nd.ID] {
					dst[nd.ID] = true
				} else {
					delete(dst, nd.ID)
				}
			}
			walk(nd.Children)
		}
	}
	walk(tm.Nodes)
}

func (a *App) applyStartCollapsedSetting() {
	if a.trees == nil {
		return
	}
	startCollapsed := a.outlineStartCollapsed()
	clear(a.trees.foldInitialized)
	if !startCollapsed {
		clear(a.trees.folded)
	}
	for kind, tm := range a.trees.work {
		if tm == nil {
			continue
		}
		tm.ResetFoldInit()
		if startCollapsed {
			tm.FoldAll()
		} else {
			tm.UnfoldAll()
		}
		if len(tm.Nodes) > 0 || !startCollapsed {
			a.trees.folded[kind] = tm.Folded
		}
	}
	if tm := a.trees.outline; tm != nil {
		tm.ResetFoldInit()
		if startCollapsed {
			tm.FoldAll()
		} else {
			tm.UnfoldAll()
		}
		if len(tm.Nodes) > 0 || !startCollapsed {
			a.trees.folded[data.KindTask] = tm.Folded
		}
	}
	for projID, tm := range a.trees.projectTrees {
		if tm == nil {
			continue
		}
		tm.ResetFoldInit()
		if startCollapsed {
			tm.FoldAll()
			if projID != "" {
				delete(tm.Folded, projID)
			}
		} else {
			tm.UnfoldAll()
		}
		a.saveTreeFolded(data.KindTask, projectOutlineTree, tm)
	}
}

func (a *App) initTreeFoldState(kind string, allNodes []data.TreeNode) {
	if len(allNodes) == 0 || a.trees.foldInitialized[kind] {
		return
	}
	a.trees.foldInitialized[kind] = true
	if !a.outlineStartCollapsed() {
		return
	}
	collapsible := collectCollapsibleRecordIDs(allNodes)
	if len(collapsible) == 0 {
		return
	}
	initFolded := traits.FoldInitial(traits.FoldConfig{DefaultFolded: true}, collapsible).Collapsed
	if a.trees.folded[kind] == nil {
		a.trees.folded[kind] = initFolded
		return
	}
	for id, v := range initFolded {
		if v {
			a.trees.folded[kind][id] = true
		}
	}
}

func (a *App) buildTreeNodes(schema data.Schema, kind string, nodes []data.TreeNode) ([]tree.Node, views.TreeOptions) {
	var taskThreads map[string][]data.Record
	if kind == data.KindTask && a.Store != nil {
		taskThreads, _ = a.Store.TaskNoteThreads()
	}

	opts := views.TreeOptions{
		Style:         a.styleOptions(),
		GapTopLevel:   a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTreeTopLevelGap, true),
		Guides:        a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTreeGuides, true),
		ShowFoldCount: true,
		ResolveRef:    a.title,
		EnrichNode: func(rec data.Record, nd *tree.Node) {
			if a.openable(rec) {
				if nd.Facts == nil {
					nd.Facts = map[string]any{}
				}
				nd.Facts[factOpenable] = true
			}
			a.applyArchiveAnimToTreeNode(rec, nd)
			if kind == data.KindTask {
				a.enrichTaskTreeNode(rec, nd, taskThreads)
			}
		},
	}
	built := views.TreeFromNodes(schema, nodes, opts)
	if kind == data.KindTask {
		a.attachTaskThreadNodes(built.Nodes, taskThreads)
	}
	return built.Nodes, opts
}

func (a *App) syncTreeModel(tm *tree.Model, kind, treeID string) (data.Schema, []data.TreeNode) {
	if kind == data.KindTask && a.trees != nil {
		if projID := a.trees.projectIDForModel(tm); projID != "" {
			return a.syncProjectTaskTree(tm, projID, projectOutlineTree)
		}
		if treeID == projectOutlineTree && a.trees.activeProjectID != "" {
			return a.syncProjectTaskTree(tm, a.trees.activeProjectID, projectOutlineTree)
		}
	}
	schema := a.schema(kind)
	allNodes, err := a.recordTreeNodes(kind)
	if err != nil {
		a.fail(kind, err)
	}
	a.initTreeFoldState(kind, allNodes)

	nodes := allNodes
	cs := a.activeCleanSlate(kind, treeID)
	if cs != nil {
		filtered := make([]data.TreeNode, 0, len(cs.roots))
		for _, nd := range allNodes {
			if cs.roots[nd.Record.ID] {
				filtered = append(filtered, nd)
			}
		}
		nodes = filtered
	}

	builtNodes, opts := a.buildTreeNodes(schema, kind, nodes)
	tm.DefaultFolded = false
	if cs != nil {
		tm.Folded = maps.Clone(a.trees.folded[kind])
	} else {
		tm.Folded = a.trees.folded[kind]
	}
	tm.GapTopLevel = opts.GapTopLevel
	tm.Guides = opts.Guides
	tm.ShowFoldCount = opts.ShowFoldCount
	tm.Wrap = a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetWrapOutline, true)
	tm.ScrollOff = a.scrollOff()
	tm.PageSize = a.treeViewportHeight(treeID)
	tm.SetNodes(builtNodes)
	a.saveTreeFolded(kind, treeID, tm)
	return schema, nodes
}

func (a *App) syncWorkTreeToTable(kind string, tm *tree.Model) {
	if tbl := a.nav.tables[workTable]; tbl != nil && tm != nil && tm.SelectedID != "" && a.workKind() == kind {
		refID := data.RefText(core.Ref{Kind: kind, ID: tm.SelectedID})
		if selectRow(tbl, refID) {
			a.trees.workTableRowID[kind] = refID
		}
	}
}

func (a *App) workTreeView() ui.ViewNode {
	kind := a.workKind()
	tbl := a.records(workTable, kind)
	tm := a.trees.workModel(kind)
	csActive := a.isCleanSlateActive(kind, workTable)

	if !csActive {
		if row, ok := tbl.FocusedRow(); ok && row.ID != "" && row.Ref != nil && row.Ref.Kind == kind {
			prevSynced := a.trees.workTableRowID[kind]
			if (prevSynced != "" && row.ID != prevSynced) || tm.SelectedID == "" {
				tm.SelectedID = row.Ref.ID
				a.trees.workTableRowID[kind] = row.ID
			}
		}
	}

	schema, _ := a.syncTreeModel(tm, kind, workTable)
	if row, ok := tm.Focused(); ok && !row.Draft && row.ID != "" {
		refID := data.RefText(core.Ref{Kind: kind, ID: row.ID})
		if selectRow(tbl, refID) {
			a.trees.workTableRowID[kind] = refID
		}
	}

	extra := []ui.Badge{{Text: "tree", Tone: "accent"}}
	if csActive {
		extra = append(extra, ui.Badge{Text: "clean slate", Tone: "accent"})
	}
	if a.nav.showArchived {
		extra = append(extra, ui.Badge{Text: "archived", Tone: "muted"})
	}
	panel := views.TreeView(schema, tm, SpaceWork, workTable, views.ScopeIndex, extra...)
	panel.Facts["cleanSlate"] = csActive
	return panel
}

func (a *App) outlineTreeView() ui.ViewNode {
	tm := a.trees.outlineModel()
	schema, _ := a.syncTreeModel(tm, data.KindTask, outlineTree)
	csActive := a.isCleanSlateActive(data.KindTask, outlineTree)
	var extra []ui.Badge
	if csActive {
		extra = append(extra, ui.Badge{Text: "clean slate", Tone: "accent"})
	}
	if a.nav.showArchived {
		extra = append(extra, ui.Badge{Text: "archived", Tone: "muted"})
	}
	panel := views.TreeView(schema, tm, "outline", outlineTree, views.ScopeOutline, extra...)
	panel.Title = "Outline"
	panel.Facts["cleanSlate"] = csActive
	return panel
}

func (a *App) workTreeMove(c core.Command) {
	kind := a.workKind()
	tm := a.trees.workModel(kind)
	a.syncTreeModel(tm, kind, workTable)
	tm.HandleCommand(c)
	a.saveTreeFolded(kind, workTable, tm)
	a.syncWorkTreeToTable(kind, tm)
}

func (a *App) outlineTreeMove(c core.Command) {
	tm := a.trees.outlineModel()
	a.syncTreeModel(tm, data.KindTask, outlineTree)
	tm.HandleCommand(c)
	a.saveTreeFolded(data.KindTask, outlineTree, tm)
}

func (a *App) activeTree(s core.State) (tm *tree.Model, kind, treeID string, isWorkTree bool, ok bool) {
	if a.Modal() == "outline" {
		return a.trees.outlineModel(), data.KindTask, outlineTree, false, true
	}
	if a.inProjectOutlineTree(&s) {
		if projID := a.activeProjectOutlineID(s); projID != "" {
			return a.trees.projectModel(projID), data.KindTask, projectOutlineTree, false, true
		}
	}
	if a.inWorkTree(s) {
		k := a.workKind()
		return a.trees.workModel(k), k, workTable, true, true
	}
	return nil, "", "", false, false
}

func (a *App) activeEditTree() (*tree.Model, *treeEditMeta, bool) {
	if a.trees == nil || a.trees.editMeta == nil {
		return nil, nil, false
	}
	meta := a.trees.editMeta
	var tm *tree.Model
	if meta.isWorkTree {
		tm = a.trees.workModel(meta.kind)
	} else if ptm, _ := a.trees.editingProjectModel(); ptm != nil {
		tm = ptm
	} else {
		tm = a.trees.outlineModel()
	}
	if !tm.Editing() {
		a.trees.editMeta = nil
		return nil, nil, false
	}
	return tm, meta, true
}

// workLayoutToggle flips 2 Work between tree and table layout for hierarchical
// nouns (bound to g t) while preserving the focused record across the switch.
func (a *App) workLayoutToggle(s core.State, _ core.Command) core.StepResult {
	kind := a.workKind()
	schema := a.schema(kind)
	nounLabel := strings.ToLower(schema.Plural)
	if nounLabel == "" {
		nounLabel = kind
	}
	if schema.EffectiveHierarchy() == nil {
		a.say("%s has no hierarchy to display as a tree", nounLabel)
		return core.Next(s)
	}
	cur := a.workLayoutFor(kind)
	if cur == WorkTasksLayoutTree {
		tm := a.trees.workModel(kind)
		selID := tm.SelectedID
		a.trees.layoutOverride[kind] = WorkTasksLayoutTable
		tbl := a.records(workTable, kind)
		if selID != "" {
			refID := data.RefText(core.Ref{Kind: kind, ID: selID})
			if selectRow(tbl, refID) {
				a.trees.workTableRowID[kind] = refID
			}
		}
		a.say("%s: table view · g t switches to tree", nounLabel)
		return core.Next(s)
	}

	var selID string
	if tbl := a.nav.tables[workTable]; tbl != nil {
		if row, ok := tbl.FocusedRow(); ok && row.Ref != nil && row.Ref.Kind == kind {
			selID = row.Ref.ID
		}
	}
	a.trees.layoutOverride[kind] = WorkTasksLayoutTree
	tm := a.trees.workModel(kind)
	a.syncTreeModel(tm, kind, workTable)
	if selID != "" {
		tm.Reveal(selID)
		a.trees.folded[kind] = tm.Folded
		a.syncWorkTreeToTable(kind, tm)
	}
	a.say("%s: tree view · g t switches to table", nounLabel)
	return core.Next(s)
}

// --- folding --------------------------------------------------------------------

func (a *App) runTreeFold(tm *tree.Model, kind, treeID string, isWork bool, cmdID string) {
	a.syncTreeModel(tm, kind, treeID)
	if kind == data.KindTask && a.handleLeafTaskFoldThread(tm, treeID, isWork, cmdID) {
		return
	}
	beforeRow, hasBefore := tm.Focused()
	switch cmdID {
	case tree.CmdFoldToggle:
		if !tm.ToggleFold() {
			if hasBefore && !beforeRow.Draft {
				a.say("%s has no children to fold", a.title(core.Ref{Kind: kind, ID: beforeRow.ID}))
			} else {
				a.say("nothing to fold")
			}
			return
		}
		afterRow, _ := tm.Focused()
		if tm.Folded[afterRow.ID] {
			a.say("folded %s", a.title(core.Ref{Kind: kind, ID: afterRow.ID}))
		} else {
			a.say("unfolded %s", a.title(core.Ref{Kind: kind, ID: afterRow.ID}))
		}
	case tree.CmdFoldOpen:
		if tm.Unfold() {
			if row, has := tm.Focused(); has {
				a.say("unfolded %s", a.title(core.Ref{Kind: kind, ID: row.ID}))
			}
		} else {
			a.say("already unfolded")
		}
	case tree.CmdFoldClose:
		if tm.Fold() {
			if row, has := tm.Focused(); has {
				a.say("folded %s", a.title(core.Ref{Kind: kind, ID: row.ID}))
			}
		} else {
			a.say("nothing to fold")
		}
	case tree.CmdFoldOpenAll:
		tm.UnfoldAll()
		a.say("unfolded all")
	case tree.CmdFoldCloseAll:
		tm.FoldAll()
		if id := tm.HoistID(); id != "" {
			delete(tm.Folded, id)
		}
		a.say("folded all")
	}
	a.saveTreeFolded(kind, treeID, tm)
	if isWork {
		a.syncWorkTreeToTable(kind, tm)
	}
}

func (a *App) treeFoldCommand(cmdID string) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		if a.Modal() == "outline" {
			c.ID = cmdID
			return a.handleModal(s, c)
		}
		tm, kind, treeID, isWork, ok := a.activeTree(s)
		if !ok {
			a.say("no tree is focused")
			return core.Next(s)
		}
		a.runTreeFold(tm, kind, treeID, isWork, cmdID)
		return core.Next(s)
	}
}

// --- hoisting -------------------------------------------------------------------

func (a *App) runTreeHoist(tm *tree.Model, kind, treeID string, isWork bool, c core.Command) {
	a.syncTreeModel(tm, kind, treeID)
	switch c.ID {
	case tree.CmdHoistIn:
		targetID := strings.TrimSpace(c.Arg("id"))
		if targetID == "" {
			if row, ok := tm.Focused(); ok && !row.Draft {
				targetID = row.ID
			}
		}
		if targetID == "" {
			a.say("no item selected to hoist")
			return
		}
		if !tm.Hoist(targetID) {
			if tm.HoistID() == targetID {
				a.say("already hoisted to %s", a.title(core.Ref{Kind: kind, ID: targetID}))
			} else {
				a.say("cannot hoist item")
			}
			return
		}
		a.say("hoisted %s · - or esc zooms out", a.title(core.Ref{Kind: kind, ID: targetID}))
	case tree.CmdHoistOut:
		projID := a.trees.projectIDForModel(tm)
		if projID != "" && len(tm.HoistStack) <= 1 && tm.HoistID() == projID {
			a.say("already at project outline")
			return
		}
		if !tm.Unhoist() {
			a.say("already at full tree")
			return
		}
		if id := tm.HoistID(); id != "" && id != projID {
			a.say("hoisted %s", a.title(core.Ref{Kind: kind, ID: id}))
		} else if projID != "" {
			a.say("returned to project outline")
		} else {
			a.say("returned to full tree")
		}
	case tree.CmdHoistClear:
		if projID := a.trees.projectIDForModel(tm); projID != "" {
			if len(tm.HoistStack) <= 1 && tm.HoistID() == projID {
				a.say("already at project outline")
				return
			}
			tm.HoistStack = []string{projID}
			delete(tm.Folded, projID)
			tm.Clamp()
			a.say("returned to project outline")
		} else {
			if !tm.ClearHoist() {
				a.say("already at full tree")
				return
			}
			a.say("returned to full tree")
		}
	}
	a.saveTreeFolded(kind, treeID, tm)
	if isWork {
		a.syncWorkTreeToTable(kind, tm)
	}
}

func (a *App) treeHoistCommand(cmdID string) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		c.ID = cmdID
		if a.Modal() == "outline" {
			return a.handleModal(s, c)
		}
		tm, kind, treeID, isWork, ok := a.activeTree(s)
		if !ok {
			a.say("no tree is focused")
			return core.Next(s)
		}
		a.runTreeHoist(tm, kind, treeID, isWork, c)
		return core.Next(s)
	}
}

// --- tree schema and location helpers -------------------------------------------

func summaryField(schema data.Schema) string {
	if schema.SummaryField != "" {
		return schema.SummaryField
	}
	for _, f := range []string{"title", "name"} {
		if _, ok := schema.Field(f); ok {
			return f
		}
	}
	return "title"
}

func primaryStatusColumn(schema data.Schema) (data.StatusColumn, bool) {
	for _, c := range schema.StatusColumns {
		if c.PrimaryRowStyle && len(c.Values) > 0 {
			return c, true
		}
	}
	if len(schema.StatusColumns) > 0 && len(schema.StatusColumns[0].Values) > 0 {
		return schema.StatusColumns[0], true
	}
	return data.StatusColumn{}, false
}

func (a *App) defaultEditPrefix(schema data.Schema) string {
	if !a.styleOptions().StatusIcons {
		return ""
	}
	if col, ok := primaryStatusColumn(schema); ok {
		icon, _, _, _ := views.StatusIndicator(schema, col.Name, col.Default)
		if icon != "" {
			return icon + " "
		}
	}
	return ""
}

func findTreeChildren(nodes []data.TreeNode, parentID string) ([]data.TreeNode, bool) {
	if parentID == "" {
		return nodes, true
	}
	for _, n := range nodes {
		if n.Record.ID == parentID {
			return n.Children, true
		}
		if kids, ok := findTreeChildren(n.Children, parentID); ok {
			return kids, true
		}
	}
	return nil, false
}

func (a *App) positionCreatedTreeRecord(nodes []data.TreeNode, created data.Record, res tree.EditResult, orderField string) {
	siblings, ok := findTreeChildren(nodes, res.ParentID)
	if !ok {
		return
	}
	others := make([]data.TreeNode, 0, len(siblings))
	for _, sib := range siblings {
		if sib.Record.ID == created.ID {
			continue
		}
		others = append(others, sib)
	}
	insertAt := len(others)
	switch {
	case res.BeforeID != "":
		for i, sib := range others {
			if sib.Record.ID == res.BeforeID {
				insertAt = i
				break
			}
		}
	case res.AfterID != "":
		for i, sib := range others {
			if sib.Record.ID == res.AfterID {
				insertAt = i + 1
				break
			}
		}
	case res.Kind == tree.EditAddAbove:
		insertAt = 0
	}
	ordered := make([]data.Record, 0, len(others)+1)
	for i := 0; i < insertAt; i++ {
		ordered = append(ordered, others[i].Record)
	}
	ordered = append(ordered, created)
	for i := insertAt; i < len(others); i++ {
		ordered = append(ordered, others[i].Record)
	}
	patches := make([]data.PatchInput, len(ordered))
	for i, rec := range ordered {
		patches[i] = data.PatchInput{Ref: rec.Ref(), Fields: map[string]any{orderField: int64(i + 1)}}
	}
	_, _ = a.Store.PatchMany(patches, "outline.order", fmt.Sprintf("order %s", views.Title(a.schema(created.Kind), created)), daemonSyncActor)
}

type treeNodeLoc struct {
	siblings    []data.TreeNode
	index       int
	parent      *data.TreeNode
	parentSibs  []data.TreeNode
	parentIndex int
	grandparent *data.TreeNode
}

func findTreeNodeLoc(nodes []data.TreeNode, id string, parent *data.TreeNode, parentSibs []data.TreeNode, parentIdx int, grandparent *data.TreeNode) (treeNodeLoc, bool) {
	for i := range nodes {
		if nodes[i].Record.ID == id {
			return treeNodeLoc{
				siblings:    nodes,
				index:       i,
				parent:      parent,
				parentSibs:  parentSibs,
				parentIndex: parentIdx,
				grandparent: grandparent,
			}, true
		}
		if loc, ok := findTreeNodeLoc(nodes[i].Children, id, &nodes[i], nodes, i, parent); ok {
			return loc, true
		}
	}
	return treeNodeLoc{}, false
}

func (a *App) activeTreeLoc(s core.State) (*tree.Model, string, string, data.Schema, treeNodeLoc, bool) {
	tm, kind, treeID, _, ok := a.activeTree(s)
	if !ok {
		return nil, "", "", data.Schema{}, treeNodeLoc{}, false
	}
	schema, nodes := a.syncTreeModel(tm, kind, treeID)
	row, has := tm.Focused()
	if !has || row.Draft || row.ID == "" {
		return tm, kind, treeID, schema, treeNodeLoc{}, false
	}
	loc, found := findTreeNodeLoc(nodes, row.ID, nil, nil, -1, nil)
	return tm, kind, treeID, schema, loc, found
}
