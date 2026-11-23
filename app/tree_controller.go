package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/tree"
)

type treeEditMeta struct {
	kind       string
	returnMode string
	isWorkTree bool
}

// treeController owns the universal schema-driven tree state for 2 Work
// (per-kind tree models and layout overrides) and the T Outline overlay,
// sharing per-kind fold maps so folding in one reflects in the other.
type treeController struct {
	work           map[string]*tree.Model
	outline        *tree.Model
	folded         map[string]map[string]bool
	layoutOverride map[string]string
	workTableRowID map[string]string
	editMeta       *treeEditMeta
}

func newTreeController() *treeController {
	return &treeController{
		work:           map[string]*tree.Model{},
		folded:         map[string]map[string]bool{},
		layoutOverride: map[string]string{},
		workTableRowID: map[string]string{},
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

func (tc *treeController) clearEdit() {
	if tc == nil || tc.editMeta == nil {
		return
	}
	if tc.editMeta.isWorkTree {
		if tm := tc.work[tc.editMeta.kind]; tm != nil {
			tm.CancelEdit()
		}
	} else if tc.outline != nil {
		tc.outline.CancelEdit()
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
	if a.Settings == nil {
		return 5
	}
	return max(0, a.Settings.Int(SetScrollOff, 5))
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
	if s.ActivePage != SpaceWork || a.Modal() != "" || a.histPos >= 0 {
		return false
	}
	if a.workLayoutFor(a.workKind()) != WorkTasksLayoutTree {
		return false
	}
	if tbl := a.tables[workTable]; tbl != nil && (tbl.Query != "" || tbl.SortField != "" || tbl.GroupField != "") {
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
		return data.BuildTree(recs, parentField, orderField, a.showArchived), nil
	}
	return a.Store.RecordTree(kind, a.showArchived)
}

func (a *App) syncTreeModel(tm *tree.Model, kind, treeID string) (data.Schema, []data.TreeNode) {
	schema := a.schema(kind)
	nodes, err := a.recordTreeNodes(kind)
	if err != nil {
		a.fail(kind, err)
	}
	opts := views.TreeOptions{
		Style:         a.styleOptions(),
		GapTopLevel:   a.Settings == nil || a.Settings.Bool(SetTreeTopLevelGap, true),
		Guides:        a.Settings == nil || a.Settings.Bool(SetTreeGuides, true),
		ShowFoldCount: true,
		ResolveRef:    a.title,
		EnrichNode: func(rec data.Record, nd *tree.Node) {
			if a.openable(rec) {
				if nd.Facts == nil {
					nd.Facts = map[string]any{}
				}
				nd.Facts[factOpenable] = true
			}
		},
	}
	built := views.TreeFromNodes(schema, nodes, opts)
	tm.Folded = a.trees.folded[kind]
	tm.GapTopLevel = opts.GapTopLevel
	tm.Guides = opts.Guides
	tm.ShowFoldCount = opts.ShowFoldCount
	tm.ScrollOff = a.scrollOff()
	tm.PageSize = a.treeViewportHeight(treeID)
	tm.SetNodes(built.Nodes)
	a.trees.folded[kind] = tm.Folded
	return schema, nodes
}

func (a *App) syncWorkTreeToTable(kind string, tm *tree.Model) {
	if tbl := a.tables[workTable]; tbl != nil && tm != nil && tm.SelectedID != "" && a.workKind() == kind {
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

	if row, ok := tbl.FocusedRow(); ok && row.ID != "" && row.Ref != nil && row.Ref.Kind == kind {
		prevSynced := a.trees.workTableRowID[kind]
		if (prevSynced != "" && row.ID != prevSynced) || tm.SelectedID == "" {
			tm.SelectedID = row.Ref.ID
			a.trees.workTableRowID[kind] = row.ID
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
	if a.showArchived {
		extra = append(extra, ui.Badge{Text: "archived", Tone: "muted"})
	}
	return views.TreeView(schema, tm, SpaceWork, workTable, views.ScopeIndex, extra...)
}

func (a *App) outlineTreeView() ui.ViewNode {
	tm := a.trees.outlineModel()
	schema, _ := a.syncTreeModel(tm, data.KindTask, outlineTree)
	var extra []ui.Badge
	if a.showArchived {
		extra = append(extra, ui.Badge{Text: "archived", Tone: "muted"})
	}
	panel := views.TreeView(schema, tm, "outline", outlineTree, views.ScopeOutline, extra...)
	panel.Title = "Outline"
	return panel
}

// outlineModal implements modal.Modal for the T task outline overlay.
type outlineModal struct {
	a *App
}

var _ modal.Modal = (*outlineModal)(nil)

func (a *App) newOutlineModal() *outlineModal {
	return &outlineModal{a: a}
}

func (a *App) activeOutline() *outlineModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("outline").(*outlineModal)
	return m
}

func (m *outlineModal) ID() string      { return "outline" }
func (m *outlineModal) Scope() string   { return views.ScopeOutline }
func (m *outlineModal) FocusID() string { return outlineTree }

func (m *outlineModal) Mode() string {
	if m.a.trees.editingOutline() {
		return ModeOutlineEdit
	}
	return ModeOutline
}

func (m *outlineModal) View(_, _ int) ui.ViewNode {
	panel := m.a.outlineTreeView()
	return modal.Overlay(modal.OverlaySpec{
		ID:        "outline",
		Scope:     views.ScopeOutline,
		Title:     panel.Title,
		WidthPct:  64,
		HeightPct: 80,
		Badges:    panel.Badges,
		Facts:     panel.Facts,
		Children:  panel.Children,
	})
}

func (m *outlineModal) OnClose(modal.CloseReason) {
	if m.a.trees.editingOutline() {
		m.a.trees.clearEdit()
	}
}

func (m *outlineModal) Handle(cmd core.Command) modal.Result {
	switch cmd.ID {
	case modal.CmdCancel:
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel}
	case CmdOutlineJump:
		tm := m.a.trees.outlineModel()
		m.a.syncTreeModel(tm, data.KindTask, outlineTree)
		row, ok := tm.Focused()
		if !ok || row.Draft || row.ID == "" {
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		}
		ref := core.Ref{Kind: data.KindTask, ID: row.ID}
		m.a.afterModal(func(st core.State) core.State {
			return m.a.show(st, ref)
		})
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	case tree.CmdFoldToggle, tree.CmdFoldOpen, tree.CmdFoldClose, tree.CmdFoldOpenAll, tree.CmdFoldCloseAll:
		m.a.runTreeFold(m.a.trees.outlineModel(), data.KindTask, outlineTree, false, cmd.ID)
		return modal.Result{Handled: true}
	case CmdOutlineEditStart:
		m.a.runTreeStartEdit(m.a.trees.outlineModel(), data.KindTask, outlineTree, false, ModeOutline, false)
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditEnd:
		m.a.runTreeStartEdit(m.a.trees.outlineModel(), data.KindTask, outlineTree, false, ModeOutline, true)
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineAddBelow:
		m.a.runTreeStartAdd(m.a.trees.outlineModel(), data.KindTask, outlineTree, false, ModeOutline, false)
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineAddAbove:
		m.a.runTreeStartAdd(m.a.trees.outlineModel(), data.KindTask, outlineTree, false, ModeOutline, true)
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditInsert:
		if tm, _, ok := m.a.activeEditTree(); ok {
			tm.HandleEditCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": cmd.Arg("text")}})
		}
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditBackspace:
		if tm, _, ok := m.a.activeEditTree(); ok {
			tm.HandleEditCommand(core.Command{ID: input.CmdBackspace})
		}
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditIndent:
		m.a.runOutlineEditIndent()
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditOutdent:
		m.a.runOutlineEditOutdent()
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditCancel:
		m.a.runOutlineEditCancel()
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditCommit:
		m.a.runOutlineEditCommit()
		return modal.Result{Handled: true, Mode: m.Mode()}
	}
	if strings.HasPrefix(cmd.ID, "motion.") {
		m.a.outlineTreeMove(cmd)
		return modal.Result{Handled: true}
	}
	if m.a.trees.editingOutline() && input.IsReadlineCommand(cmd.ID) {
		m.a.trees.outlineModel().HandleEditCommand(cmd)
		return modal.Result{Handled: true, Mode: m.Mode()}
	}
	return modal.Result{}
}

func (a *App) workTreeMove(c core.Command) {
	kind := a.workKind()
	tm := a.trees.workModel(kind)
	a.syncTreeModel(tm, kind, workTable)
	tm.HandleCommand(c)
	a.trees.folded[kind] = tm.Folded
	a.syncWorkTreeToTable(kind, tm)
}

func (a *App) outlineTreeMove(c core.Command) {
	tm := a.trees.outlineModel()
	a.syncTreeModel(tm, data.KindTask, outlineTree)
	tm.HandleCommand(c)
	a.trees.folded[data.KindTask] = tm.Folded
}

func (a *App) activeTree(s core.State) (tm *tree.Model, kind, treeID string, isWorkTree bool, ok bool) {
	if a.Modal() == "outline" {
		return a.trees.outlineModel(), data.KindTask, outlineTree, false, true
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
	if tbl := a.tables[workTable]; tbl != nil {
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
		a.say("folded all")
	}
	a.trees.folded[kind] = tm.Folded
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

// --- inline editing -------------------------------------------------------------

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

func (a *App) runTreeStartEdit(tm *tree.Model, kind, treeID string, isWork bool, returnMode string, atEnd bool) bool {
	a.syncTreeModel(tm, kind, treeID)
	row, has := tm.Focused()
	if !has || row.Draft {
		a.say("no item selected")
		return false
	}
	prefix := ""
	if row.Node.Icon != "" {
		prefix = row.Node.Icon + " "
	}
	if !tm.StartRename(prefix) {
		a.say("no item selected")
		return false
	}
	if !atEnd {
		tm.HandleEditCommand(core.Command{ID: input.CmdLineStart})
	}
	a.trees.editMeta = &treeEditMeta{
		kind:       kind,
		returnMode: returnMode,
		isWorkTree: isWork,
	}
	a.say("inline edit · enter saves · esc cancels")
	return true
}

func (a *App) runTreeStartAdd(tm *tree.Model, kind, treeID string, isWork bool, returnMode string, above bool) {
	schema, _ := a.syncTreeModel(tm, kind, treeID)
	editKind := tree.EditAddBelow
	if above {
		editKind = tree.EditAddAbove
	}
	tm.StartAdd(editKind, a.defaultEditPrefix(schema))
	a.trees.editMeta = &treeEditMeta{
		kind:       kind,
		returnMode: returnMode,
		isWorkTree: isWork,
	}
	noun := strings.ToLower(schema.Title)
	if noun == "" {
		noun = kind
	}
	a.say("new %s · tab indents · shift+tab outdents · enter saves · esc cancels", noun)
}

func (a *App) outlineStartEdit(atEnd bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		if a.Modal() == "outline" {
			return a.handleModal(s, c)
		}
		tm, kind, treeID, isWork, ok := a.activeTree(s)
		if !ok {
			a.say("no tree is focused")
			return core.Next(s)
		}
		if !a.runTreeStartEdit(tm, kind, treeID, isWork, s.ActiveFrame().Mode, atEnd) {
			return core.Next(s)
		}
		return core.Next(withMode(s, ModeOutlineEdit))
	}
}

func (a *App) outlineStartAdd(above bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		if a.Modal() == "outline" {
			return a.handleModal(s, c)
		}
		tm, kind, treeID, isWork, ok := a.activeTree(s)
		if !ok {
			a.say("no tree is focused")
			return core.Next(s)
		}
		a.runTreeStartAdd(tm, kind, treeID, isWork, s.ActiveFrame().Mode, above)
		return core.Next(withMode(s, ModeOutlineEdit))
	}
}

func (a *App) outlineEditInsert(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	tm, _, ok := a.activeEditTree()
	if !ok {
		return core.Next(s)
	}
	tm.HandleEditCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": c.Arg("text")}})
	return core.Next(s)
}

func (a *App) outlineEditBackspace(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	tm, _, ok := a.activeEditTree()
	if !ok {
		return core.Next(s)
	}
	tm.HandleEditCommand(core.Command{ID: input.CmdBackspace})
	return core.Next(s)
}

func (a *App) runOutlineEditIndent() {
	tm, meta, ok := a.activeEditTree()
	if !ok {
		return
	}
	if tm.EditIndent() {
		a.trees.folded[meta.kind] = tm.Folded
		if ed := tm.Edit(); ed != nil && ed.ParentID != "" {
			a.say("indent under %s · enter saves", a.title(core.Ref{Kind: meta.kind, ID: ed.ParentID}))
		}
	} else {
		a.say("no item above to indent under")
	}
}

func (a *App) outlineEditIndent(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	a.runOutlineEditIndent()
	return core.Next(s)
}

func (a *App) runOutlineEditOutdent() {
	tm, meta, ok := a.activeEditTree()
	if !ok {
		return
	}
	if tm.EditOutdent() {
		if ed := tm.Edit(); ed != nil && ed.ParentID != "" {
			a.say("outdented under %s", a.title(core.Ref{Kind: meta.kind, ID: ed.ParentID}))
		} else {
			a.say("outdented to top level")
		}
	} else {
		a.say("already at top level")
	}
}

func (a *App) outlineEditOutdent(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	a.runOutlineEditOutdent()
	return core.Next(s)
}

func (a *App) runOutlineEditCancel() (string, bool) {
	tm, meta, ok := a.activeEditTree()
	if !ok {
		return "", false
	}
	ret := meta.returnMode
	if ret == "" {
		ret = ModeNormal
	}
	tm.CancelEdit()
	a.trees.editMeta = nil
	a.say("")
	return ret, true
}

func (a *App) outlineEditCancel(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	if ret, ok := a.runOutlineEditCancel(); ok {
		return core.Next(withMode(s, ret))
	}
	return core.Next(s)
}

func (a *App) runOutlineEditCommit() (string, bool) {
	tm, meta, ok := a.activeEditTree()
	if !ok {
		return "", false
	}
	ret := meta.returnMode
	if ret == "" {
		ret = ModeNormal
	}
	res, committed := tm.CommitEdit()
	a.trees.editMeta = nil
	if !committed {
		return ret, true
	}

	title := strings.TrimSpace(res.Value)
	schema := a.schema(meta.kind)
	field := summaryField(schema)

	if res.Kind == tree.EditRename {
		if title == "" {
			a.say("title cannot be empty · cancelled")
			return ret, true
		}
		ref := core.Ref{Kind: meta.kind, ID: res.TargetID}
		_ = a.ensureShadow(ref)
		_, cs, err := a.Store.Patch(data.PatchInput{
			Ref:     ref,
			Fields:  map[string]any{field: title},
			Summary: "rename " + title,
		}, User)
		a.wrote("renamed to "+title, cs, err)
		return ret, true
	}

	if title == "" {
		a.say("cancelled")
		return ret, true
	}

	h := schema.EffectiveHierarchy()
	fields := map[string]any{field: title}
	if res.ParentID != "" && h != nil && h.ParentField != "" {
		fields[h.ParentField] = core.Ref{Kind: meta.kind, ID: res.ParentID}
	}
	created, cs, err := a.Store.Create(data.CreateInput{Kind: meta.kind, Fields: fields}, User)
	if err != nil {
		a.fail("new "+strings.ToLower(schema.Title), err)
		return ret, true
	}

	if h != nil && h.OrderField != "" {
		if nodes, terr := a.recordTreeNodes(meta.kind); terr == nil {
			a.positionCreatedTreeRecord(nodes, *created, res, h.OrderField)
		}
	}

	treeID := outlineTree
	if meta.isWorkTree {
		treeID = workTable
	}
	a.syncTreeModel(tm, meta.kind, treeID)
	tm.Reveal(created.ID)
	a.trees.folded[meta.kind] = tm.Folded
	if meta.isWorkTree {
		a.syncWorkTreeToTable(meta.kind, tm)
	}
	a.wrote("created "+title, cs, nil)
	return ret, true
}

func (a *App) outlineEditCommit(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	if ret, ok := a.runOutlineEditCommit(); ok {
		return core.Next(withMode(s, ret))
	}
	return core.Next(s)
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

// --- structural mutations -------------------------------------------------------

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

func (a *App) activeTreeLoc(s core.State) (*tree.Model, string, data.Schema, treeNodeLoc, bool) {
	tm, kind, treeID, _, ok := a.activeTree(s)
	if !ok {
		return nil, "", data.Schema{}, treeNodeLoc{}, false
	}
	schema, nodes := a.syncTreeModel(tm, kind, treeID)
	row, has := tm.Focused()
	if !has || row.Draft || row.ID == "" {
		return tm, kind, schema, treeNodeLoc{}, false
	}
	loc, found := findTreeNodeLoc(nodes, row.ID, nil, nil, -1, nil)
	return tm, kind, schema, loc, found
}

func (a *App) outlineMoveSibling(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		_, _, schema, loc, ok := a.activeTreeLoc(s)
		if !ok {
			a.say("no item selected in tree")
			return core.Next(s)
		}
		h := schema.EffectiveHierarchy()
		if h == nil || h.OrderField == "" {
			a.say("%s does not support sibling ordering", strings.ToLower(schema.Plural))
			return core.Next(s)
		}
		n := len(loc.siblings)
		if n < 2 {
			a.say("no siblings to reorder")
			return core.Next(s)
		}
		target := min(max(loc.index+delta*c.Times(), 0), n-1)
		if target == loc.index {
			a.say("already at edge")
			return core.Next(s)
		}
		reordered := make([]data.TreeNode, 0, n)
		cur := loc.siblings[loc.index]
		without := slices.Delete(slices.Clone(loc.siblings), loc.index, loc.index+1)
		reordered = append(reordered, without[:target]...)
		reordered = append(reordered, cur)
		reordered = append(reordered, without[target:]...)

		patches := make([]data.PatchInput, len(reordered))
		for i, node := range reordered {
			patches[i] = data.PatchInput{
				Ref:    node.Record.Ref(),
				Fields: map[string]any{h.OrderField: int64(i + 1)},
			}
		}
		dir := "down"
		if delta < 0 {
			dir = "up"
		}
		cs, err := a.Store.PatchMany(patches, "outline.reorder", "move "+a.title(cur.Record.Ref())+" "+dir, User)
		a.wrote("moved "+a.title(cur.Record.Ref())+" "+dir, cs, err)
		return core.Next(s)
	}
}

func (a *App) outlineIndent(s core.State, _ core.Command) core.StepResult {
	tm, kind, schema, loc, ok := a.activeTreeLoc(s)
	if !ok {
		a.say("no item selected in tree")
		return core.Next(s)
	}
	h := schema.EffectiveHierarchy()
	if h == nil || h.ParentField == "" {
		a.say("%s has no hierarchy", strings.ToLower(schema.Plural))
		return core.Next(s)
	}
	if loc.index == 0 {
		a.say("no preceding sibling to indent under")
		return core.Next(s)
	}
	cur := loc.siblings[loc.index]
	prev := loc.siblings[loc.index-1]
	fields := map[string]any{h.ParentField: prev.Record.Ref()}
	if h.OrderField != "" {
		fields[h.OrderField] = int64(len(prev.Children) + 1)
	}
	_ = a.ensureShadow(cur.Record.Ref())
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     cur.Record.Ref(),
		Fields:  fields,
		Summary: "indent " + a.title(cur.Record.Ref()) + " under " + a.title(prev.Record.Ref()),
	}, User)
	if err == nil && tm != nil {
		delete(tm.Folded, prev.Record.ID)
		a.trees.folded[kind] = tm.Folded
	}
	a.wrote("indented "+a.title(cur.Record.Ref())+" under "+a.title(prev.Record.Ref()), cs, err)
	return core.Next(s)
}

func (a *App) outlineOutdent(s core.State, _ core.Command) core.StepResult {
	_, _, schema, loc, ok := a.activeTreeLoc(s)
	if !ok {
		a.say("no item selected in tree")
		return core.Next(s)
	}
	h := schema.EffectiveHierarchy()
	if h == nil || h.ParentField == "" {
		a.say("%s has no hierarchy", strings.ToLower(schema.Plural))
		return core.Next(s)
	}
	if loc.parent == nil {
		a.say("already at root")
		return core.Next(s)
	}
	cur := loc.siblings[loc.index]
	var newParent any // nil removes parent (promotes to root)
	where := "root"
	if loc.grandparent != nil {
		newParent = loc.grandparent.Record.Ref()
		where = a.title(loc.grandparent.Record.Ref())
	}

	_ = a.ensureShadow(cur.Record.Ref())
	if h.OrderField != "" {
		insertAt := loc.parentIndex + 1
		newSibs := make([]data.TreeNode, 0, len(loc.parentSibs)+1)
		newSibs = append(newSibs, loc.parentSibs[:insertAt]...)
		newSibs = append(newSibs, cur)
		newSibs = append(newSibs, loc.parentSibs[insertAt:]...)

		patches := make([]data.PatchInput, len(newSibs))
		for i, node := range newSibs {
			fields := map[string]any{h.OrderField: int64(i + 1)}
			if node.Record.ID == cur.Record.ID {
				fields[h.ParentField] = newParent
			}
			patches[i] = data.PatchInput{Ref: node.Record.Ref(), Fields: fields}
		}
		cs, err := a.Store.PatchMany(patches, "outline.outdent", "outdent "+a.title(cur.Record.Ref())+" to "+where, User)
		a.wrote("outdented "+a.title(cur.Record.Ref())+" to "+where, cs, err)
		return core.Next(s)
	}

	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     cur.Record.Ref(),
		Fields:  map[string]any{h.ParentField: newParent},
		Summary: "outdent " + a.title(cur.Record.Ref()) + " to " + where,
	}, User)
	a.wrote("outdented "+a.title(cur.Record.Ref())+" to "+where, cs, err)
	return core.Next(s)
}

func (a *App) outlineStatusCycle(s core.State, _ core.Command) core.StepResult {
	tm, kind, treeID, _, ok := a.activeTree(s)
	if !ok {
		a.say("no item selected in tree")
		return core.Next(s)
	}
	schema, _ := a.syncTreeModel(tm, kind, treeID)
	row, has := tm.Focused()
	if !has || row.Draft || row.ID == "" {
		a.say("no item selected in tree")
		return core.Next(s)
	}
	col, hasCol := primaryStatusColumn(schema)
	if !hasCol {
		a.say("%s has no status column to cycle", strings.ToLower(schema.Title))
		return core.Next(s)
	}
	ref := core.Ref{Kind: kind, ID: row.ID}
	_ = a.ensureShadow(ref)
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("status", err)
		return core.Next(s)
	}
	names := col.Names()
	cur := rec.Text(col.Name)
	idx := slices.Index(names, cur)
	nextStatus := names[(idx+1)%len(names)]
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  map[string]any{col.Name: nextStatus},
		Summary: a.title(ref) + " " + col.Name + " -> " + nextStatus,
	}, User)
	a.wrote(a.title(ref)+" -> "+nextStatus, cs, err)
	return core.Next(s)
}

func (a *App) outlineRename(s core.State, _ core.Command) core.StepResult {
	tm, kind, treeID, _, ok := a.activeTree(s)
	if !ok {
		a.say("no item selected in tree")
		return core.Next(s)
	}
	schema, _ := a.syncTreeModel(tm, kind, treeID)
	row, has := tm.Focused()
	if !has || row.Draft || row.ID == "" {
		a.say("no item selected in tree")
		return core.Next(s)
	}
	ref := core.Ref{Kind: kind, ID: row.ID}
	_ = a.ensureShadow(ref)
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("rename", err)
		return core.Next(s)
	}
	fm := views.FormForRecordWith(schema, rec, a.editOptions())
	field := summaryField(schema)
	for i, sp := range fm.Specs {
		if sp.Name == field {
			fm.ActiveIdx = i
			break
		}
	}
	_, _ = fm.HandleCommand(core.Command{ID: form.CmdEdit})
	rf := a.newRecordFormModal(kind, &ref, nil, fm)
	a.say("rename %s · ctrl+s saves · esc closes", a.title(ref))
	return core.Next(a.pushModal(s, rf))
}

// taskAddRelative implements the kept task.add.sibling and task.add.child verbs
// (opening the full record form with parent prefilled) for any active tree noun.
func (a *App) taskAddRelative(child bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		tm, kind, treeID, _, ok := a.activeTree(s)
		if !ok {
			a.say("no item selected")
			return core.Next(s)
		}
		schema, _ := a.syncTreeModel(tm, kind, treeID)
		row, has := tm.Focused()
		if !has || row.Draft || row.ID == "" {
			a.say("no item selected")
			return core.Next(s)
		}
		ref := core.Ref{Kind: kind, ID: row.ID}
		rec, err := a.Store.Get(ref)
		if err != nil {
			a.fail("add "+strings.ToLower(schema.Title), err)
			return core.Next(s)
		}
		h := schema.EffectiveHierarchy()
		parentField := "parent"
		if h != nil && h.ParentField != "" {
			parentField = h.ParentField
		}
		var parentRef *core.Ref
		if child {
			r := ref
			parentRef = &r
			delete(tm.Folded, row.ID)
			a.trees.folded[kind] = tm.Folded
		} else if p, hasParent := rec.RefField(parentField); hasParent && p.Kind == kind && p.ID != "" {
			r := p
			parentRef = &r
		}

		fm := views.FormForRecordWith(schema, nil, a.editOptions())
		if parentRef != nil {
			fm.SetValue(parentField, *parentRef)
		}
		fm.Dirty = false
		field := summaryField(schema)
		for i, sp := range fm.Specs {
			if sp.Name == field {
				fm.ActiveIdx = i
				break
			}
		}
		rf := a.newRecordFormModal(kind, nil, nil, fm)
		noun := strings.ToLower(schema.Title)
		if child {
			a.say("new child %s under %s · i types · ctrl+s creates · esc closes", noun, a.title(ref))
		} else if parentRef != nil {
			a.say("new sibling %s under %s · i types · ctrl+s creates · esc closes", noun, a.title(*parentRef))
		} else {
			a.say("new root %s · i types · ctrl+s creates · esc closes", noun)
		}
		return core.Next(a.pushModal(s, rf))
	}
}
