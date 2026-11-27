package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

// Commands for anywhere-in-portal cycling across Project Portal panes and sections.
const (
	CmdProjectPaneCycle    = "project.pane.cycle"
	CmdProjectSectionCycle = "project.section.cycle"
)

func projectCycleSpecs() []core.CommandSpec {
	dirParam := []core.ParamSpec{{
		Name:        "dir",
		Type:        core.ParamInt,
		Default:     1,
		Description: "cycle direction: 1 for next, -1 for previous",
	}}
	// Intentionally untagged (no "motion" or "editing" tag) so the terminal
	// leader layer (ctrl+a <key>) falls through automatically without bespoke
	// per-key ModeTerminalLeader rows.
	return []core.CommandSpec{
		{
			ID:          CmdProjectPaneCycle,
			Description: "Cycle to the next or previous pane anywhere inside the active Project Portal (ctrl+a mirror in live terminals)",
			Params:      dirParam,
		},
		{
			ID:          CmdProjectSectionCycle,
			Description: "Cycle to the next or previous left-nav section anywhere inside the active Project Portal (ctrl+a mirror in live terminals)",
			Params:      dirParam,
		},
	}
}

func projectCycleBindings() []core.Binding {
	scope := views.ScopeProjectOverview
	return []core.Binding{
		with(bind(ModeNormal, scope, "`", CmdProjectPaneCycle, "next project pane"), map[string]any{"dir": 1}),
		with(bind(ModeNormal, scope, "~", CmdProjectPaneCycle, "prev project pane"), map[string]any{"dir": -1}),
		with(bind(ModeNormal, scope, "g ]", CmdProjectSectionCycle, "next project section"), map[string]any{"dir": 1}),
		with(bind(ModeNormal, scope, "g [", CmdProjectSectionCycle, "prev project section"), map[string]any{"dir": -1}),
		with(bind(ModePage, scope, "`", CmdProjectPaneCycle, "next project pane"), map[string]any{"dir": 1}),
		with(bind(ModePage, scope, "~", CmdProjectPaneCycle, "prev project pane"), map[string]any{"dir": -1}),
		with(bind(ModePage, scope, "g ]", CmdProjectSectionCycle, "next project section"), map[string]any{"dir": 1}),
		with(bind(ModePage, scope, "g [", CmdProjectSectionCycle, "prev project section"), map[string]any{"dir": -1}),
	}
}

func (a *App) registerProjectCycleHandlers() {
	a.handle(CmdProjectPaneCycle, a.projectPaneCycle)
	a.handle(CmdProjectSectionCycle, a.projectSectionCycle)
}

func cycleWrapIndex(cur, total, dir int) int {
	if total <= 0 {
		return -1
	}
	if dir == 0 {
		dir = 1
	}
	if cur < 0 {
		if dir < 0 {
			return total - 1
		}
		return 0
	}
	return ((cur+dir)%total + total) % total
}

func (a *App) resolveActiveProjectWorkspace(s core.State) (string, projectWorkspaceData, bool) {
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return "", projectWorkspaceData{}, false
	}
	pc := a.projectCtrl()
	if pc.activeProjectKey != projKey {
		a.View(s)
	}
	groupName := pc.activeGroupName
	projRef := pc.activeProjectRef
	if projRef == nil && a.Store != nil {
		if pid := a.activePortalProjectID(s); pid != "" {
			r := core.Ref{Kind: data.KindTask, ID: pid}
			projRef = &r
			if groupName == "" {
				groupName = a.title(r)
			}
		}
	}
	ws := a.collectProjectWorkspaceData(groupName, projRef)
	return projKey, ws, true
}

func (a *App) currentPortalPaneID(s core.State, projKey string) string {
	if paneID := a.activeChildPaneID(s); paneID != "" {
		return paneID
	}
	cur := s.ActiveFrame().FocusID
	if target, ok := a.projectCtrl().navTarget(projKey, cur); ok && target.Section == "panes" && target.Ref != nil {
		return target.Ref.ID
	}
	if tm := a.projectCtrl().trees[projectPanesTree]; tm != nil && cur == projectPanesTree {
		if row, ok := tm.Focused(); ok && row.Node.Ref != nil {
			return row.Node.Ref.ID
		}
	}
	return ""
}

func (a *App) portalPaneReturnFocus(s core.State, projKey string) string {
	pc := a.projectCtrl()
	if pc.childPaneReturnFocus != nil {
		if saved := strings.TrimSpace(pc.childPaneReturnFocus[projKey]); saved != "" {
			return saved
		}
	}
	cur := s.ActiveFrame().FocusID
	if cur != "" && cur != siteScreen && !strings.HasPrefix(cur, tocPrefix) {
		return cur
	}
	return projectPanesTree
}

// projectPaneCycle cycles through the active Project Portal's panes from
// anywhere in the portal (Home, Tasks, Notes, TOC, Screen, or live terminal
// via ctrl+a ` / ctrl+a ~). Invoking from a live terminal stays live in
// ModeTerminal on the next pane; invoking from normal mode opens the pane
// inline in ModeNormal so repeated presses keep cycling cleanly.
func (a *App) projectPaneCycle(s core.State, c core.Command) core.StepResult {
	projKey, ws, ok := a.resolveActiveProjectWorkspace(s)
	if !ok {
		a.say("open a project portal first")
		return core.Next(s)
	}
	if len(ws.panes) == 0 {
		a.say("no panes in project")
		return core.Next(s)
	}
	dir := parseCycleDir(c.Args)
	curID := a.currentPortalPaneID(s, projKey)
	curIdx := slices.IndexFunc(ws.panes, func(p data.Record) bool { return p.ID == curID })
	nextIdx := cycleWrapIndex(curIdx, len(ws.panes), dir)
	nextPane := ws.panes[nextIdx]

	wasLive := a.Engine.Mode(s) == ModeTerminal
	retFocus := a.portalPaneReturnFocus(s, projKey)
	pc := a.projectCtrl()
	pc.setSection(projKey, "panes")
	pc.setOpenPageID(projKey, "")
	a.syncProjectNavPointer("panes")

	nextState := a.openProjectChildPaneInPlace(s, projKey, nextPane.ID, retFocus, wasLive)
	title := views.Title(a.schema(data.KindPane), nextPane)
	a.say("pane %d/%d: %s", nextIdx+1, len(ws.panes), title)
	return core.Next(nextState)
}

// projectSectionCycle cycles through the active Project Portal's left-nav
// sections from anywhere in the portal (including live terminals via ctrl+a g ]
// or ctrl+a g [).
func (a *App) projectSectionCycle(s core.State, c core.Command) core.StepResult {
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		a.say("open a project portal first")
		return core.Next(s)
	}
	defs := a.projectNavSectionDefs()
	if len(defs) == 0 {
		return core.Next(s)
	}
	pc := a.projectCtrl()
	activeSec := pc.activeSection(projKey)
	curIdx := slices.IndexFunc(defs, func(d projectNavDef) bool { return d.Key == activeSec })
	nextIdx := cycleWrapIndex(curIdx, len(defs), parseCycleDir(c.Args))
	nextDef := defs[nextIdx]

	if a.Engine.Mode(s) == ModeTerminal || a.Engine.Mode(s) == ModePage {
		s = withMode(s, ModeNormal)
	}
	res := a.projectSectionSelect(s, core.Command{
		ID:   CmdProjectSectionSelect,
		Args: map[string]any{"section": nextDef.Key},
	})
	a.say("section %d/%d: %s", nextIdx+1, len(defs), nextDef.Label)
	return res
}
