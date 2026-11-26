package app

import (
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/widgets/tree"
)

// Edge spill: j/k at the first or last stop of a list-like component (a table,
// a tree, a focus region) either stays put or moves focus to the neighbouring
// thing drawn. Every component reports truthful edge facts and one decision,
// traits.ResolveSpill over the spill config, says whether the motion spills.

// spillConfig is the data that drives edge spill. nav.edgeFocusSpill turns
// vertical spill on for every list-like component; h/l change column through
// column navigation, not through spill.
func (a *App) spillConfig() traits.SpillConfig {
	on := a.edgeFocusSpillEnabled()
	return traits.SpillConfig{Top: on, Bottom: on}
}

// listEdgeFacts reports whether the stop at idx of n stops is the first or last.
func listEdgeFacts(idx, n int) map[string]any {
	empty := n <= 0
	return map[string]any{
		"empty":    empty,
		"atTop":    empty || idx <= 0,
		"atBottom": empty || idx >= n-1,
	}
}

// verticalMotion returns the single-step motion a signed delta moves in.
func verticalMotion(delta int) core.Command {
	if delta < 0 {
		return core.Command{ID: "motion.up"}
	}
	return core.Command{ID: "motion.down"}
}

// resolveEdgeSpill asks the spill trait whether a motion with the given edge
// facts leaves its component. It returns the focus step the spill asks for
// (+1 down, -1 up) or 0 when the motion stays.
func (a *App) resolveEdgeSpill(facts map[string]any, c core.Command) int {
	next, spilled := traits.ResolveSpill(facts, a.spillConfig(), c)
	if !spilled {
		return 0
	}
	switch next.ID {
	case core.CmdFocusDown:
		return 1
	case core.CmdFocusUp:
		return -1
	}
	return 0
}

// treeEdgeSpill reports whether a vertical motion the tree did not move (its
// selection is still before) spills out of the tree.
func (a *App) treeEdgeSpill(tm *tree.Model, before string, c core.Command) bool {
	if tm == nil || tm.SelectedID != before {
		return false
	}
	rows := tm.Flatten()
	idx := -1
	for i, r := range rows {
		if !r.Draft && r.ID == tm.SelectedID {
			idx = i
			break
		}
	}
	if idx < 0 && len(rows) > 0 {
		return false
	}
	return a.resolveEdgeSpill(listEdgeFacts(idx, len(rows)), c) != 0
}

// motionTree returns the tree that owns vertical motions at focus and the move
// that applies one. A tree whose backing record is missing is returned nil: it
// still owns the motion and does nothing.
func (a *App) motionTree(s core.State, focus string) (*tree.Model, func(core.Command), bool) {
	switch {
	case focus == workTable && a.inWorkTree(s):
		return a.trees.workModel(a.workKind()), a.workTreeMove, true
	case a.inProjectOutlineTree(&s):
		var tm *tree.Model
		if id := a.activeProjectOutlineID(s); id != "" {
			tm = a.trees.projectModel(id)
		}
		return tm, func(c core.Command) { a.projectOutlineTreeMove(s, c) }, true
	case a.inFeaturesCaptureTree(&s):
		var tm *tree.Model
		if wid := a.featuresWishlistTaskID(); wid != "" {
			tm = a.trees.projectModel(wid)
		}
		return tm, func(c core.Command) { a.featuresCaptureTreeMove(s, c) }, true
	}
	if tm, ok := a.projectSectionTree(focus); ok {
		return tm, func(c core.Command) { tm.HandleCommand(c) }, true
	}
	return nil, nil, false
}

// regionSpillGroups joins focus regions that j/k walk as one vertical list
// without spill: the Show page's header, props and related sections read as
// one document.
var regionSpillGroups = map[string]string{
	showHeader:     "show",
	"show.props":   "show",
	"show.related": "show",
}

func regionSpillGroup(region string) string {
	if g, ok := regionSpillGroups[region]; ok {
		return g
	}
	return region
}

func samePaneRegion(a, b string) bool {
	return regionSpillGroup(a) == regionSpillGroup(b)
}

// stepBounded moves the focus by delta stops inside the focused region. A
// motion that runs past the region's first or last stop meets that edge, and
// the spill trait decides whether it continues in drawing order across
// regions or stops at the edge.
func (a *App) stepBounded(s core.State, delta int) core.State {
	nodes := a.focusables(s)
	if len(nodes) == 0 {
		return s
	}
	cur := -1
	for i, n := range nodes {
		if n.id == s.ActiveFrame().FocusID {
			cur = i
			break
		}
	}
	if cur < 0 {
		return a.step(s, delta)
	}
	var scoped []fnode
	curScoped := -1
	for _, n := range nodes {
		if samePaneRegion(n.region, nodes[cur].region) {
			if n.id == nodes[cur].id {
				curScoped = len(scoped)
			}
			scoped = append(scoped, n)
		}
	}
	if len(scoped) == 0 || curScoped < 0 {
		return s
	}
	want := curScoped + delta
	if want < 0 || want > len(scoped)-1 {
		// The motion meets the region's edge: the facts are those of the
		// stop it would cross from.
		edge := len(scoped) - 1
		if want < 0 {
			edge = 0
		}
		if a.resolveEdgeSpill(listEdgeFacts(edge, len(scoped)), verticalMotion(delta)) != 0 {
			return a.step(s, delta)
		}
	}
	to := min(max(want, 0), len(scoped)-1)
	if to == curScoped {
		return s
	}
	if tbl := a.nav.tables[scoped[to].id]; tbl != nil {
		tbl.SelectedIdx, tbl.Offset = 0, 0
		if delta < 0 {
			tbl.SelectedIdx = len(tbl.Items()) - 1
		}
		tbl.Focused()
	}
	return withFocus(s, scoped[to].id)
}
