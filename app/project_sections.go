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
	"ofam.dev/ochazuke/widgets/tree"
)

const projectNavCompactPeekLimit = 4

var legacyProjectNavSections = []projectNavDef{
	{Key: "home", Label: "Home", Icon: "⌂", Group: "Core"},
	{Key: "tasks", Label: "Tasks", Icon: "☐", Group: "Core"},
	{Key: "pages", Label: "Pages", Icon: "◫", Group: "Core"},
	{Key: "signals", Label: "Needs Attention", Icon: "!", Group: "Core"},
	{Key: "context", Label: "Context", Icon: "◈", Group: "Context", EntryFocus: projectContextTable},
	{Key: "pinned", Label: "Pinned Links & Files", Icon: "★", Group: "Context"},
	{Key: "panes", Label: "Panes", Icon: "▣", Group: "Workspace"},
	{Key: "lanes", Label: "Lanes", Icon: "⎇", Group: "Workspace"},
	{Key: projectQueueSection, Label: "Queues", Icon: "⇶", Group: "Workspace", EntryFocus: views.QueueListID, Leaf: true},
	{Key: projectThreadsSection, Label: "Threads", Icon: "✉", Group: "Workspace", EntryFocus: views.ThreadsListID, Leaf: true},
	{Key: "worktrees", Label: "Worktrees", Icon: "⌘", Group: "Workspace"},
	{Key: "prs", Label: "PRs", Icon: "⇅", Group: "Workspace"},
	{Key: "documents", Label: "Documents", Icon: "▤", Group: "Knowledge"},
	{Key: "notes", Label: "Notes", Icon: "✎", Group: "Knowledge"},
	{Key: "questions", Label: "Questions & Decisions", Icon: "?", Group: "Knowledge"},
	{Key: "commands", Label: "Commands", Icon: "$", Group: "Knowledge"},
}

func projectNavBaseSections(priorityOrder bool) []projectNavDef {
	if priorityOrder {
		return defaultProjectNavSections
	}
	return legacyProjectNavSections
}

func projectNavDefsCacheKey(defs []projectNavDef) string {
	var b strings.Builder
	for i, d := range defs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(d.Key)
		b.WriteByte(':')
		b.WriteString(d.Group)
	}
	return b.String()
}

func countVisibleNavNodes(nodes []views.ProjectNavNode) int {
	total := 0
	for _, n := range nodes {
		total += 1 + countVisibleNavNodes(n.Children)
	}
	return total
}

func trimNavNodesToBudget(nodes []views.ProjectNavNode, budget *int) []views.ProjectNavNode {
	if *budget <= 0 || len(nodes) == 0 {
		return nil
	}
	out := make([]views.ProjectNavNode, 0, min(len(nodes), *budget))
	for _, n := range nodes {
		if *budget <= 0 {
			break
		}
		*budget--
		if len(n.Children) > 0 {
			n.Children = trimNavNodesToBudget(n.Children, budget)
		}
		out = append(out, n)
	}
	return out
}

func capPeekedNavChildren(pc *ProjectController, projKey, secKey string, children []views.ProjectNavNode, limit int) []views.ProjectNavNode {
	total := countVisibleNavNodes(children)
	if limit <= 0 || total <= limit {
		return children
	}
	budget := limit
	trimmed := trimNavNodesToBudget(children, &budget)
	hidden := total - countVisibleNavNodes(trimmed)
	if hidden <= 0 {
		return trimmed
	}
	moreFocusID := projectNavNodePrefix + secKey + "." + projectNavOverflowNodeID
	label := fmt.Sprintf("+%d more", hidden)
	trimmed = append(trimmed, views.ProjectNavNode{
		ID:      projectNavOverflowNodeID,
		Section: secKey,
		Label:   label,
		Tone:    "muted",
		Peeked:  true,
		Facts: map[string]any{
			"navOverflow":   true,
			"overflowCount": hidden,
		},
	})
	pc.registerNavTarget(projKey, projectNavTarget{
		FocusID:       moreFocusID,
		Section:       secKey,
		NodeID:        projectNavOverflowNodeID,
		ParentFocusID: projectNavPrefix + secKey,
		Label:         label,
	})
	return trimmed
}

func maybeCapPeekedNavChildren(pc *ProjectController, projKey, secKey string, children []views.ProjectNavNode, compact, expanded, allowPeek bool) []views.ProjectNavNode {
	if !compact || expanded || !allowPeek || len(children) == 0 {
		return children
	}
	return capPeekedNavChildren(pc, projKey, secKey, children, projectNavCompactPeekLimit)
}

func projectNavSectionBadge(secKey string, count, officialCount, ctxLen int, compact bool) string {
	switch secKey {
	case "signals":
		if !compact {
			return "0"
		}
		return ""
	case "context":
		if officialCount > 0 {
			return fmt.Sprintf("★%d·%d", officialCount, ctxLen)
		}
	}
	if !compact && secKey != "home" && count == 0 {
		return "0"
	}
	return ""
}

func (a *App) projectPanesTelemetryFP(panes []data.Record) string {
	if len(panes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range panes {
		status, _, needsYou := a.effectivePaneLiveStatus(p)
		fmt.Fprintf(&b, "%s:%s:%t:%d|", p.ID, status, needsYou, len(a.pendingApprovalsFor(p.ID)))
	}
	return b.String()
}

func (a *App) projectPanesHomeTelemetryFP(panes []data.Record) string {
	if len(panes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range panes {
		status, pt, pending := a.effectiveHookState(p.ID)
		turns := 0
		var lastTurn int64
		if pt != nil && len(pt.RecentTurns) > 0 {
			turns = len(pt.RecentTurns)
			lastTurn = pt.RecentTurns[turns-1].At.UnixNano()
		}
		fmt.Fprintf(&b, "%s:%s:%d:%d:%d|", p.ID, status, len(pending), turns, lastTurn)
	}
	return b.String()
}

func (a *App) refreshCachedNavPanes(pc *ProjectController, ws projectWorkspaceData, cached *cachedProjectNav, focusID string, treeEnabled, allowPeek, compact bool) {
	if cached == nil {
		return
	}
	panesIdx := slices.IndexFunc(cached.sections, func(s views.ProjectNavSection) bool { return s.Key == "panes" })
	if panesIdx < 0 || len(ws.panes) == 0 {
		return
	}
	nextTargets := make(map[string]projectNavTarget, len(cached.targets))
	for k, v := range cached.targets {
		if v.Section != "panes" {
			nextTargets[k] = v
		}
	}
	pc.navTargets[ws.projectKey] = nextTargets
	expanded := treeEnabled && pc.isNavSectionExpanded(ws.projectKey, "panes")
	children, unreadCount := a.buildProjectNavChildren(ws, "panes", focusID, expanded, allowPeek)
	children = maybeCapPeekedNavChildren(pc, ws.projectKey, "panes", children, compact, expanded, allowPeek)
	sec := cached.sections[panesIdx]
	sec.UnreadCount = unreadCount
	sec.Unread = unreadCount > 0
	sec.Children = nil
	if treeEnabled && len(children) > 0 {
		sec.Children = children
	}
	cached.sections[panesIdx] = sec
	cached.targets = pc.navTargets[ws.projectKey]
}

func (a *App) cachedProjectNavSections(pc *ProjectController, ws projectWorkspaceData, navKey, paneTelFP, activeKey, focusID, lockedFocus, openPage string, treeEnabled, allowPeek, compact bool) ([]views.ProjectNavSection, bool) {
	if pc.navCache == nil {
		return nil, false
	}
	cached, ok := pc.navCache[ws.projectKey]
	if !ok || cached.key != navKey {
		return nil, false
	}
	if pc.navTargets == nil {
		pc.navTargets = map[string]map[string]projectNavTarget{}
	}
	if cached.paneTelFP != paneTelFP {
		a.refreshCachedNavPanes(pc, ws, &cached, focusID, treeEnabled, allowPeek, compact)
		cached.paneTelFP = paneTelFP
		pc.navCache[ws.projectKey] = cached
	}
	pc.navTargets[ws.projectKey] = cached.targets
	return cloneNavSectionsWithFocus(cached.sections, activeKey, focusID, lockedFocus, openPage), true
}

func (a *App) buildProjectNavItems(defs []projectNavDef, ws projectWorkspaceData, activeKey, focusID string) []views.ProjectNavSection {
	pc := a.projectCtrl()
	treeEnabled := a.projectNavTreeEnabled()
	allowPeek := a.projectNavPeeksUnread()
	compact := a.projectNavCompactEnabled()
	lockedFocus := ""
	if locked := pc.activeNavItem(ws.projectKey); locked != nil {
		lockedFocus = locked.FocusID
	}
	openPage := pc.openPageID(ws.projectKey)
	paneTelFP := a.projectPanesTelemetryFP(ws.panes)
	navKey := fmt.Sprintf("%d|%d|%t|%t|%t|%t|%t|%t|%t|%s|%s|%s|%d|%s",
		ws.cacheRev, ws.cacheCtxGen,
		a.nav.showArchived, treeEnabled, allowPeek, compact,
		a.projectNavSurfaceContextUnreadEnabled(), a.projectNavUnreadDotOnlyEnabled(), a.projectNavLaneProgressBarEnabled(),
		projectNavDefsCacheKey(defs),
		sortedTrueKeys(pc.expandedSections[ws.projectKey]),
		sortedTrueKeys(pc.expandedNodes[ws.projectKey]),
		a.navUnreadCutoff.UnixNano(),
		a.archiveAnimCacheKey(),
	)
	if hit, ok := a.cachedProjectNavSections(pc, ws, navKey, paneTelFP, activeKey, focusID, lockedFocus, openPage, treeEnabled, allowPeek, compact); ok {
		return hit
	}

	counts := map[string]int{
		"tasks": len(ws.tasks), "pages": len(ws.pages), "context": len(ws.contextItems),
		"pinned": len(ws.pinnedItems), "panes": len(ws.panes), "lanes": len(ws.allLanes),
		"worktrees": len(ws.worktrees), "prs": len(ws.prItems), "documents": len(ws.documents),
		"notes": len(ws.notes), "questions": len(ws.questions), "commands": len(ws.commands),
		projectQueueSection:   a.projectOpenQueueItems(ws.projectID),
		projectThreadsSection: a.projectThreadCount(ws.projectID),
	}
	pc.resetNavTargets(ws.projectKey)

	out := make([]views.ProjectNavSection, 0, len(defs))
	for _, d := range defs {
		expandable := treeEnabled && !d.Leaf && d.Key != "home" && d.Key != "signals" && counts[d.Key] > 0
		expanded := expandable && pc.isNavSectionExpanded(ws.projectKey, d.Key)
		sec := views.ProjectNavSection{
			Key: d.Key, Label: d.Label, Icon: d.Icon, Group: d.Group,
			Badge:      projectNavSectionBadge(d.Key, counts[d.Key], ws.officialCount, len(ws.contextItems), compact),
			Count:      counts[d.Key],
			Active:     d.Key == activeKey,
			Expandable: expandable, Expanded: expanded,
		}
		if !d.Leaf && d.Key != "home" && d.Key != "signals" && counts[d.Key] > 0 {
			children, unreadCount := a.buildProjectNavChildren(ws, d.Key, focusID, expanded, allowPeek)
			children = maybeCapPeekedNavChildren(pc, ws.projectKey, d.Key, children, compact, expanded, allowPeek)
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
		key:       navKey,
		paneTelFP: paneTelFP,
		sections:  out,
		targets:   pc.navTargets[ws.projectKey],
	}
	return cloneNavSectionsWithFocus(out, activeKey, focusID, lockedFocus, openPage)
}

func cloneNavSectionsWithFocus(src []views.ProjectNavSection, activeKey, focusID, lockedFocus, openPage string) []views.ProjectNavSection {
	out := make([]views.ProjectNavSection, len(src))
	for i, s := range src {
		s.Active = s.Key == activeKey
		if len(s.Children) > 0 {
			s.Children = cloneNavNodesWithFocus(s.Children, focusID, lockedFocus, openPage)
		}
		out[i] = s
	}
	return out
}

func cloneNavNodesWithFocus(src []views.ProjectNavNode, focusID, lockedFocus, openPage string) []views.ProjectNavNode {
	out := make([]views.ProjectNavNode, len(src))
	for i, n := range src {
		nfid := projectNavNodePrefix + n.Section + "." + n.ID
		n.Active = focusID == nfid || lockedFocus == nfid || (n.Section == "pages" && openPage != "" && n.ID == openPage)
		if len(n.Children) > 0 {
			n.Children = cloneNavNodesWithFocus(n.Children, focusID, lockedFocus, openPage)
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
	case projectThreadsSection:
		return a.projectSectionThreads(ws)
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
	pages := a.filterProjectRecords(projectPagesTable, data.KindPage, ws.pages)
	cacheKey := fmt.Sprintf("pages|%s|%d|%v|%s|%d|%s", ws.projectKey, ws.cacheRev, a.nav.showArchived, ws.cacheRailKey, len(pages), a.zeroFieldSpecs[projectPagesTable].FilterSummary())
	tbl, cached := a.cachedProjectTable(projectPagesTable, cacheKey)
	if !cached {
		cols := []table.Column{
			{Key: "title", Title: "Page"},
			{Key: "pageKind", Title: "Kind", Width: 11},
			{Key: "pane", Title: "Pane", Width: 18},
			{Key: "flags", Title: "Flags", Width: 5},
		}
		rows := make([]table.Row, 0, len(pages)+1)
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
		for _, p := range pages {
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
		ID: "project.pages", Kind: ui.KindPanel, Scope: scopeProjectPages, Filter: &ui.FilterSpec{ID: projectPagesTable}, Title: "Pages", Border: true,
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
	a.registerTreeFilterable(treeID, tm)
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

func (a *App) filterProjectRecords(tableID, kind string, recs []data.Record) []data.Record {
	a.rememberTableRecords(tableID, recs)
	if !a.hasActiveZeroFieldFilter(tableID) {
		return recs
	}
	return a.tableQuery(tableID, a.schema(kind)).Filter(recs)
}

func (a *App) projectRecordPanel(panelID, tableID, kind, section, title, panelScope string, ws projectWorkspaceData, recs []data.Record, badges []ui.Badge) ui.ViewNode {
	filtered := a.filterProjectRecords(tableID, kind, recs)
	st := a.styleOptions()
	cacheKey := fmt.Sprintf("rec|%s|%s|%s|%d|%v|%v:%v|%d|%s", tableID, ws.projectKey, kind, ws.cacheRev, a.nav.showArchived, st.RowSemanticStyling, st.StatusIcons, len(filtered), a.zeroFieldSpecs[tableID].FilterSummary())
	tbl, cached := a.cachedProjectTable(tableID, cacheKey)
	if !cached {
		schema := a.schema(kind)
		fresh := views.TableFromRecordsWith(schema, filtered, st)
		a.resolveTableRefCells(ws, schema, filtered, fresh)
		for i, rec := range filtered {
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
		ID: panelID, Kind: ui.KindPanel, Scope: panelScope, Filter: &ui.FilterSpec{ID: tableID}, Title: title, Border: true,
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
		ID: "project.worktrees", Kind: ui.KindPanel, Scope: scopeProjectWorktrees, Filter: &ui.FilterSpec{ID: projectWorktreesTable}, Title: "Worktrees", Border: true,
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
	notes := a.filterProjectRecords(projectNotesTable, data.KindNote, ws.notes)
	st := a.styleOptions()
	cacheKey := fmt.Sprintf("notes|%s|%d|%v|%v:%v|%d|%s", ws.projectKey, ws.cacheRev, a.nav.showArchived, st.RowSemanticStyling, st.StatusIcons, len(notes), a.zeroFieldSpecs[projectNotesTable].FilterSummary())
	tbl, cached := a.cachedProjectTable(projectNotesTable, cacheKey)
	if !cached {
		cols := []table.Column{
			{Key: "title", Title: "Note"}, {Key: "target", Title: "Target", Width: 20},
			{Key: "authorKind", Title: "Author", Width: 9}, {Key: "body", Title: "Excerpt"},
		}
		rows := make([]table.Row, len(notes))
		schema := a.schema(data.KindNote)
		refTitles := a.projectWorkspaceRefTitles(ws)
		aboutByNote := firstOutgoingRelByFrom(ws.relations, data.RelAbout, data.KindNote)
		for i, n := range notes {
			ref := n.Ref()
			target := a.projectNoteTargetLabelFrom(n, refTitles, aboutByNote)
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
		ID: "project.notes", Kind: ui.KindPanel, Scope: views.ScopeProjectContent, Filter: &ui.FilterSpec{ID: projectNotesTable}, Title: "Notes", Border: true,
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
	a.registerTableFilterable(id, tbl)
	return tbl
}

func (a *App) projectWorkspaceRefTitles(ws projectWorkspaceData) map[core.Ref]string {
	total := len(ws.tasks) + len(ws.panes) + len(ws.allLanes) + len(ws.pages) + len(ws.documents) + len(ws.notes) + len(ws.questions) + len(ws.commands) + 1
	titles := make(map[core.Ref]string, total)
	if ws.projRec != nil {
		titles[ws.projRec.Ref()] = views.Title(a.schema(ws.projRec.Kind), *ws.projRec)
	}
	buckets := []struct {
		kind string
		recs []data.Record
	}{
		{data.KindTask, ws.tasks},
		{data.KindPane, ws.panes},
		{data.KindLane, ws.allLanes},
		{data.KindPage, ws.pages},
		{data.KindDocument, ws.documents},
		{data.KindNote, ws.notes},
		{data.KindQuestion, ws.questions},
		{data.KindCommand, ws.commands},
	}
	for _, b := range buckets {
		if len(b.recs) == 0 {
			continue
		}
		schema := a.schema(b.kind)
		for _, rec := range b.recs {
			titles[rec.Ref()] = views.Title(schema, rec)
		}
	}
	return titles
}

func firstOutgoingRelByFrom(rels []data.Relation, relKind, fromKind string) map[string]core.Ref {
	out := make(map[string]core.Ref, len(rels))
	for _, rel := range rels {
		if rel.Kind != relKind || rel.From.Kind != fromKind || rel.From.ID == "" {
			continue
		}
		if _, exists := out[rel.From.ID]; !exists {
			out[rel.From.ID] = rel.To
		}
	}
	return out
}

func (a *App) resolveWorkspaceRefTitle(ref core.Ref, titles map[core.Ref]string) string {
	if t, ok := titles[ref]; ok {
		return t
	}
	t := a.title(ref)
	titles[ref] = t
	return t
}

func (a *App) resolveTableRefCells(ws projectWorkspaceData, schema data.Schema, recs []data.Record, tbl *table.Model) {
	names := a.projectWorkspaceRefTitles(ws)
	for _, f := range schema.Fields {
		if f.Type != core.ParamRef {
			continue
		}
		for i, rec := range recs {
			ref, ok := rec.RefField(f.Name)
			if !ok {
				continue
			}
			tbl.Rows[i].Cells[f.Name] = a.resolveWorkspaceRefTitle(ref, names)
		}
	}
}

func (a *App) syncProjectSectionTree(treeID string, ws projectWorkspaceData, schema data.Schema, recs []data.Record, parentField string) *tree.Model {
	st := a.styleOptions()
	guides := a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTreeGuides, true)
	var telSeq uint64
	if schema.Kind == data.KindPane {
		telSeq = a.telemetryViewSeq()
	}
	cacheKey := fmt.Sprintf("tree|%s|%s|%s|%d|%d|%v|%s|%v:%v:%v|%d|%s", treeID, ws.projectKey, schema.Kind, ws.cacheRev, telSeq, a.nav.showArchived, ws.cacheRailKey, st.RowSemanticStyling, st.StatusIcons, guides, len(recs), a.archiveAnimCacheKey())
	if tm, ok := a.cachedProjectTree(treeID, cacheKey); ok {
		return tm
	}
	pc := a.projectCtrl()
	tm := pc.trees[treeID]
	if tm == nil {
		tm = tree.New(nil)
		pc.trees[treeID] = tm
	}
	nodes := data.BuildTree(recs, parentField, "", a.treeIncludesArchived(schema.Kind))
	refTitles := a.projectWorkspaceRefTitles(ws)
	resolveRef := func(ref core.Ref) string {
		return a.resolveWorkspaceRefTitle(ref, refTitles)
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
			a.applyArchiveAnimToTreeNode(rec, nd)
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

// projectActiveRecords are a project's records of a kind as its lists show
// them: see activeRecords.
func (a *App) projectActiveRecords(scope data.ProjectScope, kind string) []data.Record {
	if a.Store == nil {
		return nil
	}
	all, _ := a.Store.ProjectRecords(scope, kind, true)
	return a.activeRecords(kind, all)
}

func recordRecencyTime(r data.Record) time.Time {
	if !r.UpdatedAt.IsZero() {
		return r.UpdatedAt
	}
	return r.CreatedAt
}

func compareRecordsNewestFirst(x, y data.Record) int {
	if c := recordRecencyTime(y).Compare(recordRecencyTime(x)); c != 0 {
		return c
	}
	return y.CreatedAt.Compare(x.CreatedAt)
}

func sortRecordsNewestFirst(recs []data.Record) []data.Record {
	out := slices.Clone(recs)
	slices.Reverse(out)
	slices.SortStableFunc(out, compareRecordsNewestFirst)
	return out
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
	slices.Reverse(out)
	slices.SortStableFunc(out, compareRecordsNewestFirst)
	return out, unread
}

func projectQuestionsList(raw []data.Record) ([]data.Record, int) {
	out := slices.Clone(raw)
	openCount := 0
	for _, q := range out {
		if q.Text("status") == "open" {
			openCount++
		}
	}
	slices.Reverse(out)
	slices.SortStableFunc(out, func(x, y data.Record) int {
		xo, yo := x.Text("status") == "open", y.Text("status") == "open"
		if xo != yo {
			if xo {
				return -1
			}
			return 1
		}
		return compareRecordsNewestFirst(x, y)
	})
	return out, openCount
}

func (a *App) projectNoteTargetLabelFrom(n data.Record, titles map[core.Ref]string, aboutByNote map[string]core.Ref) string {
	if tRef, ok := n.RefField("task"); ok {
		return a.resolveWorkspaceRefTitle(tRef, titles)
	}
	if to, ok := aboutByNote[n.ID]; ok {
		return a.resolveWorkspaceRefTitle(to, titles)
	}
	if pRef, ok := n.RefField("pane"); ok {
		return a.resolveWorkspaceRefTitle(pRef, titles)
	}
	if a.Store != nil {
		if rels, err := a.Store.Related(n.Ref(), data.RelAbout, data.Outgoing); err == nil && len(rels) > 0 {
			return a.resolveWorkspaceRefTitle(rels[0].To, titles)
		}
	}
	return ""
}
