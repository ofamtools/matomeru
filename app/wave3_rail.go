package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
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
	if a.Settings != nil && a.Settings.String(SetPanesGroup, PanesGroupProjectPane) == PanesGroupFlat {
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
	a.userSettings[SetPanesGroup] = nextMode
	a.railGroupSet = false
	a.railCustomGroup = ""
	a.reloadSettings()
	a.applySettings()
	if tbl := a.tables[railTable]; tbl != nil {
		if nextMode == PanesGroupProjectPane {
			tbl.GroupField = "project"
		} else {
			tbl.GroupField = ""
		}
		tbl.Collapsed = nil
	}
	a.rail()
	if saved, err := a.writeConfig(settingsFileName, a.userSettings); err != nil {
		a.fail("save panes.group", err)
	} else if saved {
		a.say("panes rail: %s · saved", nextMode)
	} else {
		a.say("panes rail: %s", nextMode)
	}
	return core.Next(s)
}

// resolvePaneProject walks a pane's task -> parent chain up to its root project
// task. Panes without a task (or whose task cannot be found) belong to
// "Unassigned".
func (a *App) resolvePaneProject(pane data.Record) (string, *core.Ref) {
	taskRef, ok := pane.RefField("task")
	if !ok || taskRef.Kind != data.KindTask || taskRef.ID == "" {
		return unassignedProjectLabel, nil
	}
	cur, err := a.Store.Get(taskRef)
	if err != nil || cur == nil {
		return unassignedProjectLabel, nil
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

// rail returns the pane rail: panes strictly ordered by the owner's last prompt
// (promptedAt descending). Agent output, hook telemetry, and needs_you NEVER
// reorder panes; instead status is shown via row indicator symbols and tone.
// When panes.group == "project-pane", rows are grouped under their root project
// (or "Unassigned") while preserving stable promptedAt order within every group
// and across groups.
func (a *App) buildRail() *table.Model {
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: a.showArchived})
	if err != nil {
		a.fail("read panes", err)
	}
	panes := res.Items
	slices.SortStableFunc(panes, func(x, y data.Record) int {
		return y.Time("promptedAt").Compare(x.Time("promptedAt"))
	})

	paneSchema := a.schema(data.KindPane)
	styleOpts := a.styleOptions()
	fresh := views.TableFromRecordsWith(paneSchema, panes, styleOpts)
	fresh.Columns = []table.Column{
		{Key: "rail", Title: "Pane"},
		{Key: "runStatus", Title: "State", Width: 11},
	}

	projRefs := map[string]core.Ref{}
	groupPanes := map[string][]data.Record{}
	for i := range fresh.Rows {
		row := &fresh.Rows[i]
		pane := panes[i]
		projTitle, projRef := a.resolvePaneProject(pane)
		row.Cells["project"] = projTitle
		groupPanes[projTitle] = append(groupPanes[projTitle], pane)
		if projRef != nil {
			projRefs[projTitle] = *projRef
			row.Facts["projectId"] = projRef.ID
			row.Facts["projectTitle"] = projTitle
		}

		mark := "  "
		if a.attention(row.Ref.ID) {
			mark, row.Unread = "● ", true
		}
		row.Cells["rail"] = mark + views.Title(paneSchema, pane)

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
	}
	a.railProjectRefs = projRefs
	a.railGroupPanes = groupPanes

	old := a.tables[railTable]
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
	case a.railGroupSet:
		tbl.GroupField = a.railCustomGroup
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
	a.tables[railTable] = tbl
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
	if ref, exists := a.railProjectRefs[it.Group]; exists {
		r := ref
		return it.Group, &r, true
	}
	return it.Group, nil, true
}

// projectDescendants collects all task records that belong to rootID.
func (a *App) projectDescendants(rootID string) []data.Record {
	if rootID == "" {
		return nil
	}
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		return nil
	}
	var out []data.Record
	var collect func(ns []data.TaskNode)
	collect = func(ns []data.TaskNode) {
		for _, n := range ns {
			out = append(out, n.Record)
			collect(n.Children)
		}
	}
	for _, root := range nodes {
		if root.Record.ID == rootID {
			collect(root.Children)
			break
		}
	}
	return out
}

// projectLanes collects active lanes associated with a project, its descendant
// tasks, or its panes.
func (a *App) projectLanes(projRef *core.Ref, taskIDs map[string]bool, paneIDs map[string]bool) []data.Record {
	res, err := a.Store.Query(data.Query{Kind: data.KindLane, IncludeArchived: a.showArchived})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []data.Record
	add := func(rec data.Record) {
		if seen[rec.ID] {
			return
		}
		status := strings.ToLower(strings.TrimSpace(rec.Text("laneStatus")))
		if status == "done" || status == "merged" || status == "cancelled" {
			return
		}
		seen[rec.ID] = true
		out = append(out, rec)
	}
	for _, lane := range res.Items {
		if t, ok := lane.RefField("task"); ok && (taskIDs[t.ID] || (projRef != nil && t.ID == projRef.ID)) {
			add(lane)
			continue
		}
		rels, err := a.Store.Related(lane.Ref(), data.RelClaim, data.Outgoing)
		if err != nil {
			continue
		}
		for _, r := range rels {
			if r.To.Kind == data.KindPane && paneIDs[r.To.ID] {
				add(lane)
				break
			}
		}
	}
	return out
}

// projectNotes collects notes associated with a project, its descendant tasks,
// or its panes.
func (a *App) projectNotes(projRef *core.Ref, taskIDs map[string]bool, paneIDs map[string]bool) []data.Record {
	res, err := a.Store.Query(data.Query{Kind: data.KindNote, IncludeArchived: a.showArchived})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []data.Record
	add := func(rec data.Record) {
		if seen[rec.ID] {
			return
		}
		seen[rec.ID] = true
		out = append(out, rec)
	}
	for _, n := range res.Items {
		if t, ok := n.RefField("task"); ok && (taskIDs[t.ID] || (projRef != nil && t.ID == projRef.ID)) {
			add(n)
			continue
		}
		rels, err := a.Store.Related(n.Ref(), data.RelAbout, data.Outgoing)
		if err != nil {
			continue
		}
		for _, r := range rels {
			if (projRef != nil && r.To == *projRef) || (r.To.Kind == data.KindTask && taskIDs[r.To.ID]) || (r.To.Kind == data.KindPane && paneIDs[r.To.ID]) {
				add(n)
				break
			}
		}
	}
	return out
}

// projectLinks collects documents, directory/context relations, and URL links
// associated with a project or its panes.
func (a *App) projectLinks(projRef *core.Ref, taskIDs map[string]bool, ctxItems []CollectedContextItem) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if projRef != nil {
		if rec, err := a.Store.Get(*projRef); err == nil {
			if dir := rec.Text("defaultDir"); dir != "" {
				add("dir: " + dir)
			}
			if u := rec.Text("url"); u != "" {
				add(u)
			}
		}
		for _, relKind := range []string{data.RelDirectory, data.RelContext} {
			if rels, err := a.Store.Related(*projRef, relKind, data.Outgoing); err == nil {
				for _, r := range rels {
					add(r.To.ID)
				}
			}
		}
		if docs, err := a.Store.Query(data.Query{Kind: data.KindDocument, IncludeArchived: a.showArchived}); err == nil {
			for _, d := range docs.Items {
				if t, ok := d.RefField("task"); ok && (t.ID == projRef.ID || taskIDs[t.ID]) {
					if fp := d.Text("filePath"); fp != "" {
						add(views.Title(a.schema(data.KindDocument), d) + " (" + fp + ")")
					}
				}
			}
		}
	}
	for _, it := range ctxItems {
		if it.Kind == "url" || it.Kind == "osc8" || it.Kind == "pr" {
			add(it.Value)
		}
	}
	return out
}

// projectContextTableModel returns the filterable/sortable/groupable table of
// high-signal Context Items collected across a project's panes.
func (a *App) projectContextTableModel(groupName string, projRef *core.Ref) *table.Model {
	a.syncContextItems()
	items := a.projectContextItems(groupName, projRef)
	cols := []table.Column{
		{Key: "star", Title: "★", Width: 2},
		{Key: "kind", Title: "Kind", Width: 6},
		{Key: "value", Title: "Value"},
		{Key: "pane", Title: "Pane", Width: 18},
		{Key: "source", Title: "Source", Width: 10},
	}
	rows := make([]table.Row, len(items))
	byID := make(map[string]CollectedContextItem, len(items))
	for i, it := range items {
		star := " "
		if it.Starred {
			star = "★"
		}
		ref := core.Ref{Kind: "context." + it.Kind, ID: it.Value}
		rows[i] = table.Row{
			ID:    it.Key,
			Ref:   &ref,
			Scope: ScopeProjectContext,
			Cells: map[string]any{
				"star":         star,
				"kind":         it.Kind,
				"value":        it.Value,
				"pane":         it.PaneTitle,
				"source":       it.Source,
				"label":        it.Label,
				"project":      it.Project,
				views.AnyField: strings.Join([]string{it.Kind, it.Value, it.PaneTitle, it.Source, it.Label, it.Project}, " "),
			},
			Facts: map[string]any{
				"contextItem": true,
				"contextKey":  it.Key,
				"contextKind": it.Kind,
				"starred":     it.Starred,
			},
		}
		byID[it.Key] = it
	}
	a.projectCtxRows = byID
	a.projectCtxGroup = groupName
	a.projectCtxRef = projRef

	fresh := table.New(cols, rows)
	old := a.tables[projectContextTable]
	tbl := keep(old, fresh)
	if old == nil {
		tbl.MatchMode = a.matchMode()
		a.applyDefaultPreset(tbl, projectContextTable)
	}
	a.pageSize(tbl, projectContextTable)
	a.tables[projectContextTable] = tbl
	return tbl
}

// projectOverviewView renders the right side of 1 Panes when the rail cursor is
// on a Project header row: notes, links, related tasks, and the filterable /
// sortable / groupable Context Items table for that project's panes.
func (a *App) projectOverviewView(_ core.State, groupName string, projRef *core.Ref) ui.ViewNode {
	groupPanes := a.railGroupPanes[groupName]
	paneIDs := make(map[string]bool, len(groupPanes))
	for _, p := range groupPanes {
		paneIDs[p.ID] = true
	}

	var (
		projRec *data.Record
		tasks   []data.Record
		badges  []ui.Badge
	)
	taskIDs := map[string]bool{}
	if projRef != nil {
		if rec, err := a.Store.Get(*projRef); err == nil {
			projRec = rec
			badges = append(badges, views.StatusBadges(a.schema(data.KindTask), *rec)...)
		}
		tasks = a.projectDescendants(projRef.ID)
		for _, t := range tasks {
			taskIDs[t.ID] = true
		}
	}

	ctxTbl := a.projectContextTableModel(groupName, projRef)
	ctxItems := a.projectContextItems(groupName, projRef)
	lanes := a.projectLanes(projRef, taskIDs, paneIDs)
	notes := a.projectNotes(projRef, taskIDs, paneIDs)
	links := a.projectLinks(projRef, taskIDs, ctxItems)

	badges = append(badges,
		ui.Badge{Text: fmt.Sprintf("%d panes", len(groupPanes))},
		ui.Badge{Text: fmt.Sprintf("%d context", len(ctxItems)), Tone: "accent"},
	)
	if len(lanes) > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d lanes", len(lanes)), Tone: "info"})
	}

	var summaryLines []ui.ViewNode
	if projRec != nil {
		var meta []string
		if desc := strings.TrimSpace(projRec.Text("description")); desc != "" {
			meta = append(meta, desc)
		}
		if repo := projRec.Text("repo"); repo != "" {
			meta = append(meta, "repo: "+repo)
		}
		if dir := projRec.Text("defaultDir"); dir != "" {
			meta = append(meta, "dir: "+dir)
		}
		if len(meta) > 0 {
			summaryLines = append(summaryLines, line("project.overview.meta", " "+strings.Join(meta, " · "), "muted"))
		}
	} else {
		summaryLines = append(summaryLines, line("project.overview.meta", " Panes not assigned to any project task (L associates a pane with a task)", "muted"))
	}

	// Active Lanes summary.
	laneSummary := "none"
	if len(lanes) > 0 {
		var parts []string
		for i, l := range lanes {
			if i >= 4 {
				parts = append(parts, fmt.Sprintf("+%d more", len(lanes)-4))
				break
			}
			parts = append(parts, fmt.Sprintf("[%s] %s", l.Text("laneStatus"), views.Title(a.schema(data.KindLane), l)))
		}
		laneSummary = strings.Join(parts, " · ")
	}
	summaryLines = append(summaryLines, line("project.overview.lanes", fmt.Sprintf(" Lanes (%d): %s", len(lanes), laneSummary), "accent"))

	// Related Tasks summary.
	taskSummary := "none"
	if len(tasks) > 0 {
		var parts []string
		for i, t := range tasks {
			if i >= 4 {
				parts = append(parts, fmt.Sprintf("+%d more", len(tasks)-4))
				break
			}
			parts = append(parts, fmt.Sprintf("[%s] %s", t.Text("workStatus"), views.Title(a.schema(data.KindTask), t)))
		}
		taskSummary = strings.Join(parts, " · ")
	}
	summaryLines = append(summaryLines, line("project.overview.tasks", fmt.Sprintf(" Tasks (%d): %s", len(tasks), taskSummary), ""))

	// Notes summary.
	noteSummary := "none"
	if len(notes) > 0 {
		var parts []string
		for i, n := range notes {
			if i >= 4 {
				parts = append(parts, fmt.Sprintf("+%d more", len(notes)-4))
				break
			}
			parts = append(parts, views.Title(a.schema(data.KindNote), n))
		}
		noteSummary = strings.Join(parts, " · ")
	}
	summaryLines = append(summaryLines, line("project.overview.notes", fmt.Sprintf(" Notes (%d): %s", len(notes), noteSummary), ""))

	// Links summary.
	linkSummary := "none"
	if len(links) > 0 {
		shown := links
		more := 0
		if len(shown) > 3 {
			more = len(shown) - 3
			shown = shown[:3]
		}
		linkSummary = strings.Join(shown, " · ")
		if more > 0 {
			linkSummary += fmt.Sprintf(" · +%d more", more)
		}
	}
	summaryLines = append(summaryLines, line("project.overview.links", fmt.Sprintf(" Links (%d): %s", len(links), linkSummary), "info"))

	ctxNode := ctxTbl.View(projectContextTable)
	ctxNode.Scope = ScopeProjectContext
	if ctxNode.Facts == nil {
		ctxNode.Facts = map[string]any{}
	}
	ctxNode.Facts["projectOverview"] = true

	ctxBadges := []ui.Badge{{Text: fmt.Sprintf("%d/%d", len(ctxTbl.Filtered()), len(ctxTbl.Rows))}}
	if ctxTbl.Query != "" {
		ctxBadges = append(ctxBadges, ui.Badge{Text: "/" + ctxTbl.Query, Tone: "accent"})
	}
	if ctxTbl.SortField != "" && ctxTbl.SortDir != table.SortNone {
		ctxBadges = append(ctxBadges, ui.Badge{Text: "sort:" + ctxTbl.SortField, Tone: "info"})
	}
	if ctxTbl.GroupField != "" {
		ctxBadges = append(ctxBadges, ui.Badge{Text: "group:" + ctxTbl.GroupField, Tone: "info"})
	}

	ctxPanel := ui.ViewNode{
		ID:       "project.context",
		Kind:     ui.KindPanel,
		Scope:    ScopeProjectContext,
		Title:    "Context Items",
		Border:   true,
		Badges:   ctxBadges,
		Box:      layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{ctxNode},
	}

	summaryPanel := ui.ViewNode{
		ID:       "project.summary",
		Kind:     ui.KindPanel,
		Title:    "Overview",
		Border:   true,
		Box:      layout.Constraints{FixedH: len(summaryLines) + 2},
		Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: summaryLines}},
	}

	overview := ui.ViewNode{
		ID:     "project.overview",
		Kind:   ui.KindPanel,
		Scope:  ScopeProjectOverview,
		Ref:    projRef,
		Title:  "Project · " + groupName,
		Border: true,
		Badges: badges,
		Facts:  map[string]any{"projectGroup": groupName, "paneCount": len(groupPanes), "contextCount": len(ctxItems)},
		Children: []ui.ViewNode{
			{Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{summaryPanel, ctxPanel}},
		},
	}
	return overview
}

// focusedProjectContextItem returns the CollectedContextItem currently focused
// in projectContextTable.
func (a *App) focusedProjectContextItem() (CollectedContextItem, bool) {
	tbl := a.tables[projectContextTable]
	if tbl == nil {
		return CollectedContextItem{}, false
	}
	row, ok := tbl.FocusedRow()
	if !ok {
		return CollectedContextItem{}, false
	}
	it, exists := a.projectCtxRows[row.ID]
	return it, exists
}

// projectContextCommand handles open, yank, star, and attach actions on rows of
// the project overview's Context Items table.
func (a *App) projectContextOpen(s core.State, _ core.Command) core.StepResult {
	tbl := a.tables[projectContextTable]
	if tbl != nil {
		if it, ok := tbl.Focused(); ok && it.Header {
			tbl.ToggleGroup()
			return core.Next(s)
		}
	}
	it, ok := a.focusedProjectContextItem()
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
	it, ok := a.focusedProjectContextItem()
	if !ok {
		a.say("no context item selected")
		return core.Next(s)
	}
	a.yank(it.Value, it.Kind)
	return core.Next(s)
}

func (a *App) projectContextStar(s core.State, _ core.Command) core.StepResult {
	it, ok := a.focusedProjectContextItem()
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
	it, ok := a.focusedProjectContextItem()
	if !ok {
		a.say("no context item selected")
		return core.Next(s)
	}
	target := a.projectCtxRef
	if target == nil && it.PaneID != "" {
		r := core.Ref{Kind: data.KindPane, ID: it.PaneID}
		target = &r
	}
	return core.Next(a.attachContextItem(s, it, target))
}
