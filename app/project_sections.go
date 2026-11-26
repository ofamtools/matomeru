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
	taskTree                         []data.TreeNode
	tasks, panes, pages              []data.Record
	relations                        []data.Relation
	activeLanes, allLanes            []data.Record
	documents, notes                 []data.Record
	questions, commands              []data.Record
	worktrees                        []data.ProjectWorktree
	contextItems, officialItems      []CollectedContextItem
	pinnedItems, prItems             []CollectedContextItem
	reviewContext                    []CollectedContextItem
	contextRecords                   map[string]data.Record
	unreadNoteTasks, unreadPagePanes map[string]bool
	unreadPages, officialCount       int
	openQuestions                    int
	baseUnreadTotal, unreadTotal     int
	runningPanes, needsYouPanes      int
	cacheRev                         int64
	cacheCtxGen                      uint64
	cacheRailKey                     string
}

func (a *App) collectProjectWorkspaceData(groupName string, projRef *core.Ref) projectWorkspaceData {
	projKey, projID := projectKeyFor(groupName, projRef), ""
	if projRef != nil {
		projID = projRef.ID
	}
	var rev int64
	if a.Store != nil {
		rev, _ = a.Store.LatestChangeSetNumber()
	}
	telSeq := a.rt.live.telemetrySeq.Load()
	pc := a.projectCtrl()
	ctxGen := a.contextIndexGen()
	railKey := a.railGroupPanesKey(groupName)
	if a.Store != nil && pc.wsCache != nil {
		if c, ok := pc.wsCache[projKey]; ok && c.rev == rev && c.showArchived == a.nav.showArchived && c.ctxGen == ctxGen && c.groupName == groupName && c.projID == projID && c.railPanesKey == railKey {
			if c.telemetrySeq == telSeq {
				return c.data
			}
			ws := c.data
			a.populateProjectLiveTelemetry(&ws)
			c.telemetrySeq = telSeq
			c.data = ws
			pc.wsCache[projKey] = c
			return ws
		}
	}

	ws := projectWorkspaceData{
		groupName:  groupName,
		projectKey: projKey,
		projectID:  projID,
		projRef:    projRef,
		taskIDs:    map[string]bool{},
		paneIDs:    map[string]bool{},
	}
	var paneSeeds []data.Record
	if a.panes != nil {
		paneSeeds = a.panes.railGroupPanes[groupName]
	}
	var scope data.ProjectScope
	if a.Store != nil {
		scope, _ = a.Store.ProjectScope(projID, paneSeeds, a.nav.showArchived)
	}
	ws.taskIDs, ws.paneIDs = scope.TaskIDs, scope.PaneIDs
	ws.panes, ws.relations = scope.Panes, scope.Relations
	if ws.panes == nil {
		ws.panes = paneSeeds
	}
	allTaskTree := data.BuildTree(scope.Tasks, "parent", "order", a.nav.showArchived)
	if projNode, ok := findProjectTreeNode(allTaskTree, projID); ok {
		ws.projRec = &projNode.Record
		ws.tasks = flattenTreeDescendants(projNode.Children)
		ws.taskTree = projNode.Children
	} else if projRef != nil && a.Store != nil {
		ws.projRec, _ = a.Store.Get(*projRef)
	}
	paneSchema := a.schema(data.KindPane)
	paneTitles := make(map[string]string, len(ws.panes))
	for _, p := range ws.panes {
		paneTitles[p.ID] = views.Title(paneSchema, p)
	}
	ws.pages, ws.unreadPages = projectPagesList(a.projectRecords(scope, data.KindPage))
	ws.allLanes = a.projectRecords(scope, data.KindLane)
	for _, lane := range ws.allLanes {
		if data.LaneProgressOf(lane).IsOpen() {
			ws.activeLanes = append(ws.activeLanes, lane)
		}
	}
	if ws.projectID != "" && a.Store != nil {
		ws.worktrees = a.Store.ProjectScopeWorktrees(scope, ws.allLanes)
	}
	ws.contextItems, ws.reviewContext, ws.contextRecords = a.projectUnifiedContextItems(ws, scope, paneTitles)
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
	ws.documents = a.projectRecords(scope, data.KindDocument)
	ws.notes = a.projectRecords(scope, data.KindNote)
	ws.questions, ws.openQuestions = projectQuestionsList(a.projectRecords(scope, data.KindQuestion))
	ws.commands = a.projectRecords(scope, data.KindCommand)
	a.populateProjectWorkspaceActivity(&ws)
	ws.cacheRailKey = railKey
	ws.cacheCtxGen = a.contextIndexGen()
	if a.Store != nil {
		if pc.wsCache == nil {
			pc.wsCache = map[string]cachedProjectWorkspace{}
		}
		postRev, _ := a.Store.LatestChangeSetNumber()
		ws.cacheRev = postRev
		pc.wsCache[projKey] = cachedProjectWorkspace{
			rev: postRev, telemetrySeq: telSeq, showArchived: a.nav.showArchived, ctxGen: ws.cacheCtxGen,
			groupName: groupName, projID: projID, railPanesKey: railKey, data: ws,
		}
	}
	return ws
}

func (a *App) buildProjectNavItems(defs []projectNavDef, ws projectWorkspaceData, activeKey, focusID string) []views.ProjectNavSection {
	pc := a.projectCtrl()
	treeEnabled := a.projectNavTreeEnabled()
	allowPeek := treeEnabled && a.projectNavSurfaceUnreadEnabled()
	lockedFocus := ""
	if locked := pc.activeNavItem(ws.projectKey); locked != nil {
		lockedFocus = locked.FocusID
	}
	navKey := fmt.Sprintf("%d|%d|%d|%t|%t|%t|%t|%t|%t|%s|%s|%s|%d|%s",
		ws.cacheRev, ws.cacheCtxGen, a.rt.live.telemetrySeq.Load(),
		a.nav.showArchived, treeEnabled, allowPeek,
		a.projectNavSurfaceContextUnreadEnabled(), a.projectNavUnreadDotOnlyEnabled(), a.projectNavLaneProgressBarEnabled(),
		activeKey,
		sortedTrueKeys(pc.expandedSections[ws.projectKey]),
		sortedTrueKeys(pc.expandedNodes[ws.projectKey]),
		a.navUnreadCutoff.UnixNano(),
		lockedFocus,
	)
	if pc.navCache != nil {
		if cached, ok := pc.navCache[ws.projectKey]; ok && cached.key == navKey {
			if pc.navTargets == nil {
				pc.navTargets = map[string]map[string]projectNavTarget{}
			}
			pc.navTargets[ws.projectKey] = cached.targets
			return cloneNavSectionsWithFocus(cached.sections, focusID, lockedFocus)
		}
	}

	counts := map[string]int{
		"tasks": len(ws.tasks), "pages": len(ws.pages), "context": len(ws.contextItems),
		"pinned": len(ws.pinnedItems), "panes": len(ws.panes), "lanes": len(ws.allLanes),
		"worktrees": len(ws.worktrees), "prs": len(ws.prItems), "documents": len(ws.documents),
		"notes": len(ws.notes), "questions": len(ws.questions), "commands": len(ws.commands),
		projectQueueSection: a.projectOpenQueueItems(ws.projectID),
	}
	pc.resetNavTargets(ws.projectKey)

	out := make([]views.ProjectNavSection, 0, len(defs))
	for _, d := range defs {
		expandable := treeEnabled && !d.Leaf && d.Key != "home" && d.Key != "signals" && counts[d.Key] > 0
		expanded := expandable && pc.isNavSectionExpanded(ws.projectKey, d.Key)
		sec := views.ProjectNavSection{
			Key: d.Key, Label: d.Label, Icon: d.Icon, Group: d.Group,
			Count: counts[d.Key], Active: d.Key == activeKey,
			Expandable: expandable, Expanded: expanded,
		}
		switch d.Key {
		case "signals":
			sec.Badge = "0"
		case "context":
			if ws.officialCount > 0 {
				sec.Badge = fmt.Sprintf("★%d·%d", ws.officialCount, len(ws.contextItems))
			}
		}
		if !d.Leaf && d.Key != "home" && d.Key != "signals" && counts[d.Key] > 0 {
			children, unreadCount := a.buildProjectNavChildren(ws, d.Key, focusID, expanded, allowPeek)
			sec.UnreadCount = unreadCount
			sec.Unread = unreadCount > 0
			if treeEnabled && len(children) > 0 {
				sec.Children = children
			}
		}
		out = append(out, sec)
	}
	pc.navBuilds++
	if pc.navCache == nil {
		pc.navCache = map[string]cachedProjectNav{}
	}
	pc.navCache[ws.projectKey] = cachedProjectNav{
		key:      navKey,
		sections: out,
		targets:  pc.navTargets[ws.projectKey],
	}
	return cloneNavSectionsWithFocus(out, focusID, lockedFocus)
}

func cloneNavSectionsWithFocus(src []views.ProjectNavSection, focusID, lockedFocus string) []views.ProjectNavSection {
	out := make([]views.ProjectNavSection, len(src))
	for i, s := range src {
		if len(s.Children) > 0 {
			s.Children = cloneNavNodesWithFocus(s.Children, focusID, lockedFocus)
		}
		out[i] = s
	}
	return out
}

func cloneNavNodesWithFocus(src []views.ProjectNavNode, focusID, lockedFocus string) []views.ProjectNavNode {
	out := make([]views.ProjectNavNode, len(src))
	for i, n := range src {
		nfid := projectNavNodePrefix + n.Section + "." + n.ID
		n.Active = focusID == nfid || lockedFocus == nfid
		if len(n.Children) > 0 {
			n.Children = cloneNavNodesWithFocus(n.Children, focusID, lockedFocus)
		}
		out[i] = n
	}
	return out
}

func (a *App) buildProjectSectionBody(s core.State, ws projectWorkspaceData, sec string) ui.ViewNode {
	switch sec {
	case "tasks":
		if a.projectOutlineAsPageEnabled() && ws.projectID != "" {
			return a.projectOutlineView(&s, ws.projectID)
		}
		return a.projectRecordPanel("project.tasks", projectTasksFlatTable, data.KindTask, "tasks", "Tasks", views.ScopeProjectContent, ws, ws.tasks,
			[]ui.Badge{{Text: fmt.Sprintf("%d tasks", len(ws.tasks))}})
	case "pages":
		return a.projectSectionPages(s, ws)
	case "signals":
		return a.projectSectionSignals()
	case "context":
		return a.projectSectionContext(ws)
	case "pinned":
		return a.projectContextPanel("project.pinned", projectPinnedTable, "pinned", "Pinned Links & Files", ws, ws.pinnedItems, projectContextColumns,
			[]ui.Badge{{Text: fmt.Sprintf("%d curated", len(ws.pinnedItems)), Tone: "accent"}, {Text: "N add · p official", Tone: "muted"}})
	case "panes":
		return a.projectTreePanel("project.panes", projectPanesTree, data.KindPane, "parentPane", "panes", scopeProjectPanes, ws, ws.panes,
			ui.Badge{Text: "enter jump live", Tone: "muted"})
	case "lanes":
		return a.projectTreePanel("project.lanes", projectLanesTree, data.KindLane, "parentLane", "lanes", views.ScopeProjectContent, ws, ws.allLanes,
			ui.Badge{Text: fmt.Sprintf("%d active", len(ws.activeLanes)), Tone: "accent"})
	case projectQueueSection:
		return a.projectSectionQueues(ws)
	case "worktrees":
		return a.projectSectionWorktrees(ws)
	case "prs":
		return a.projectContextPanel("project.prs", projectPRsTable, "prs", "PRs", ws, ws.prItems, projectContextColumns,
			[]ui.Badge{{Text: fmt.Sprintf("%d PRs", len(ws.prItems)), Tone: "accent"}})
	case "documents":
		return a.projectRecordPanel("project.documents", projectDocumentsTable, data.KindDocument, "documents", "Documents", scopeProjectDocuments, ws, ws.documents,
			[]ui.Badge{{Text: fmt.Sprintf("%d docs", len(ws.documents))}, {Text: "enter / o read", Tone: "muted"}})
	case "notes":
		return a.projectSectionNotes(ws)
	case "questions":
		badges := []ui.Badge{{Text: fmt.Sprintf("%d total", len(ws.questions))}}
		if ws.openQuestions > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d open", ws.openQuestions), Tone: "warning"})
		}
		return a.projectRecordPanel("project.questions", projectQuestionsTable, data.KindQuestion, "questions", "Questions & Decisions", views.ScopeProjectContent, ws, ws.questions, badges)
	case "commands":
		return a.projectRecordPanel("project.commands", projectCommandsTable, data.KindCommand, "commands", "Commands", views.ScopeProjectContent, ws, ws.commands,
			[]ui.Badge{{Text: fmt.Sprintf("%d commands", len(ws.commands))}, {Text: "enter / r run", Tone: "muted"}})
	default:
		return a.projectSectionHome(s, ws)
	}
}

// --- Section builders -----------------------------------------------------------

func (a *App) projectSectionHome(s core.State, ws projectWorkspaceData) ui.ViewNode {
	if openID := a.projectCtrl().openPageID(ws.projectKey); openID != "" {
		if page, err := a.Store.Get(core.Ref{Kind: data.KindPage, ID: openID}); err == nil && page != nil {
			return a.buildProjectInteractivePageNode(s, *page, false)
		}
		a.projectCtrl().setOpenPageID(ws.projectKey, "")
	}
	return a.buildProjectHomeSection(ws, true)
}

func (a *App) projectSectionPages(s core.State, ws projectWorkspaceData) ui.ViewNode {
	pc := a.projectCtrl()
	if openID := pc.openPageID(ws.projectKey); openID != "" {
		if page, err := a.Store.Get(core.Ref{Kind: data.KindPage, ID: openID}); err == nil && page != nil {
			return a.buildProjectInteractivePageNode(s, *page, false)
		}
		pc.setOpenPageID(ws.projectKey, "")
	}
	cacheKey := fmt.Sprintf("pages|%s|%d|%v|%s|%d", ws.projectKey, ws.cacheRev, a.nav.showArchived, ws.cacheRailKey, len(ws.pages))
	tbl, cached := a.cachedProjectTable(projectPagesTable, cacheKey)
	if !cached {
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
		paneTitles := make(map[string]string, len(ws.panes))
		paneSchema := a.schema(data.KindPane)
		for _, p := range ws.panes {
			paneTitles[p.ID] = views.Title(paneSchema, p)
		}
		for _, p := range ws.pages {
			ref := p.Ref()
			paneLabel := "project"
			if pRef, ok := p.RefField("pane"); ok {
				if t, found := paneTitles[pRef.ID]; found {
					paneLabel = t
				} else {
					paneLabel = a.title(pRef)
					paneTitles[pRef.ID] = paneLabel
				}
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
		tbl = a.keepProjectTable(projectPagesTable, table.New(cols, rows))
		a.storeProjectTableKey(projectPagesTable, cacheKey)
	}
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

func (a *App) projectTreePanel(panelID, treeID, kind, parentField, section, panelScope string, ws projectWorkspaceData, recs []data.Record, extra ...ui.Badge) ui.ViewNode {
	schema := a.schema(kind)
	tm := a.syncProjectSectionTree(treeID, ws, schema, recs, parentField)
	panel := views.TreeViewWith(schema, tm, panelID, treeID, panelScope, a.stickyTreeBreadcrumbsEnabled(), extra...)
	panel.Facts["section"] = section
	if len(panel.Children) > 0 {
		if panel.Children[0].Props == nil {
			panel.Children[0].Props = map[string]any{}
		}
		panel.Children[0].Props["scrollKey"] = ws.projectKey + "|" + section + "|" + tm.HoistID()
	}
	return panel
}

func (a *App) projectRecordPanel(panelID, tableID, kind, section, title, panelScope string, ws projectWorkspaceData, recs []data.Record, badges []ui.Badge) ui.ViewNode {
	st := a.styleOptions()
	cacheKey := fmt.Sprintf("rec|%s|%s|%s|%d|%v|%v:%v|%d", tableID, ws.projectKey, kind, ws.cacheRev, a.nav.showArchived, st.RowSemanticStyling, st.StatusIcons, len(recs))
	tbl, cached := a.cachedProjectTable(tableID, cacheKey)
	if !cached {
		schema := a.schema(kind)
		fresh := views.TableFromRecordsWith(schema, recs, st)
		a.resolveTableRefCells(schema, recs, fresh)
		for i, rec := range recs {
			if a.openable(rec) {
				fresh.Rows[i].Facts[factOpenable] = true
			}
		}
		tbl = a.keepProjectTable(tableID, fresh)
		a.storeProjectTableKey(tableID, cacheKey)
	}
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
	cacheKey := fmt.Sprintf("wt|%s|%d|%v|%s|%d", ws.projectKey, ws.cacheRev, a.nav.showArchived, ws.cacheRailKey, len(ws.worktrees))
	tbl, cached := a.cachedProjectTable(projectWorktreesTable, cacheKey)
	if !cached {
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
		tbl = a.keepProjectTable(projectWorktreesTable, table.New(cols, rows))
		a.storeProjectTableKey(projectWorktreesTable, cacheKey)
	}
	node := tbl.View(projectWorktreesTable)
	node.Scope = scopeProjectWorktrees
	panel := ui.ViewNode{
		ID: "project.worktrees", Kind: ui.KindPanel, Scope: scopeProjectWorktrees, Title: "Worktrees", Border: true,
		Badges: []ui.Badge{{Text: fmt.Sprintf("%d paths", len(ws.worktrees))}},
		Facts:  map[string]any{"section": "worktrees"}, Children: []ui.ViewNode{node},
	}
	if a.livePointerChipsEnabled() {
		if row, ok := tbl.FocusedRow(); ok {
			if i := slices.IndexFunc(ws.worktrees, func(wt data.ProjectWorktree) bool { return wt.Path == row.ID }); i >= 0 {
				panel = withPointerChips(panel, "project.worktrees.chips", worktreePointerChips(ws.worktrees[i]))
			}
		}
	}
	return panel
}

func (a *App) projectSectionNotes(ws projectWorkspaceData) ui.ViewNode {
	st := a.styleOptions()
	cacheKey := fmt.Sprintf("notes|%s|%d|%v|%v:%v|%d", ws.projectKey, ws.cacheRev, a.nav.showArchived, st.RowSemanticStyling, st.StatusIcons, len(ws.notes))
	tbl, cached := a.cachedProjectTable(projectNotesTable, cacheKey)
	if !cached {
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
		tbl = a.keepProjectTable(projectNotesTable, table.New(cols, rows))
		a.storeProjectTableKey(projectNotesTable, cacheKey)
	}
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

func (a *App) syncProjectSectionTree(treeID string, ws projectWorkspaceData, schema data.Schema, recs []data.Record, parentField string) *tree.Model {
	st := a.styleOptions()
	guides := a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTreeGuides, true)
	var telSeq uint64
	if schema.Kind == data.KindPane {
		telSeq = a.rt.live.telemetrySeq.Load()
	}
	cacheKey := fmt.Sprintf("tree|%s|%s|%s|%d|%d|%v|%s|%v:%v:%v|%d", treeID, ws.projectKey, schema.Kind, ws.cacheRev, telSeq, a.nav.showArchived, ws.cacheRailKey, st.RowSemanticStyling, st.StatusIcons, guides, len(recs))
	if tm, ok := a.cachedProjectTree(treeID, cacheKey); ok {
		return tm
	}
	pc := a.projectCtrl()
	tm := pc.trees[treeID]
	if tm == nil {
		tm = tree.New(nil)
		pc.trees[treeID] = tm
	}
	nodes := data.BuildTree(recs, parentField, "", a.nav.showArchived)
	refTitles := map[core.Ref]string{}
	resolveRef := func(ref core.Ref) string {
		if t, ok := refTitles[ref]; ok {
			return t
		}
		t := a.title(ref)
		refTitles[ref] = t
		return t
	}
	opts := views.TreeOptions{
		Style: st, GapTopLevel: false, ShowFoldCount: true, ResolveRef: resolveRef,
		Guides: guides,
		EnrichNode: func(rec data.Record, nd *tree.Node) {
			if a.openable(rec) {
				if nd.Facts == nil {
					nd.Facts = map[string]any{}
				}
				nd.Facts[factOpenable] = true
			}
			if rec.Kind == data.KindPane {
				if nd.Facts == nil {
					nd.Facts = map[string]any{}
				}
				nd.Facts["hasPendingApproval"] = len(a.pendingApprovalsFor(rec.ID)) > 0
			}
		},
	}
	built := views.TreeFromNodes(schema, nodes, opts)
	tm.DefaultFolded, tm.GapTopLevel, tm.Guides, tm.ShowFoldCount = false, false, guides, true
	tm.ScrollOff, tm.PageSize = a.scrollOff(), a.treeViewportHeight(treeID)
	tm.SetNodes(built.Nodes)
	a.storeProjectTreeKey(treeID, cacheKey)
	return tm
}

func (a *App) projectRecords(scope data.ProjectScope, kind string) []data.Record {
	if a.Store == nil {
		return nil
	}
	recs, _ := a.Store.ProjectRecords(scope, kind, a.nav.showArchived)
	return recs
}

func projectPagesList(raw []data.Record) ([]data.Record, int) {
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

func projectQuestionsList(out []data.Record) ([]data.Record, int) {
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
