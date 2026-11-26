package app

import (
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func (a *App) cleanSlateStateFor(kind, treeID string, create bool) *cleanSlateState {
	if a.trees == nil {
		return nil
	}
	if treeID == featuresCaptureTree {
		return &a.trees.featuresCleanSlate
	}
	if treeID == workTable {
		if a.trees.workCleanSlate == nil {
			if !create {
				return nil
			}
			a.trees.workCleanSlate = map[string]*cleanSlateState{}
		}
		cs := a.trees.workCleanSlate[kind]
		if cs == nil && create {
			cs = &cleanSlateState{}
			a.trees.workCleanSlate[kind] = cs
		}
		return cs
	}
	return &a.trees.outlineCleanSlate
}

// visibleTreeRoots are the top-level nodes an outline shows: the clean
// slate's roots when one is active, else every root the focus priority
// filter keeps.
func (a *App) visibleTreeRoots(all []data.TreeNode, cs *cleanSlateState, kind string) []data.TreeNode {
	if cs == nil {
		return a.focusOutlineRoots(all, kind)
	}
	filtered := make([]data.TreeNode, 0, len(cs.roots))
	for _, nd := range all {
		if cs.roots[nd.Record.ID] {
			filtered = append(filtered, nd)
		}
	}
	return filtered
}

func (a *App) activeCleanSlate(kind, treeID string) *cleanSlateState {
	cs := a.cleanSlateStateFor(kind, treeID, false)
	if cs == nil || !cs.active {
		return nil
	}
	return cs
}

func (a *App) isCleanSlateActive(kind, treeID string) bool {
	return a.activeCleanSlate(kind, treeID) != nil
}

func (a *App) addCleanSlateRoot(kind, treeID, id string) {
	if id == "" {
		return
	}
	if cs := a.activeCleanSlate(kind, treeID); cs != nil {
		if cs.roots == nil {
			cs.roots = map[string]bool{}
		}
		cs.roots[id] = true
	}
}

func (a *App) clearCleanSlate(kind, treeID string) {
	if cs := a.cleanSlateStateFor(kind, treeID, false); cs != nil {
		cs.active = false
		cs.roots = nil
	}
}

func (a *App) exitCleanSlate(tm *tree.Model, kind, treeID string, isWork bool) bool {
	if !a.isCleanSlateActive(kind, treeID) {
		return false
	}
	focusID := ""
	if tm != nil {
		focusID = tm.SelectedID
	}
	a.clearCleanSlate(kind, treeID)
	if tm != nil {
		a.syncTreeModel(tm, kind, treeID)
		if focusID != "" {
			tm.Reveal(focusID)
			a.saveTreeFolded(kind, treeID, tm)
		}
		if isWork {
			a.syncWorkTreeToTable(kind, tm)
		}
	}
	return true
}

// outlineCleanSlate opens an instant zero-noise canvas: activates a per-tree
// clean-slate root filter hiding prior outlines without modifying or hoisting
// them, and starts an inline root draft at depth 0 ready to type immediately.
func (a *App) outlineCleanSlate(s core.State, _ core.Command) core.StepResult {
	if a.trees.editing() {
		a.runOutlineEditCancel()
	}

	var (
		tm         *tree.Model
		kind       = data.KindTask
		treeID     = outlineTree
		isWork     = false
		openModal  = false
		returnMode = ModeOutline
	)
	switch {
	case a.Modal() == "outline":
		tm = a.trees.outlineModel()
	case s.ActivePage == SpaceFeatures && a.Modal() == "":
		wid := a.ensureFeaturesWishlistTask()
		if wid == "" {
			return core.Next(s)
		}
		if !a.featuresCaptureDockedEnabled() {
			tm = a.trees.outlineModel()
			treeID = outlineTree
			cs := a.cleanSlateStateFor(kind, treeID, true)
			cs.active = true
			cs.roots = map[string]bool{wid: true}
			tm.HoistStack = []string{wid}
			tm.SelectedID = wid
			a.runTreeStartAdd(tm, kind, treeID, false, ModeOutline, false)
			a.say("clean slate · type thought · enter adds next · tab indents · esc exits edit")
			return core.Next(a.pushModal(s, a.newOutlineModal()))
		}
		tm = a.trees.projectModel(wid)
		treeID = featuresCaptureTree
		returnMode = ModeNormal
		s = withFocus(s, featuresCaptureTree)
		hidden := map[string]bool{}
		if allNodes, err := a.recordTreeNodes(data.KindTask); err == nil {
			if wNode, ok := findProjectTreeNode(allNodes, wid); ok {
				for _, ch := range wNode.Children {
					hidden[ch.Record.ID] = true
				}
			}
		}
		tm.HoistStack = []string{wid}
		cs := a.cleanSlateStateFor(kind, treeID, true)
		cs.active = true
		cs.roots = hidden
		a.runTreeStartAdd(tm, kind, treeID, isWork, returnMode, false)
		a.say("clean slate · type thought · enter adds next · tab indents · esc exits edit")
		return core.Next(withMode(s, ModeOutlineEdit))
	case a.inWorkTree(s):
		kind = a.workKind()
		tm = a.trees.workModel(kind)
		treeID = workTable
		isWork = true
		returnMode = ModeNormal
	default:
		tm = a.trees.outlineModel()
		openModal = true
	}

	tm.ClearHoist()
	cs := a.cleanSlateStateFor(kind, treeID, true)
	cs.active = true
	cs.roots = map[string]bool{}

	a.runTreeStartAdd(tm, kind, treeID, isWork, returnMode, false)
	a.say("clean slate · type title · enter adds next · tab indents · esc exits edit")

	if openModal {
		return core.Next(a.pushModal(s, a.newOutlineModal()))
	}
	return core.Next(withMode(s, ModeOutlineEdit))
}

func (a *App) outlineCleanSlateExit(s core.State, c core.Command) core.StepResult {
	if a.Modal() == "outline" {
		return a.handleModal(s, c)
	}
	tm, kind, treeID, isWork, ok := a.activeTree(s)
	if !ok {
		a.say("no tree is focused")
		return core.Next(s)
	}
	if a.exitCleanSlate(tm, kind, treeID, isWork) {
		a.say("returned to full tree")
	} else {
		a.say("already at full tree")
	}
	return core.Next(s)
}
