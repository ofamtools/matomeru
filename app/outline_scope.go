package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

// Outline re-scoping commands: hoist/scope into any task or subtask as root,
// step up one parent level along the ancestor chain, or jump back to the
// outline's default root (active Project in Project Portal, or full outline).
const (
	CmdOutlineScopeInto = "outline.scope.into"
	CmdOutlineHoist     = "outline.hoist"
	CmdOutlineScopeUp   = "outline.scope.up"
	CmdOutlineScopeRoot = "outline.scope.root"
)

func outlineScopeCommandSpecs() []core.CommandSpec {
	idParam := []core.ParamSpec{{
		Name:        "id",
		Type:        core.ParamString,
		Description: "Optional task or item ID to re-scope as outline root (defaults to focused item)",
	}}
	return []core.CommandSpec{
		{
			ID:          CmdOutlineScopeInto,
			Description: "Re-scope (hoist) the outline view onto the focused task or subtask as root",
			Params:      idParam,
		},
		{
			ID:          CmdOutlineHoist,
			Description: "Hoist the focused task or subtask as the outline root (alias for outline.scope.into)",
			Params:      idParam,
		},
		spec(CmdOutlineScopeUp, "Step the outline scope root up one parent level"),
		spec(CmdOutlineScopeRoot, "Jump the outline scope back to its default root (active Project or full outline)"),
	}
}

func outlineScopeBindings() []core.Binding {
	n, all := ModeNormal, ""
	return []core.Binding{
		hidden(when(bind(n, views.ScopeIndex, "z i", CmdOutlineScopeInto, "scope into item"), "facts.isTree && facts.outlineRescoping")),
		hidden(when(bind(n, views.ScopeIndex, "z u", CmdOutlineScopeUp, "scope up one level"), "facts.isTree && facts.hoisted && facts.outlineRescoping")),
		hidden(when(bind(n, views.ScopeIndex, "z r", CmdOutlineScopeRoot, "jump to root"), "facts.isTree && facts.hoisted && facts.outlineRescoping")),
		hidden(when(bind(n, views.ScopeIndex, "_", CmdOutlineScopeRoot, "jump to root"), "facts.isTree && facts.hoisted && facts.outlineRescoping")),

		when(bind(ModeOutline, all, "z i", CmdOutlineScopeInto, "scope into item"), "facts.outlineRescoping"),
		when(bind(ModeOutline, all, "z u", CmdOutlineScopeUp, "scope up one level"), "facts.hoisted && facts.outlineRescoping"),
		when(bind(ModeOutline, all, "z r", CmdOutlineScopeRoot, "jump to root"), "facts.hoisted && facts.outlineRescoping"),
		hidden(when(bind(ModeOutline, all, "_", CmdOutlineScopeRoot, "jump to root"), "facts.hoisted && facts.outlineRescoping")),
	}
}

func (a *App) registerOutlineScopeHandlers() {
	a.handle(CmdOutlineScopeInto, a.outlineScopeInto)
	a.handle(CmdOutlineHoist, a.outlineScopeInto)
	a.handle(CmdOutlineScopeUp, a.outlineScopeUp)
	a.handle(CmdOutlineScopeRoot, a.outlineScopeRoot)
}

// syncOutlineRescopingSetting resets any active sub-hoists back to their
// default roots when ui.outlineRescoping is turned off.
func (a *App) syncOutlineRescopingSetting() {
	if a == nil || a.trees == nil || a.outlineRescopingEnabled() {
		return
	}
	if a.trees.outline != nil {
		a.trees.outline.ClearHoist()
	}
	for _, tm := range a.trees.work {
		if tm != nil {
			tm.ClearHoist()
		}
	}
	for projID, tm := range a.trees.projectTrees {
		if tm != nil {
			resetOutlineScopeToRoot(tm, projID)
		}
	}
	if a.trees.featuresOutline != nil && a.trees.featuresOutline.model != nil {
		a.trees.featuresOutline.model.ClearHoist()
	}
}

func (a *App) resolveOutlineScopeTree(s core.State) (*tree.Model, string, string, bool, bool) {
	ts, _ := a.treeTargetState(s)
	return a.activeTree(ts)
}

func (a *App) finishOutlineScopeChange(tm *tree.Model, kind, treeID string, isWork bool) {
	a.saveTreeFolded(kind, treeID, tm)
	if isWork {
		a.syncWorkTreeToTable(kind, tm)
	}
	a.viewMemo.gen++
}

func (a *App) outlineScopeInto(s core.State, c core.Command) core.StepResult {
	if !a.outlineRescopingEnabled() {
		a.say("outline re-scoping is disabled (ui.outlineRescoping = false)")
		return core.Next(s)
	}
	tm, kind, treeID, isWork, ok := a.resolveOutlineScopeTree(s)
	if !ok {
		return core.Next(s)
	}
	a.syncTreeModel(tm, kind, treeID)
	targetID := resolveOutlineScopeTargetID(tm, c.Arg("id"))
	if targetID == "" {
		a.say("no item selected to scope into")
		return core.Next(s)
	}
	if tm.HoistID() == targetID {
		a.say("already scoped to %s", a.title(core.Ref{Kind: kind, ID: targetID}))
		return core.Next(s)
	}
	baseRootID := a.trees.projectIDForModel(tm)
	stack, found := buildOutlineScopeStack(tm.Nodes, baseRootID, targetID)
	if !found {
		a.say("cannot scope to item")
		return core.Next(s)
	}
	tm.HoistStack = stack
	delete(tm.Folded, targetID)
	tm.SelectedID = targetID
	tm.Clamp()
	a.finishOutlineScopeChange(tm, kind, treeID, isWork)
	a.say("scoped to %s · z u up · z r root", a.title(core.Ref{Kind: kind, ID: targetID}))
	return core.Next(s)
}

func (a *App) outlineScopeUp(s core.State, _ core.Command) core.StepResult {
	if !a.outlineRescopingEnabled() {
		a.say("outline re-scoping is disabled (ui.outlineRescoping = false)")
		return core.Next(s)
	}
	tm, kind, treeID, isWork, ok := a.resolveOutlineScopeTree(s)
	if !ok {
		return core.Next(s)
	}
	a.syncTreeModel(tm, kind, treeID)
	baseRootID := a.trees.projectIDForModel(tm)
	changed, atBase := stepUpOutlineScope(tm, baseRootID)
	if !changed {
		a.sayOutlineScopeAtBase(atBase)
		return core.Next(s)
	}
	a.finishOutlineScopeChange(tm, kind, treeID, isWork)
	a.sayOutlineScopeLocation(tm, kind, baseRootID)
	return core.Next(s)
}

func (a *App) outlineScopeRoot(s core.State, _ core.Command) core.StepResult {
	if !a.outlineRescopingEnabled() {
		a.say("outline re-scoping is disabled (ui.outlineRescoping = false)")
		return core.Next(s)
	}
	tm, kind, treeID, isWork, ok := a.resolveOutlineScopeTree(s)
	if !ok {
		return core.Next(s)
	}
	a.syncTreeModel(tm, kind, treeID)
	baseRootID := a.trees.projectIDForModel(tm)
	if !resetOutlineScopeToRoot(tm, baseRootID) {
		a.sayOutlineScopeAtBase(baseRootID != "")
		return core.Next(s)
	}
	a.finishOutlineScopeChange(tm, kind, treeID, isWork)
	a.sayOutlineScopeLocation(tm, kind, baseRootID)
	return core.Next(s)
}

func (a *App) sayOutlineScopeAtBase(hasBase bool) {
	if hasBase {
		a.say("already at project root")
		return
	}
	a.say("already at outline root")
}

func (a *App) sayOutlineScopeLocation(tm *tree.Model, kind, baseRootID string) {
	if top := tm.HoistID(); top != "" && top != baseRootID {
		a.say("scoped to %s", a.title(core.Ref{Kind: kind, ID: top}))
		return
	}
	if baseRootID != "" {
		a.say("returned to project root")
		return
	}
	a.say("returned to outline root")
}

// --- pure tree scope path helpers -----------------------------------------------

func resolveOutlineScopeTargetID(tm *tree.Model, rawID string) string {
	if id := strings.TrimSpace(rawID); id != "" {
		if _, after, ok := strings.Cut(id, ":"); ok && after != "" {
			return after
		}
		return id
	}
	row, ok := tm.Focused()
	if !ok || row.Draft || row.ID == "" {
		return ""
	}
	if isThreadNoteRow(row) {
		return threadNoteParentTaskID(row)
	}
	return row.ID
}

func treeNodeIDPath(nodes []tree.Node, targetID string) []string {
	if targetID == "" {
		return nil
	}
	for _, nd := range nodes {
		if nd.ID == targetID {
			return []string{nd.ID}
		}
		if sub := treeNodeIDPath(nd.Children, targetID); len(sub) > 0 {
			return append([]string{nd.ID}, sub...)
		}
	}
	return nil
}

func sliceScopeStack(ids []string, baseRootID string) []string {
	if baseRootID == "" {
		return slices.Clone(ids)
	}
	if idx := slices.Index(ids, baseRootID); idx >= 0 {
		return slices.Clone(ids[idx:])
	}
	return slices.Clone(ids)
}

func buildOutlineScopeStack(nodes []tree.Node, baseRootID, targetID string) ([]string, bool) {
	ids := treeNodeIDPath(nodes, targetID)
	if len(ids) == 0 {
		return nil, false
	}
	stack := sliceScopeStack(ids, baseRootID)
	if len(stack) == 0 {
		return nil, false
	}
	return stack, true
}

func stepUpOutlineScope(tm *tree.Model, baseRootID string) (changed bool, atBase bool) {
	prevID := tm.HoistID()
	if baseRootID != "" && (prevID == "" || prevID == baseRootID) {
		return false, true
	}
	if !tm.Hoisted() {
		return false, false
	}
	path := tm.HoistPath()
	if len(path) < 2 {
		if baseRootID != "" {
			return resetOutlineScopeToRoot(tm, baseRootID), false
		}
		return tm.ClearHoist(), false
	}
	parentIDs := make([]string, len(path)-1)
	for i := 0; i < len(path)-1; i++ {
		parentIDs[i] = path[i].ID
	}
	nextStack := sliceScopeStack(parentIDs, baseRootID)
	tm.HoistStack = nextStack
	if top := tm.HoistID(); top != "" {
		delete(tm.Folded, top)
	}
	tm.Reveal(prevID)
	tm.Clamp()
	return true, false
}

func resetOutlineScopeToRoot(tm *tree.Model, baseRootID string) bool {
	if baseRootID == "" {
		return tm.ClearHoist()
	}
	if len(tm.HoistStack) <= 1 && tm.HoistID() == baseRootID {
		return false
	}
	prevID := tm.HoistID()
	tm.HoistStack = []string{baseRootID}
	delete(tm.Folded, baseRootID)
	if prevID != "" {
		tm.Reveal(prevID)
	}
	tm.Clamp()
	return true
}
