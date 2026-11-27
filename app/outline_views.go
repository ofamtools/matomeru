package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

const (
	OutlineViewAll          = "all"
	OutlineViewActive       = "active"
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
	{ID: OutlineViewActive, Label: "active (no done)", Badge: "view: active (no done)", Tone: "accent"},
	{ID: OutlineViewAgentThreads, Label: "agent threads", Badge: "view: agent threads", Tone: "info"},
}

var outlineViewOrder = []string{OutlineViewAll, OutlineViewActive, OutlineViewAgentThreads}

var doneOutlineStatuses = map[string]bool{
	"done":      true,
	"cancelled": true,
	"canceled":  true,
	"completed": true,
	"merged":    true,
	"closed":    true,
}

func outlineViewCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdOutlineViewCycle,
			Description: "Cycle the active outline view mode (all, active / no done, agent threads)",
			Params: []core.ParamSpec{
				{Name: "dir", Type: core.ParamString, Enum: []string{"next", "prev"}, Default: "next", Description: "Cycle direction"},
			},
		},
		{
			ID:          CmdOutlineViewSet,
			Description: "Set the active outline view mode directly",
			Params: []core.ParamSpec{
				{Name: "view", Type: core.ParamString, Enum: outlineViewOrder, Default: OutlineViewAll, Description: "Outline view mode"},
			},
		},
	}
}

func (a *App) registerOutlineViewHandlers() {
	a.handle(CmdOutlineViewCycle, a.outlineViewCycleCmd)
	a.handle(CmdOutlineViewSet, a.outlineViewSetCmd)
}

func lookupOutlineViewSpec(mode string) outlineViewSpec {
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
	if mode := a.trees.outlineViews[treeID]; slices.Contains(outlineViewOrder, mode) {
		return mode
	}
	return OutlineViewAll
}

func (a *App) setOutlineViewMode(treeID, mode string) {
	if a == nil || a.trees == nil || treeID == "" {
		return
	}
	if !slices.Contains(outlineViewOrder, mode) {
		mode = OutlineViewAll
	}
	if a.trees.outlineViews == nil {
		a.trees.outlineViews = map[string]string{}
	}
	if mode == OutlineViewAll {
		delete(a.trees.outlineViews, treeID)
	} else {
		a.trees.outlineViews[treeID] = mode
	}
	clear(a.trees.syncCache)
	a.viewMemo.gen++
}

func (a *App) cycleOutlineView(s core.State, treeID string, dir int) core.StepResult {
	cur := a.outlineViewMode(treeID)
	next, _ := traits.Cycle(outlineViewOrder, cur, dir, true)
	a.setOutlineViewMode(treeID, next)
	sp := lookupOutlineViewSpec(next)
	idx := slices.Index(outlineViewOrder, next) + 1
	a.say("outline view: %s (%d/%d) · ( / ) cycles", sp.Label, idx, len(outlineViewOrder))
	return core.Next(s)
}

func (a *App) stdCycleOutlineView(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	treeID := a.resolveOutlineCycleTreeID(ctx, nd)
	if treeID == "" {
		return core.StepResult{}, false
	}
	return a.cycleOutlineView(ctx.state, treeID, ctx.dir), true
}

func (a *App) resolveOutlineCycleTreeID(ctx standardVerbContext, nd *ui.ViewNode) string {
	if a.activeOutline() != nil {
		return outlineTree
	}
	if nd != nil {
		switch nd.ID {
		case outlineTree, projectOutlineTree, projectOutlinePanel, featuresCaptureTree, featuresCapturePanel, featuresOutlineTree, featuresOutlinePanel:
			return normalizeOutlineViewTreeID(nd.ID)
		}
	}
	if _, _, treeID, _, ok := a.activeTree(ctx.state); ok {
		return treeID
	}
	return ""
}

func normalizeOutlineViewTreeID(id string) string {
	switch id {
	case projectOutlinePanel:
		return projectOutlineTree
	case featuresCapturePanel:
		return featuresCaptureTree
	case featuresOutlinePanel:
		return featuresOutlineTree
	default:
		return id
	}
}

func (a *App) outlineViewCycleCmd(s core.State, c core.Command) core.StepResult {
	_, _, treeID, _, ok := a.activeTree(s)
	if !ok {
		treeID = outlineTree
	}
	return a.cycleOutlineView(s, treeID, parseCycleDir(c.Args))
}

func (a *App) outlineViewSetCmd(s core.State, c core.Command) core.StepResult {
	_, _, treeID, _, ok := a.activeTree(s)
	if !ok {
		treeID = outlineTree
	}
	mode := strings.TrimSpace(c.Arg("view"))
	if !slices.Contains(outlineViewOrder, mode) {
		a.say("unknown outline view %q (use all, active, or agent-threads)", mode)
		return core.Next(s)
	}
	a.setOutlineViewMode(treeID, mode)
	sp := lookupOutlineViewSpec(mode)
	a.say("outline view: %s", sp.Label)
	return core.Next(s)
}

func isRecordDone(schema data.Schema, rec data.Record) bool {
	status := strings.ToLower(strings.TrimSpace(outlinePrimaryStatus(schema, rec)))
	if doneOutlineStatuses[status] {
		return true
	}
	for _, field := range []string{"workStatus", "laneStatus", "runStatus"} {
		if doneOutlineStatuses[strings.ToLower(strings.TrimSpace(rec.Text(field)))] {
			return true
		}
	}
	return false
}

func filterActiveTreeNodes(schema data.Schema, nodes []data.TreeNode) []data.TreeNode {
	var out []data.TreeNode
	for _, nd := range nodes {
		if isRecordDone(schema, nd.Record) {
			continue
		}
		cp := nd
		cp.Children = filterActiveTreeNodes(schema, nd.Children)
		out = append(out, cp)
	}
	return out
}

func isAgentThreadRecord(rec data.Record, threads map[string][]data.Record) bool {
	if len(threads[rec.ID]) > 0 || rec.Bool("forAgent") {
		return true
	}
	st := strings.ToLower(strings.TrimSpace(rec.Text("agentStatus")))
	return st != "" && st != data.AgentStatusNone
}

func filterAgentThreadTreeNodes(nodes []data.TreeNode, threads map[string][]data.Record) []data.TreeNode {
	var out []data.TreeNode
	for _, nd := range nodes {
		kids := filterAgentThreadTreeNodes(nd.Children, threads)
		if isAgentThreadRecord(nd.Record, threads) || len(kids) > 0 {
			cp := nd
			cp.Children = kids
			out = append(out, cp)
		}
	}
	return out
}

func (a *App) applyOutlineViewMode(nodes []data.TreeNode, kind, treeID string) []data.TreeNode {
	mode := a.outlineViewMode(treeID)
	switch mode {
	case OutlineViewActive:
		return filterActiveTreeNodes(a.schema(kind), nodes)
	case OutlineViewAgentThreads:
		threads, _ := a.taskNoteThreads()
		return filterAgentThreadTreeNodes(nodes, threads)
	default:
		return nodes
	}
}

func (a *App) applyOutlineViewToProjectRoot(nodes []data.TreeNode, kind, treeID string) []data.TreeNode {
	mode := a.outlineViewMode(treeID)
	if mode == OutlineViewAll || len(nodes) == 0 {
		return nodes
	}
	out := slices.Clone(nodes)
	out[0].Children = a.applyOutlineViewMode(out[0].Children, kind, treeID)
	return out
}

func (a *App) outlineViewBadges(treeID string) []ui.Badge {
	sp := lookupOutlineViewSpec(a.outlineViewMode(treeID))
	if sp.Badge == "" {
		return nil
	}
	return []ui.Badge{{Text: sp.Badge, Tone: sp.Tone}}
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
	return fmt.Sprintf("|view:%s", mode)
}
