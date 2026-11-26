package app

import (
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
)

// Object motions: } and { walk blocks, ) and ( walk sections, g j and g k
// walk siblings, and i p / i s select the block or section around the
// cursor while visual select is on. They are trait motions: the object kind
// is binding data (args {object: ...}), and each component says what its
// objects are. Tables, trees and the reader declare their own kinds (see
// their MotionNodes); outside them the page itself is the component, and
// its blocks are the focus regions in drawing order.

// Values of nav.blockMotion: what } and { do outside a component that
// declares blocks.
const (
	BlockMotionBlock     = "block"      // the next / previous region, in drawing order
	BlockMotionFiveStops = "five-stops" // five focus stops inside the region (the old behaviour)
)

// objectMotionIDs are the object motions a.motion routes (the select command
// has its own handler, objectSelect).
var objectMotionIDs = []string{
	traits.CmdMotionBlockNext, traits.CmdMotionBlockPrev,
	traits.CmdMotionObjectNext, traits.CmdMotionObjectPrev,
	traits.CmdMotionSameNext, traits.CmdMotionSamePrev,
}

// objectSelectWhen guards i p / i s: visual select must be on, and the row
// must not turn a bare `i` that can fire while visual is on into a prefix
// (that `i` would then wait for the next key). The excluded places bind a
// bare `i`: the outline (edit inline), the Work tree (edit inline, scope
// index, in the work space), installable extension rows (install) and the Features page
// (quick capture).
const objectSelectWhen = `facts.visualActive && mode != "outline" && facts.space != "features" && !facts.extInstallable && !(facts.isTree && facts.space == "work")`

// motionObjectBindings are the default rows for the object motions. } and {
// (block) live with the other motions in DefaultBindings.
func motionObjectBindings() []core.Binding {
	block := map[string]any{"object": traits.ObjectBlock}
	section := map[string]any{"object": traits.ObjectSection}
	return []core.Binding{
		with(bind(ModeReader, "", ")", traits.CmdMotionObjectNext, "next section"), section),
		with(bind(ModeReader, "", "(", traits.CmdMotionObjectPrev, "previous section"), section),
		bind(ModeNormal, "", "g j", traits.CmdMotionSameNext, "next sibling"),
		bind(ModeNormal, "", "g k", traits.CmdMotionSamePrev, "previous sibling"),
		bind(ModeReader, "", "g j", traits.CmdMotionSameNext, "next sibling"),
		bind(ModeReader, "", "g k", traits.CmdMotionSamePrev, "previous sibling"),
		when(with(bind(ModeNormal, "", "i p", traits.CmdMotionObjectSelect, "select block"), block), objectSelectWhen),
		when(with(bind(ModeNormal, "", "i s", traits.CmdMotionObjectSelect, "select section"), section), objectSelectWhen),
	}
}

func (a *App) blockMotionPolicy() string {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return BlockMotionBlock
	}
	return a.cfg.Settings.String(SetBlockMotion, BlockMotionBlock)
}

// pageMotionNodes describes the page's focus stops, in drawing order, as
// motion nodes: each stop's block is its region group, so the Show page's
// header, props and related sections read as one block, as they do for j/k.
func pageMotionNodes(nodes []fnode) []traits.MotionNode {
	out := make([]traits.MotionNode, len(nodes))
	for i, n := range nodes {
		out[i] = traits.MotionNode{
			ID:      n.id,
			Span:    traits.ItemSpan{ID: n.id, Y: i, H: 1},
			Objects: map[string]string{traits.ObjectBlock: regionSpillGroup(n.region)},
		}
	}
	return out
}

// pageObjectMotion runs an object motion over the page's focus stops when no
// component under the focus declared the motion's objects. It lands on the
// first stop of the target block (a table there selects its first row) and
// does not wrap.
func (a *App) pageObjectMotion(s core.State, c core.Command) core.State {
	cfg := traits.MotionConfig{DefaultObject: traits.ObjectBlock}
	kind, _ := traits.MotionObjectKind(c, cfg)
	if kind == traits.ObjectBlock && a.blockMotionPolicy() == BlockMotionFiveStops {
		switch c.ID {
		case traits.CmdMotionBlockNext, traits.CmdMotionObjectNext:
			return a.stepBounded(s, 5*c.Times())
		case traits.CmdMotionBlockPrev, traits.CmdMotionObjectPrev:
			return a.stepBounded(s, -5*c.Times())
		}
	}
	nodes := a.focusables(s)
	cur := -1
	for i, n := range nodes {
		if n.id == s.ActiveFrame().FocusID {
			cur = i
			break
		}
	}
	if cur < 0 {
		return s
	}
	target, _, moved, _ := traits.MotionStep(pageMotionNodes(nodes), cur, cfg, c)
	if !moved || target == cur {
		return s
	}
	if tbl := a.nav.tables[nodes[target].id]; tbl != nil {
		tbl.SelectedIdx, tbl.Offset = 0, 0
		tbl.Focused()
	}
	return withFocus(s, nodes[target].id)
}

// objectSelect applies motion.object.select: the focused tree or table
// reports the range of the object around its cursor, and that range becomes
// the visual selection (anchor at its start, cursor at its end, replacing any
// earlier anchor). In a modal without visual select (the reader) the widget
// keeps the range and nothing else changes.
func (a *App) objectSelect(s core.State, c core.Command) core.StepResult {
	kind, _ := traits.MotionObjectKind(c, traits.MotionConfig{})
	if kind == "" {
		kind = traits.ObjectBlock
	}
	if tm, treeKind, treeID, _, syncable, ok := a.activeSelectionTree(s); ok {
		if syncable {
			a.syncTreeModel(tm, treeKind, treeID)
		}
		tm.MotionSelection = nil
		tm.HandleCommand(c)
		rows := tm.Flatten()
		if sel := tm.MotionSelection; sel != nil && sel.End < len(rows) {
			tm.VisualActive, tm.VisualAnchor = true, sel.Start
			tm.SelectedID = rows[sel.End].ID
			tm.Clamp()
			a.say("VISUAL · %d selected", len(tm.VisualIDs()))
		} else {
			a.say("no %s here", kind)
		}
		return core.Next(s)
	}
	if tbl, _, ok := a.activeSelectionTable(s); ok {
		tbl.MotionSelection = nil
		tbl.HandleCommand(c)
		if sel := tbl.MotionSelection; sel != nil && sel.End < len(tbl.Items()) {
			tbl.VisualActive, tbl.VisualAnchor = true, sel.Start
			tbl.SelectedIdx = sel.End
			a.say("VISUAL · %d selected", len(tbl.VisualRows()))
		} else {
			a.say("no %s here", kind)
		}
		return core.Next(s)
	}
	if a.Modal() != "" {
		return a.handleModal(s, c)
	}
	a.say("no list or tree is focused")
	return core.Next(s)
}
