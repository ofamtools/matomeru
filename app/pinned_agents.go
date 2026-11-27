package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

// Commands for B2: Pin / Bookmark Agents (anywhere jump, cycle, send prompt).
const (
	CmdPanePin             = "pane.pin"
	CmdPaneUnpin           = "pane.unpin"
	CmdPaneTogglePin       = "pane.togglePin"
	CmdPaneJumpPinned      = "pane.jumpPinned"
	CmdPaneCyclePinnedNext = "pane.cyclePinnedNext"
	CmdPaneCyclePinnedPrev = "pane.cyclePinnedPrev"
	CmdPaneSendToPinned    = "pane.sendToPinned"
)

func (a *App) pinnedAgentChromeEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPinnedAgentChrome, true)
}

func pinnedAgentsCommandSpecs() []core.CommandSpec {
	paneParam := []core.ParamSpec{{Name: "pane", Type: core.ParamString, Description: "optional pane ID (defaults to focused or active pane)"}}
	return []core.CommandSpec{
		{
			ID:          CmdPanePin,
			Description: "Pin/bookmark the focused or active agent pane as the active pinned agent",
			Params:      paneParam,
			Meta:        map[string]any{"record": true, "kinds": []string{data.KindPane}},
		},
		{
			ID:          CmdPaneUnpin,
			Description: "Unpin the focused or active agent pane",
			Params:      paneParam,
			Meta:        map[string]any{"record": true, "kinds": []string{data.KindPane}},
		},
		{
			ID:          CmdPaneTogglePin,
			Description: "Toggle pin/bookmark state on the focused or active agent pane",
			Params:      paneParam,
			Meta:        map[string]any{"record": true, "kinds": []string{data.KindPane}},
		},
		spec(CmdPaneJumpPinned, "Jump straight to the active pinned agent pane from anywhere"),
		spec(CmdPaneCyclePinnedNext, "Cycle forward to the next pinned agent pane and jump to it"),
		spec(CmdPaneCyclePinnedPrev, "Cycle backward to the previous pinned agent pane and jump to it"),
		spec(CmdPaneSendToPinned, "Open the Capture/Compose modal pre-targeted at the active pinned agent"),
	}
}

func pinnedAgentsBindings() []core.Binding {
	n, all := ModeNormal, ""
	return []core.Binding{
		// Global anywhere bindings (automatically mirrored by ctrl+a leader in live terminal/editor).
		bind(n, all, "g k", CmdPaneJumpPinned, "jump to pinned agent"),
		bind(n, all, "g K", CmdPaneSendToPinned, "send to pinned agent"),
		bind(n, all, "g >", CmdPaneCyclePinnedNext, "next pinned agent"),
		bind(n, all, "g <", CmdPaneCyclePinnedPrev, "prev pinned agent"),
		bind(n, all, "g B", CmdPaneTogglePin, "pin/unpin agent pane"),

		// Contextual p on pane surfaces (also mirrored as ctrl+a p inside live terminal).
		when(bind(n, ScopeRail, "p", CmdPaneTogglePin, "pin/unpin agent"), "facts.onPane"),
		bind(n, scopeProjectPanes, "p", CmdPaneTogglePin, "pin/unpin agent"),
		when(bind(n, views.ScopeProjectNav, "p", CmdPaneTogglePin, "pin/unpin agent"), "facts.section == \"panes\" && facts.navNode"),
		when(bind(n, views.ScopeProjectTab, "p", CmdPaneTogglePin, "pin/unpin agent"), "facts.inChildPaneView"),
		bind(n, "terminal", "p", CmdPaneTogglePin, "pin/unpin agent"),
	}
}

func (a *App) registerPinnedAgents() {
	a.handle(CmdPanePin, a.panePin)
	a.handle(CmdPaneUnpin, a.paneUnpin)
	a.handle(CmdPaneTogglePin, a.paneTogglePin)
	a.handle(CmdPaneJumpPinned, a.paneJumpPinned)
	a.handle(CmdPaneCyclePinnedNext, a.paneCyclePinned(1))
	a.handle(CmdPaneCyclePinnedPrev, a.paneCyclePinned(-1))
	a.handle(CmdPaneSendToPinned, a.paneSendToPinned)
}

// --- Pin state queries & resolution --------------------------------------------

func (a *App) pinnedPanes() []data.Record {
	if a == nil || a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: false})
	if err != nil {
		return nil
	}
	return data.SortPinnedPanes(res.Items)
}

func (a *App) activePinnedPane() (*data.Record, int, int) {
	pinned := a.pinnedPanes()
	if len(pinned) == 0 {
		if a.panes != nil {
			a.panes.activePinnedPaneID = ""
		}
		return nil, -1, 0
	}
	idx := 0
	if a.panes != nil && a.panes.activePinnedPaneID != "" {
		if found := slices.IndexFunc(pinned, func(r data.Record) bool { return r.ID == a.panes.activePinnedPaneID }); found >= 0 {
			idx = found
		}
	}
	if a.panes != nil {
		a.panes.activePinnedPaneID = pinned[idx].ID
	}
	cp := pinned[idx]
	return &cp, idx, len(pinned)
}

func (a *App) resolvePinTargetPane(s core.State, c core.Command) (*data.Record, bool) {
	if a.Store == nil {
		return nil, false
	}
	if id := strings.TrimSpace(c.Arg("pane")); id != "" {
		rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: id})
		return rec, err == nil && rec != nil
	}
	if c.Target != nil && data.IsPaneRef(*c.Target) {
		rec, err := a.Store.Get(*c.Target)
		return rec, err == nil && rec != nil
	}
	if ref, ok := a.subject(s, c); ok && data.IsPaneRef(ref) {
		rec, err := a.Store.Get(ref)
		return rec, err == nil && rec != nil
	}
	if p, ok := a.activePane(s); ok && p != nil {
		return p, true
	}
	return a.termPane(s)
}

func (a *App) setPanePinned(pane data.Record, pinned bool) error {
	fields := map[string]any{"pinned": pinned}
	if pinned {
		fields["pinnedAt"] = a.now().UTC().Format(time.RFC3339)
	} else {
		fields["pinnedAt"] = ""
	}
	_, _, err := a.Store.Patch(data.PatchInput{Ref: pane.Ref(), Fields: fields}, User)
	if err != nil {
		return err
	}
	a.syncActivePinnedAfterChange(pane.ID, pinned)
	a.panes.railCacheKey = ""
	a.viewMemo.gen++
	return nil
}

func (a *App) syncActivePinnedAfterChange(paneID string, pinned bool) {
	if a.panes == nil {
		return
	}
	if pinned {
		a.panes.activePinnedPaneID = paneID
		return
	}
	if a.panes.activePinnedPaneID == paneID {
		a.panes.activePinnedPaneID = ""
		a.activePinnedPane()
	}
}

// --- Handlers ------------------------------------------------------------------

func (a *App) panePin(s core.State, c core.Command) core.StepResult {
	pane, ok := a.resolvePinTargetPane(s, c)
	if !ok {
		a.say("no pane selected to pin")
		return core.Next(s)
	}
	if err := a.setPanePinned(*pane, true); err != nil {
		a.fail("pin pane", err)
		return core.Next(s)
	}
	a.say("★ pinned %s", views.Title(a.schema(data.KindPane), *pane))
	return core.Next(s)
}

func (a *App) paneUnpin(s core.State, c core.Command) core.StepResult {
	pane, ok := a.resolvePinTargetPane(s, c)
	if !ok {
		a.say("no pane selected to unpin")
		return core.Next(s)
	}
	if err := a.setPanePinned(*pane, false); err != nil {
		a.fail("unpin pane", err)
		return core.Next(s)
	}
	a.say("unpinned %s", views.Title(a.schema(data.KindPane), *pane))
	return core.Next(s)
}

func (a *App) paneTogglePin(s core.State, c core.Command) core.StepResult {
	pane, ok := a.resolvePinTargetPane(s, c)
	if !ok {
		a.say("no pane selected to pin")
		return core.Next(s)
	}
	next := !pane.Bool("pinned")
	if err := a.setPanePinned(*pane, next); err != nil {
		a.fail("toggle pin", err)
		return core.Next(s)
	}
	title := views.Title(a.schema(data.KindPane), *pane)
	if next {
		a.say("★ pinned %s", title)
	} else {
		a.say("unpinned %s", title)
	}
	return core.Next(s)
}

func (a *App) paneJumpPinned(s core.State, _ core.Command) core.StepResult {
	pane, idx, total := a.activePinnedPane()
	if pane == nil {
		a.say("no pinned agent · press p on any pane to pin")
		return core.Next(s)
	}
	next := a.jumpToAgentPane(s, *pane)
	a.say("★ jumped to %s (%d/%d)", views.Title(a.schema(data.KindPane), *pane), idx+1, total)
	return core.Next(next)
}

func (a *App) paneCyclePinned(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		pinned := a.pinnedPanes()
		if len(pinned) == 0 {
			a.say("no pinned agents · press p on any pane to pin")
			return core.Next(s)
		}
		curIdx := -1
		if a.panes != nil && a.panes.activePinnedPaneID != "" {
			curIdx = slices.IndexFunc(pinned, func(r data.Record) bool { return r.ID == a.panes.activePinnedPaneID })
		}
		nextIdx := nextPinnedIndex(curIdx, len(pinned), delta)
		target := pinned[nextIdx]
		if a.panes != nil {
			a.panes.activePinnedPaneID = target.ID
		}
		next := a.jumpToAgentPane(s, target)
		a.say("★ pinned agent %d/%d: %s", nextIdx+1, len(pinned), views.Title(a.schema(data.KindPane), target))
		return core.Next(next)
	}
}

func nextPinnedIndex(curIdx, total, delta int) int {
	if total <= 1 {
		return 0
	}
	if curIdx < 0 {
		if delta < 0 {
			return total - 1
		}
		return 0
	}
	return (curIdx + delta + total) % total
}

func (a *App) paneSendToPinned(s core.State, _ core.Command) core.StepResult {
	pane, _, _ := a.activePinnedPane()
	if pane == nil {
		a.say("no pinned agent · opening capture modal (alt+a picks an agent)")
		return a.openCaptureCompose(s, core.Command{})
	}
	return a.openCaptureCompose(s, core.Command{Args: map[string]any{"pane": pane.ID}})
}

// jumpToAgentPane navigates from anywhere directly into pane's live view:
// inside its Project Portal tab when available, otherwise in 1 Panes.
func (a *App) jumpToAgentPane(s core.State, pane data.Record) core.State {
	s = a.dropModals(s)
	a.panes.activePage[pane.ID] = views.ScreenPageID
	if a.canOpenInProjectPortal(pane) {
		return a.jumpToPaneInPortal(s, pane)
	}
	rail := a.rail()
	selectRow(rail, data.RefText(pane.Ref()))
	s = a.openSpace(s, SpacePanes)
	s = withPage(s, SpacePanes, siteScreen, ModeNormal)
	return a.enterScreen(s, pane)
}

func (a *App) canOpenInProjectPortal(pane data.Record) bool {
	if !a.projectsDynamicTabsEnabled() || !a.projectWorkspaceEnabled() || !a.projectChildPaneViewEnabled() {
		return false
	}
	_, projRef := a.resolvePaneProject(pane)
	if projRef == nil || projRef.ID == "" {
		return false
	}
	return !a.focusRefuseProject(projRef.ID)
}

func (a *App) jumpToPaneInPortal(s core.State, pane data.Record) core.State {
	_, projRef := a.resolvePaneProject(pane)
	a.ensureProjectTabOpen(projRef.ID)
	tabID := projectTabID(projRef.ID)
	pc := a.projectCtrl()
	if pc.childPaneByProject == nil {
		pc.childPaneByProject = map[string]string{}
	}
	pc.childPaneByProject[projRef.ID] = pane.ID
	pc.setSection(projRef.ID, "panes")
	s = a.openSpace(s, tabID)
	s = withPage(s, tabID, siteScreen, ModeNormal)
	return a.enterScreen(s, pane)
}

// --- Chrome & Rail indicators --------------------------------------------------

// pinnedAgentChromeChip returns the subtle top-chrome indicator for the active
// pinned agent, or "" when no agent is pinned or ui.pinnedAgentChrome is off.
func (a *App) pinnedAgentChromeChip() string {
	if !a.pinnedAgentChromeEnabled() {
		return ""
	}
	pane, idx, total := a.activePinnedPane()
	if pane == nil {
		return ""
	}
	title := clip(views.Title(a.schema(data.KindPane), *pane), 22)
	if total > 1 {
		return fmt.Sprintf("★ %s (%d/%d)", title, idx+1, total)
	}
	return "★ " + title
}

// decoratePinnedRailRow adds a subtle ★ marker and pinned facts to a pane rail row.
func (a *App) decoratePinnedRailRow(row *table.Row, pane data.Record) {
	if row == nil || !pane.Bool("pinned") {
		return
	}
	row.Facts["pinned"] = true
	active, _, _ := a.activePinnedPane()
	isActive := active != nil && active.ID == pane.ID
	row.Facts["activePinned"] = isActive
	if cur, ok := row.Cells["rail"].(string); ok {
		row.Cells["rail"] = "★ " + cur
	}
	row.Badges = append(row.Badges, ui.Badge{Text: "★", Tone: "accent"})
}
