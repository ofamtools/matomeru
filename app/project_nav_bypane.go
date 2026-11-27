package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

const byPaneSectionPrefix = "bypane_"

var projectNavModeCycle = []string{
	views.ProjectNavModeSections,
	views.ProjectNavModeByPane,
}

func normalizeProjectNavMode(raw string) string {
	if strings.TrimSpace(raw) == views.ProjectNavModeByPane {
		return views.ProjectNavModeByPane
	}
	return views.ProjectNavModeSections
}

func (a *App) defaultProjectNavMode() string {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return views.ProjectNavModeSections
	}
	return normalizeProjectNavMode(a.cfg.Settings.String(SetProjectNavMode, views.ProjectNavModeSections))
}

func (pc *ProjectController) navMode(projKey string) string {
	if pc == nil || pc.navModeByProject == nil || projKey == "" {
		return ""
	}
	return pc.navModeByProject[projKey]
}

func (pc *ProjectController) setNavMode(projKey, mode string) {
	if pc == nil || projKey == "" {
		return
	}
	if pc.navModeByProject == nil {
		pc.navModeByProject = map[string]string{}
	}
	pc.navModeByProject[projKey] = normalizeProjectNavMode(mode)
}

func (a *App) projectNavMode(projKey string) string {
	if a != nil && a.proj != nil {
		if override := a.proj.navMode(projKey); override != "" {
			return normalizeProjectNavMode(override)
		}
	}
	return a.defaultProjectNavMode()
}

func (a *App) cycleProjectNavMode(s core.State, dir int) core.StepResult {
	pc := a.projectCtrl()
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		projKey = pc.activeProjectKey
	}
	cur := a.projectNavMode(projKey)
	next, _ := traits.Cycle(projectNavModeCycle, cur, dir, true)
	pc.setNavMode(projKey, next)
	a.viewMemo.gen++
	root := a.View(s)
	if curID := s.ActiveFrame().FocusID; curID != "" && root.Find(curID) == nil {
		if homeID := projectNavPrefix + "home"; root.Find(homeID) != nil {
			s = withFocus(s, homeID)
		}
	}
	a.say("project nav: %s", next)
	return core.Next(s)
}

// --- By-pane focus & dispatch helpers -------------------------------------------

func byPaneSectionKey(paneID string) string {
	return byPaneSectionPrefix + paneID
}

func byPaneSectionPaneID(secKey string) (string, bool) {
	id, ok := strings.CutPrefix(secKey, byPaneSectionPrefix)
	return id, ok && id != ""
}

func isByPaneSectionFocus(id string) bool {
	sec, isNode := projectNavFocusSection(id)
	if isNode {
		return false
	}
	_, ok := byPaneSectionPaneID(sec)
	return ok
}

func isByPaneSubgroupFocus(id string) bool {
	sec, isNode := projectNavFocusSection(id)
	if !isNode {
		return false
	}
	_, ok := byPaneSectionPaneID(sec)
	return ok
}

func isProjectNavDirectOpenFocus(id string) bool {
	return strings.HasPrefix(id, projectNavNodePrefix) || isByPaneSectionFocus(id)
}

func (a *App) syncByPaneFocusState(pc *ProjectController, projKey, id string) {
	if !isByPaneSectionFocus(id) && !isByPaneSubgroupFocus(id) {
		return
	}
	pc.setOpenPageID(projKey, "")
	if pc.childPaneByProject != nil {
		delete(pc.childPaneByProject, projKey)
	}
	pc.setActiveNavItem(projKey, nil)
}

func (a *App) resolveProjectSectionOrByPaneBody(s core.State, ws projectWorkspaceData, activeKey, id string) ui.ViewNode {
	if isByPaneSectionFocus(id) {
		if target, ok := a.projectCtrl().navTarget(ws.projectKey, id); ok {
			return a.buildProjectNavItemBody(s, ws, target, true)
		}
	}
	return a.buildProjectSectionBody(s, ws, activeKey)
}

func (a *App) openProjectNavTarget(s core.State, id string) core.StepResult {
	if isByPaneSubgroupFocus(id) {
		res, _ := a.foldProjectNavAt(s, id, "toggle")
		return res
	}
	return a.projectNavNodeOpen(s, id)
}

func (a *App) buildProjectNav(defs []projectNavDef, ws projectWorkspaceData, activeKey, focusID string) []views.ProjectNavSection {
	if a.projectNavMode(ws.projectKey) == views.ProjectNavModeByPane {
		return a.buildProjectNavByPane(ws, activeKey, focusID)
	}
	return a.buildProjectNavItems(defs, ws, activeKey, focusID)
}

// --- Workspace partitioning by pane ---------------------------------------------

type byPaneBucket struct {
	pane      data.Record
	pages     []data.Record
	questions []data.Record
	lanes     []data.Record
	context   []CollectedContextItem
	commands  []data.Record
	notes     []data.Record
}

type byPanePartition struct {
	panes               []byPaneBucket
	unassignedPages     []data.Record
	unassignedQuestions []data.Record
	unassignedLanes     []data.Record
	unassignedContext   []CollectedContextItem
	unassignedCommands  []data.Record
	unassignedNotes     []data.Record
	laneTreeByID        map[string]data.TreeNode
}

func indexLaneTreeNodes(nodes []data.TreeNode, dst map[string]data.TreeNode) {
	for _, nd := range nodes {
		dst[nd.Record.ID] = nd
		if len(nd.Children) > 0 {
			indexLaneTreeNodes(nd.Children, dst)
		}
	}
}

func sortPaneLanes(lanes []data.Record) {
	slices.SortStableFunc(lanes, func(x, y data.Record) int {
		xo := data.LaneProgressOf(x).IsOpen()
		yo := data.LaneProgressOf(y).IsOpen()
		if xo != yo {
			if xo {
				return -1
			}
			return 1
		}
		if cmp := y.UpdatedAt.Compare(x.UpdatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(x.ID, y.ID)
	})
}

func splitRecordsByPaneRef(recs []data.Record, paneRefToIdx map[core.Ref]int) ([][]data.Record, []data.Record) {
	perPane := make([][]data.Record, len(paneRefToIdx))
	var unassigned []data.Record
	for _, rec := range recs {
		if pRef, ok := rec.RefField("pane"); ok {
			if idx, found := paneRefToIdx[pRef]; found {
				perPane[idx] = append(perPane[idx], rec)
				continue
			}
		}
		unassigned = append(unassigned, rec)
	}
	return perPane, unassigned
}

func partitionLanesByPane(ws projectWorkspaceData, paneRefToIdx map[core.Ref]int) ([][]data.Record, []data.Record) {
	perPane := make([][]data.Record, len(paneRefToIdx))
	laneByRef := make(map[core.Ref]data.Record, len(ws.allLanes))
	for _, l := range ws.allLanes {
		laneByRef[l.Ref()] = l
	}
	claimed := map[string]bool{}
	seen := map[string]bool{}
	for _, rel := range ws.relations {
		if rel.Kind != data.RelClaim {
			continue
		}
		idx, hasPane := paneRefToIdx[rel.To]
		lane, hasLane := laneByRef[rel.From]
		if !hasPane || !hasLane {
			continue
		}
		key := fmt.Sprintf("%d|%s", idx, lane.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		perPane[idx] = append(perPane[idx], lane)
		claimed[lane.ID] = true
	}
	var unassigned []data.Record
	for _, l := range ws.allLanes {
		if !claimed[l.ID] {
			unassigned = append(unassigned, l)
		}
	}
	return perPane, unassigned
}

func partitionNotesByPane(ws projectWorkspaceData, paneRefToIdx map[core.Ref]int) ([][]data.Record, []data.Record) {
	perPane := make([][]data.Record, len(paneRefToIdx))
	notePaneIdx := map[string]int{}
	for _, rel := range ws.relations {
		if rel.Kind != data.RelAbout {
			continue
		}
		if idx, found := paneRefToIdx[rel.To]; found {
			notePaneIdx[rel.From.ID] = idx
		}
	}
	var unassigned []data.Record
	for _, n := range ws.notes {
		if pRef, ok := n.RefField("pane"); ok {
			if idx, found := paneRefToIdx[pRef]; found {
				perPane[idx] = append(perPane[idx], n)
				continue
			}
		}
		if idx, found := notePaneIdx[n.ID]; found {
			perPane[idx] = append(perPane[idx], n)
			continue
		}
		unassigned = append(unassigned, n)
	}
	return perPane, unassigned
}

func partitionContextByPane(items []CollectedContextItem, paneIDToIdx map[string]int) ([][]CollectedContextItem, []CollectedContextItem) {
	perPane := make([][]CollectedContextItem, len(paneIDToIdx))
	var unassigned []CollectedContextItem
	for _, it := range items {
		if idx, found := paneIDToIdx[it.PaneID]; found && it.PaneID != "" {
			perPane[idx] = append(perPane[idx], it)
			continue
		}
		unassigned = append(unassigned, it)
	}
	return perPane, unassigned
}

func (a *App) sortPaneBuckets(buckets []byPaneBucket) {
	rankOf := func(p data.Record) (int, time.Time) {
		_, running, needsYou := a.effectivePaneLiveStatus(p)
		rank := 2
		if needsYou {
			rank = 0
		} else if running {
			rank = 1
		}
		ts := p.Time("promptedAt")
		if ts.IsZero() {
			ts = p.UpdatedAt
		}
		return rank, ts
	}
	slices.SortStableFunc(buckets, func(x, y byPaneBucket) int {
		rx, tx := rankOf(x.pane)
		ry, ty := rankOf(y.pane)
		if rx != ry {
			return rx - ry
		}
		if cmp := ty.Compare(tx); cmp != 0 {
			return cmp
		}
		return strings.Compare(x.pane.ID, y.pane.ID)
	})
}

func (a *App) partitionWorkspaceByPane(ws projectWorkspaceData) byPanePartition {
	buckets := make([]byPaneBucket, len(ws.panes))
	for i, p := range ws.panes {
		buckets[i].pane = p
	}
	a.sortPaneBuckets(buckets)

	paneRefToIdx := make(map[core.Ref]int, len(buckets))
	paneIDToIdx := make(map[string]int, len(buckets))
	for i, b := range buckets {
		paneRefToIdx[b.pane.Ref()] = i
		paneIDToIdx[b.pane.ID] = i
	}

	pagesPer, unassignedPages := splitRecordsByPaneRef(ws.pages, paneRefToIdx)
	questionsPer, unassignedQuestions := splitRecordsByPaneRef(ws.questions, paneRefToIdx)
	lanesPer, unassignedLanes := partitionLanesByPane(ws, paneRefToIdx)
	commandsPer, unassignedCommands := splitRecordsByPaneRef(ws.commands, paneRefToIdx)
	notesPer, unassignedNotes := partitionNotesByPane(ws, paneRefToIdx)
	contextPer, unassignedContext := partitionContextByPane(ws.contextItems, paneIDToIdx)

	for i := range buckets {
		pagesPer[i] = sortRecordsNewestFirst(pagesPer[i])
		sortPaneLanes(lanesPer[i])
		buckets[i].pages = pagesPer[i]
		buckets[i].questions = questionsPer[i]
		buckets[i].lanes = lanesPer[i]
		buckets[i].commands = commandsPer[i]
		buckets[i].notes = notesPer[i]
		buckets[i].context = contextPer[i]
	}

	laneTree := data.BuildTree(ws.allLanes, "parentLane", "", a.treeIncludesArchived(data.KindLane))
	laneTreeByID := make(map[string]data.TreeNode, len(ws.allLanes))
	indexLaneTreeNodes(laneTree, laneTreeByID)

	return byPanePartition{
		panes:               buckets,
		unassignedPages:     unassignedPages,
		unassignedQuestions: unassignedQuestions,
		unassignedLanes:     unassignedLanes,
		unassignedContext:   unassignedContext,
		unassignedCommands:  unassignedCommands,
		unassignedNotes:     unassignedNotes,
		laneTreeByID:        laneTreeByID,
	}
}

// --- By-pane nav builder --------------------------------------------------------

func (a *App) byPaneCacheKey(ws projectWorkspaceData, pc *ProjectController, treeEnabled, allowPeek, compact bool) string {
	return fmt.Sprintf("bypane|%d|%d|%d|%t|%t|%t|%t|%t|%t|%t|%s|%s|%d|%s",
		ws.cacheRev, ws.cacheCtxGen, a.telemetryViewSeq(),
		a.nav.showArchived, treeEnabled, allowPeek, compact,
		a.projectNavSurfaceContextUnreadEnabled(), a.projectNavUnreadDotOnlyEnabled(), a.projectNavLaneProgressBarEnabled(),
		sortedTrueKeys(pc.expandedSections[ws.projectKey]),
		sortedTrueKeys(pc.expandedNodes[ws.projectKey]),
		a.navUnreadCutoff.UnixNano(),
		a.archiveAnimCacheKey(),
	)
}

func cloneByPaneSectionsWithFocus(src []views.ProjectNavSection, activeKey, focusID, lockedFocus, openPage string) []views.ProjectNavSection {
	out := make([]views.ProjectNavSection, len(src))
	for i, s := range src {
		sfid := projectNavPrefix + s.Key
		s.Active = s.Key == activeKey || focusID == sfid || lockedFocus == sfid
		if len(s.Children) > 0 {
			s.Children = cloneNavNodesWithFocus(s.Children, focusID, lockedFocus, openPage)
		}
		out[i] = s
	}
	return out
}

func (a *App) buildProjectNavByPane(ws projectWorkspaceData, activeKey, focusID string) []views.ProjectNavSection {
	pc := a.projectCtrl()
	treeEnabled := a.projectNavTreeEnabled()
	allowPeek := a.projectNavPeeksUnread()
	compact := a.projectNavCompactEnabled()
	lockedFocus := ""
	if locked := pc.activeNavItem(ws.projectKey); locked != nil {
		lockedFocus = locked.FocusID
	}
	openPage := pc.openPageID(ws.projectKey)
	navKey := a.byPaneCacheKey(ws, pc, treeEnabled, allowPeek, compact)
	if pc.navCache != nil {
		if cached, ok := pc.navCache[ws.projectKey]; ok && cached.key == navKey {
			if pc.navTargets == nil {
				pc.navTargets = map[string]map[string]projectNavTarget{}
			}
			pc.navTargets[ws.projectKey] = cached.targets
			return cloneByPaneSectionsWithFocus(cached.sections, activeKey, focusID, lockedFocus, openPage)
		}
	}

	pc.resetNavTargets(ws.projectKey)
	part := a.partitionWorkspaceByPane(ws)

	out := make([]views.ProjectNavSection, 0, len(part.panes)+10)
	out = append(out, views.ProjectNavSection{
		Key: "home", Label: "Home", Icon: "⌂", Group: "Core",
		Active: activeKey == "home",
	})
	for _, bucket := range part.panes {
		out = append(out, a.buildByPaneSection(ws, bucket, part.laneTreeByID, focusID, lockedFocus, treeEnabled, allowPeek))
	}
	out = append(out, a.buildByPaneProjectLevelSections(ws, part, activeKey, focusID, lockedFocus, treeEnabled, allowPeek, compact)...)

	pc.navBuilds++
	if pc.navCache == nil {
		pc.navCache = map[string]cachedProjectNav{}
	}
	pc.navCache[ws.projectKey] = cachedProjectNav{
		key:      navKey,
		sections: out,
		targets:  pc.navTargets[ws.projectKey],
	}
	return cloneByPaneSectionsWithFocus(out, activeKey, focusID, lockedFocus, openPage)
}

func (a *App) byPaneStatusIcon(paneSchema data.Schema, pane data.Record, styleOpts views.StyleOptions) string {
	_, running, needsYou := a.effectivePaneLiveStatus(pane)
	if needsYou {
		return "!"
	}
	if running {
		return "▶"
	}
	if rs := views.ResolveRecordStyle(paneSchema, pane, styleOpts); strings.TrimSpace(rs.Icon) != "" {
		return rs.Icon
	}
	return "▣"
}

func (a *App) buildByPaneSection(ws projectWorkspaceData, bucket byPaneBucket, laneTreeByID map[string]data.TreeNode, focusID, lockedFocus string, treeEnabled, allowPeek bool) views.ProjectNavSection {
	pc := a.projectCtrl()
	paneSchema := a.schema(data.KindPane)
	styleOpts := a.styleOptions()
	secKey := byPaneSectionKey(bucket.pane.ID)
	paneFocusID := projectNavPrefix + secKey
	paneRef := bucket.pane.Ref()
	title := strings.TrimSpace(views.Title(paneSchema, bucket.pane))
	if title == "" {
		title = bucket.pane.ID
	}

	totalItems := len(bucket.pages) + len(bucket.questions) + len(bucket.lanes) +
		len(bucket.context) + len(bucket.commands) + len(bucket.notes)
	expandable := treeEnabled && totalItems > 0
	expanded := expandable && pc.isNavSectionExpanded(ws.projectKey, secKey)

	subgroups, childUnread := a.buildByPaneSubgroups(ws, bucket, laneTreeByID, secKey, paneFocusID, paneRef, focusID, lockedFocus, expanded, allowPeek)
	selfAttn := a.paneHasAttention(bucket.pane.ID)
	isUnread := selfAttn || childUnread > 0

	pc.registerNavTarget(ws.projectKey, projectNavTarget{
		FocusID:    paneFocusID,
		Section:    "panes",
		NodeID:     bucket.pane.ID,
		Label:      title,
		Ref:        &paneRef,
		Unread:     isUnread,
		Expandable: expandable,
		Expanded:   expanded,
	})

	var children []views.ProjectNavNode
	if treeEnabled && len(subgroups) > 0 {
		children = subgroups
	}
	return views.ProjectNavSection{
		Key:         secKey,
		Label:       title,
		Icon:        a.byPaneStatusIcon(paneSchema, bucket.pane, styleOpts),
		Group:       "Panes",
		Count:       totalItems,
		UnreadCount: childUnread,
		Unread:      isUnread,
		Expandable:  expandable,
		Expanded:    expanded,
		Children:    children,
	}
}

type byPaneSubgroupSpec struct {
	key        string
	label      string
	icon       string
	count      int
	unread     int
	leafNodes  []views.ProjectNavNode
	leafTarget []projectNavTarget
}

func byPaneSubgroupBadge(count, unread int) string {
	if unread > 0 && count > unread {
		return fmt.Sprintf("%d · ●%d", count, unread)
	}
	if unread > 0 {
		return fmt.Sprintf("●%d", unread)
	}
	return fmt.Sprintf("%d", count)
}

func (a *App) buildByPaneSubgroups(ws projectWorkspaceData, bucket byPaneBucket, laneTreeByID map[string]data.TreeNode, secKey, paneFocusID string, paneRef core.Ref, focusID, lockedFocus string, paneExpanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	specs := a.collectByPaneSubgroupSpecs(ws, bucket, laneTreeByID, secKey, focusID, lockedFocus, paneExpanded, allowPeek)
	return a.assembleByPaneSubgroupNodes(ws.projectKey, secKey, paneFocusID, paneRef, specs, focusID, lockedFocus, paneExpanded, allowPeek)
}

func (a *App) collectByPaneSubgroupSpecs(ws projectWorkspaceData, bucket byPaneBucket, laneTreeByID map[string]data.TreeNode, secKey, focusID, lockedFocus string, paneExpanded, allowPeek bool) []byPaneSubgroupSpec {
	var specs []byPaneSubgroupSpec
	if sp, ok := a.byPanePagesSubgroup(ws, bucket.pages, secKey, focusID, lockedFocus, paneExpanded, allowPeek); ok {
		specs = append(specs, sp)
	}
	if sp, ok := a.byPaneRecordsSubgroup(ws, "questions", "Questions", "?", data.KindQuestion, bucket.questions, secKey, focusID, lockedFocus, paneExpanded, allowPeek); ok {
		specs = append(specs, sp)
	}
	if sp, ok := a.byPaneLanesSubgroup(ws, bucket.pane.ID, bucket.lanes, laneTreeByID, secKey, focusID, lockedFocus, paneExpanded, allowPeek); ok {
		specs = append(specs, sp)
	}
	if sp, ok := a.byPaneContextSubgroup(ws, bucket.pane.ID, bucket.context, secKey, focusID, lockedFocus, paneExpanded, allowPeek); ok {
		specs = append(specs, sp)
	}
	if sp, ok := a.byPaneRecordsSubgroup(ws, "commands", "Commands", "$", data.KindCommand, bucket.commands, secKey, focusID, lockedFocus, paneExpanded, allowPeek); ok {
		specs = append(specs, sp)
	}
	if sp, ok := a.byPaneRecordsSubgroup(ws, "notes", "Notes", "✎", data.KindNote, bucket.notes, secKey, focusID, lockedFocus, paneExpanded, allowPeek); ok {
		specs = append(specs, sp)
	}
	return specs
}

func (a *App) byPaneSubgroupExpanded(projKey, secKey, subKey string, paneExpanded bool) (string, bool) {
	sfid := projectNavNodePrefix + secKey + "." + subKey
	return sfid, paneExpanded && a.projectCtrl().isNavNodeExpanded(projKey, secKey+"."+subKey)
}

func (a *App) byPanePagesSubgroup(ws projectWorkspaceData, pages []data.Record, secKey, focusID, lockedFocus string, paneExpanded, allowPeek bool) (byPaneSubgroupSpec, bool) {
	if len(pages) == 0 {
		return byPaneSubgroupSpec{}, false
	}
	sfid, exp := a.byPaneSubgroupExpanded(ws.projectKey, secKey, "pages", paneExpanded)
	nodes, targets, unread := a.byPanePageLeafNodes(ws, pages, sfid, focusID, lockedFocus, exp, allowPeek)
	return byPaneSubgroupSpec{key: "pages", label: "Pages", icon: "◫", count: len(pages), unread: unread, leafNodes: nodes, leafTarget: targets}, true
}

func (a *App) byPaneRecordsSubgroup(ws projectWorkspaceData, subKey, label, icon, kind string, recs []data.Record, secKey, focusID, lockedFocus string, paneExpanded, allowPeek bool) (byPaneSubgroupSpec, bool) {
	if len(recs) == 0 {
		return byPaneSubgroupSpec{}, false
	}
	sfid, exp := a.byPaneSubgroupExpanded(ws.projectKey, secKey, subKey, paneExpanded)
	nodes, targets, unread := a.byPaneRecordLeafNodes(ws, subKey, kind, recs, sfid, focusID, lockedFocus, exp, allowPeek)
	return byPaneSubgroupSpec{key: subKey, label: label, icon: icon, count: len(recs), unread: unread, leafNodes: nodes, leafTarget: targets}, true
}

func (a *App) byPaneLanesSubgroup(ws projectWorkspaceData, paneID string, lanes []data.Record, laneTreeByID map[string]data.TreeNode, secKey, focusID, lockedFocus string, paneExpanded, allowPeek bool) (byPaneSubgroupSpec, bool) {
	if len(lanes) == 0 {
		return byPaneSubgroupSpec{}, false
	}
	sfid, exp := a.byPaneSubgroupExpanded(ws.projectKey, secKey, "lanes", paneExpanded)
	nodes, targets, unread := a.byPaneLaneLeafNodes(ws, paneID, lanes, laneTreeByID, sfid, focusID, lockedFocus, exp, allowPeek)
	return byPaneSubgroupSpec{key: "lanes", label: "Lanes", icon: "⎇", count: len(lanes), unread: unread, leafNodes: nodes, leafTarget: targets}, true
}

func (a *App) byPaneContextSubgroup(ws projectWorkspaceData, paneID string, items []CollectedContextItem, secKey, focusID, lockedFocus string, paneExpanded, allowPeek bool) (byPaneSubgroupSpec, bool) {
	if len(items) == 0 {
		return byPaneSubgroupSpec{}, false
	}
	sfid, exp := a.byPaneSubgroupExpanded(ws.projectKey, secKey, "context", paneExpanded)
	peek := allowPeek && a.projectNavSurfaceContextUnreadEnabled()
	nodes, targets, unread := a.byPaneContextLeafNodes(ws, paneID, items, sfid, focusID, lockedFocus, exp, peek)
	return byPaneSubgroupSpec{key: "context", label: "Context", icon: "◈", count: len(items), unread: unread, leafNodes: nodes, leafTarget: targets}, true
}

func byPaneRecordTitle(schema data.Schema, rec data.Record) string {
	if title := strings.TrimSpace(views.Title(schema, rec)); title != "" {
		return title
	}
	return rec.ID
}

func byPaneLeafTarget(secKey, nodeID, parentFocus, label string, ref *core.Ref, unread bool) projectNavTarget {
	return projectNavTarget{
		FocusID: projectNavNodePrefix + secKey + "." + nodeID, Section: secKey,
		NodeID: nodeID, ParentFocusID: parentFocus, Label: label, Ref: ref, Unread: unread,
	}
}

func (a *App) assembleByPaneSubgroupNodes(projKey, secKey, paneFocusID string, paneRef core.Ref, specs []byPaneSubgroupSpec, focusID, lockedFocus string, paneExpanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	pc := a.projectCtrl()
	totalUnread := 0
	out := make([]views.ProjectNavNode, 0, len(specs))
	for _, sp := range specs {
		totalUnread += sp.unread
		if !paneExpanded && (!allowPeek || sp.unread == 0) {
			continue
		}
		sfid := projectNavNodePrefix + secKey + "." + sp.key
		expanded := pc.isNavNodeExpanded(projKey, secKey+"."+sp.key)
		refCopy := paneRef
		out = append(out, views.ProjectNavNode{
			ID: sp.key, Section: secKey, Label: sp.label, Icon: sp.icon,
			Badge: byPaneSubgroupBadge(sp.count, sp.unread), Ref: &refCopy,
			Peeked: !paneExpanded, HasUnread: sp.unread > 0, Active: focusID == sfid || lockedFocus == sfid,
			Expandable: true, Expanded: expanded, Children: sp.leafNodes,
			Facts: map[string]any{"byPaneSubgroup": sp.key, "subgroupCount": sp.count, "unreadCount": sp.unread},
		})
		pc.registerNavTarget(projKey, projectNavTarget{
			FocusID: sfid, Section: secKey, NodeID: sp.key, ParentFocusID: paneFocusID,
			Label: sp.label, Ref: &refCopy, Expandable: true, Expanded: expanded,
		})
		for _, lt := range sp.leafTarget {
			pc.registerNavTarget(projKey, lt)
		}
	}
	return out, totalUnread
}

// --- Pane-scoped leaf builders --------------------------------------------------

func byPanePageIcon(p data.Record) string {
	switch {
	case p.Text("pageKind") == "questions":
		return "?"
	case p.Bool("pinned"):
		return "▪"
	default:
		return "≡"
	}
}

func (a *App) byPanePageLeafNodes(ws projectWorkspaceData, pages []data.Record, parentFocus, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, []projectNavTarget, int) {
	schema := a.schema(data.KindPage)
	openPage := a.projectCtrl().openPageID(ws.projectKey)
	var nodes []views.ProjectNavNode
	var targets []projectNavTarget
	unreadCount := 0
	for _, p := range pages {
		unread := a.isNavRecordUnread(data.KindPage, p, &ws)
		if unread {
			unreadCount++
		}
		if !expanded && (!allowPeek || !unread) {
			continue
		}
		ref := p.Ref()
		nfid := projectNavNodePrefix + "pages." + p.ID
		title := byPaneRecordTitle(schema, p)
		facts := a.projectNavRecordFacts(schema, p)
		facts["pageId"], facts["pageKind"], facts["pinned"] = p.ID, p.Text("pageKind"), p.Bool("pinned")
		nodes = append(nodes, views.ProjectNavNode{
			ID: p.ID, Section: "pages", ParentID: parentFocus, Label: title,
			Icon: byPanePageIcon(p), Badge: p.Text("pageKind"), Ref: &ref,
			Unread: unread, Peeked: !expanded && unread, HasUnread: unread,
			Active: focusID == nfid || lockedFocus == nfid || openPage == p.ID, Facts: facts,
		})
		targets = append(targets, byPaneLeafTarget("pages", p.ID, parentFocus, title, &ref, unread))
	}
	return nodes, targets, unreadCount
}

func (a *App) byPaneRecordLeafNodes(ws projectWorkspaceData, secKey, kind string, recs []data.Record, parentFocus, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, []projectNavTarget, int) {
	schema, styleOpts := a.schema(kind), a.styleOptions()
	statusField, _, hasStatus := schema.PrimaryStatusField()
	var nodes []views.ProjectNavNode
	var targets []projectNavTarget
	unreadCount := 0
	for _, rec := range recs {
		unread := a.isNavRecordUnread(kind, rec, &ws)
		if unread {
			unreadCount++
		}
		if !expanded && (!allowPeek || !unread) {
			continue
		}
		ref := rec.Ref()
		nfid := projectNavNodePrefix + secKey + "." + rec.ID
		title := byPaneRecordTitle(schema, rec)
		rs := views.ResolveRecordStyle(schema, rec, styleOpts)
		badge := ""
		if hasStatus {
			badge = strings.TrimSpace(rec.Text(statusField))
		}
		nodes = append(nodes, views.ProjectNavNode{
			ID: rec.ID, Section: secKey, ParentID: parentFocus, Label: title,
			Icon: rs.Icon, IconTone: rs.IconTone, Badge: badge, Tone: rs.RowTone, Ref: &ref,
			Unread: unread, Peeked: !expanded && unread, HasUnread: unread,
			Active: focusID == nfid || lockedFocus == nfid, Facts: a.projectNavRecordFacts(schema, rec),
		})
		targets = append(targets, byPaneLeafTarget(secKey, rec.ID, parentFocus, title, &ref, unread))
	}
	return nodes, targets, unreadCount
}

func (a *App) byPaneLaneLeafNodes(ws projectWorkspaceData, paneID string, lanes []data.Record, laneTreeByID map[string]data.TreeNode, parentFocus, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, []projectNavTarget, int) {
	schema, styleOpts, showBar := a.schema(data.KindLane), a.styleOptions(), a.projectNavLaneProgressBarEnabled()
	var nodes []views.ProjectNavNode
	var targets []projectNavTarget
	unreadCount := 0
	for _, lane := range lanes {
		unread := a.isNavRecordUnread(data.KindLane, lane, &ws)
		if unread {
			unreadCount++
		}
		if !expanded && (!allowPeek || !unread) {
			continue
		}
		ref := lane.Ref()
		nodeID := paneID + "__" + lane.ID
		nfid := projectNavNodePrefix + "lanes." + nodeID
		title := byPaneRecordTitle(schema, lane)
		rs := views.ResolveRecordStyle(schema, lane, styleOpts)
		nd, ok := laneTreeByID[lane.ID]
		if !ok {
			nd = data.TreeNode{Record: lane}
		}
		node := views.ProjectNavNode{
			ID: nodeID, Section: "lanes", ParentID: parentFocus, Label: title,
			Icon: rs.Icon, IconTone: rs.IconTone, Badge: strings.TrimSpace(lane.Text("laneStatus")), Tone: rs.RowTone, Ref: &ref,
			Unread: unread, Peeked: !expanded && unread, HasUnread: unread,
			Active: focusID == nfid || lockedFocus == nfid, Facts: a.projectNavRecordFacts(schema, lane),
		}
		applyLaneNavPresentation(nd, showBar, &node)
		nodes = append(nodes, node)
		targets = append(targets, byPaneLeafTarget("lanes", nodeID, parentFocus, title, &ref, unread))
	}
	return nodes, targets, unreadCount
}

func byPaneContextLabel(it CollectedContextItem) string {
	if label := singleLineContextText(it.Label); label != "" {
		return label
	}
	if it.Kind == contextitems.KindCode {
		return data.CodeContextTitle(it.Value, it.Language)
	}
	return singleLineContextText(it.Value)
}

func byPaneContextNodeID(paneID string, it CollectedContextItem, idx int, seen map[string]int) string {
	rawID := it.RecordID
	if rawID == "" {
		rawID = projectNavSafeID(it.CanonKey, idx)
	}
	nodeID := paneID + "__" + rawID
	if n := seen[nodeID]; n > 0 {
		nodeID = fmt.Sprintf("%s_%d", nodeID, n)
	}
	seen[nodeID]++
	return nodeID
}

func byPaneContextEntry(it CollectedContextItem, nodeID, parentFocus, focusID, lockedFocus string, unread, expanded bool) (views.ProjectNavNode, projectNavTarget) {
	nfid := projectNavNodePrefix + "context." + nodeID
	label := byPaneContextLabel(it)
	var ref *core.Ref
	if it.RecordID != "" {
		r := core.Ref{Kind: data.KindContext, ID: it.RecordID}
		ref = &r
	}
	icon := ""
	if it.Official || it.Starred {
		icon = "★"
	}
	itCopy := it
	node := views.ProjectNavNode{
		ID: nodeID, Section: "context", ParentID: parentFocus, Label: clip(label, 40),
		Icon: icon, Badge: it.Kind, Ref: ref, Unread: unread, Peeked: !expanded && unread, HasUnread: unread,
		Active: focusID == nfid || lockedFocus == nfid,
		Facts:  map[string]any{"contextItem": true, "contextKey": it.Key, "official": it.Official},
	}
	target := byPaneLeafTarget("context", nodeID, parentFocus, label, ref, unread)
	target.ContextKey, target.ContextItem = it.Key, &itCopy
	return node, target
}

func (a *App) byPaneContextLeafNodes(ws projectWorkspaceData, paneID string, items []CollectedContextItem, parentFocus, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, []projectNavTarget, int) {
	var nodes []views.ProjectNavNode
	var targets []projectNavTarget
	seen := map[string]int{}
	unreadCount := 0
	for i, it := range items {
		rec, persisted := ws.contextRecords[it.RecordID]
		unread := persisted && a.isNavRecordUnread(data.KindContext, rec, &ws)
		if unread {
			unreadCount++
		}
		if !expanded && (!allowPeek || !unread) {
			continue
		}
		nodeID := byPaneContextNodeID(paneID, it, i, seen)
		node, target := byPaneContextEntry(it, nodeID, parentFocus, focusID, lockedFocus, unread, expanded)
		nodes = append(nodes, node)
		targets = append(targets, target)
	}
	if !a.projectNavSurfaceContextUnreadEnabled() {
		unreadCount = 0
	}
	return nodes, targets, unreadCount
}

// --- Project-Level (unassigned) sections ----------------------------------------

func byPaneLabel(hasPanes bool, withPanesLabel, fallbackLabel string) string {
	if hasPanes {
		return withPanesLabel
	}
	return fallbackLabel
}

func (a *App) buildProjectLevelNavSection(ws projectWorkspaceData, secKey, label, icon string, count, ctxCount int, activeKey string, treeEnabled, allowPeek, compact bool, build func(expanded bool) ([]views.ProjectNavNode, int)) views.ProjectNavSection {
	pc := a.projectCtrl()
	expandable := treeEnabled && build != nil && count > 0
	expanded := expandable && pc.isNavSectionExpanded(ws.projectKey, secKey)
	sec := views.ProjectNavSection{
		Key: secKey, Label: label, Icon: icon, Group: "Project-Level",
		Badge:      projectNavSectionBadge(secKey, count, ws.officialCount, ctxCount, compact),
		Count:      count,
		Active:     secKey == activeKey,
		Expandable: expandable, Expanded: expanded,
	}
	if count > 0 && build != nil {
		children, unread := build(expanded)
		children = maybeCapPeekedNavChildren(pc, ws.projectKey, secKey, children, compact, expanded, allowPeek)
		sec.UnreadCount = unread
		sec.Unread = unread > 0
		if treeEnabled && len(children) > 0 {
			sec.Children = children
		}
	}
	return sec
}

func (a *App) buildByPaneProjectLevelSections(ws projectWorkspaceData, part byPanePartition, activeKey, focusID, lockedFocus string, treeEnabled, allowPeek, compact bool) []views.ProjectNavSection {
	hasPanes := len(part.panes) > 0
	ctxCount := len(part.unassignedContext)
	add := func(secKey, label, icon string, count int, build func(expanded bool) ([]views.ProjectNavNode, int)) views.ProjectNavSection {
		return a.buildProjectLevelNavSection(ws, secKey, label, icon, count, ctxCount, activeKey, treeEnabled, allowPeek, compact, build)
	}

	out := []views.ProjectNavSection{
		add("tasks", "Tasks", "☐", len(ws.tasks), func(exp bool) ([]views.ProjectNavNode, int) {
			return a.projectTaskNavNodes(ws, focusID, lockedFocus, exp, allowPeek)
		}),
		add("documents", "Documents", "▤", len(ws.documents), func(exp bool) ([]views.ProjectNavNode, int) {
			return a.projectRecordNavNodes(ws, "documents", data.KindDocument, ws.documents, focusID, lockedFocus, exp, allowPeek)
		}),
	}
	if len(part.unassignedLanes) > 0 || !hasPanes {
		laneNodes := data.BuildTree(part.unassignedLanes, "parentLane", "", a.treeIncludesArchived(data.KindLane))
		out = append(out, add("lanes", byPaneLabel(hasPanes, "Unassigned Lanes", "Lanes"), "⎇", len(part.unassignedLanes), func(exp bool) ([]views.ProjectNavNode, int) {
			return a.projectTreeNavNodes(ws, "lanes", data.KindLane, laneNodes, focusID, lockedFocus, exp, allowPeek)
		}))
	}
	if len(part.unassignedPages) > 0 || !hasPanes {
		wsPages := ws
		wsPages.pages = part.unassignedPages
		out = append(out, add("pages", byPaneLabel(hasPanes, "Unassigned Pages", "Pages"), "◫", len(part.unassignedPages), func(exp bool) ([]views.ProjectNavNode, int) {
			return a.projectPageNavNodes(wsPages, focusID, lockedFocus, exp, allowPeek)
		}))
	}
	if len(part.unassignedQuestions) > 0 || !hasPanes {
		out = append(out, add("questions", byPaneLabel(hasPanes, "Unassigned Questions", "Questions & Decisions"), "?", len(part.unassignedQuestions), func(exp bool) ([]views.ProjectNavNode, int) {
			return a.projectRecordNavNodes(ws, "questions", data.KindQuestion, part.unassignedQuestions, focusID, lockedFocus, exp, allowPeek)
		}))
	}
	if len(part.unassignedContext) > 0 || !hasPanes {
		peekCtx := allowPeek && a.projectNavSurfaceContextUnreadEnabled()
		out = append(out, add("context", byPaneLabel(hasPanes, "Project Context", "Context"), "◈", len(part.unassignedContext), func(exp bool) ([]views.ProjectNavNode, int) {
			return a.projectContextNavNodes(ws, "context", part.unassignedContext, focusID, lockedFocus, exp, peekCtx)
		}))
	}
	return append(out, a.buildByPaneOptionalProjectSections(ws, part, activeKey, focusID, lockedFocus, treeEnabled, allowPeek, compact)...)
}

func (a *App) buildByPaneOptionalProjectSections(ws projectWorkspaceData, part byPanePartition, activeKey, focusID, lockedFocus string, treeEnabled, allowPeek, compact bool) []views.ProjectNavSection {
	var out []views.ProjectNavSection
	addIfNonEmpty := func(secKey, label, icon string, count int, build func(expanded bool) ([]views.ProjectNavNode, int)) {
		if count == 0 {
			return
		}
		out = append(out, a.buildProjectLevelNavSection(ws, secKey, label, icon, count, 0, activeKey, treeEnabled, allowPeek, compact, build))
	}

	addIfNonEmpty("pinned", "Pinned Links & Files", "★", len(ws.pinnedItems), func(exp bool) ([]views.ProjectNavNode, int) {
		return a.projectContextNavNodes(ws, "pinned", ws.pinnedItems, focusID, lockedFocus, exp, allowPeek)
	})
	addIfNonEmpty("notes", "Unassigned Notes", "✎", len(part.unassignedNotes), func(exp bool) ([]views.ProjectNavNode, int) {
		return a.projectRecordNavNodes(ws, "notes", data.KindNote, part.unassignedNotes, focusID, lockedFocus, exp, allowPeek)
	})
	addIfNonEmpty("commands", "Unassigned Commands", "$", len(part.unassignedCommands), func(exp bool) ([]views.ProjectNavNode, int) {
		return a.projectRecordNavNodes(ws, "commands", data.KindCommand, part.unassignedCommands, focusID, lockedFocus, exp, allowPeek)
	})
	addIfNonEmpty("worktrees", "Worktrees", "⌘", len(ws.worktrees), func(exp bool) ([]views.ProjectNavNode, int) {
		if !exp {
			return nil, 0
		}
		return a.projectWorktreeNavNodes(ws.projectKey, ws.worktrees, focusID, lockedFocus), 0
	})
	addIfNonEmpty("prs", "PRs", "⇅", len(ws.prItems), func(exp bool) ([]views.ProjectNavNode, int) {
		return a.projectContextNavNodes(ws, "prs", ws.prItems, focusID, lockedFocus, exp, allowPeek)
	})
	addIfNonEmpty(projectQueueSection, "Queues", "⇶", a.projectOpenQueueItems(ws.projectID), nil)
	addIfNonEmpty(projectThreadsSection, "Threads", "✉", a.projectThreadCount(ws.projectID), nil)
	return out
}
