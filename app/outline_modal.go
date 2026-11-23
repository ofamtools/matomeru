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

// --- inline editing -------------------------------------------------------------

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

// --- structural mutations -------------------------------------------------------

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
