package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

const (
	OutlineViewAll          = string(data.FinalViewAll)
	OutlineViewHideFinal    = string(data.FinalViewHideFinal)
	OutlineViewOnlyFinal    = string(data.FinalViewOnlyFinal)
	OutlineViewActive       = "active" // backward-compatible alias for hide-final
	OutlineViewAgentThreads = "agent-threads"

	CmdOutlineViewCycle = "outline.view.cycle"
	CmdOutlineViewSet   = "outline.view.set"
)

type outlineViewSpec struct {
	ID    string
	Label string
	Badge string
	Tone  string
}

var outlineViewSpecs = []outlineViewSpec{
	{ID: OutlineViewAll, Label: "all"},
	{ID: OutlineViewHideFinal, Label: "hide-final", Badge: "view: hide-final", Tone: "accent"},
	{ID: OutlineViewOnlyFinal, Label: "only-final", Badge: "view: only-final", Tone: "warning"},
	{ID: OutlineViewAgentThreads, Label: "agent threads", Badge: "view: agent threads", Tone: "info"},
}

var (
	outlineViewOrderFull  = []string{OutlineViewAll, OutlineViewHideFinal, OutlineViewOnlyFinal, OutlineViewAgentThreads}
	outlineViewOrderFinal = []string{OutlineViewAll, OutlineViewHideFinal, OutlineViewOnlyFinal}
	outlineViewSetEnum    = []string{OutlineViewAll, OutlineViewHideFinal, OutlineViewOnlyFinal, OutlineViewActive, OutlineViewAgentThreads}
)

var outlineViewPanelToTree = map[string]string{
	projectOutlinePanel:  projectOutlineTree,
	featuresCapturePanel: featuresCaptureTree,
	featuresOutlinePanel: featuresOutlineTree,
	"project.lanes":      projectLanesTree,
	"project.panes":      projectPanesTree,
}

var outlineViewTreeKinds = map[string]string{
	outlineTree:         data.KindTask,
	projectOutlineTree:  data.KindTask,
	featuresCaptureTree: data.KindTask,
	featuresOutlineTree: data.KindTask,
	projectLanesTree:    data.KindLane,
	projectPanesTree:    data.KindPane,
}

func outlineViewCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdOutlineViewCycle,
			Description: "Cycle the active tree or outline view mode (all, hide-final, only-final, agent-threads)",
			Params: []core.ParamSpec{
				{Name: "dir", Type: core.ParamString, Enum: []string{"next", "prev"}, Default: "next", Description: "Cycle direction"},
			},
		},
		{
			ID:          CmdOutlineViewSet,
			Description: "Set the active tree or outline view mode directly (active is accepted as an alias for hide-final)",
			Params: []core.ParamSpec{
				{Name: "view", Type: core.ParamString, Enum: outlineViewSetEnum, Default: OutlineViewAll, Description: "Tree or outline view mode"},
			},
		},
	}
}

func (a *App) registerOutlineViewHandlers() {
	a.handle(CmdOutlineViewCycle, a.outlineViewCycleCmd)
	a.handle(CmdOutlineViewSet, a.outlineViewSetCmd)
}

func normalizeOutlineViewMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case OutlineViewAll, "":
		return OutlineViewAll, true
	case OutlineViewHideFinal, OutlineViewActive:
		return OutlineViewHideFinal, true
	case OutlineViewOnlyFinal, "final", "finished":
		return OutlineViewOnlyFinal, true
	case OutlineViewAgentThreads:
		return OutlineViewAgentThreads, true
	default:
		return "", false
	}
}

func outlineViewOrderForSchema(schema data.Schema) []string {
	if _, hasAgentQueue := schema.Field("forAgent"); hasAgentQueue {
		return outlineViewOrderFull
	}
	return outlineViewOrderFinal
}

func lookupOutlineViewSpec(mode string) outlineViewSpec {
	if norm, ok := normalizeOutlineViewMode(mode); ok {
		mode = norm
	}
	for _, sp := range outlineViewSpecs {
		if sp.ID == mode {
			return sp
		}
	}
	return outlineViewSpecs[0]
}

func (a *App) outlineViewMode(treeID string) string {
	if a == nil || a.trees == nil || len(a.trees.outlineViews) == 0 {
		return OutlineViewAll
	}
	if mode, ok := normalizeOutlineViewMode(a.trees.outlineViews[treeID]); ok {
		return mode
	}
	return OutlineViewAll
}

func (a *App) setOutlineViewMode(treeID, mode string) {
	if a == nil || a.trees == nil || treeID == "" {
		return
	}
	norm, ok := normalizeOutlineViewMode(mode)
	if !ok {
		norm = OutlineViewAll
	}
	if a.trees.outlineViews == nil {
		a.trees.outlineViews = map[string]string{}
	}
	if norm == OutlineViewAll {
		delete(a.trees.outlineViews, treeID)
	} else {
		a.trees.outlineViews[treeID] = norm
	}
	clear(a.trees.syncCache)
	a.viewMemo.gen++
}

func (a *App) cycleOutlineView(s core.State, treeID string, dir int) core.StepResult {
	kind := outlineViewTreeKinds[normalizeOutlineViewTreeID(treeID)]
	if kind == "" {
		kind = data.KindTask
	}
	return a.cycleOutlineViewForSchema(s, treeID, a.schema(kind), dir)
}

func (a *App) cycleOutlineViewForSchema(s core.State, treeID string, schema data.Schema, dir int) core.StepResult {
	order := outlineViewOrderForSchema(schema)
	cur := a.outlineViewMode(treeID)
	if !slices.Contains(order, cur) {
		cur = OutlineViewAll
	}
	next, _ := traits.Cycle(order, cur, dir, true)
	a.setOutlineViewMode(treeID, next)
	sp := lookupOutlineViewSpec(next)
	idx := slices.Index(order, next) + 1
	a.say("view: %s (%d/%d) · ( / ) cycles", sp.Label, idx, len(order))
	return core.Next(s)
}

func (a *App) stdCycleOutlineView(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	treeID, schema := a.resolveOutlineCycleTarget(ctx, nd)
	if treeID == "" {
		return core.StepResult{}, false
	}
	return a.cycleOutlineViewForSchema(ctx.state, treeID, schema, ctx.dir), true
}

func (a *App) resolveOutlineCycleTarget(ctx standardVerbContext, nd *ui.ViewNode) (string, data.Schema) {
	if a.activeOutline() != nil {
		return outlineTree, a.schema(data.KindTask)
	}
	if nd != nil {
		id := normalizeOutlineViewTreeID(nd.ID)
		if kind, ok := outlineViewTreeKinds[id]; ok {
			return id, a.schema(kind)
		}
	}
	if _, kind, treeID, _, ok := a.activeTree(ctx.state); ok {
		return treeID, a.schema(kind)
	}
	return "", data.Schema{}
}

func (a *App) resolveOutlineCmdTarget(s core.State) (string, data.Schema) {
	if _, kind, treeID, _, ok := a.activeTree(s); ok {
		return treeID, a.schema(kind)
	}
	focusID := normalizeOutlineViewTreeID(s.ActiveFrame().FocusID)
	if kind, ok := outlineViewTreeKinds[focusID]; ok {
		return focusID, a.schema(kind)
	}
	return outlineTree, a.schema(data.KindTask)
}

func normalizeOutlineViewTreeID(id string) string {
	if mapped, ok := outlineViewPanelToTree[id]; ok {
		return mapped
	}
	return id
}

func (a *App) outlineViewCycleCmd(s core.State, c core.Command) core.StepResult {
	treeID, schema := a.resolveOutlineCmdTarget(s)
	return a.cycleOutlineViewForSchema(s, treeID, schema, parseCycleDir(c.Args))
}

func (a *App) outlineViewSetCmd(s core.State, c core.Command) core.StepResult {
	treeID, schema := a.resolveOutlineCmdTarget(s)
	raw := strings.TrimSpace(c.Arg("view"))
	mode, ok := normalizeOutlineViewMode(raw)
	if !ok || !slices.Contains(outlineViewOrderForSchema(schema), mode) {
		a.say("unknown view %q (use all, hide-final, only-final, or agent-threads)", raw)
		return core.Next(s)
	}
	a.setOutlineViewMode(treeID, mode)
	sp := lookupOutlineViewSpec(mode)
	a.say("view: %s", sp.Label)
	return core.Next(s)
}

func isAgentThreadRecord(rec data.Record, threads map[string][]data.Record) bool {
	if len(threads[rec.ID]) > 0 || rec.Bool("forAgent") {
		return true
	}
	st := strings.ToLower(strings.TrimSpace(rec.Text("agentStatus")))
	return st != "" && st != data.AgentStatusNone
}

func (a *App) filterOutlineNodes(nodes []data.TreeNode, schema data.Schema, mode string) []data.FilteredTreeNode {
	switch mode {
	case OutlineViewHideFinal:
		return data.FilterTreeByFinalState(nodes, schema, data.FinalViewHideFinal)
	case OutlineViewOnlyFinal:
		return data.FilterTreeByFinalState(nodes, schema, data.FinalViewOnlyFinal)
	case OutlineViewAgentThreads:
		threads, _ := a.taskNoteThreads()
		return data.FilterTreeByPredicate(nodes, func(rec data.Record) bool {
			return isAgentThreadRecord(rec, threads)
		})
	default:
		return data.TreeNodesToFiltered(nodes)
	}
}

func (a *App) recordMatchesOutlineView(schema data.Schema, rec data.Record, mode string) bool {
	switch mode {
	case OutlineViewHideFinal:
		return !data.IsFinalRecord(schema, rec)
	case OutlineViewOnlyFinal:
		return data.IsFinalRecord(schema, rec)
	case OutlineViewAgentThreads:
		threads, _ := a.taskNoteThreads()
		return isAgentThreadRecord(rec, threads)
	default:
		return true
	}
}

func (a *App) filterViewStyleOptions(mode, recordScope string) views.FilterViewStyleOptions {
	return views.FilterViewStyleOptions{
		Filtered:            mode != OutlineViewAll,
		DimAncestors:        a.finalViewDimAncestorsEnabled(),
		PromoteFinalMatches: mode == OutlineViewOnlyFinal,
		RecordScope:         recordScope,
	}
}

func (a *App) buildFilteredTreeNodes(schema data.Schema, kind, treeID string, raw []data.TreeNode) ([]data.TreeNode, []tree.Node, views.TreeOptions) {
	mode := a.outlineViewMode(treeID)
	filtered := a.filterOutlineNodes(raw, schema, mode)
	plain := data.FilteredToTreeNodes(filtered)
	builtNodes, opts := a.buildTreeNodes(schema, kind, plain)
	if mode != OutlineViewAll {
		views.ApplyFilterViewStyling(builtNodes, data.CollectContextOnlyIDs(filtered), a.filterViewStyleOptions(mode, schema.Kind))
	}
	return plain, builtNodes, opts
}

func (a *App) buildProjectFilteredTreeNodes(schema data.Schema, kind, treeID string, rootNodes []data.TreeNode) ([]data.TreeNode, []tree.Node, views.TreeOptions) {
	mode := a.outlineViewMode(treeID)
	if mode == OutlineViewAll || len(rootNodes) == 0 {
		builtNodes, opts := a.buildTreeNodes(schema, kind, rootNodes)
		return rootNodes, builtNodes, opts
	}
	root := rootNodes[0]
	filteredKids := a.filterOutlineNodes(root.Children, schema, mode)
	wrapped := []data.FilteredTreeNode{{
		Record:      root.Record,
		Depth:       root.Depth,
		ContextOnly: !a.recordMatchesOutlineView(schema, root.Record, mode),
		Children:    filteredKids,
	}}
	plain := data.FilteredToTreeNodes(wrapped)
	builtNodes, opts := a.buildTreeNodes(schema, kind, plain)
	views.ApplyFilterViewStyling(builtNodes, data.CollectContextOnlyIDs(wrapped), a.filterViewStyleOptions(mode, schema.Kind))
	return plain, builtNodes, opts
}

func (a *App) buildProjectSectionFilteredTree(schema data.Schema, treeID string, nodes []data.TreeNode, opts views.TreeOptions) *tree.Model {
	mode := a.outlineViewMode(treeID)
	if mode == OutlineViewAll {
		return views.TreeFromNodes(schema, nodes, opts)
	}
	filtered := a.filterOutlineNodes(nodes, schema, mode)
	return views.TreeFromFilteredNodes(schema, filtered, opts, a.filterViewStyleOptions(mode, schema.Kind))
}

func (a *App) outlineViewBadgesFor(treeID string, tm *tree.Model) []ui.Badge {
	mode := a.outlineViewMode(treeID)
	sp := lookupOutlineViewSpec(mode)
	if sp.Badge == "" {
		return nil
	}
	badges := []ui.Badge{{Text: sp.Badge, Tone: sp.Tone}}
	if tm != nil {
		matched, total := views.CountTreeFilterMatches(tm)
		if total > matched {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d matched · %d ctx", matched, total-matched), Tone: "muted"})
		} else {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d matched", matched), Tone: "muted"})
		}
	}
	return badges
}

func (a *App) applyOutlineViewFacts(panel *ui.ViewNode, treeID string) {
	if panel == nil {
		return
	}
	mode := a.outlineViewMode(treeID)
	if panel.Facts == nil {
		panel.Facts = map[string]any{}
	}
	panel.Facts["outlineView"] = mode
	if len(panel.Children) > 0 {
		if panel.Children[0].Facts == nil {
			panel.Children[0].Facts = map[string]any{}
		}
		panel.Children[0].Facts["outlineView"] = mode
	}
}

func (a *App) outlineViewSyncSuffix(treeID string) string {
	mode := a.outlineViewMode(treeID)
	if mode == OutlineViewAll {
		return ""
	}
	return fmt.Sprintf("|view:%s|dim:%v", mode, a.finalViewDimAncestorsEnabled())
}
