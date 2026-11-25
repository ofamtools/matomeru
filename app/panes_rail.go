package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

const (
	projectContextTable    = "project.context.table"
	ScopeProjectContext    = "project.context"
	ScopeProjectOverview   = "project.overview"
	unassignedProjectLabel = "Unassigned"
)

// panesGroupMode returns the effective rail grouping setting ("project-pane" or "flat").
func (a *App) panesGroupMode() string {
	if a.cfg.Settings != nil && a.cfg.Settings.String(SetPanesGroup, PanesGroupProjectPane) == PanesGroupFlat {
		return PanesGroupFlat
	}
	return PanesGroupProjectPane
}

// panesGroupToggle toggles panes.group between "project-pane" and "flat".
func (a *App) panesGroupToggle(s core.State, _ core.Command) core.StepResult {
	nextMode := PanesGroupFlat
	if a.panesGroupMode() == PanesGroupFlat {
		nextMode = PanesGroupProjectPane
	}
	a.cfg.userSettings[SetPanesGroup] = nextMode
	a.setActivePreset(railTable, "")
	a.panes.railGroupSet = false
	a.panes.railCustomGroup = ""
	a.reloadSettings()
	a.applySettings()
	if tbl := a.nav.tables[railTable]; tbl != nil {
		if nextMode == PanesGroupProjectPane {
			tbl.GroupField = "project"
		} else {
			tbl.GroupField = ""
		}
		tbl.Collapsed = nil
	}
	a.rail()
	if saved, err := a.writeConfig(settingsFileName, a.cfg.userSettings); err != nil {
		a.fail("save panes.group", err)
	} else if saved {
		a.say("panes rail: %s · saved", nextMode)
	} else {
		a.say("panes rail: %s", nextMode)
	}
	return core.Next(s)
}

// topLevelTask walks parent refs up to the root top-level task with cycle protection.
func (a *App) topLevelTask(ref core.Ref) (core.Ref, bool) {
	if a.Store == nil || ref.Kind != data.KindTask || ref.ID == "" {
		return core.Ref{}, false
	}
	cur, err := a.Store.Get(ref)
	if err != nil || cur == nil {
		return core.Ref{}, false
	}
	seen := map[string]bool{cur.ID: true}
	for range 32 {
		parentRef, hasParent := cur.RefField("parent")
		if !hasParent || parentRef.Kind != data.KindTask || parentRef.ID == "" || seen[parentRef.ID] {
			break
		}
		parent, err := a.Store.Get(parentRef)
		if err != nil || parent == nil {
			break
		}
		seen[parent.ID] = true
		cur = parent
	}
	return cur.Ref(), true
}

// railActiveProjectTask resolves the active top-level project task from the
// left rail selection: either the focused project group header or the focused
// pane's task walked up to its top-level root task.
func (a *App) railActiveProjectTask(_ core.State) (core.Ref, bool) {
	if _, projRef, onProj := a.selectedRailProject(); onProj {
		if projRef == nil {
			return core.Ref{}, false
		}
		if root, ok := a.topLevelTask(*projRef); ok {
			return root, true
		}
		return *projRef, true
	}
	if pane, ok := a.SelectedPane(); ok && pane != nil {
		if taskRef, hasTask := pane.RefField("task"); hasTask {
			return a.topLevelTask(taskRef)
		}
	}
	return core.Ref{}, false
}

// projectPageOpen opens the Project Page workspace in 1 Panes (or the Show page
// when project.workspaceInPanes is opted out).
func (a *App) projectPageOpen(s core.State, c core.Command) core.StepResult {
	return a.projectHomeOpen(s, c)
}

// resolvePaneProject walks a pane's task -> parent chain up to its root project
// task. Panes without a task (or whose task cannot be found) belong to
// "Unassigned".
func (a *App) resolvePaneProject(pane data.Record) (string, *core.Ref) {
	taskRef, ok := pane.RefField("task")
	if !ok || taskRef.Kind != data.KindTask || taskRef.ID == "" {
		return unassignedProjectLabel, nil
	}
	rootRef, ok := a.topLevelTask(taskRef)
	if !ok {
		return unassignedProjectLabel, nil
	}
	cur, err := a.Store.Get(rootRef)
	if err != nil || cur == nil {
		return unassignedProjectLabel, nil
	}
	ref := cur.Ref()
	title := views.Title(a.schema(data.KindTask), *cur)
	if strings.TrimSpace(title) == "" {
		title = cur.ID
	}
	return title, &ref
}

// selectGroupHeader moves the table cursor to the group header for groupName.
func selectGroupHeader(tbl *table.Model, groupName string) bool {
	if tbl == nil || groupName == "" {
		return false
	}
	for i, it := range tbl.Items() {
		if it.Header && it.Group == groupName {
			tbl.SelectedIdx = i
			tbl.Focused()
			return true
		}
	}
	return false
}

// buildRail builds the pane rail: panes strictly ordered by the owner's last
// prompt (promptedAt descending). Agent output, hook telemetry, and needs_you
// NEVER reorder panes; instead status is shown via row indicator symbols and
// tone. When panes.group == "project-pane", rows are grouped under their root
// project (or "Unassigned") while preserving stable promptedAt order within
// every group and across groups.
func (a *App) buildRail() *table.Model {
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: a.nav.showArchived})
	if err != nil {
		a.fail("read panes", err)
	}
	panes := res.Items
	if !a.nav.showArchived {
		panes = a.appendAnimatingRecords(data.KindPane, panes)
	}
	slices.SortStableFunc(panes, func(x, y data.Record) int {
		return y.Time("promptedAt").Compare(x.Time("promptedAt"))
	})

	paneSchema := a.schema(data.KindPane)
	styleOpts := a.styleOptions()
	fresh := views.TableFromRecordsWith(paneSchema, panes, styleOpts)
	showStateCol := a.cfg != nil && a.cfg.Settings != nil && a.cfg.Settings.Bool(SetRailStateColumn, false)
	if showStateCol {
		fresh.Columns = []table.Column{
			{Key: "rail", Title: "Pane"},
			{Key: "runStatus", Title: "State", Width: 11},
		}
	} else {
		fresh.Columns = []table.Column{
			{Key: "rail", Title: "Pane"},
		}
	}

	projRefs := map[string]core.Ref{}
	groupPanes := map[string][]data.Record{}
	for i := range fresh.Rows {
		row := &fresh.Rows[i]
		pane := panes[i]
		row.Facts["onPane"] = true
		projTitle, projRef := a.resolvePaneProject(pane)
		row.Cells["project"] = projTitle
		groupPanes[projTitle] = append(groupPanes[projTitle], pane)
		if projRef != nil {
			projRefs[projTitle] = *projRef
			row.Facts["projectId"] = projRef.ID
			row.Facts["projectTitle"] = projTitle
		}

		title := views.Title(paneSchema, pane)
		if a.attention(row.Ref.ID) {
			row.Unread = true
			row.Cells["rail"] = "● " + title
		} else if showStateCol {
			row.Cells["rail"] = "  " + title
		} else {
			row.Cells["rail"] = title
		}

		baseState := pane.Text("runStatus")
		icon, label, baseTone, _ := views.StatusIndicator(paneSchema, "runStatus", baseState)
		row.Cells["runStatus"] = label
		if styleOpts.StatusIcons && icon != "" {
			if row.CellIcons == nil {
				row.CellIcons = map[string]string{}
			}
			row.CellIcons["runStatus"] = icon
		}
		row.Facts["statusTone"] = baseTone
		a.decorateRailRow(row, pane)
		if !showStateCol {
			stateIcon := ""
			if row.CellIcons != nil {
				stateIcon = row.CellIcons["runStatus"]
			}
			if stateIcon == "" {
				stateIcon = icon
			}
			if stateIcon != "" {
				if row.CellIcons == nil {
					row.CellIcons = map[string]string{}
				}
				row.CellIcons["rail"] = stateIcon
				if styleOpts.RowSemanticStyling {
					stateTone := ""
					if row.IconTones != nil {
						stateTone = row.IconTones["runStatus"]
					}
					if stateTone == "" {
						if t, ok := row.Facts["statusTone"].(string); ok {
							stateTone = t
						}
					}
					if stateTone != "" {
						if row.IconTones == nil {
							row.IconTones = map[string]string{}
						}
						row.IconTones["rail"] = stateTone
					}
				}
			}
		}
		if _, hasTask := pane.RefField("task"); !hasTask {
			if cur, ok := row.Cells["rail"].(string); ok {
				row.Cells["rail"] = cur + " [unlinked]"
			}
			row.Badges = append(row.Badges, ui.Badge{Text: "[unlinked]", Tone: "warning"})
			row.Facts["unlinked"] = true
			row.Facts["statusTone"] = "warning"
		}
		if qn := a.queuedPromptCount(pane.ID); qn > 0 {
			if cur, ok := row.Cells["rail"].(string); ok {
				row.Cells["rail"] = fmt.Sprintf("%s [Q:%d]", cur, qn)
			}
			row.Facts["queuedPrompts"] = qn
		}
		if a.openable(pane) {
			row.Facts[factOpenable] = true
		}
		a.applyArchiveAnimToRow(row, "rail", data.Text(row.Cells["rail"]), pane.Ref())
	}
	a.panes.railProjectRefs = projRefs
	a.panes.railGroupPanes = groupPanes

	old := a.nav.tables[railTable]
	var oldRowID, oldHeaderGroup string
	if old != nil {
		if it, ok := old.Focused(); ok {
			if it.Header {
				oldHeaderGroup = it.Group
			} else {
				oldRowID = it.Row.ID
			}
		}
	}

	tbl := keep(old, fresh)
	switch {
	case a.panes.railGroupSet:
		tbl.GroupField = a.panes.railCustomGroup
	case a.panesGroupMode() == PanesGroupProjectPane:
		tbl.GroupField = "project"
	default:
		tbl.GroupField = ""
	}
	if old == nil {
		tbl.MatchMode = a.matchMode()
		a.applyDefaultPreset(tbl, railTable)
	}

	switch {
	case old == nil:
		// First render: land on the most-recently-prompted pane rather than a group header.
		if len(tbl.Rows) > 0 {
			selectRow(tbl, tbl.Rows[0].ID)
		}
	case oldHeaderGroup != "" && tbl.GroupField != "":
		if !selectGroupHeader(tbl, oldHeaderGroup) && len(tbl.Rows) > 0 {
			selectRow(tbl, tbl.Rows[0].ID)
		}
	case oldRowID != "":
		if !selectRow(tbl, oldRowID) && len(tbl.Rows) > 0 {
			selectRow(tbl, tbl.Rows[0].ID)
		}
	case tbl.GroupField != "" && len(tbl.Rows) > 0:
		if it, ok := tbl.Focused(); ok && it.Header {
			selectRow(tbl, tbl.Rows[0].ID)
		}
	}

	a.pageSize(tbl, railTable)
	a.nav.tables[railTable] = tbl
	return tbl
}

// selectedRailProject reports whether the pane rail cursor is currently on a
// Project group header row.
func (a *App) selectedRailProject() (groupName string, projRef *core.Ref, ok bool) {
	tbl := a.rail()
	it, focused := tbl.Focused()
	if !focused || !it.Header {
		return "", nil, false
	}
	if ref, exists := a.panes.railProjectRefs[it.Group]; exists {
		r := ref
		return it.Group, &r, true
	}
	return it.Group, nil, true
}

// focusedProjectContextItemFor returns the CollectedContextItem currently
// focused in focusID (or in the project context / pinned / PRs tables).
func (a *App) focusedProjectContextItemFor(focusID string) (CollectedContextItem, bool) {
	if a.panes == nil || a.nav == nil {
		return CollectedContextItem{}, false
	}
	seen := map[string]bool{}
	for _, id := range []string{focusID, projectContextTable, projectPinnedTable, projectPRsTable} {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		tbl := a.nav.tables[id]
		if tbl == nil {
			continue
		}
		row, ok := tbl.FocusedRow()
		if !ok {
			continue
		}
		if it, exists := a.panes.projectCtxRows[row.ID]; exists {
			return it, true
		}
	}
	return CollectedContextItem{}, false
}

// focusedProjectContextItem returns the CollectedContextItem currently focused
// in the active project context table.
func (a *App) focusedProjectContextItem() (CollectedContextItem, bool) {
	focusID := ""
	if a.Shell != nil {
		focusID = a.Focus()
	}
	return a.focusedProjectContextItemFor(focusID)
}

// projectContextOpen handles opening the focused context row or toggling a group.
func (a *App) projectContextOpen(s core.State, _ core.Command) core.StepResult {
	focus := s.ActiveFrame().FocusID
	if tbl := a.nav.tables[focus]; tbl != nil {
		if it, ok := tbl.Focused(); ok && it.Header {
			tbl.ToggleGroup()
			return core.Next(s)
		}
	}
	it, ok := a.focusedProjectContextItemFor(focus)
	if !ok {
		a.say("no context item selected")
		return core.Next(s)
	}
	var pane *data.Record
	if it.PaneID != "" {
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: it.PaneID}); err == nil {
			pane = rec
		}
	}
	return a.openContextValue(s, it.Kind, it.Value, pane)
}

func (a *App) projectContextYank(s core.State, _ core.Command) core.StepResult {
	it, ok := a.focusedProjectContextItemFor(s.ActiveFrame().FocusID)
	if !ok {
		a.say("no context item selected")
		return core.Next(s)
	}
	a.yank(it.Value, it.Kind)
	return core.Next(s)
}

func (a *App) projectContextStar(s core.State, _ core.Command) core.StepResult {
	it, ok := a.focusedProjectContextItemFor(s.ActiveFrame().FocusID)
	if !ok {
		a.say("no context item selected")
		return core.Next(s)
	}
	starred := a.toggleContextStar(it.Kind, it.Value)
	if starred {
		a.say("starred %s", clip(it.Value, 48))
	} else {
		a.say("unstarred %s", clip(it.Value, 48))
	}
	return core.Next(s)
}

func (a *App) projectContextAttach(s core.State, _ core.Command) core.StepResult {
	it, ok := a.focusedProjectContextItemFor(s.ActiveFrame().FocusID)
	if !ok {
		a.say("no context item selected")
		return core.Next(s)
	}
	target := a.panes.projectCtxRef
	if target == nil && it.PaneID != "" {
		r := core.Ref{Kind: data.KindPane, ID: it.PaneID}
		target = &r
	}
	return core.Next(a.attachContextItem(s, it, target))
}
