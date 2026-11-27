package app

import (
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
// questions, pages, commands, worktrees, and store-derived unread indices).
// It is cached across live daemon ticks whenever storeRevision and its durable
// inputs have not changed.
type projectDurableSnapshot struct {
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
	baseUnreadTotal                  int
	cacheRev                         int64
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
	key  projectDurableKey
	snap projectDurableSnapshot
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
		return
	}
	clear(pc.durableCache)
	clear(pc.liveCache)
}

func (a *App) makeProjectDurableKey(groupName string, projRef *core.Ref) (string, projectDurableKey) {
	projKey, projID := projectKeyFor(groupName, projRef), ""
	if projRef != nil {
		projID = projRef.ID
	}
	var rev int64
	if a.Store != nil {
		rev, _ = a.Store.LatestChangeSetNumber()
	}
	return projKey, projectDurableKey{
		storeRevision: rev,
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
	if a.Store != nil && pc.durableCache != nil {
		if cached, ok := pc.durableCache[projKey]; ok && cached.key == reqKey {
			return cached.snap, cached.key
		}
	}
	snap, finalKey := a.buildProjectDurableSnapshot(projKey, groupName, projRef, reqKey)
	if a.Store != nil {
		if pc.durableCache == nil {
			pc.durableCache = map[string]cachedProjectDurable{}
		}
		pc.durableCache[projKey] = cachedProjectDurable{key: finalKey, snap: snap}
		delete(pc.liveCache, projKey)
		pc.durableBuilds++
	}
	return snap, finalKey
}

func (a *App) buildProjectDurableSnapshot(projKey, groupName string, projRef *core.Ref, reqKey projectDurableKey) (projectDurableSnapshot, projectDurableKey) {
	snap := projectDurableSnapshot{
		groupName:    groupName,
		projectKey:   projKey,
		projectID:    reqKey.projID,
		projRef:      projRef,
		taskIDs:      map[string]bool{},
		paneIDs:      map[string]bool{},
		cacheRailKey: reqKey.railPanesKey,
	}
	var paneSeeds []data.Record
	if a.panes != nil {
		paneSeeds = a.panes.railGroupPanes[groupName]
	}
	var scope data.ProjectScope
	if a.Store != nil {
		scope, _ = a.Store.ProjectScope(reqKey.projID, paneSeeds, a.nav.showArchived)
	}
	snap.taskIDs, snap.paneIDs = scope.TaskIDs, scope.PaneIDs
	snap.panes, snap.relations = scope.Panes, scope.Relations
	if snap.panes == nil {
		snap.panes = paneSeeds
	}
	a.populateDurableScopeRecords(&snap, scope, projRef)
	a.populateDurableContext(&snap, scope)
	a.populateProjectDurableActivity(&snap)
	snap.cacheCtxGen = a.contextIndexGen()
	finalKey := reqKey
	finalKey.ctxGen = snap.cacheCtxGen
	if a.Store != nil {
		postRev, _ := a.Store.LatestChangeSetNumber()
		snap.cacheRev = postRev
		finalKey.storeRevision = postRev
	}
	return snap, finalKey
}

func (a *App) populateDurableScopeRecords(snap *projectDurableSnapshot, scope data.ProjectScope, projRef *core.Ref) {
	allTaskTree := data.BuildTree(scope.Tasks, "parent", "order", a.nav.showArchived)
	if projNode, ok := findProjectTreeNode(allTaskTree, snap.projectID); ok {
		snap.projRec = &projNode.Record
		snap.tasks = flattenTreeDescendants(projNode.Children)
		snap.taskTree = projNode.Children
	} else if projRef != nil && a.Store != nil {
		snap.projRec, _ = a.Store.Get(*projRef)
	}
	snap.pages, snap.unreadPages = projectPagesList(a.projectRecords(scope, data.KindPage))
	snap.allLanes = a.projectActiveRecords(scope, data.KindLane)
	for _, lane := range snap.allLanes {
		if !lane.IsArchived() && data.LaneProgressOf(lane).IsOpen() {
			snap.activeLanes = append(snap.activeLanes, lane)
		}
	}
	if snap.projectID != "" && a.Store != nil {
		snap.worktrees = a.Store.ProjectScopeWorktrees(scope, snap.allLanes)
	}
	snap.documents = a.projectRecords(scope, data.KindDocument)
	snap.notes = a.projectRecords(scope, data.KindNote)
	snap.questions, snap.openQuestions = projectQuestionsList(a.projectRecords(scope, data.KindQuestion))
	snap.commands = a.projectRecords(scope, data.KindCommand)
}

func (a *App) populateDurableContext(snap *projectDurableSnapshot, scope data.ProjectScope) {
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
