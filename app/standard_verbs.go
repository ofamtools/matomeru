package app

import (
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

// Canonical targetable standard verbs over ViewNode.ByID / ViewNode.Ancestors
// and ochazuke/traits.
const (
	CmdStandardCycle  = "cycle"
	CmdStandardFold   = "fold"
	CmdStandardToggle = "toggle"
)

const (
	standardTargetShowFoldEmpty     = "show.fold.empty"
	standardTargetShow              = "show"
	standardTargetWorkShow          = "work.show"
	standardTargetWorkPanel         = "work"
	standardTargetWorkLayout        = "work.layout"
	standardTargetPanesRail         = "panes.rail"
	standardTargetPanesGroup        = "panes.group"
	standardTargetProjectContext    = "project.context"
	standardTargetSite              = "site"
	standardTargetSiteContent       = "site.content"
	standardTargetSitePane          = "site.pane"
	standardTargetFilesPage         = "files.page"
	standardTargetHints             = "hints"
	standardTargetScreenHints       = "screen.hints"
	standardTargetScreenHintsOvl    = "screen.hints.overlay"
	standardTargetProjectNavNewItem = "project.nav.new_items"
	standardTargetProjectNavHome    = views.ProjectNavPrefix + "home"
	standardScopeShowFold           = "show.fold"
)

var foldActionToTreeCmd = map[string]string{
	"toggle":    tree.CmdFoldToggle,
	"open":      tree.CmdFoldOpen,
	"close":     tree.CmdFoldClose,
	"open_all":  tree.CmdFoldOpenAll,
	"close_all": tree.CmdFoldCloseAll,
}

func standardVerbSpecs() []core.CommandSpec {
	targetParam := core.ParamSpec{
		Name:        "target",
		Type:        core.ParamString,
		Description: "Optional ViewNode ID to target (defaults to the focused node)",
	}
	return []core.CommandSpec{
		{
			ID:          CmdStandardCycle,
			Description: "Cycle the targeted or focused view, tabs, presets, or mode",
			Params: []core.ParamSpec{
				targetParam,
				{Name: "dir", Type: core.ParamString, Enum: []string{"next", "prev"}, Default: "next", Description: "Cycle direction: next (1) or prev (-1)"},
			},
		},
		{
			ID:          CmdStandardFold,
			Description: "Fold or unfold the targeted or focused tree, group, or section",
			Params: []core.ParamSpec{
				targetParam,
				{Name: "action", Type: core.ParamString, Enum: []string{"toggle", "open", "close", "open_all", "close_all"}, Default: "toggle", Description: "Fold action: toggle, open, close, open_all, or close_all"},
			},
		},
		{
			ID:          CmdStandardToggle,
			Description: "Toggle the targeted or focused view layout or panel option",
			Params:      []core.ParamSpec{targetParam},
		},
	}
}

func (a *App) registerStandardVerbs() {
	a.handle(CmdStandardCycle, a.standardCycle)
	a.handle(CmdStandardFold, a.standardFold)
	a.handle(CmdStandardToggle, a.standardToggle)
}

type standardVerbContext struct {
	state     core.State
	cmd       core.Command
	root      ui.ViewNode
	node      *ui.ViewNode
	ancestors []*ui.ViewNode
	targetArg string
	dir       int
	action    string
	explicit  bool
}

type standardVerbHandler func(*App, standardVerbContext, *ui.ViewNode) (core.StepResult, bool)

type standardVerbTable struct {
	byID    map[string]standardVerbHandler
	byScope map[string]standardVerbHandler
	byTrait map[string]standardVerbHandler
	byKind  map[string]standardVerbHandler
}

type standardVerbRegistry struct {
	cycle  standardVerbTable
	fold   standardVerbTable
	toggle standardVerbTable
}

var standardVerbs = newStandardVerbRegistry()

func newStandardVerbRegistry() standardVerbRegistry {
	return standardVerbRegistry{
		cycle: standardVerbTable{
			byID: map[string]standardVerbHandler{
				railTable:                    (*App).stdCycleRail,
				standardTargetPanesRail:      (*App).stdCycleRail,
				workTable:                    (*App).stdCycleWorkIndex,
				standardTargetWorkPanel:      (*App).stdCycleWorkIndex,
				projectContextTable:          (*App).stdCycleProjectContext,
				standardTargetProjectContext: (*App).stdCycleProjectContext,
				standardTargetShow:           (*App).stdCycleShowCompact,
				standardTargetWorkShow:       (*App).stdCycleShowCompact,
				standardTargetSite:           (*App).stdToggleSiteTOC,
				views.SiteTOCID:              (*App).stdCycleTOCStyle,
				standardTargetSiteContent:    (*App).stdToggleSiteTOC,
				siteScreen:                   (*App).stdToggleSiteTOC,
				views.QueueListID:            (*App).stdCycleQueueTabs,
				views.QueuePageID:            (*App).stdCycleQueueTabs,
				views.FilesListID:            (*App).stdCycleFilesTabs,
				standardTargetFilesPage:      (*App).stdCycleFilesTabs,
				views.JobsListID:             (*App).stdCycleJobsTabs,
				standardTargetHints:          (*App).stdCycleHintsAction,
				standardTargetScreenHints:    (*App).stdCycleHintsAction,
				standardTargetScreenHintsOvl: (*App).stdCycleHintsAction,
				runsTable:                    (*App).stdCycleTableByID,
				extTable:                     (*App).stdCycleTableByID,
				outlineTree:                  (*App).stdCycleOutlineView,
				projectOutlineTree:           (*App).stdCycleOutlineView,
				projectOutlinePanel:          (*App).stdCycleOutlineView,
				featuresCaptureTree:          (*App).stdCycleOutlineView,
				featuresCapturePanel:         (*App).stdCycleOutlineView,
				featuresOutlineTree:          (*App).stdCycleOutlineView,
				featuresOutlinePanel:         (*App).stdCycleOutlineView,
			},
			byScope: map[string]standardVerbHandler{
				ScopeRail:           (*App).stdCycleRail,
				ScopeProjectContext: (*App).stdCycleProjectContext,
				views.ScopeShow:     (*App).stdCycleShowCompact,
				views.ScopeSite:     (*App).stdToggleSiteTOC,
				views.ScopeTOC:      (*App).stdCycleTOCStyle,
				views.ScopeQueue:    (*App).stdCycleQueueTabs,
				views.ScopeFiles:    (*App).stdCycleFilesTabs,
				views.ScopeJobs:     (*App).stdCycleJobsTabs,
				views.ScopeOutline:  (*App).stdCycleOutlineView,
			},
			byTrait: map[string]standardVerbHandler{
				traits.TraitCycle: (*App).stdCycleTableNode,
			},
			byKind: map[string]standardVerbHandler{
				ui.KindTable: (*App).stdCycleTableNode,
			},
		},
		fold: standardVerbTable{
			byID: map[string]standardVerbHandler{
				standardTargetShowFoldEmpty: (*App).stdFoldShowEmpty,
				outlineTree:                 (*App).stdFoldTreeNode,
				projectOutlineTree:          (*App).stdFoldTreeNode,
				featuresCaptureTree:         (*App).stdFoldTreeNode,
				featuresOutlineTree:         (*App).stdFoldTreeNode,
			},
			byScope: map[string]standardVerbHandler{
				standardScopeShowFold: (*App).stdFoldShowEmpty,
				views.ScopeTOC:        (*App).stdFoldTOCGroup,
				views.ScopeProjectNav: (*App).stdFoldProjectNav,
				views.ScopeOutline:    (*App).stdFoldTreeNode,
			},
			byKind: map[string]standardVerbHandler{
				ui.KindTree:  (*App).stdFoldTreeNode,
				ui.KindTable: (*App).stdFoldTableGroup,
			},
		},
		toggle: standardVerbTable{
			byID: map[string]standardVerbHandler{
				workTable:                       (*App).stdToggleWorkLayout,
				standardTargetWorkPanel:         (*App).stdToggleWorkLayout,
				standardTargetWorkLayout:        (*App).stdToggleWorkLayout,
				railTable:                       (*App).stdTogglePanesGroup,
				standardTargetPanesRail:         (*App).stdTogglePanesGroup,
				standardTargetPanesGroup:        (*App).stdTogglePanesGroup,
				standardTargetSite:              (*App).stdToggleSiteTOC,
				views.SiteTOCID:                 (*App).stdToggleSiteTOC,
				standardTargetSiteContent:       (*App).stdToggleSiteTOC,
				standardTargetSitePane:          (*App).stdToggleSiteTOC,
				siteScreen:                      (*App).stdToggleSiteTOC,
				standardTargetProjectNavNewItem: (*App).stdToggleProjectNavNewItems,
				standardTargetProjectNavHome:    (*App).stdToggleProjectNavNewItems,
				views.ScopeProjectNav:           (*App).stdToggleProjectNavNewItems,
			},
			byScope: map[string]standardVerbHandler{
				ScopeRail:             (*App).stdTogglePanesGroup,
				views.ScopeSite:       (*App).stdToggleSiteTOC,
				views.ScopeTOC:        (*App).stdToggleSiteTOC,
				views.ScopeProjectNav: (*App).stdToggleProjectNavNewItems,
			},
		},
	}
}

func parseCycleDir(args map[string]any) int {
	raw, ok := args["dir"]
	if !ok {
		raw = args["delta"]
	}
	switch v := raw.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "prev", "previous", "back", "backward", "-1":
			return -1
		}
	case int:
		if v < 0 {
			return -1
		}
	case int64:
		if v < 0 {
			return -1
		}
	case float64:
		if v < 0 {
			return -1
		}
	}
	return 1
}

func parseFoldAction(args map[string]any) string {
	act := strings.ToLower(strings.TrimSpace(strArg(args["action"])))
	if _, ok := foldActionToTreeCmd[act]; ok {
		return act
	}
	return "toggle"
}

func resolveFoldNext(cur bool, action string) bool {
	switch action {
	case "open", "open_all":
		return true
	case "close", "close_all":
		return false
	default:
		return !cur
	}
}

func strArg(v any) string {
	s, _ := v.(string)
	return s
}

func (a *App) buildStandardVerbContext(s core.State, c core.Command) standardVerbContext {
	targetArg := strings.TrimSpace(c.Arg("target"))
	ctx := standardVerbContext{
		state:     s,
		cmd:       c,
		root:      a.View(s),
		targetArg: targetArg,
		dir:       parseCycleDir(c.Args),
		action:    parseFoldAction(c.Args),
		explicit:  targetArg != "",
	}
	lookup := targetArg
	if lookup == "" {
		lookup = s.ActiveFrame().FocusID
	}
	if lookup != "" {
		ctx.node = ctx.root.ByID(lookup)
		ctx.ancestors = ctx.root.Ancestors(lookup)
	}
	return ctx
}

func (a *App) matchNodeInTable(tbl standardVerbTable, ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	if nd == nil {
		return core.StepResult{}, false
	}
	if fn := tbl.byID[nd.ID]; fn != nil {
		if res, ok := fn(a, ctx, nd); ok {
			return res, true
		}
	}
	if fn := tbl.byScope[nd.Scope]; fn != nil {
		if res, ok := fn(a, ctx, nd); ok {
			return res, true
		}
	}
	for _, tr := range nd.Traits {
		if fn := tbl.byTrait[tr]; fn != nil {
			if res, ok := fn(a, ctx, nd); ok {
				return res, true
			}
		}
	}
	if fn := tbl.byKind[nd.Kind]; fn != nil {
		if res, ok := fn(a, ctx, nd); ok {
			return res, true
		}
	}
	return core.StepResult{}, false
}

func (a *App) dispatchStandardVerb(tbl standardVerbTable, ctx standardVerbContext) (core.StepResult, bool) {
	chain := ctx.ancestors
	if len(chain) == 0 && ctx.node != nil {
		chain = []*ui.ViewNode{ctx.node}
	}
	for _, nd := range chain {
		if res, ok := a.matchNodeInTable(tbl, ctx, nd); ok {
			return res, true
		}
	}
	if ctx.explicit {
		if fn := tbl.byID[ctx.targetArg]; fn != nil {
			if res, ok := fn(a, ctx, ctx.node); ok {
				return res, true
			}
		}
		if fn := tbl.byScope[ctx.targetArg]; fn != nil {
			if res, ok := fn(a, ctx, ctx.node); ok {
				return res, true
			}
		}
	}
	return core.StepResult{}, false
}

// --- standard cycle / fold / toggle entrypoints ---------------------------------

func (a *App) standardCycle(s core.State, c core.Command) core.StepResult {
	return a.runStandardCycle(s, c)
}

func (a *App) runStandardCycle(s core.State, c core.Command) core.StepResult {
	ctx := a.buildStandardVerbContext(s, c)
	if !ctx.explicit {
		if res, ok := a.cycleActiveOverlay(ctx); ok {
			return res
		}
	}
	if res, ok := a.dispatchStandardVerb(standardVerbs.cycle, ctx); ok {
		return res
	}
	if !ctx.explicit {
		if tbl, tblID := a.focusedTableForPreset(s, c); tbl != nil && tblID != "" {
			a.cycleTablePreset(tblID, ctx.dir)
			return core.Next(s)
		}
	}
	a.say("no view modes to cycle here")
	return core.Next(s)
}

func (a *App) cycleActiveOverlay(ctx standardVerbContext) (core.StepResult, bool) {
	if res, ok := a.cycleHintsActionDir(ctx.state, ctx.dir); ok {
		return res, true
	}
	if a.hasOpenModal() {
		return a.cycleModalView(ctx.state, ctx.dir), true
	}
	return core.StepResult{}, false
}

func (a *App) standardFold(s core.State, c core.Command) core.StepResult {
	ctx := a.buildStandardVerbContext(s, c)
	if res, ok := a.dispatchStandardVerb(standardVerbs.fold, ctx); ok {
		return res
	}
	if ctx.explicit {
		if res, ok := a.foldExplicitDynamicTarget(ctx); ok {
			return res
		}
	}
	a.say("nothing to fold here")
	return core.Next(s)
}

func (a *App) foldExplicitDynamicTarget(ctx standardVerbContext) (core.StepResult, bool) {
	if group, ok := strings.CutPrefix(ctx.targetArg, views.TOCGroupPrefix); ok && group != "" {
		return a.foldTOCGroup(ctx.state, group, ctx.action), true
	}
	if sec, _ := projectNavFocusSection(ctx.targetArg); sec != "" {
		return a.foldProjectNavAt(ctx.state, ctx.targetArg, ctx.action)
	}
	if tbl := a.nav.tables[ctx.targetArg]; tbl != nil {
		return a.foldTableModel(ctx.state, tbl, ctx.action), true
	}
	return core.StepResult{}, false
}

func (a *App) standardToggle(s core.State, c core.Command) core.StepResult {
	ctx := a.buildStandardVerbContext(s, c)
	if res, ok := a.dispatchStandardVerb(standardVerbs.toggle, ctx); ok {
		return res
	}
	a.say("nothing to toggle here")
	return core.Next(s)
}

// --- cycle target handlers ------------------------------------------------------

func (a *App) stdCycleRail(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.cycleRailView(ctx.state, ctx.cmd, ctx.dir), true
}

func (a *App) stdCycleWorkIndex(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.cycleWorkIndexView(ctx.state, ctx.cmd, ctx.dir), true
}

func (a *App) stdCycleProjectContext(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	a.cycleProjectContextView(ctx.dir)
	return core.Next(ctx.state), true
}

func (a *App) stdCycleShowCompact(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.toggleShowCompact(ctx.state), true
}

func (a *App) stdCycleTOCStyle(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.cycleTOCStyle(ctx.state, ctx.dir), true
}

func (a *App) stdCycleQueueTabs(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.cycleQueueTabDir(ctx.state, ctx.dir*ctx.cmd.Times()), true
}

func (a *App) stdCycleFilesTabs(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.cycleFilesTabDir(ctx.state, ctx.dir*ctx.cmd.Times()), true
}

func (a *App) stdCycleJobsTabs(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.cycleJobsTabDir(ctx.state, ctx.dir*ctx.cmd.Times()), true
}

func (a *App) stdCycleHintsAction(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.cycleHintsActionDir(ctx.state, ctx.dir)
}

func (a *App) stdCycleTableByID(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	id := ctx.targetArg
	if nd != nil && nd.ID != "" {
		id = nd.ID
	}
	if id == "" || a.ensureTable(id) == nil {
		return core.StepResult{}, false
	}
	a.cycleTablePreset(id, ctx.dir)
	return core.Next(ctx.state), true
}

func (a *App) stdCycleTableNode(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	if nd == nil || nd.ID == "" || a.ensureTable(nd.ID) == nil {
		return core.StepResult{}, false
	}
	a.cycleTablePreset(nd.ID, ctx.dir)
	return core.Next(ctx.state), true
}

// --- fold target handlers -------------------------------------------------------

func (a *App) stdFoldShowEmpty(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.setShowFold(ctx.state, ctx.action), true
}

func (a *App) stdFoldTOCGroup(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	group := ""
	if nd != nil && nd.Facts != nil {
		group, _ = nd.Facts["tocGroup"].(string)
	}
	if group == "" && nd != nil {
		group, _ = strings.CutPrefix(nd.ID, views.TOCGroupPrefix)
	}
	if group == "" {
		return core.StepResult{}, false
	}
	return a.foldTOCGroup(ctx.state, group, ctx.action), true
}

func (a *App) stdFoldProjectNav(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	id := ctx.state.ActiveFrame().FocusID
	if nd != nil && nd.ID != "" {
		id = nd.ID
	} else if ctx.explicit {
		id = ctx.targetArg
	}
	return a.foldProjectNavAt(ctx.state, id, ctx.action)
}

func (a *App) stdFoldTreeNode(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	cmdID := foldActionToTreeCmd[ctx.action]
	if cmdID == "" {
		cmdID = tree.CmdFoldToggle
	}
	targetState := ctx.state
	if ctx.explicit && nd != nil && nd.ID != "" {
		targetState = withFocus(ctx.state, nd.ID)
	}
	res := a.treeFoldCommand(cmdID)(targetState, ctx.cmd)
	if ctx.explicit {
		res.State = withFocus(res.State, ctx.state.ActiveFrame().FocusID)
	}
	return res, true
}

func (a *App) stdFoldTableGroup(ctx standardVerbContext, nd *ui.ViewNode) (core.StepResult, bool) {
	if nd == nil || nd.ID == "" {
		return core.StepResult{}, false
	}
	tbl := a.nav.tables[nd.ID]
	if tbl == nil || tbl.GroupField == "" {
		return core.StepResult{}, false
	}
	return a.foldTableModel(ctx.state, tbl, ctx.action), true
}

func (a *App) foldTableModel(s core.State, tbl *table.Model, action string) core.StepResult {
	switch action {
	case "open_all":
		tbl.Collapsed = nil
	case "close_all":
		collapseAllTableGroups(tbl)
	case "open":
		setFocusedTableGroupCollapsed(tbl, false)
	case "close":
		setFocusedTableGroupCollapsed(tbl, true)
	default:
		tbl.ToggleGroup()
	}
	return core.Next(s)
}

func setFocusedTableGroupCollapsed(tbl *table.Model, collapsed bool) {
	if tbl == nil {
		return
	}
	it, ok := tbl.Focused()
	if !ok || it.Group == "" {
		return
	}
	if tbl.Collapsed == nil {
		tbl.Collapsed = map[string]bool{}
	}
	if collapsed {
		tbl.Collapsed[it.Group] = true
		return
	}
	delete(tbl.Collapsed, it.Group)
}

func collapseAllTableGroups(tbl *table.Model) {
	if tbl == nil {
		return
	}
	if tbl.Collapsed == nil {
		tbl.Collapsed = map[string]bool{}
	}
	for _, it := range tbl.Items() {
		if it.Header && it.Group != "" {
			tbl.Collapsed[it.Group] = true
		}
	}
}

// --- toggle target handlers -----------------------------------------------------

func (a *App) stdToggleWorkLayout(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.workLayoutToggle(ctx.state, ctx.cmd), true
}

func (a *App) stdTogglePanesGroup(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.panesGroupToggle(ctx.state, ctx.cmd), true
}

func (a *App) stdToggleSiteTOC(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.siteTOCToggle(ctx.state, ctx.cmd), true
}

func (a *App) stdToggleProjectNavNewItems(ctx standardVerbContext, _ *ui.ViewNode) (core.StepResult, bool) {
	return a.projectNavNewItemsToggle(ctx.state, ctx.cmd), true
}
