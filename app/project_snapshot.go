package app

import (
	"maps"
	"strings"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

// projectDurableKey uniquely identifies a store-backed durable snapshot of a
// project workspace. Any change to these inputs invalidates the durable cache.
type projectDurableKey struct {
	storeRevision int64
	showArchived  bool
	ctxGen        uint64
	groupName     string
	projID        string
	railPanesKey  string
	animKey       string
}

// projectDurableSnapshot holds only store-backed, durable project data
// (records, relations, tasks, lanes, documents, notes, context items,
// questions, pages, commands, worktrees, repo labels, and store-derived unread
// indices). It is cached across live daemon ticks whenever storeRevision and
// its durable inputs have not changed.
type projectDurableSnapshot struct {
	groupName, projectKey, projectID string
	projRef                          *core.Ref
	projRec                          *data.Record
	projRepoLabels                   []string
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
	baseUnreadTotal                  int
	cacheRev                         int64
	contextRev                       int64
	cacheCtxGen                      uint64
	cacheRailKey                     string
}

// projectLiveOverlay holds volatile runtime state layered on top of a
// projectDurableSnapshot: live daemon PTY pane statuses, attention/unread
// pane flags, and running/needs-you counters keyed by telemetrySeq.
type projectLiveOverlay struct {
	telemetrySeq    uint64
	paneStatus      map[string]string
	unreadPanes     map[string]bool
	runningPanes    int
	needsYouPanes   int
	unreadPaneCount int
}

type cachedProjectDurable struct {
	key   projectDurableKey
	snap  projectDurableSnapshot
	scope data.ProjectScope
}

type cachedProjectLive struct {
	durableKey projectDurableKey
	overlay    projectLiveOverlay
}

// projectWorkspaceData is the composed read model for a project workspace,
// combining a store-backed projectDurableSnapshot with a volatile
// projectLiveOverlay.
type projectWorkspaceData struct {
	projectDurableSnapshot
	live          projectLiveOverlay
	unreadTotal   int
	runningPanes  int
	needsYouPanes int
}

func composeProjectWorkspace(durable projectDurableSnapshot, live projectLiveOverlay) projectWorkspaceData {
	return projectWorkspaceData{
		projectDurableSnapshot: durable,
		live:                   live,
		unreadTotal:            durable.baseUnreadTotal + live.unreadPaneCount,
		runningPanes:           live.runningPanes,
		needsYouPanes:          live.needsYouPanes,
	}
}

func (pc *ProjectController) clearWorkspaceCaches(projKey string) {
	if pc == nil {
		return
	}
	if projKey != "" {
		delete(pc.durableCache, projKey)
		delete(pc.liveCache, projKey)
		delete(pc.navCache, projKey)
		delete(pc.tabUnreadCache, projKey)
		delete(pc.homeCache, projKey+"|true")
		delete(pc.homeCache, projKey+"|false")
		return
	}
	clear(pc.durableCache)
	clear(pc.liveCache)
	clear(pc.navCache)
	clear(pc.tabUnreadCache)
	clear(pc.homeCache)
	clear(pc.showSnapCache)
}

func (a *App) makeProjectDurableKey(groupName string, projRef *core.Ref) (string, projectDurableKey) {
	projKey, projID := projectKeyFor(groupName, projRef), ""
	if projRef != nil {
		projID = projRef.ID
	}
	return projKey, projectDurableKey{
		storeRevision: a.storeRev(),
		showArchived:  a.nav.showArchived,
		ctxGen:        a.contextIndexGen(),
		groupName:     groupName,
		projID:        projID,
		railPanesKey:  a.railGroupPanesKey(groupName),
		animKey:       a.archiveAnimCacheKey(),
	}
}

func (a *App) projectDurableSnapshotFor(groupName string, projRef *core.Ref) (projectDurableSnapshot, projectDurableKey) {
	projKey, reqKey := a.makeProjectDurableKey(groupName, projRef)
	pc := a.projectCtrl()
	var prev cachedProjectDurable
	var hadPrev bool
	if a.Store != nil && pc.durableCache != nil {
		if cached, ok := pc.durableCache[projKey]; ok {
			if cached.key == reqKey {
				return cached.snap, cached.key
			}
			prev, hadPrev = cached, true
		}
	}
	snap, finalKey, scope := a.resolveProjectDurableBuild(projKey, groupName, projRef, reqKey, prev, hadPrev)
	if a.Store != nil {
		if pc.durableCache == nil {
			pc.durableCache = map[string]cachedProjectDurable{}
		}
		pc.durableCache[projKey] = cachedProjectDurable{key: finalKey, snap: snap, scope: scope}
		delete(pc.liveCache, projKey)
		pc.durableBuilds++
	}
	return snap, finalKey
}

func (a *App) resolveProjectDurableBuild(projKey, groupName string, projRef *core.Ref, reqKey projectDurableKey, prev cachedProjectDurable, hadPrev bool) (projectDurableSnapshot, projectDurableKey, data.ProjectScope) {
	if hadPrev && a.canDeltaRefreshDurable(prev.key, reqKey) {
		if snap, key, scope, ok := a.refreshProjectDurableDelta(prev, projRef, reqKey); ok {
			a.projectCtrl().durableDeltaRefreshes++
			return snap, key, scope
		}
	}
	return a.buildProjectDurableSnapshot(projKey, groupName, projRef, reqKey)
}

func (a *App) canDeltaRefreshDurable(prevKey, reqKey projectDurableKey) bool {
	return a.Store != nil &&
		reqKey.storeRevision >= prevKey.storeRevision &&
		prevKey.showArchived == reqKey.showArchived &&
		prevKey.groupName == reqKey.groupName &&
		prevKey.projID == reqKey.projID &&
		prevKey.railPanesKey == reqKey.railPanesKey &&
		prevKey.animKey == reqKey.animKey
}

func (a *App) refreshProjectDurableDelta(prev cachedProjectDurable, projRef *core.Ref, reqKey projectDurableKey) (projectDurableSnapshot, projectDurableKey, data.ProjectScope, bool) {
	delta, err := a.Store.ChangedKindsSince(prev.key.storeRevision)
	if err != nil || delta.FullRebuild {
		return projectDurableSnapshot{}, projectDurableKey{}, data.ProjectScope{}, false
	}
	snap := prev.snap
	scope := prev.scope
	anchorsChanged := false
	if delta.Kinds[data.KindTask] || delta.Kinds[data.KindPane] || delta.HasRelation {
		paneSeeds := a.projectRailPaneSeeds(reqKey.groupName)
		scope, _ = a.Store.ProjectScope(reqKey.projID, paneSeeds, a.nav.showArchived)
		anchorsChanged = !maps.Equal(prev.scope.TaskIDs, scope.TaskIDs) || !maps.Equal(prev.scope.PaneIDs, scope.PaneIDs)
		applyScopeAnchors(&snap, scope, paneSeeds)
	}
	a.refreshDurableScopeSlices(&snap, prev.scope, scope, projRef, delta, anchorsChanged)
	needCtx := shouldRefreshDurableContext(prev.scope, scope, delta, anchorsChanged, reqKey.ctxGen != prev.key.ctxGen)
	if needCtx {
		a.populateDurableContext(&snap, scope)
	}
	a.populateProjectDurableActivity(&snap)
	finalKey := a.finalizeDurableSnapshot(&snap, reqKey, needCtx)
	return snap, finalKey, scope, true
}

func shouldRefreshDurableContext(prevScope, scope data.ProjectScope, delta data.ChangeDelta, anchorsChanged, ctxGenChanged bool) bool {
	if anchorsChanged || ctxGenChanged || delta.Kinds[data.KindContext] || delta.Kinds[data.KindPane] {
		return true
	}
	return delta.HasRelation && !maps.Equal(prevScope.RelatedIDs(data.KindContext), scope.RelatedIDs(data.KindContext))
}

func kindScopeChanged(prevScope, scope data.ProjectScope, delta data.ChangeDelta, kind string, anchorsChanged bool) bool {
	if anchorsChanged || delta.Kinds[kind] {
		return true
	}
	return delta.HasRelation && !maps.Equal(prevScope.RelatedIDs(kind), scope.RelatedIDs(kind))
}

func anyKindChanged(delta data.ChangeDelta, kinds ...string) bool {
	for _, k := range kinds {
		if delta.Kinds[k] {
			return true
		}
	}
	return false
}

func (a *App) refreshDurableScopeSlices(snap *projectDurableSnapshot, prevScope, scope data.ProjectScope, projRef *core.Ref, delta data.ChangeDelta, anchorsChanged bool) {
	if anchorsChanged || delta.Kinds[data.KindTask] {
		a.populateDurableTasks(snap, scope, projRef)
	}
	if anchorsChanged || delta.HasRelation || anyKindChanged(delta, data.KindTask, data.KindRepo) {
		a.populateDurableProjectRepos(snap)
	}
	if kindScopeChanged(prevScope, scope, delta, data.KindPage, anchorsChanged) {
		snap.pages, snap.unreadPages = projectPagesList(a.projectRecords(scope, data.KindPage))
	}
	if kindScopeChanged(prevScope, scope, delta, data.KindLane, anchorsChanged) {
		a.populateDurableLanes(snap, scope)
	}
	if anchorsChanged || delta.HasRelation || anyKindChanged(delta, data.KindTask, data.KindLane, data.KindRepo) {
		a.populateDurableWorktrees(snap, scope)
	}
	a.refreshDurableLeafRecordSlices(snap, prevScope, scope, delta, anchorsChanged)
}

func (a *App) refreshDurableLeafRecordSlices(snap *projectDurableSnapshot, prevScope, scope data.ProjectScope, delta data.ChangeDelta, anchorsChanged bool) {
	if kindScopeChanged(prevScope, scope, delta, data.KindDocument, anchorsChanged) {
		snap.documents = sortRecordsNewestFirst(a.projectRecords(scope, data.KindDocument))
	}
	if kindScopeChanged(prevScope, scope, delta, data.KindNote, anchorsChanged) {
		snap.notes = sortRecordsNewestFirst(a.projectRecords(scope, data.KindNote))
	}
	if kindScopeChanged(prevScope, scope, delta, data.KindQuestion, anchorsChanged) {
		snap.questions, snap.openQuestions = projectQuestionsList(a.projectRecords(scope, data.KindQuestion))
	}
	if kindScopeChanged(prevScope, scope, delta, data.KindCommand, anchorsChanged) {
		snap.commands = sortRecordsNewestFirst(a.projectRecords(scope, data.KindCommand))
	}
}

func (a *App) projectRailPaneSeeds(groupName string) []data.Record {
	if a.panes == nil {
		return nil
	}
	return a.panes.railGroupPanes[groupName]
}

func applyScopeAnchors(snap *projectDurableSnapshot, scope data.ProjectScope, paneSeeds []data.Record) {
	snap.taskIDs, snap.paneIDs = scope.TaskIDs, scope.PaneIDs
	snap.panes, snap.relations = scope.Panes, scope.Relations
	if snap.panes == nil {
		snap.panes = paneSeeds
	}
}

func (a *App) finalizeDurableSnapshot(snap *projectDurableSnapshot, reqKey projectDurableKey, contextRebuilt bool) projectDurableKey {
	snap.cacheCtxGen = a.contextIndexGen()
	finalKey := reqKey
	finalKey.ctxGen = snap.cacheCtxGen
	if a.Store != nil {
		postRev := a.storeRev()
		snap.cacheRev = postRev
		finalKey.storeRevision = postRev
		if contextRebuilt {
			snap.contextRev = postRev
		}
	}
	return finalKey
}

func (a *App) buildProjectDurableSnapshot(projKey, groupName string, projRef *core.Ref, reqKey projectDurableKey) (projectDurableSnapshot, projectDurableKey, data.ProjectScope) {
	snap := projectDurableSnapshot{
		groupName:    groupName,
		projectKey:   projKey,
		projectID:    reqKey.projID,
		projRef:      projRef,
		taskIDs:      map[string]bool{},
		paneIDs:      map[string]bool{},
		cacheRailKey: reqKey.railPanesKey,
	}
	paneSeeds := a.projectRailPaneSeeds(groupName)
	var scope data.ProjectScope
	if a.Store != nil {
		scope, _ = a.Store.ProjectScope(reqKey.projID, paneSeeds, a.nav.showArchived)
	}
	applyScopeAnchors(&snap, scope, paneSeeds)
	a.populateDurableScopeRecords(&snap, scope, projRef)
	a.populateDurableContext(&snap, scope)
	a.populateProjectDurableActivity(&snap)
	finalKey := a.finalizeDurableSnapshot(&snap, reqKey, true)
	return snap, finalKey, scope
}

func (a *App) populateDurableTasks(snap *projectDurableSnapshot, scope data.ProjectScope, projRef *core.Ref) {
	snap.projRec, snap.tasks, snap.taskTree = nil, nil, nil
	allTaskTree := data.BuildTree(scope.Tasks, "parent", "order", a.nav.showArchived)
	if projNode, ok := findProjectTreeNode(allTaskTree, snap.projectID); ok {
		snap.projRec = &projNode.Record
		snap.tasks = flattenTreeDescendants(projNode.Children)
		snap.taskTree = projNode.Children
	} else if projRef != nil && a.Store != nil {
		snap.projRec, _ = a.Store.Get(*projRef)
	}
}

func (a *App) populateDurableProjectRepos(snap *projectDurableSnapshot) {
	snap.projRepoLabels = nil
	if snap.projRec != nil && a.Store != nil {
		_, snap.projRepoLabels = data.RecordRepos(a.Store, *snap.projRec)
	}
}

func (a *App) populateDurableLanes(snap *projectDurableSnapshot, scope data.ProjectScope) {
	snap.allLanes = a.projectActiveRecords(scope, data.KindLane)
	snap.activeLanes = nil
	for _, lane := range snap.allLanes {
		if !lane.IsArchived() && data.LaneProgressOf(lane).IsOpen() {
			snap.activeLanes = append(snap.activeLanes, lane)
		}
	}
}

func (a *App) populateDurableWorktrees(snap *projectDurableSnapshot, scope data.ProjectScope) {
	snap.worktrees = nil
	if snap.projectID != "" && a.Store != nil {
		snap.worktrees = a.Store.ProjectScopeWorktrees(scope, snap.allLanes)
	}
}

func (a *App) populateDurableScopeRecords(snap *projectDurableSnapshot, scope data.ProjectScope, projRef *core.Ref) {
	a.populateDurableTasks(snap, scope, projRef)
	a.populateDurableProjectRepos(snap)
	snap.pages, snap.unreadPages = projectPagesList(a.projectRecords(scope, data.KindPage))
	a.populateDurableLanes(snap, scope)
	a.populateDurableWorktrees(snap, scope)
	snap.documents = sortRecordsNewestFirst(a.projectRecords(scope, data.KindDocument))
	snap.notes = sortRecordsNewestFirst(a.projectRecords(scope, data.KindNote))
	snap.questions, snap.openQuestions = projectQuestionsList(a.projectRecords(scope, data.KindQuestion))
	snap.commands = sortRecordsNewestFirst(a.projectRecords(scope, data.KindCommand))
}

func (a *App) populateDurableContext(snap *projectDurableSnapshot, scope data.ProjectScope) {
	snap.officialCount = 0
	snap.officialItems, snap.pinnedItems, snap.prItems = nil, nil, nil
	paneSchema := a.schema(data.KindPane)
	paneTitles := make(map[string]string, len(snap.panes))
	for _, p := range snap.panes {
		paneTitles[p.ID] = views.Title(paneSchema, p)
	}
	wsView := projectWorkspaceData{projectDurableSnapshot: *snap}
	snap.contextItems, snap.reviewContext, snap.contextRecords = a.projectUnifiedContextItems(wsView, scope, paneTitles)
	for _, it := range snap.contextItems {
		if it.Official {
			snap.officialCount++
			snap.officialItems = append(snap.officialItems, it)
		}
		if it.Official || it.Pinned || it.Starred {
			snap.pinnedItems = append(snap.pinnedItems, it)
		}
		if it.Kind == "pr" {
			snap.prItems = append(snap.prItems, it)
		}
	}
	a.projectCtrl().contextBuilds++
}

func (a *App) effectivePaneLiveStatus(p data.Record) (status string, running, needsYou bool) {
	hookState, _, pending := a.effectiveHookState(p.ID)
	effState := hookState
	if effState == "" {
		effState = strings.ToLower(strings.TrimSpace(p.Text("runStatus")))
	}
	switch {
	case len(pending) > 0 || effState == daemon.StateNeedsYou || effState == "waiting":
		return daemon.StateNeedsYou, false, true
	case effState == daemon.StateRunning || effState == "working":
		return daemon.StateRunning, true, false
	default:
		return effState, false, false
	}
}

func (a *App) buildProjectLiveOverlay(durable projectDurableSnapshot, telSeq uint64) projectLiveOverlay {
	overlay := projectLiveOverlay{
		telemetrySeq: telSeq,
		paneStatus:   make(map[string]string, len(durable.panes)),
		unreadPanes:  map[string]bool{},
	}
	for _, p := range durable.panes {
		status, running, needsYou := a.effectivePaneLiveStatus(p)
		if p.ID != "" && status != "" {
			overlay.paneStatus[p.ID] = status
		}
		if needsYou {
			overlay.needsYouPanes++
		} else if running {
			overlay.runningPanes++
		}
		if p.ID != "" && !p.IsArchived() && (a.paneHasAttention(p.ID) || durable.unreadPagePanes[p.ID]) {
			overlay.unreadPanes[p.ID] = true
			overlay.unreadPaneCount++
		}
	}
	return overlay
}

func (a *App) projectLiveOverlayFor(durable projectDurableSnapshot, dKey projectDurableKey) projectLiveOverlay {
	telSeq := a.telemetryViewSeq()
	pc := a.projectCtrl()
	if pc.liveCache != nil {
		if cached, ok := pc.liveCache[durable.projectKey]; ok && cached.durableKey == dKey && cached.overlay.telemetrySeq == telSeq {
			return cached.overlay
		}
	}
	overlay := a.buildProjectLiveOverlay(durable, telSeq)
	if pc.liveCache == nil {
		pc.liveCache = map[string]cachedProjectLive{}
	}
	pc.liveCache[durable.projectKey] = cachedProjectLive{
		durableKey: dKey,
		overlay:    overlay,
	}
	pc.liveBuilds++
	return overlay
}

func (a *App) collectProjectWorkspaceData(groupName string, projRef *core.Ref) projectWorkspaceData {
	durable, dKey := a.projectDurableSnapshotFor(groupName, projRef)
	live := a.projectLiveOverlayFor(durable, dKey)
	return composeProjectWorkspace(durable, live)
}
