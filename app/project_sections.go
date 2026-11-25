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
	"ofam.dev/ochazuke/widgets/tree"
)

type projectWorkspaceData struct {
	groupName, projectKey, projectID string
	projRef                          *core.Ref
	projRec                          *data.Record
	taskIDs, paneIDs                 map[string]bool
	tasks, panes, pages              []data.Record
	activeLanes, allLanes            []data.Record
	documents, notes                 []data.Record
	questions, commands              []data.Record
	worktrees                        []data.ProjectWorktree
	contextItems, officialItems      []CollectedContextItem
	pinnedItems, prItems             []CollectedContextItem
	unreadPages, officialCount       int
	openQuestions                    int
}

func (a *App) collectProjectWorkspaceData(groupName string, projRef *core.Ref) projectWorkspaceData {
	ws := projectWorkspaceData{
		groupName:  groupName,
		projectKey: projectKeyFor(groupName, projRef),
		projRef:    projRef,
		taskIDs:    map[string]bool{},
		paneIDs:    map[string]bool{},
	}
	if projRef != nil && projRef.ID != "" {
		ws.projectID = projRef.ID
		ws.taskIDs[projRef.ID] = true
		if rec, err := a.Store.Get(*projRef); err == nil {
			ws.projRec = rec
		}
		if ids, err := a.Store.ProjectTaskIDs(projRef.ID, a.nav.showArchived); err == nil {
			for id := range ids {
				ws.taskIDs[id] = true
			}
		}
		ws.tasks = a.projectDescendants(projRef.ID)
		for _, t := range ws.tasks {
			ws.taskIDs[t.ID] = true
		}
	}
	ws.panes = a.projectPanesList(groupName, ws.taskIDs)
	for _, p := range ws.panes {
		ws.paneIDs[p.ID] = true
	}
	ws.pages, ws.unreadPages = a.projectPagesList(ws.taskIDs, ws.paneIDs)
	ws.allLanes, ws.activeLanes = a.projectLanesSplit(projRef, ws.taskIDs, ws.paneIDs)
	if ws.projectID != "" {
		ws.worktrees, _ = a.Store.ProjectWorktrees(ws.projectID)
	}
	ws.contextItems = a.projectUnifiedContextItems(groupName, projRef)
	for _, it := range ws.contextItems {
		if it.Official {
			ws.officialCount++
			ws.officialItems = append(ws.officialItems, it)
		}
		if it.Official || it.Pinned || it.Starred {
			ws.pinnedItems = append(ws.pinnedItems, it)
		}
		if it.Kind == "pr" {
			ws.prItems = append(ws.prItems, it)
		}
	}
	ws.documents = a.projectDocuments(projRef, ws.taskIDs, ws.paneIDs)
	ws.notes = a.projectNotes(projRef, ws.taskIDs, ws.paneIDs)
	ws.questions, ws.openQuestions = a.projectQuestionsList(ws.taskIDs, ws.paneIDs)
	ws.commands = a.projectScopedByTaskOrPane(data.KindCommand, ws.taskIDs, ws.paneIDs)
	return ws
}

func (a *App) buildProjectNavItems(defs []projectNavDef, ws projectWorkspaceData, activeKey, focusID string) []views.ProjectNavSection {
	counts := map[string]int{
		"tasks": len(ws.tasks), "pages": len(ws.pages), "context": len(ws.contextItems),
		"pinned": len(ws.pinnedItems), "panes": len(ws.panes), "lanes": len(ws.allLanes),
		"worktrees": len(ws.worktrees), "prs": len(ws.prItems), "documents": len(ws.documents),
		"notes": len(ws.notes), "questions": len(ws.questions), "commands": len(ws.commands),
	}
	pc := a.projectCtrl()
	pc.resetNavTargets(ws.projectKey)
	treeEnabled := a.projectNavTreeEnabled()

	out := make([]views.ProjectNavSection, 0, len(defs))
	for _, d := range defs {
		expandable := treeEnabled && d.Key != "home" && d.Key != "signals" && counts[d.Key] > 0
		expanded := expandable && pc.isNavSectionExpanded(ws.projectKey, d.Key)
		sec := views.ProjectNavSection{
			Key: d.Key, Label: d.Label, Icon: d.Icon, Group: d.Group,
			Count: counts[d.Key], Active: d.Key == activeKey,
			Expandable: expandable, Expanded: expanded,
		}
		switch d.Key {
		case "pages":
			sec.Unread = ws.unreadPages > 0
		case "signals":
			sec.Badge = "0"
		case "context":
			if ws.officialCount > 0 {
				sec.Badge = fmt.Sprintf("★%d·%d", ws.officialCount, len(ws.contextItems))
			}
		case "questions":
			sec.Unread = ws.openQuestions > 0
		}
		if expanded {
			sec.Children = a.buildProjectNavChildren(ws, d.Key, focusID)
		}
		out = append(out, sec)
	}
	return out
}

func (a *App) buildProjectSectionBody(s core.State, ws projectWorkspaceData, sec string) ui.ViewNode {
	switch sec {
	case "tasks":
		if a.projectOutlineAsPageEnabled() && ws.projectID != "" {
			return a.projectOutlineView(&s, ws.projectID)
		}
		return a.projectRecordPanel("project.tasks", projectTasksFlatTable, data.KindTask, "tasks", "Tasks", views.ScopeProjectContent, ws.tasks,
			[]ui.Badge{{Text: fmt.Sprintf("%d tasks", len(ws.tasks))}})
	case "pages":
		return a.projectSectionPages(s, ws)
	case "signals":
		return a.projectSectionSignals()
	case "context":
		badges := []ui.Badge{}
		if ws.officialCount > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("★ %d official", ws.officialCount), Tone: "accent"})
		}
		badges = append(badges, ui.Badge{Text: "p official · N add", Tone: "muted"})
		return a.projectContextPanel("project.context", projectContextTable, "context", "Context · Official & Collected", ws, ws.contextItems, badges)
	case "pinned":
		return a.projectContextPanel("project.pinned", projectPinnedTable, "pinned", "Pinned Links & Files", ws, ws.pinnedItems,
			[]ui.Badge{{Text: fmt.Sprintf("%d curated", len(ws.pinnedItems)), Tone: "accent"}, {Text: "N add · p official", Tone: "muted"}})
	case "panes":
		return a.projectTreePanel("project.panes", projectPanesTree, data.KindPane, "parentPane", "panes", scopeProjectPanes, ws.panes,
			ui.Badge{Text: "enter jump live", Tone: "muted"})
	case "lanes":
		return a.projectTreePanel("project.lanes", projectLanesTree, data.KindLane, "parentLane", "lanes", views.ScopeProjectContent, ws.allLanes,
			ui.Badge{Text: fmt.Sprintf("%d active", len(ws.activeLanes)), Tone: "accent"})
	case "worktrees":
		return a.projectSectionWorktrees(ws)
	case "prs":
		return a.projectContextPanel("project.prs", projectPRsTable, "prs", "PRs", ws, ws.prItems,
			[]ui.Badge{{Text: fmt.Sprintf("%d PRs", len(ws.prItems)), Tone: "accent"}})
	case "documents":
		return a.projectRecordPanel("project.documents", projectDocumentsTable, data.KindDocument, "documents", "Documents", scopeProjectDocuments, ws.documents,
			[]ui.Badge{{Text: fmt.Sprintf("%d docs", len(ws.documents))}, {Text: "enter / o read", Tone: "muted"}})
	case "notes":
		return a.projectSectionNotes(ws)
	case "questions":
		badges := []ui.Badge{{Text: fmt.Sprintf("%d total", len(ws.questions))}}
		if ws.openQuestions > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d open", ws.openQuestions), Tone: "warning"})
		}
		return a.projectRecordPanel("project.questions", projectQuestionsTable, data.KindQuestion, "questions", "Questions & Decisions", views.ScopeProjectContent, ws.questions, badges)
	case "commands":
		return a.projectRecordPanel("project.commands", projectCommandsTable, data.KindCommand, "commands", "Commands", views.ScopeProjectContent, ws.commands,
			[]ui.Badge{{Text: fmt.Sprintf("%d commands", len(ws.commands))}, {Text: "enter / r run", Tone: "muted"}})
	default:
		return a.projectSectionHome(ws)
	}
}

// --- Section builders -----------------------------------------------------------

func (a *App) projectSectionHome(ws projectWorkspaceData) ui.ViewNode {
	var rows []ui.ViewNode
	if ws.projRec != nil {
		if desc := strings.TrimSpace(ws.projRec.Text("description")); desc != "" {
			rows = append(rows, line("project.home.desc", " "+desc, ""))
		}
		var meta []string
		if repo := strings.TrimSpace(ws.projRec.Text("repo")); repo != "" {
			meta = append(meta, "repo: "+repo)
		}
		if dir := strings.TrimSpace(ws.projRec.Text("defaultDir")); dir != "" {
			meta = append(meta, "dir: "+dir)
		}
		if len(meta) > 0 {
			rows = append(rows, line("project.home.meta", " "+strings.Join(meta, " · "), "muted"))
		}
	} else {
		rows = append(rows, line("project.home.meta", " Panes not assigned to any project task (L associates a pane)", "muted"))
	}
	rows = append(rows,
		line("project.home.gap1", "", ""),
		line("project.home.counts.hdr", " WORKSPACE COUNTS", "muted"),
		line("project.home.counts.1", fmt.Sprintf(" Tasks: %d · Panes: %d · Lanes: %d active (%d total) · Pages: %d",
			len(ws.tasks), len(ws.panes), len(ws.activeLanes), len(ws.allLanes), len(ws.pages)), ""),
		line("project.home.counts.2", fmt.Sprintf(" Docs: %d · Notes: %d · Questions: %d (%d open) · Commands: %d · Worktrees: %d",
			len(ws.documents), len(ws.notes), len(ws.questions), ws.openQuestions, len(ws.commands), len(ws.worktrees)), ""),
		line("project.home.gap2", "", ""),
		line("project.home.official.hdr", fmt.Sprintf(" ★ OFFICIAL PROJECT CONTEXT (AUTO-INCLUDED · %d)", len(ws.officialItems)), "accent"),
	)
	if len(ws.officialItems) == 0 {
		rows = append(rows, line("project.home.official.empty", " None yet · press N to add official context, or p in Context to promote", "muted"))
	} else {
		limit := min(len(ws.officialItems), 6)
		for i := range limit {
			it := ws.officialItems[i]
			label := strings.TrimSpace(it.Label)
			if label == "" {
				label = it.Value
			}
			entry := fmt.Sprintf(" ★ [%s] %s", it.Kind, clip(label, 42))
			if it.Value != "" && it.Value != label {
				entry += " · " + clip(it.Value, 44)
			}
			rows = append(rows, line(fmt.Sprintf("project.home.official.%d", i), entry, ""))
		}
		if len(ws.officialItems) > limit {
			rows = append(rows, line("project.home.official.more", fmt.Sprintf(" +%d more in Context", len(ws.officialItems)-limit), "muted"))
		}
	}
	if len(ws.activeLanes) > 0 {
		rows = append(rows, line("project.home.gap3", "", ""), line("project.home.lanes.hdr", " ACTIVE LANES", "muted"))
		for i, l := range ws.activeLanes {
			if i >= 4 {
				rows = append(rows, line("project.home.lanes.more", fmt.Sprintf(" +%d more lanes", len(ws.activeLanes)-4), "muted"))
				break
			}
			rows = append(rows, line(fmt.Sprintf("project.home.lane.%d", i),
				fmt.Sprintf(" [%s] %s", l.Text("laneStatus"), views.Title(a.schema(data.KindLane), l)), "accent"))
		}
	}
	if ws.openQuestions > 0 {
		rows = append(rows, line("project.home.gap4", "", ""),
			line("project.home.q.hdr", fmt.Sprintf(" OPEN QUESTIONS & DECISIONS (%d)", ws.openQuestions), "warning"))
		shown := 0
		for _, q := range ws.questions {
			if q.Text("status") != "open" || shown >= 3 {
				continue
			}
			rows = append(rows, line(fmt.Sprintf("project.home.q.%d", shown), " ? "+clip(views.Title(a.schema(data.KindQuestion), q), 72), "warning"))
			shown++
		}
	}
	return ui.ViewNode{
		ID: "project.home", Kind: ui.KindPanel, Scope: views.ScopeProjectContent, Title: "Home", Border: true,
		Badges: []ui.Badge{{Text: fmt.Sprintf("%d tasks", len(ws.tasks))}, {Text: fmt.Sprintf("%d context", len(ws.contextItems)), Tone: "accent"}},
		Facts:  map[string]any{"section": "home"},
		Children: []ui.ViewNode{{
			ID: projectHomeSummaryID, Kind: ui.KindVStack, Scope: views.ScopeProjectContent,
			Focusable: true, Facts: map[string]any{"section": "home"}, Children: rows,
		}},
	}
}

func (a *App) projectSectionPages(s core.State, ws projectWorkspaceData) ui.ViewNode {
	pc := a.projectCtrl()
	if openID := pc.openPageID(ws.projectKey); openID != "" {
		if page, err := a.Store.Get(core.Ref{Kind: data.KindPage, ID: openID}); err == nil && page != nil {
			return a.buildProjectInteractivePageNode(s, *page, false)
		}
		pc.setOpenPageID(ws.projectKey, "")
	}
	cols := []table.Column{
		{Key: "title", Title: "Page"},
		{Key: "pageKind", Title: "Kind", Width: 11},
		{Key: "pane", Title: "Pane", Width: 18},
		{Key: "flags", Title: "Flags", Width: 5},
	}
	rows := make([]table.Row, 0, len(ws.pages)+1)
	rows = append(rows, table.Row{
		ID: projectPagesOutlineRowID, Scope: scopeProjectPages,
		Cells:  map[string]any{"title": "★ Tasks (Outline)", "pageKind": "outline", "pane": "project", "flags": "★", views.AnyField: "Tasks Outline project"},
		Facts:  map[string]any{"section": "pages", "projectOutlineEntry": true},
		Badges: []ui.Badge{{Text: "pinned", Tone: "accent"}},
	})
	for _, p := range ws.pages {
		ref := p.Ref()
		paneLabel := "project"
		if pRef, ok := p.RefField("pane"); ok {
			paneLabel = a.title(pRef)
		}
		unread := p.IsUnread()
		var flags []string
		if unread {
			flags = append(flags, "●")
		}
		if p.Bool("pinned") {
			flags = append(flags, "▪")
		}
		if p.IsStarred() {
			flags = append(flags, "★")
		}
		title := views.Title(a.schema(data.KindPage), p)
		rows = append(rows, table.Row{
			ID: data.RefText(ref), Ref: &ref, Scope: scopeProjectPages, Unread: unread,
			Cells: map[string]any{
				"title": title, "pageKind": p.Text("pageKind"), "pane": paneLabel, "flags": strings.Join(flags, ""),
				views.AnyField: strings.Join([]string{title, p.Text("pageKind"), paneLabel, p.Text("slug")}, " "),
			},
			Facts: map[string]any{"section": "pages", "pageId": p.ID, "projectPageRow": true, "unread": unread},
		})
	}
	tbl := a.keepProjectTable(projectPagesTable, table.New(cols, rows))
	node := tbl.View(projectPagesTable)
	node.Scope = scopeProjectPages
	badges := []ui.Badge{{Text: fmt.Sprintf("%d pages", len(ws.pages))}}
	if ws.unreadPages > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d unread", ws.unreadPages), Tone: "warning"})
	}
	badges = append(badges, ui.Badge{Text: "enter open", Tone: "muted"})
	return ui.ViewNode{
		ID: "project.pages", Kind: ui.KindPanel, Scope: scopeProjectPages, Title: "Pages", Border: true,
		Badges: badges, Facts: map[string]any{"section": "pages"}, Children: []ui.ViewNode{node},
	}
}

func (a *App) projectSectionSignals() ui.ViewNode {
	return ui.ViewNode{
		ID: "project.signals", Kind: ui.KindPanel, Scope: views.ScopeProjectContent, Title: "Needs Attention", Border: true,
		Badges: []ui.Badge{{Text: "0", Tone: "muted"}, {Text: "Phase 2", Tone: "info"}},
		Facts:  map[string]any{"empty": true, "section": "signals"},
		Children: []ui.ViewNode{{
			ID: projectSignalsNodeID, Kind: ui.KindVStack, Scope: views.ScopeProjectContent,
			Focusable: true, Gap: 1, Facts: map[string]any{"empty": true, "section": "signals"},
			Children: []ui.ViewNode{
				line("project.signals.msg", " 0 items needing attention · GitHub, Linear, and Slack signal ingestion coming in Phase 2", "muted"),
				line("project.signals.hint", " esc / h back to Project nav", "info"),
			},
		}},
	}
}

func (a *App) projectContextTableModel(ws projectWorkspaceData) *table.Model {
	return a.buildContextSectionTable(projectContextTable, "context", ws.groupName, ws.projRef, ws.contextItems)
}

func (a *App) buildContextSectionTable(tableID, section, groupName string, projRef *core.Ref, items []CollectedContextItem) *table.Model {
	cols := []table.Column{
		{Key: "tier", Title: "Tier", Width: 17},
		{Key: "kind", Title: "Kind", Width: 6},
		{Key: "value", Title: "Value"},
		{Key: "status", Title: "Status", Width: 18},
		{Key: "source", Title: "Source", Width: 10},
	}
	rows := make([]table.Row, len(items))
	if a.panes.projectCtxRows == nil {
		a.panes.projectCtxRows = map[string]CollectedContextItem{}
	}
	for i, it := range items {
		tier, status, rowTone := "Collected", "", ""
		var badges []ui.Badge
		switch {
		case it.Official:
			tier, status, rowTone = "★ Official (Auto)", "[official · auto]", "accent"
			badges = append(badges, ui.Badge{Text: "official · auto", Tone: "accent"})
		case it.Pinned:
			status = "[pinned]"
			badges = append(badges, ui.Badge{Text: "pinned", Tone: "info"})
		case it.Starred:
			status = "★"
		}
		display := it.Value
		if lbl := strings.TrimSpace(it.Label); lbl != "" && lbl != it.Value {
			display = fmt.Sprintf("%s — %s", lbl, it.Value)
		}
		if notes := strings.TrimSpace(it.Notes); notes != "" {
			display += " (" + clip(notes, 32) + ")"
		}
		ref := core.Ref{Kind: "context." + it.Kind, ID: it.Value}
		if it.RecordID != "" {
			ref = core.Ref{Kind: data.KindContext, ID: it.RecordID}
		}
		rows[i] = table.Row{
			ID: it.Key, Ref: &ref, Scope: ScopeProjectContext, Tone: rowTone, Badges: badges,
			Cells: map[string]any{
				"tier": tier, "kind": it.Kind, "value": display, "status": status, "pane": it.PaneTitle,
				"source": it.Source, "label": it.Label, "notes": it.Notes, "project": it.Project,
				views.AnyField: strings.Join([]string{tier, it.Kind, it.Value, it.Label, it.Notes, it.PaneTitle, it.Source, status}, " "),
			},
			Facts: map[string]any{
				"contextItem": true, "contextKey": it.Key, "contextKind": it.Kind,
				"starred": it.Starred, "official": it.Official, "pinned": it.Pinned,
				"recordId": it.RecordID, "section": section,
			},
		}
		a.panes.projectCtxRows[it.Key] = it
	}
	a.panes.projectCtxGroup, a.panes.projectCtxRef = groupName, projRef
	return a.keepProjectTable(tableID, table.New(cols, rows))
}

func (a *App) projectContextPanel(panelID, tableID, section, title string, ws projectWorkspaceData, items []CollectedContextItem, extra []ui.Badge) ui.ViewNode {
	tbl := a.buildContextSectionTable(tableID, section, ws.groupName, ws.projRef, items)
	node := tbl.View(tableID)
	node.Scope = ScopeProjectContext
	if node.Facts == nil {
		node.Facts = map[string]any{}
	}
	node.Facts["section"] = section
	if tableID == projectContextTable {
		node.Facts["projectOverview"] = true
	}
	badges := append([]ui.Badge{{Text: fmt.Sprintf("%d/%d", len(tbl.Filtered()), len(tbl.Rows))}}, extra...)
	if tbl.Query != "" {
		badges = append(badges, ui.Badge{Text: "/" + tbl.Query, Tone: "accent"})
	}
	return ui.ViewNode{
		ID: panelID, Kind: ui.KindPanel, Scope: ScopeProjectContext, Title: title, Border: true,
		Badges: badges, Facts: map[string]any{"section": section}, Children: []ui.ViewNode{node},
	}
}

func (a *App) projectTreePanel(panelID, treeID, kind, parentField, section, panelScope string, recs []data.Record, extra ...ui.Badge) ui.ViewNode {
	schema := a.schema(kind)
	tm := a.syncProjectSectionTree(treeID, schema, data.BuildTree(recs, parentField, "", a.nav.showArchived))
	panel := views.TreeView(schema, tm, panelID, treeID, panelScope, extra...)
	panel.Facts["section"] = section
	return panel
}

func (a *App) projectRecordPanel(panelID, tableID, kind, section, title, panelScope string, recs []data.Record, badges []ui.Badge) ui.ViewNode {
	schema := a.schema(kind)
	fresh := views.TableFromRecordsWith(schema, recs, a.styleOptions())
	a.resolveTableRefCells(schema, recs, fresh)
	for i, rec := range recs {
		if a.openable(rec) {
			fresh.Rows[i].Facts[factOpenable] = true
		}
	}
	tbl := a.keepProjectTable(tableID, fresh)
	node := tbl.View(tableID)
	if node.Scope == "" {
		node.Scope, node.Ref = kind, &core.Ref{Kind: kind}
	}
	return ui.ViewNode{
		ID: panelID, Kind: ui.KindPanel, Scope: panelScope, Title: title, Border: true,
		Badges: badges, Facts: map[string]any{"section": section}, Children: []ui.ViewNode{node},
	}
}

func (a *App) projectSectionWorktrees(ws projectWorkspaceData) ui.ViewNode {
	cols := []table.Column{
		{Key: "path", Title: "Path"}, {Key: "branch", Title: "Branch", Width: 20},
		{Key: "repo", Title: "Repo", Width: 18}, {Key: "source", Title: "Source", Width: 10},
	}
	rows := make([]table.Row, len(ws.worktrees))
	for i, wt := range ws.worktrees {
		ref := wt.Ref
		rows[i] = table.Row{
			ID: wt.Path, Ref: &ref, Scope: scopeProjectWorktrees,
			Cells: map[string]any{
				"path": wt.Path, "branch": wt.Branch, "repo": wt.Repo, "source": wt.Source,
				views.AnyField: strings.Join([]string{wt.Path, wt.Branch, wt.Repo, wt.Source}, " "),
			},
			Facts: map[string]any{"section": "worktrees", "path": wt.Path, factOpenable: true},
		}
	}
	tbl := a.keepProjectTable(projectWorktreesTable, table.New(cols, rows))
	node := tbl.View(projectWorktreesTable)
	node.Scope = scopeProjectWorktrees
	return ui.ViewNode{
		ID: "project.worktrees", Kind: ui.KindPanel, Scope: scopeProjectWorktrees, Title: "Worktrees", Border: true,
		Badges: []ui.Badge{{Text: fmt.Sprintf("%d paths", len(ws.worktrees))}},
		Facts:  map[string]any{"section": "worktrees"}, Children: []ui.ViewNode{node},
	}
}

func (a *App) projectSectionNotes(ws projectWorkspaceData) ui.ViewNode {
	cols := []table.Column{
		{Key: "title", Title: "Note"}, {Key: "target", Title: "Target", Width: 20},
		{Key: "authorKind", Title: "Author", Width: 9}, {Key: "body", Title: "Excerpt"},
	}
	rows := make([]table.Row, len(ws.notes))
	schema := a.schema(data.KindNote)
	for i, n := range ws.notes {
		ref := n.Ref()
		target := a.projectNoteTargetLabel(n)
		author := strings.TrimSpace(n.Text("authorKind"))
		if author == "" {
			author = "owner"
		}
		title := views.Title(schema, n)
		excerpt := clip(strings.ReplaceAll(strings.TrimSpace(n.Text("body")), "\n", " "), 56)
		facts := map[string]any{"section": "notes", "authorKind": author}
		if a.openable(n) {
			facts[factOpenable] = true
		}
		rows[i] = table.Row{
			ID: data.RefText(ref), Ref: &ref, Scope: data.KindNote, Badges: views.StatusBadges(schema, n),
			Cells: map[string]any{
				"title": title, "target": target, "authorKind": author, "body": excerpt,
				views.AnyField: strings.Join([]string{title, target, author, n.Text("body")}, " "),
			},
			Facts: facts,
		}
	}
	tbl := a.keepProjectTable(projectNotesTable, table.New(cols, rows))
	node := tbl.View(projectNotesTable)
	if node.Scope == "" {
		node.Scope, node.Ref = data.KindNote, &core.Ref{Kind: data.KindNote}
	}
	return ui.ViewNode{
		ID: "project.notes", Kind: ui.KindPanel, Scope: views.ScopeProjectContent, Title: "Notes", Border: true,
		Badges: []ui.Badge{{Text: fmt.Sprintf("%d notes", len(ws.notes))}},
		Facts:  map[string]any{"section": "notes"}, Children: []ui.ViewNode{node},
	}
}

// --- Scoped data helpers --------------------------------------------------------

func (a *App) keepProjectTable(id string, fresh *table.Model) *table.Model {
	pc := a.projectCtrl()
	old := a.nav.tables[id]
	if old == nil {
		old = pc.tables[id]
	}
	tbl := keep(old, fresh)
	if old == nil {
		tbl.MatchMode = a.matchMode()
		a.applyDefaultPreset(tbl, id)
	}
	a.pageSize(tbl, id)
	a.nav.tables[id], pc.tables[id] = tbl, tbl
	return tbl
}

func (a *App) resolveTableRefCells(schema data.Schema, recs []data.Record, tbl *table.Model) {
	names := map[core.Ref]string{}
	for _, f := range schema.Fields {
		if f.Type != core.ParamRef {
			continue
		}
		for i, rec := range recs {
			ref, ok := rec.RefField(f.Name)
			if !ok {
				continue
			}
			if _, seen := names[ref]; !seen {
				names[ref] = a.title(ref)
			}
			tbl.Rows[i].Cells[f.Name] = names[ref]
		}
	}
}

func (a *App) syncProjectSectionTree(treeID string, schema data.Schema, nodes []data.TreeNode) *tree.Model {
	pc := a.projectCtrl()
	tm := pc.trees[treeID]
	if tm == nil {
		tm = tree.New(nil)
		pc.trees[treeID] = tm
	}
	opts := views.TreeOptions{
		Style: a.styleOptions(), GapTopLevel: false, ShowFoldCount: true, ResolveRef: a.title,
		Guides: a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTreeGuides, true),
		EnrichNode: func(rec data.Record, nd *tree.Node) {
			if a.openable(rec) {
				if nd.Facts == nil {
					nd.Facts = map[string]any{}
				}
				nd.Facts[factOpenable] = true
			}
		},
	}
	built := views.TreeFromNodes(schema, nodes, opts)
	tm.DefaultFolded, tm.GapTopLevel, tm.Guides, tm.ShowFoldCount = false, false, opts.Guides, true
	tm.ScrollOff, tm.PageSize = a.scrollOff(), a.treeViewportHeight(treeID)
	tm.SetNodes(built.Nodes)
	return tm
}

func (a *App) projectDescendants(rootID string) []data.Record {
	if rootID == "" || a.Store == nil {
		return nil
	}
	nodes, err := a.Store.TaskTree(a.nav.showArchived)
	if err != nil {
		return nil
	}
	var out []data.Record
	var collect func([]data.TaskNode)
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

func (a *App) projectPanesList(groupName string, taskIDs map[string]bool) []data.Record {
	seen := map[string]bool{}
	var out []data.Record
	add := func(rec data.Record) {
		if !seen[rec.ID] {
			seen[rec.ID] = true
			out = append(out, rec)
		}
	}
	if a.panes != nil {
		for _, p := range a.panes.railGroupPanes[groupName] {
			add(p)
		}
	}
	if a.Store == nil {
		return out
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return out
	}
	for _, p := range res.Items {
		if tRef, ok := p.RefField("task"); ok && taskIDs[tRef.ID] {
			add(p)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range res.Items {
			if !seen[p.ID] {
				if parentRef, ok := p.RefField("parentPane"); ok && parentRef.Kind == data.KindPane && seen[parentRef.ID] {
					add(p)
					changed = true
				}
			}
		}
	}
	return out
}

func (a *App) projectScopedByTaskOrPane(kind string, taskIDs, paneIDs map[string]bool) []data.Record {
	if a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: kind, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return nil
	}
	var out []data.Record
	for _, rec := range res.Items {
		if tRef, ok := rec.RefField("task"); ok && taskIDs[tRef.ID] {
			out = append(out, rec)
			continue
		}
		if pRef, ok := rec.RefField("pane"); ok && paneIDs[pRef.ID] {
			out = append(out, rec)
		}
	}
	return out
}

func (a *App) projectPagesList(taskIDs, paneIDs map[string]bool) ([]data.Record, int) {
	raw := a.projectScopedByTaskOrPane(data.KindPage, taskIDs, paneIDs)
	var out []data.Record
	unread := 0
	for _, p := range raw {
		if p.Text("pageKind") == "screen" {
			continue
		}
		if p.IsUnread() {
			unread++
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(x, y data.Record) int {
		if x.Bool("pinned") != y.Bool("pinned") {
			if x.Bool("pinned") {
				return -1
			}
			return 1
		}
		if x.IsUnread() != y.IsUnread() {
			if x.IsUnread() {
				return -1
			}
			return 1
		}
		return y.UpdatedAt.Compare(x.UpdatedAt)
	})
	return out, unread
}

func (a *App) projectQuestionsList(taskIDs, paneIDs map[string]bool) ([]data.Record, int) {
	out := a.projectScopedByTaskOrPane(data.KindQuestion, taskIDs, paneIDs)
	openCount := 0
	for _, q := range out {
		if q.Text("status") == "open" {
			openCount++
		}
	}
	slices.SortStableFunc(out, func(x, y data.Record) int {
		xo, yo := x.Text("status") == "open", y.Text("status") == "open"
		if xo != yo {
			if xo {
				return -1
			}
			return 1
		}
		return y.UpdatedAt.Compare(x.UpdatedAt)
	})
	return out, openCount
}

func (a *App) projectLanesSplit(projRef *core.Ref, taskIDs, paneIDs map[string]bool) (all, active []data.Record) {
	if a.Store == nil {
		return nil, nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindLane, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	add := func(rec data.Record) {
		if seen[rec.ID] {
			return
		}
		seen[rec.ID] = true
		all = append(all, rec)
		switch strings.ToLower(strings.TrimSpace(rec.Text("laneStatus"))) {
		case "done", "merged", "cancelled":
		default:
			active = append(active, rec)
		}
	}
	for _, lane := range res.Items {
		if t, ok := lane.RefField("task"); ok && (taskIDs[t.ID] || (projRef != nil && t.ID == projRef.ID)) {
			add(lane)
			continue
		}
		if rels, err := a.Store.Related(lane.Ref(), data.RelClaim, data.Outgoing); err == nil {
			for _, r := range rels {
				if r.To.Kind == data.KindPane && paneIDs[r.To.ID] {
					add(lane)
					break
				}
			}
		}
	}
	return all, active
}

func (a *App) projectUnifiedContextItems(groupName string, projRef *core.Ref) []CollectedContextItem {
	items := a.projectContextItems(groupName, projRef)
	if a.Store == nil || projRef == nil || projRef.ID == "" {
		return items
	}
	persisted, err := a.Store.ProjectContextAll(projRef.ID)
	if err != nil || len(persisted) == 0 {
		return items
	}
	byVal := make(map[string]int, len(items)*2)
	for i, it := range items {
		byVal[strings.ToLower(strings.TrimSpace(it.Value))] = i
		if it.RecordID != "" {
			byVal["id:"+it.RecordID] = i
		}
	}
	for _, rec := range persisted {
		val := strings.TrimSpace(rec.Text("value"))
		if val == "" {
			continue
		}
		kind := rec.Text("contextKind")
		if kind == "" {
			kind = data.ClassifyContextKind(val, "")
		}
		canon := strings.TrimSpace(rec.Text("canonKey"))
		if canon == "" {
			canon = contextCanonKey(kind, val)
		}
		item := CollectedContextItem{
			RecordID: rec.ID, Key: "rec\x00" + rec.ID, CanonKey: canon, Kind: kind, Value: val,
			Label: rec.Text("title"), Source: rec.Text("source"), ProjectID: projRef.ID, Project: groupName,
			Starred: rec.IsStarred(), Official: rec.Bool("official"), Pinned: rec.Bool("pinned"),
			Notes: rec.Text("notes"), SeenAt: rec.UpdatedAt,
		}
		if pRef, ok := rec.RefField("pane"); ok {
			item.PaneID, item.PaneTitle = pRef.ID, a.title(pRef)
		}
		if idx, found := byVal["id:"+rec.ID]; found {
			items[idx] = mergeContextItem(items[idx], item)
			continue
		}
		if idx, found := byVal[strings.ToLower(val)]; found {
			items[idx] = mergeContextItem(items[idx], item)
			continue
		}
		byVal["id:"+rec.ID], byVal[strings.ToLower(val)] = len(items), len(items)
		items = append(items, item)
	}
	sortCollectedContext(items)
	return items
}

func mergeContextItem(harvested, persisted CollectedContextItem) CollectedContextItem {
	out := persisted
	if harvested.Key != "" {
		out.Key = harvested.Key
	}
	if out.RecordID == "" {
		out.RecordID = harvested.RecordID
	}
	if out.Label == "" {
		out.Label = harvested.Label
	}
	if out.PaneID == "" {
		out.PaneID, out.PaneTitle = harvested.PaneID, harvested.PaneTitle
	}
	if out.Notes == "" {
		out.Notes = harvested.Notes
	}
	out.Starred = harvested.Starred || persisted.Starred
	if harvested.SeenAt.After(out.SeenAt) {
		out.SeenAt = harvested.SeenAt
	}
	return out
}

func (a *App) projectDocuments(projRef *core.Ref, taskIDs, paneIDs map[string]bool) []data.Record {
	if a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindDocument, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []data.Record
	for _, doc := range res.Items {
		if seen[doc.ID] {
			continue
		}
		matched := false
		if t, ok := doc.RefField("task"); ok && (taskIDs[t.ID] || (projRef != nil && t.ID == projRef.ID)) {
			matched = true
		}
		for _, relKind := range []string{data.RelCreated, data.RelAbout, data.RelContext} {
			if matched {
				break
			}
			rels, err := a.Store.Related(doc.Ref(), relKind, data.Both)
			if err != nil {
				continue
			}
			for _, r := range rels {
				other := r.Other(doc.Ref())
				if (projRef != nil && other == *projRef) || (other.Kind == data.KindTask && taskIDs[other.ID]) || (other.Kind == data.KindPane && paneIDs[other.ID]) {
					matched = true
					break
				}
			}
		}
		if matched {
			seen[doc.ID] = true
			out = append(out, doc)
		}
	}
	return out
}

func (a *App) projectNotes(projRef *core.Ref, taskIDs, paneIDs map[string]bool) []data.Record {
	if a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindNote, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []data.Record
	for _, n := range res.Items {
		if seen[n.ID] {
			continue
		}
		matched := false
		if t, ok := n.RefField("task"); ok && (taskIDs[t.ID] || (projRef != nil && t.ID == projRef.ID)) {
			matched = true
		} else if p, ok := n.RefField("pane"); ok && paneIDs[p.ID] {
			matched = true
		} else if rels, err := a.Store.Related(n.Ref(), data.RelAbout, data.Outgoing); err == nil {
			for _, r := range rels {
				if (projRef != nil && r.To == *projRef) || (r.To.Kind == data.KindTask && taskIDs[r.To.ID]) || (r.To.Kind == data.KindPane && paneIDs[r.To.ID]) {
					matched = true
					break
				}
			}
		}
		if matched {
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	return out
}

func (a *App) projectNoteTargetLabel(n data.Record) string {
	if tRef, ok := n.RefField("task"); ok {
		return a.title(tRef)
	}
	if a.Store != nil {
		if rels, err := a.Store.Related(n.Ref(), data.RelAbout, data.Outgoing); err == nil && len(rels) > 0 {
			return a.title(rels[0].To)
		}
	}
	if pRef, ok := n.RefField("pane"); ok {
		return a.title(pRef)
	}
	return ""
}
