package app

import (
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
	if m.a.nav != nil && m.a.nav.filterTarget == outlineTree {
		return ModeFilter
	}
	if m.a.trees.editingOutline() {
		return ModeOutlineEdit
	}
	return ModeOutline
}

func (m *outlineModal) View(_, _ int) ui.ViewNode {
	panel := m.a.outlineTreeView()
	m.a.registerTreeFilterable(outlineTree, m.a.trees.outlineModel())
	ov := modal.Overlay(modal.OverlaySpec{
		ID:        "outline",
		Scope:     views.ScopeOutline,
		Title:     panel.Title,
		WidthPct:  64,
		HeightPct: 80,
		Badges:    panel.Badges,
		Facts:     panel.Facts,
		Children:  panel.Children,
	})
	ov.Filter = panel.Filter
	return ov
}

func (m *outlineModal) OnClose(modal.CloseReason) {
	if m.a.nav != nil && m.a.nav.filterTarget == outlineTree {
		m.a.nav.filterTarget = ""
		m.a.nav.filterSpec = nil
	}
	if m.a.trees.editingOutline() {
		m.a.trees.clearEdit()
	}
	m.a.clearCleanSlate(data.KindTask, outlineTree)
	if tm := m.a.trees.outlineModel(); tm != nil {
		tm.ClearVisual()
		tm.ClearHoist()
		tm.Filter("", "")
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
		if row.Node.Ref != nil && row.Node.Ref.ID != "" {
			ref = *row.Node.Ref
		} else if noteID, isNote := strings.CutPrefix(row.ID, threadNoteIDPrefix); isNote {
			ref = core.Ref{Kind: data.KindNote, ID: noteID}
		}
		m.a.afterModal(func(st core.State) core.State {
			return m.a.show(st, ref)
		})
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	case tree.CmdFoldToggle, tree.CmdFoldOpen, tree.CmdFoldClose, tree.CmdFoldOpenAll, tree.CmdFoldCloseAll:
		m.a.runTreeFold(m.a.trees.outlineModel(), data.KindTask, outlineTree, false, cmd.ID)
		return modal.Result{Handled: true}
	case tree.CmdHoistIn, tree.CmdHoistOut, tree.CmdHoistClear:
		m.a.runTreeHoist(m.a.trees.outlineModel(), data.KindTask, outlineTree, false, cmd)
		return modal.Result{Handled: true}
	case CmdOutlineCleanSlateExit:
		if m.a.exitCleanSlate(m.a.trees.outlineModel(), data.KindTask, outlineTree, false) {
			m.a.say("returned to full tree")
		} else {
			m.a.say("already at full tree")
		}
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
	case CmdOutlineEditNewline:
		m.a.runOutlineEditNewline()
		return modal.Result{Handled: true, Mode: m.Mode()}
	case CmdOutlineEditLinebreak:
		m.a.runOutlineEditLinebreak()
		return modal.Result{Handled: true, Mode: m.Mode()}
	}
	if strings.HasPrefix(cmd.ID, "motion.") {
		m.a.outlineTreeMove(cmd)
		if tm := m.a.trees.outlineModel(); tm != nil && tm.VisualActive {
			m.a.say("VISUAL · %d selected", len(tm.VisualIDs()))
		}
		return modal.Result{Handled: true}
	}
	if m.a.trees.editingOutline() && input.IsReadlineCommand(cmd.ID) {
		m.a.trees.outlineModel().HandleEditCommand(cmd)
		return modal.Result{Handled: true, Mode: m.Mode()}
	}
	return modal.Result{}
}

// --- inline editing -------------------------------------------------------------

func (a *App) runTreeStartEdit(tm *tree.Model, kind, treeID string, isWork bool, returnMode string, atEnd bool) bool {
	a.syncTreeModel(tm, kind, treeID)
	row, has := tm.Focused()
	if !has || row.Draft {
		a.say("no item selected")
		return false
	}
	if isThreadNoteRow(row) {
		if a.isTaskThreadLocked(threadNoteParentTaskID(row)) {
			a.refuseLockedThreadEdit()
			return false
		}
		a.say("press E to open note or r to reply")
		return false
	}
	if kind == data.KindTask && row.Node.Facts != nil {
		if hasNotes, _ := row.Node.Facts["hasNotes"].(bool); hasNotes && a.isTaskThreadLocked(row.ID) {
			a.refuseLockedThreadEdit()
			return false
		}
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
	a.say("inline edit · enter saves & adds · esc saves · shift+enter newline · ctrl+c discards")
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
	a.say("new %s · tab indents · shift+tab outdents · enter saves & adds · esc saves · ctrl+c discards", noun)
}

func (a *App) outlineStartEdit(atEnd bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		if a.Modal() == "outline" {
			return a.handleModal(s, c)
		}
		// The focused tree, else the page's (focus moves to it for the edit).
		ts, _ := a.treeTargetState(s)
		tm, kind, treeID, isWork, ok := a.activeTree(ts)
		if !ok {
			return core.Next(s)
		}
		if !a.runTreeStartEdit(tm, kind, treeID, isWork, ts.ActiveFrame().Mode, atEnd) {
			return core.Next(s)
		}
		return core.Next(withMode(ts, ModeOutlineEdit))
	}
}

func (a *App) outlineStartAdd(above bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		if a.Modal() == "outline" {
			return a.handleModal(s, c)
		}
		ts, _ := a.treeTargetState(s) // as outlineStartEdit
		tm, kind, treeID, isWork, ok := a.activeTree(ts)
		if !ok {
			return core.Next(s)
		}
		a.runTreeStartAdd(tm, kind, treeID, isWork, ts.ActiveFrame().Mode, above)
		return core.Next(withMode(ts, ModeOutlineEdit))
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
	treeID := outlineTree
	if meta.isWorkTree {
		treeID = workTable
	}
	if tm.EditIndent() {
		a.saveTreeFolded(meta.kind, treeID, tm)
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
	ed := tm.Edit()
	wasAddDraft := ed != nil && ed.Kind != tree.EditRename
	treeID := outlineTree
	if meta.isWorkTree {
		treeID = workTable
	}
	tm.CancelEdit()
	a.trees.editMeta = nil
	if wasAddDraft {
		if cs := a.activeCleanSlate(meta.kind, treeID); cs != nil && len(cs.roots) == 0 {
			a.exitCleanSlate(tm, meta.kind, treeID, meta.isWorkTree)
			a.say("discarded empty clean-slate outline")
			return ret, true
		}
	}
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
	treeID := outlineTree
	if meta.isWorkTree {
		treeID = workTable
	}

	if res.Kind == tree.EditRename {
		ref := core.Ref{Kind: meta.kind, ID: res.TargetID}
		_ = a.ensureShadow(ref)
		if title == "" {
			_, cs, err := a.Store.Archive(ref, User)
			if tm.HoistID() == res.TargetID {
				tm.Unhoist()
			}
			if state := a.activeCleanSlate(meta.kind, treeID); state != nil {
				delete(state.roots, res.TargetID)
				if len(state.roots) == 0 {
					a.clearCleanSlate(meta.kind, treeID)
				}
			}
			noun := strings.ToLower(schema.Title)
			if noun == "" {
				noun = meta.kind
			}
			a.wrote("removed empty "+noun, cs, err)
			a.syncTreeModel(tm, meta.kind, treeID)
			if meta.isWorkTree {
				a.syncWorkTreeToTable(meta.kind, tm)
			}
			return ret, true
		}
		_, cs, err := a.Store.Patch(data.PatchInput{
			Ref:     ref,
			Fields:  map[string]any{field: title},
			Summary: "rename " + title,
		}, User)
		a.syncTreeModel(tm, meta.kind, treeID)
		if meta.isWorkTree {
			a.syncWorkTreeToTable(meta.kind, tm)
		}
		a.wrote("renamed to "+title, cs, err)
		return ret, true
	}

	if title == "" {
		if cs := a.activeCleanSlate(meta.kind, treeID); cs != nil && len(cs.roots) == 0 {
			a.exitCleanSlate(tm, meta.kind, treeID, meta.isWorkTree)
			a.say("discarded empty clean-slate outline")
			return ret, true
		}
		a.say("")
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

	if res.ParentID == "" {
		a.addCleanSlateRoot(meta.kind, treeID, created.ID)
	}

	if h != nil && h.OrderField != "" {
		if nodes, terr := a.recordTreeNodes(meta.kind); terr == nil {
			a.positionCreatedTreeRecord(nodes, *created, res, h.OrderField)
		}
	}

	a.syncTreeModel(tm, meta.kind, treeID)
	tm.Reveal(created.ID)
	a.saveTreeFolded(meta.kind, treeID, tm)
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

func (a *App) runOutlineEditNewline() (string, bool) {
	tm, meta, ok := a.activeEditTree()
	if !ok {
		return "", false
	}
	if a.cfg != nil && a.cfg.Settings != nil && !a.cfg.Settings.Bool(SetOutlineEnterNewline, true) {
		return a.runOutlineEditCommit()
	}
	kind := meta.kind
	isWork := meta.isWorkTree
	treeID := outlineTree
	if isWork {
		treeID = workTable
	}
	ret := meta.returnMode
	if ret == "" {
		ret = ModeNormal
	}
	var prevParentID string
	var prevDepth int
	wasEmptyCleanSlate := false
	if ed := tm.Edit(); ed != nil {
		prevParentID = ed.ParentID
		prevDepth = ed.Depth
		if ed.Kind != tree.EditRename && strings.TrimSpace(ed.Value()) == "" {
			if cs := a.activeCleanSlate(kind, treeID); cs != nil && len(cs.roots) == 0 {
				wasEmptyCleanSlate = true
			}
		}
		if ed.Kind == tree.EditRename {
			if row, has := tm.Focused(); has {
				prevParentID = row.ParentID
				prevDepth = row.Depth
			}
		}
	}

	ret, ok = a.runOutlineEditCommit()
	if !ok {
		return "", false
	}
	if wasEmptyCleanSlate {
		return ret, true
	}
	a.runTreeStartAdd(tm, kind, treeID, isWork, ret, false)
	if prevParentID != "" {
		if ed := tm.Edit(); ed != nil && ed.ParentID != prevParentID {
			delete(tm.Folded, prevParentID)
			ed.ParentID = prevParentID
			ed.Depth = prevDepth
			ed.AfterID = ""
			ed.BeforeID = ""
			tm.Clamp()
		}
	}
	return ModeOutlineEdit, true
}

func (a *App) outlineEditNewline(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	if mode, ok := a.runOutlineEditNewline(); ok {
		return core.Next(withMode(s, mode))
	}
	return core.Next(s)
}

func (a *App) runOutlineEditLinebreak() {
	tm, _, ok := a.activeEditTree()
	if !ok {
		return
	}
	tm.EditInsertNewline()
}

func (a *App) outlineEditLinebreak(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	a.runOutlineEditLinebreak()
	return core.Next(s)
}

// --- structural mutations -------------------------------------------------------

func siblingOrderBase(sibs []data.TreeNode, orderField string) int64 {
	var minOrder int64
	for _, n := range sibs {
		v, ok := n.Record.Value(orderField)
		if !ok {
			continue
		}
		var num int64
		switch x := v.(type) {
		case int:
			num = int64(x)
		case int64:
			num = x
		case float64:
			num = int64(x)
		default:
			continue
		}
		if num > 0 && (minOrder == 0 || num < minOrder) {
			minOrder = num
		}
	}
	if minOrder <= 0 {
		return 1
	}
	return minOrder
}

func (a *App) outlineMoveSibling(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		tm, kind, treeID, schema, loc, ok := a.activeTreeLoc(s)
		if !ok {
			a.say("no item selected in tree")
			return core.Next(s)
		}
		if tm != nil && tm.Hoisted() && loc.siblings[loc.index].Record.ID == tm.HoistID() {
			a.say("cannot reorder hoisted root (- zooms out)")
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

		base := int64(1)
		if loc.parent == nil && a.isCleanSlateActive(kind, treeID) {
			base = siblingOrderBase(loc.siblings, h.OrderField)
		}
		patches := make([]data.PatchInput, len(reordered))
		for i, node := range reordered {
			patches[i] = data.PatchInput{
				Ref:    node.Record.Ref(),
				Fields: map[string]any{h.OrderField: base + int64(i)},
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
	if tm, kind, treeID, _, ok := a.activeTree(s); ok {
		schema, _ := a.syncTreeModel(tm, kind, treeID)
		if tm.VisualActive {
			if ids := tm.VisualIDs(); len(ids) > 0 {
				return a.outlineBatchIndent(s, tm, kind, treeID, schema, ids)
			}
		} else if len(tm.Marked) > 0 {
			if ids := tm.MarkedIDs(); len(ids) > 0 {
				return a.outlineBatchIndent(s, tm, kind, treeID, schema, ids)
			}
		}
	}
	tm, kind, treeID, schema, loc, ok := a.activeTreeLoc(s)
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
	if tm != nil && tm.Hoisted() && cur.Record.ID == tm.HoistID() {
		a.say("cannot indent hoisted root (- zooms out)")
		return core.Next(s)
	}
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
	if err == nil {
		if loc.parent == nil {
			if state := a.activeCleanSlate(kind, treeID); state != nil {
				delete(state.roots, cur.Record.ID)
			}
		}
		if tm != nil {
			delete(tm.Folded, prev.Record.ID)
			a.saveTreeFolded(kind, treeID, tm)
		}
	}
	a.wrote("indented "+a.title(cur.Record.Ref())+" under "+a.title(prev.Record.Ref()), cs, err)
	return core.Next(s)
}

func (a *App) outlineOutdent(s core.State, _ core.Command) core.StepResult {
	if tm, kind, treeID, _, ok := a.activeTree(s); ok {
		schema, _ := a.syncTreeModel(tm, kind, treeID)
		if tm.VisualActive {
			if ids := tm.VisualIDs(); len(ids) > 0 {
				return a.outlineBatchOutdent(s, tm, kind, treeID, schema, ids)
			}
		} else if len(tm.Marked) > 0 {
			if ids := tm.MarkedIDs(); len(ids) > 0 {
				return a.outlineBatchOutdent(s, tm, kind, treeID, schema, ids)
			}
		}
	}
	tm, kind, treeID, schema, loc, ok := a.activeTreeLoc(s)
	if !ok {
		a.say("no item selected in tree")
		return core.Next(s)
	}
	h := schema.EffectiveHierarchy()
	if h == nil || h.ParentField == "" {
		a.say("%s has no hierarchy", strings.ToLower(schema.Plural))
		return core.Next(s)
	}
	cur := loc.siblings[loc.index]
	if tm != nil && tm.Hoisted() {
		hoistID := tm.HoistID()
		if cur.Record.ID == hoistID || (loc.parent != nil && loc.parent.Record.ID == hoistID) {
			a.say("already at top of hoisted outline (- zooms out)")
			return core.Next(s)
		}
	}
	if loc.parent == nil {
		a.say("already at root")
		return core.Next(s)
	}
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

		base := int64(1)
		if newParent == nil && a.isCleanSlateActive(kind, treeID) {
			base = siblingOrderBase(loc.parentSibs, h.OrderField)
		}
		patches := make([]data.PatchInput, len(newSibs))
		for i, node := range newSibs {
			fields := map[string]any{h.OrderField: base + int64(i)}
			if node.Record.ID == cur.Record.ID {
				fields[h.ParentField] = newParent
			}
			patches[i] = data.PatchInput{Ref: node.Record.Ref(), Fields: fields}
		}
		cs, err := a.Store.PatchMany(patches, "outline.outdent", "outdent "+a.title(cur.Record.Ref())+" to "+where, User)
		if err == nil && newParent == nil {
			a.addCleanSlateRoot(kind, treeID, cur.Record.ID)
		}
		a.wrote("outdented "+a.title(cur.Record.Ref())+" to "+where, cs, err)
		return core.Next(s)
	}

	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     cur.Record.Ref(),
		Fields:  map[string]any{h.ParentField: newParent},
		Summary: "outdent " + a.title(cur.Record.Ref()) + " to " + where,
	}, User)
	if err == nil && newParent == nil {
		a.addCleanSlateRoot(kind, treeID, cur.Record.ID)
	}
	a.wrote("outdented "+a.title(cur.Record.Ref())+" to "+where, cs, err)
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
	if a.cfg != nil && a.cfg.Settings != nil {
		fm.ShowAllFields = !a.cfg.Settings.Bool(SetFormReactiveFields, true)
	}
	fm.FocusField(summaryField(schema))
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
			a.saveTreeFolded(kind, treeID, tm)
		} else if p, hasParent := rec.RefField(parentField); hasParent && p.Kind == kind && p.ID != "" {
			r := p
			parentRef = &r
		}

		fm := views.FormForRecordWith(schema, nil, a.editOptions())
		if parentRef != nil {
			fm.SetValue(parentField, *parentRef)
		}
		fm.Dirty = false
		if a.cfg != nil && a.cfg.Settings != nil {
			fm.ShowAllFields = !a.cfg.Settings.Bool(SetFormReactiveFields, true)
		}
		fm.FocusField(summaryField(schema))
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
