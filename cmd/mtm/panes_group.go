package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// The Panes tree's levels are the panes.group setting: project → pane
// (the default; a project's panes from all its sessions), project →
// session → pane, session → pane, or flat (panes alone). Sessions stay in
// the data and their verbs on their rows wherever a grouping shows them.
// While a project is pinned the pin is the project level, so it is
// dropped: project-pane lists the pin's panes flat, project-session-pane
// and session-pane its sessions then panes, flat its panes. The attached
// pane outside the pin keeps its labelled "outside pin" group in every
// grouping. Rows that need input sort first in every grouping.

const settingPanesGroup = "panes.group"

const (
	groupProjectPane        = "project-pane"
	groupProjectSessionPane = "project-session-pane"
	groupSessionPane        = "session-pane"
	groupFlat               = "flat"
)

// paneGroups are panes.group's values in cycle order.
var paneGroups = []string{groupProjectPane, groupProjectSessionPane, groupSessionPane, groupFlat}

var paneGroupSetting = ochazuke.Setting{Name: settingPanesGroup, Type: ochazuke.SettingString, Default: groupProjectPane, Enum: paneGroups,
	Description: "How the Panes tree groups its rows: project-pane (projects, then their panes), project-session-pane (projects, sessions, panes), session-pane (sessions, then panes) or flat; while pinned the project level is the pin"}

// paneGrouping is the panes.group setting, the default when unset.
func (a *App) paneGrouping() string {
	if g := a.r.Settings().String(settingPanesGroup); slices.Contains(paneGroups, g) {
		return g
	}
	return groupProjectPane
}

// definePaneGroups adds panes.group and panes.cycle-group (palette and
// CLI; no key: z and Z are kept for ochazuke's generic grouping) and
// regroups the tree whenever the setting changes, from either verb, the
// Settings page or settings.json.
func (a *App) definePaneGroups() {
	a.r.Define("panes").
		Verb("group", a.setPaneGroup, "Group the pane list: project-pane, project-session-pane, session-pane or flat (saves panes.group)").
		Verb("cycle-group", a.cyclePaneGroup, "Group the pane list the next way (delta -1: the previous): project-pane, project-session-pane, session-pane, flat")
	a.parameters("panes.group", map[string]ochazuke.Parameter{"group": {Type: "string", Required: true, Enum: paneGroups}})
	a.parameters("panes.cycle-group", map[string]ochazuke.Parameter{"delta": {Type: "integer"}})
	a.r.Settings().Observe(settingPanesGroup, func(string, any) {
		if err := a.regroupPanes(); err != nil {
			a.r.UI().Notify("panes.group: " + err.Error())
		}
	})
}

func (a *App) setPaneGroup(ctx ochazuke.Ctx) ochazuke.Result {
	group := ochazuke.StringArg(ctx.Args, "group", "")
	if !slices.Contains(paneGroups, group) {
		return ochazuke.Failed(fmt.Errorf("group %q: one of %s", group, strings.Join(paneGroups, ", ")))
	}
	if err := a.r.Settings().Set(settingPanesGroup, group); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(group)
}

func (a *App) cyclePaneGroup(ctx ochazuke.Ctx) ochazuke.Result {
	delta := ochazuke.IntArg(ctx.Args, "delta", 1)
	n := len(paneGroups)
	next := paneGroups[((slices.Index(paneGroups, a.paneGrouping())+delta)%n+n)%n]
	if err := a.r.Settings().Set(settingPanesGroup, next); err != nil {
		return ochazuke.Failed(err)
	}
	ctx.Notify("panes grouped " + next)
	return ochazuke.Done(next)
}

// regroupPanes redraws the tree from the current listing; the selected
// pane stays selected, and a group row that is gone gives way to the
// first pane.
func (a *App) regroupPanes() error {
	if a.pv.tree == nil {
		return nil
	}
	before, had := a.pv.tree.Selected()
	if err := a.rebuildPanes(); err != nil {
		return err
	}
	if after, ok := a.pv.tree.Selected(); ok && had && after.ID == before.ID {
		return nil
	}
	if first := a.firstPaneRow(); first != "" {
		return a.pv.tree.Select(first)
	}
	return nil
}
