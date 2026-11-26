package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

// activeSelectionTree resolves the currently focused tree widget across the
// Outline modal, embedded Project Page outline, 2 Work tree, and Project Page
// section trees.
func (a *App) activeSelectionTree(s core.State) (tm *tree.Model, kind, treeID string, isWork, syncable, ok bool) {
	if a.Modal() == "outline" {
		return a.trees.outlineModel(), data.KindTask, outlineTree, false, true, true
	}
	if a.Modal() != "" {
		return nil, "", "", false, false, false
	}
	if a.inProjectOutlineTree(&s) {
		if projID := a.activeProjectOutlineID(s); projID != "" {
			return a.trees.projectModel(projID), data.KindTask, projectOutlineTree, false, true, true
		}
	}
	focus := s.ActiveFrame().FocusID
	if focus == workTable && a.inWorkTree(s) {
		k := a.workKind()
		return a.trees.workModel(k), k, workTable, true, true, true
	}
	if tm, ok := a.projectSectionTree(focus); ok {
		k := data.KindPane
		if focus == projectLanesTree {
			k = data.KindLane
		}
		return tm, k, focus, false, false, true
	}
	return nil, "", "", false, false, false
}

// activeSelectionTable resolves the currently focused table widget when no
// modal is open and focus is not in a tree view.
func (a *App) activeSelectionTable(s core.State) (*table.Model, string, bool) {
	if a.Modal() != "" {
		return nil, "", false
	}
	focus := s.ActiveFrame().FocusID
	if focus == workTable && a.inWorkTree(s) {
		return nil, "", false
	}
	if tbl := a.nav.tables[focus]; tbl != nil {
		return tbl, focus, true
	}
	return nil, "", false
}

func (a *App) reportSelectStatus(visualActive bool, visualCount, markedCount int) {
	switch {
	case visualActive:
		a.say("VISUAL · %d selected", visualCount)
	case markedCount > 0:
		a.say("%d marked", markedCount)
	default:
		a.say("selection cleared")
	}
}

// selectCommand dispatches all traits.SelectCmd* commands (and the kept
// table.mark.toggle verb) to the focused tree or table widget, else to the
// first one drawn on the page, moving focus there so the keys that follow
// extend the selection. With none it does nothing (the keys are hidden by
// facts.selectTarget).
func (a *App) selectCommand(s core.State, c core.Command) core.StepResult {
	if c.ID == CmdMarkToggle {
		c.ID = traits.SelectCmdMarkToggle
	}
	ts, _ := a.selectionTargetState(s)
	if tm, kind, treeID, _, syncable, ok := a.activeSelectionTree(ts); ok {
		if syncable {
			a.syncTreeModel(tm, kind, treeID)
		}
		tm.HandleCommand(c)
		a.reportSelectStatus(tm.VisualActive, len(tm.VisualIDs()), len(tm.Marked))
		return core.Next(ts)
	}
	if tbl, _, ok := a.activeSelectionTable(ts); ok {
		tbl.HandleCommand(c)
		a.reportSelectStatus(tbl.VisualActive, len(tbl.VisualRows()), len(tbl.Marked))
		return core.Next(ts)
	}
	return core.Next(s)
}

// syncVisualStatus refreshes the status line count while moving in visual mode.
func (a *App) syncVisualStatus(s core.State) {
	if tm, _, _, _, _, ok := a.activeSelectionTree(s); ok && tm.VisualActive {
		a.say("VISUAL · %d selected", len(tm.VisualIDs()))
		return
	}
	if tbl, _, ok := a.activeSelectionTable(s); ok && tbl.VisualActive {
		a.say("VISUAL · %d selected", len(tbl.VisualRows()))
	}
}

// clearActiveVisual exits visual range mode on the focused tree or table after
// applying an operator. Explicit marks are preserved until cleared by the user.
func (a *App) clearActiveVisual(s core.State) {
	if tm, _, _, _, _, ok := a.activeSelectionTree(s); ok && tm.VisualActive {
		tm.ClearVisual()
		return
	}
	if tbl, _, ok := a.activeSelectionTable(s); ok && tbl.VisualActive {
		tbl.ClearVisual()
	}
}

func treeRefIndex(nodes []tree.Node, out map[string]core.Ref) {
	for _, nd := range nodes {
		if nd.ID != "" && nd.Ref != nil && nd.Ref.ID != "" {
			out[nd.ID] = *nd.Ref
		}
		if len(nd.Children) > 0 {
			treeRefIndex(nd.Children, out)
		}
	}
}

func treeRefsForIDs(nodes []tree.Node, kind string, ids []string) []core.Ref {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]core.Ref, len(ids)*2)
	treeRefIndex(nodes, byID)
	out := make([]core.Ref, 0, len(ids))
	seen := make(map[core.Ref]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		ref, ok := byID[id]
		if !ok {
			if noteID, isNote := strings.CutPrefix(id, threadNoteIDPrefix); isNote && noteID != "" {
				ref = core.Ref{Kind: data.KindNote, ID: noteID}
			} else if kind != "" {
				ref = core.Ref{Kind: kind, ID: id}
			}
		}
		if ref.Kind != "" && ref.ID != "" && !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

// targetRefs resolves the records a command acts on:
//  1. explicit programmatic/CLI/MCP c.Target (keybinding auto-targets have
//     Source prefixed with "key:" and yield to active visual/marked selection),
//  2. active visual range on the focused tree or table,
//  3. marked entries on the focused tree or table,
//  4. fallback to the single focused subject.
func (a *App) targetRefs(s core.State, c core.Command) ([]core.Ref, bool) {
	if c.Target != nil && c.Target.ID != "" && !strings.HasPrefix(c.Source, "key:") {
		return []core.Ref{*c.Target}, false
	}
	if tm, kind, treeID, _, syncable, ok := a.activeSelectionTree(s); ok {
		if syncable {
			a.syncTreeModel(tm, kind, treeID)
		}
		if tm.VisualActive {
			if refs := treeRefsForIDs(tm.Nodes, kind, tm.VisualIDs()); len(refs) > 0 {
				return refs, true
			}
		}
		if len(tm.Marked) > 0 {
			if refs := treeRefsForIDs(tm.Nodes, kind, tm.MarkedIDs()); len(refs) > 0 {
				return refs, true
			}
		}
	}
	if tbl, _, ok := a.activeSelectionTable(s); ok {
		if tbl.VisualActive {
			var refs []core.Ref
			for _, row := range tbl.VisualRows() {
				if row.Ref != nil && row.Ref.ID != "" {
					refs = append(refs, *row.Ref)
				}
			}
			if len(refs) > 0 {
				return refs, true
			}
		}
		if len(tbl.Marked) > 0 {
			var refs []core.Ref
			for _, row := range tbl.MarkedRows() {
				if row.Ref != nil && row.Ref.ID != "" {
					refs = append(refs, *row.Ref)
				}
			}
			if len(refs) > 0 {
				return refs, true
			}
		}
	}
	if ref, ok := a.subject(s, c); ok {
		return []core.Ref{ref}, false
	}
	return nil, false
}

// --- generalized schema-driven status cycling -----------------------------------

// statusCycle cycles the primary status field declared by each targeted record's
// schema (workStatus, laneStatus, status, etc.) with zero per-noun switches.
func (a *App) statusCycle(s core.State, c core.Command) core.StepResult {
	refs, fromSel := a.targetRefs(s, c)
	if len(refs) == 0 {
		a.say("nothing here to cycle status")
		return core.Next(s)
	}
	if fromSel {
		a.clearActiveVisual(s)
	}
	if len(refs) == 1 {
		ref := refs[0]
		schema := a.schema(ref.Kind)
		fieldName, values, ok := schema.PrimaryStatusField()
		if !ok {
			noun := strings.ToLower(schema.Title)
			if noun == "" {
				noun = ref.Kind
			}
			a.say("%s has no status column to cycle", noun)
			return core.Next(s)
		}
		_ = a.ensureShadow(ref)
		rec, err := a.Store.Get(ref)
		if err != nil {
			a.fail("status", err)
			return core.Next(s)
		}
		idx := slices.Index(values, rec.Text(fieldName))
		nextStatus := values[(idx+1)%len(values)]
		_, cs, err := a.Store.Patch(data.PatchInput{
			Ref:     ref,
			Fields:  map[string]any{fieldName: nextStatus},
			Summary: a.title(ref) + " " + fieldName + " -> " + nextStatus,
		}, User)
		a.wrote(a.title(ref)+" -> "+nextStatus, cs, err)
		return core.Next(s)
	}

	patches := make([]data.PatchInput, 0, len(refs))
	for _, ref := range refs {
		fieldName, values, ok := a.schema(ref.Kind).PrimaryStatusField()
		if !ok || len(values) == 0 {
			continue
		}
		_ = a.ensureShadow(ref)
		rec, err := a.Store.Get(ref)
		if err != nil {
			continue
		}
		idx := slices.Index(values, rec.Text(fieldName))
		nextStatus := values[(idx+1)%len(values)]
		patches = append(patches, data.PatchInput{
			Ref:    ref,
			Fields: map[string]any{fieldName: nextStatus},
		})
	}
	if len(patches) == 0 {
		a.say("no selected items have a status column to cycle")
		return core.Next(s)
	}
	cs, err := a.Store.PatchMany(patches, "record.status.cycle", fmt.Sprintf("cycled status on %d items", len(patches)), User)
	a.wrote(fmt.Sprintf("cycled status on %d items", len(patches)), cs, err)
	return core.Next(s)
}

// --- batch record assignment ----------------------------------------------------

func (a *App) assignMany(refs []core.Ref, target *core.Ref) {
	if len(refs) == 0 {
		return
	}
	patches := make([]data.PatchInput, 0, len(refs))
	where := "the root"
	if target != nil {
		where = a.title(*target)
	}
	for _, ref := range refs {
		_ = a.ensureShadow(ref)
		field := assignField(ref.Kind)
		var value any
		if target != nil {
			value = *target
		}
		patches = append(patches, data.PatchInput{
			Ref:    ref,
			Fields: map[string]any{field: value},
		})
	}
	cs, err := a.Store.PatchMany(patches, "record.assign", fmt.Sprintf("moved %d items to %s", len(patches), where), User)
	if err == nil && target != nil && target.Kind == data.KindTask {
		for _, ref := range refs {
			if ref.Kind == data.KindPane {
				_, _ = data.BackfillPaneTask(a.Store, ref.ID, target.ID, User)
				a.updatePaneContextProject(ref.ID)
			}
		}
	}
	a.wrote(fmt.Sprintf("moved %d items to %s", len(patches), where), cs, err)
}

// --- batch tree indent / outdent ------------------------------------------------

func filterTopmostSelectedIDs(nodes []data.TreeNode, ids []string) []string {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			want[id] = true
		}
	}
	var out []string
	var walk func(ns []data.TreeNode, ancestorSelected bool)
	walk = func(ns []data.TreeNode, ancestorSelected bool) {
		for _, nd := range ns {
			sel := want[nd.Record.ID]
			if sel && !ancestorSelected {
				out = append(out, nd.Record.ID)
			}
			walk(nd.Children, ancestorSelected || sel)
		}
	}
	walk(nodes, false)
	return out
}

func (a *App) outlineBatchIndent(s core.State, tm *tree.Model, kind, treeID string, schema data.Schema, rawIDs []string) core.StepResult {
	h := schema.EffectiveHierarchy()
	if h == nil || h.ParentField == "" {
		a.say("%s has no hierarchy", strings.ToLower(schema.Plural))
		return core.Next(s)
	}
	_, nodes := a.syncTreeModel(tm, kind, treeID)
	ids := filterTopmostSelectedIDs(nodes, rawIDs)
	a.clearActiveVisual(s)
	moved := 0
	for _, id := range ids {
		_, fresh := a.syncTreeModel(tm, kind, treeID)
		loc, ok := findTreeNodeLoc(fresh, id, nil, nil, -1, nil)
		if !ok || loc.index == 0 {
			continue
		}
		cur := loc.siblings[loc.index]
		if tm != nil && tm.Hoisted() && cur.Record.ID == tm.HoistID() {
			continue
		}
		prev := loc.siblings[loc.index-1]
		fields := map[string]any{h.ParentField: prev.Record.Ref()}
		if h.OrderField != "" {
			fields[h.OrderField] = int64(len(prev.Children) + 1)
		}
		_ = a.ensureShadow(cur.Record.Ref())
		_, _, err := a.Store.Patch(data.PatchInput{
			Ref:     cur.Record.Ref(),
			Fields:  fields,
			Summary: "indent " + a.title(cur.Record.Ref()) + " under " + a.title(prev.Record.Ref()),
		}, User)
		if err != nil {
			a.fail("indent", err)
			break
		}
		if loc.parent == nil {
			if state := a.activeCleanSlate(kind, treeID); state != nil {
				delete(state.roots, cur.Record.ID)
			}
		}
		if tm != nil {
			delete(tm.Folded, prev.Record.ID)
			a.saveTreeFolded(kind, treeID, tm)
		}
		moved++
	}
	if moved == 0 {
		a.say("no preceding sibling to indent under")
	} else {
		a.say("indented %d items · u undoes", moved)
	}
	return core.Next(s)
}

func (a *App) outlineBatchOutdent(s core.State, tm *tree.Model, kind, treeID string, schema data.Schema, rawIDs []string) core.StepResult {
	h := schema.EffectiveHierarchy()
	if h == nil || h.ParentField == "" {
		a.say("%s has no hierarchy", strings.ToLower(schema.Plural))
		return core.Next(s)
	}
	_, nodes := a.syncTreeModel(tm, kind, treeID)
	ids := filterTopmostSelectedIDs(nodes, rawIDs)
	a.clearActiveVisual(s)
	moved := 0
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		_, fresh := a.syncTreeModel(tm, kind, treeID)
		loc, ok := findTreeNodeLoc(fresh, id, nil, nil, -1, nil)
		if !ok || loc.parent == nil {
			continue
		}
		cur := loc.siblings[loc.index]
		if tm != nil && tm.Hoisted() {
			hoistID := tm.HoistID()
			if cur.Record.ID == hoistID || loc.parent.Record.ID == hoistID {
				continue
			}
		}
		var newParent any
		where := "root"
		if loc.grandparent != nil {
			newParent = loc.grandparent.Record.Ref()
			where = a.title(loc.grandparent.Record.Ref())
		}
		_ = a.ensureShadow(cur.Record.Ref())
		var err error
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
			for j, node := range newSibs {
				fields := map[string]any{h.OrderField: base + int64(j)}
				if node.Record.ID == cur.Record.ID {
					fields[h.ParentField] = newParent
				}
				patches[j] = data.PatchInput{Ref: node.Record.Ref(), Fields: fields}
			}
			_, err = a.Store.PatchMany(patches, "outline.outdent", "outdent "+a.title(cur.Record.Ref())+" to "+where, User)
		} else {
			_, _, err = a.Store.Patch(data.PatchInput{
				Ref:     cur.Record.Ref(),
				Fields:  map[string]any{h.ParentField: newParent},
				Summary: "outdent " + a.title(cur.Record.Ref()) + " to " + where,
			}, User)
		}
		if err != nil {
			a.fail("outdent", err)
			break
		}
		if newParent == nil {
			a.addCleanSlateRoot(kind, treeID, cur.Record.ID)
		}
		moved++
	}
	if moved == 0 {
		a.say("already at root")
	} else {
		a.say("outdented %d items · u undoes", moved)
	}
	return core.Next(s)
}

// --- smart h/l tree & group folding with region edge spill ----------------------

// treeSmartFoldCommand handles tree.fold.in (l) and tree.fold.out (h) in trees
// and outlines, spilling seamlessly to region column navigation at leaf/root
// edges when not inside ModeOutline.
func (a *App) treeSmartFoldCommand(cmdID string) func(core.State, core.Command) core.StepResult {
	delta := -1
	if cmdID == tree.CmdFoldOrStepIn {
		delta = 1
	}
	return func(s core.State, _ core.Command) core.StepResult {
		if a.Modal() == "outline" {
			tm := a.trees.outlineModel()
			a.syncTreeModel(tm, data.KindTask, outlineTree)
			if delta > 0 {
				tm.StepIn()
			} else {
				tm.StepOut()
			}
			a.saveTreeFolded(data.KindTask, outlineTree, tm)
			if tm.VisualActive {
				a.syncVisualStatus(s)
			}
			return core.Next(s)
		}
		if tm, kind, treeID, isWork, ok := a.activeTree(s); ok {
			a.syncTreeModel(tm, kind, treeID)
			acted := false
			if delta > 0 {
				acted = tm.StepIn()
			} else {
				acted = tm.StepOut()
			}
			if acted {
				a.saveTreeFolded(kind, treeID, tm)
				if isWork {
					a.syncWorkTreeToTable(kind, tm)
				}
				if tm.VisualActive {
					a.syncVisualStatus(s)
				}
				return core.Next(s)
			}
		}
		return core.Next(a.column(s, delta))
	}
}

// smartGroupHL handles h/l on Project nav tree nodes/sections, grouped table
// headers, and TOC group headers, spilling cleanly to region column navigation.
func (a *App) smartGroupHL(s core.State, delta int) (core.State, bool) {
	if a.Modal() != "" {
		return s, false
	}
	if next, handled := a.projectNavHL(s, delta); handled {
		return next, true
	}
	if !a.treeHLFolds() {
		return s, false
	}
	focus := s.ActiveFrame().FocusID
	if tbl := a.nav.tables[focus]; tbl != nil {
		if it, ok := tbl.Focused(); ok && it.Header {
			if delta < 0 && !it.Collapsed {
				tbl.ToggleGroup()
				a.say("collapsed %s", it.Group)
				return s, true
			}
			if delta > 0 && it.Collapsed {
				tbl.ToggleGroup()
				a.say("expanded %s", it.Group)
				return s, true
			}
		}
	}
	if group, ok := strings.CutPrefix(focus, views.TOCGroupPrefix); ok && group != "" {
		collapsed := a.panes != nil && a.panes.tocCollapsed[group]
		if delta < 0 && !collapsed {
			if a.panes.tocCollapsed == nil {
				a.panes.tocCollapsed = map[string]bool{}
			}
			a.panes.tocCollapsed[group] = true
			a.say("collapsed %s group", group)
			return s, true
		}
		if delta > 0 && collapsed {
			a.panes.tocCollapsed[group] = false
			a.say("expanded %s group", group)
			return s, true
		}
	}
	return s, false
}
