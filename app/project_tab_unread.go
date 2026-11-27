package app

import (
	"maps"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

type cachedProjectTabUnread struct {
	rev              int64
	showArchived     bool
	cutoffNano       int64
	railKey          string
	scope            data.ProjectScope
	unreadPages      int
	hasDurableUnread bool
	panes            []data.Record
	unreadPagePanes  map[string]bool
}

// projectTabHasUnreadFast reports whether a dynamic project tab has any unread
// items or needs-you panes without loading the full durable workspace snapshot
// (or 7,000+ historical context items) for inactive background tabs.
func (a *App) projectTabHasUnreadFast(projectID, title string) bool {
	if a == nil || a.Store == nil || projectID == "" {
		return false
	}
	ref := core.Ref{Kind: data.KindTask, ID: projectID}
	projKey, reqKey := a.makeProjectDurableKey(title, &ref)
	pc := a.projectCtrl()
	if cached, ok := pc.durableCache[projKey]; ok && cached.key == reqKey {
		if cached.snap.baseUnreadTotal > 0 {
			return true
		}
		live := a.projectLiveOverlayFor(cached.snap, cached.key)
		return live.unreadPaneCount > 0 || live.needsYouPanes > 0
	}
	entry := a.projectTabUnreadEntry(projectID, title, reqKey)
	if entry.hasDurableUnread {
		return true
	}
	return a.projectTabHasLivePaneAttention(entry.panes, entry.unreadPagePanes)
}

func (a *App) projectTabHasLivePaneAttention(panes []data.Record, unreadPagePanes map[string]bool) bool {
	for _, p := range panes {
		_, _, needsYou := a.effectivePaneLiveStatus(p)
		if needsYou {
			return true
		}
		if p.ID != "" && !p.IsArchived() && (a.paneHasAttention(p.ID) || unreadPagePanes[p.ID]) {
			return true
		}
	}
	return false
}

func (a *App) projectTabUnreadEntry(projectID, title string, reqKey projectDurableKey) cachedProjectTabUnread {
	pc := a.projectCtrl()
	cutoffNano := a.navUnreadCutoff.UnixNano()
	var prev cachedProjectTabUnread
	var hasPrev bool
	if pc.tabUnreadCache != nil {
		if ent, ok := pc.tabUnreadCache[projectID]; ok &&
			ent.showArchived == reqKey.showArchived &&
			ent.cutoffNano == cutoffNano &&
			ent.railKey == reqKey.railPanesKey {
			if ent.rev == reqKey.storeRevision {
				return ent
			}
			prev, hasPrev = ent, true
		}
	}
	ent := a.resolveProjectTabUnreadEntry(projectID, title, reqKey, cutoffNano, prev, hasPrev)
	if pc.tabUnreadCache == nil {
		pc.tabUnreadCache = map[string]cachedProjectTabUnread{}
	}
	pc.tabUnreadCache[projectID] = ent
	return ent
}

func (a *App) resolveProjectTabUnreadEntry(projectID, title string, reqKey projectDurableKey, cutoffNano int64, prev cachedProjectTabUnread, hasPrev bool) cachedProjectTabUnread {
	if hasPrev && reqKey.storeRevision > prev.rev {
		if next, ok := a.refreshProjectTabUnreadDelta(projectID, title, reqKey, cutoffNano, prev); ok {
			return next
		}
	}
	return a.computeProjectTabUnreadEntry(projectID, title, reqKey, cutoffNano)
}

func (a *App) refreshProjectTabScope(projectID, title string, reqKey projectDurableKey, prev cachedProjectTabUnread, delta data.ChangeDelta) (data.ProjectScope, []data.Record, bool, bool) {
	if !delta.HasRelation && !anyKindChanged(delta, data.KindTask, data.KindPane) {
		return prev.scope, prev.panes, false, true
	}
	var paneSeeds []data.Record
	if a.panes != nil {
		paneSeeds = a.panes.railGroupPanes[title]
	}
	scope, err := a.Store.ProjectScope(projectID, paneSeeds, reqKey.showArchived)
	if err != nil {
		return data.ProjectScope{}, nil, false, false
	}
	anchorsChanged := !maps.Equal(prev.scope.TaskIDs, scope.TaskIDs) || !maps.Equal(prev.scope.PaneIDs, scope.PaneIDs)
	panes := scope.Panes
	if panes == nil {
		panes = paneSeeds
	}
	return scope, panes, anchorsChanged, true
}

func (a *App) refreshProjectTabUnreadDelta(projectID, title string, reqKey projectDurableKey, cutoffNano int64, prev cachedProjectTabUnread) (cachedProjectTabUnread, bool) {
	delta, err := a.Store.ChangedKindsSince(prev.rev)
	if err != nil || delta.FullRebuild {
		return cachedProjectTabUnread{}, false
	}
	scope, panes, anchorsChanged, ok := a.refreshProjectTabScope(projectID, title, reqKey, prev, delta)
	if !ok {
		return cachedProjectTabUnread{}, false
	}
	unreadPages, unreadPagePanes := prev.unreadPages, prev.unreadPagePanes
	if kindScopeChanged(prev.scope, scope, delta, data.KindPage, anchorsChanged) {
		pages, count := projectPagesList(a.projectRecords(scope, data.KindPage))
		unreadPages, unreadPagePanes = count, collectUnreadPagePanes(pages)
	}
	hasDurable := unreadPages > 0 || a.projectScopeHasOtherUnread(scope, projectID, cutoffNano)
	return cachedProjectTabUnread{
		rev:              reqKey.storeRevision,
		showArchived:     reqKey.showArchived,
		cutoffNano:       cutoffNano,
		railKey:          reqKey.railPanesKey,
		scope:            scope,
		unreadPages:      unreadPages,
		hasDurableUnread: hasDurable,
		panes:            panes,
		unreadPagePanes:  unreadPagePanes,
	}, true
}

func (a *App) computeProjectTabUnreadEntry(projectID, title string, reqKey projectDurableKey, cutoffNano int64) cachedProjectTabUnread {
	var paneSeeds []data.Record
	if a.panes != nil {
		paneSeeds = a.panes.railGroupPanes[title]
	}
	scope, err := a.Store.ProjectScope(projectID, paneSeeds, reqKey.showArchived)
	if err != nil {
		return cachedProjectTabUnread{
			rev: reqKey.storeRevision, showArchived: reqKey.showArchived,
			cutoffNano: cutoffNano, railKey: reqKey.railPanesKey,
		}
	}
	panes := scope.Panes
	if panes == nil {
		panes = paneSeeds
	}
	pages, unreadPages := projectPagesList(a.projectRecords(scope, data.KindPage))
	unreadPagePanes := collectUnreadPagePanes(pages)
	hasDurable := unreadPages > 0 || a.projectScopeHasOtherUnread(scope, projectID, cutoffNano)
	return cachedProjectTabUnread{
		rev:              reqKey.storeRevision,
		showArchived:     reqKey.showArchived,
		cutoffNano:       cutoffNano,
		railKey:          reqKey.railPanesKey,
		scope:            scope,
		unreadPages:      unreadPages,
		hasDurableUnread: hasDurable,
		panes:            panes,
		unreadPagePanes:  unreadPagePanes,
	}
}

func collectUnreadPagePanes(pages []data.Record) map[string]bool {
	out := map[string]bool{}
	for _, p := range pages {
		if !p.IsUnread() {
			continue
		}
		if pRef, ok := p.RefField("pane"); ok && pRef.ID != "" {
			out[pRef.ID] = true
		}
	}
	return out
}

func (a *App) projectScopeHasOtherUnread(scope data.ProjectScope, projectID string, cutoffNano int64) bool {
	if a.anyUnreadOfKind(data.KindQuestion, a.projectRecords(scope, data.KindQuestion)) {
		return true
	}
	if a.anyUnreadOfKind(data.KindNote, a.projectRecords(scope, data.KindNote)) {
		return true
	}
	if a.projectScopeHasUnreadTask(scope, projectID) {
		return true
	}
	if a.projectScopeHasUnreadGenericCandidates(scope, cutoffNano) {
		return true
	}
	return a.projectScopeHasUnreadContext(scope, cutoffNano)
}

func (a *App) projectScopeHasUnreadTask(scope data.ProjectScope, projectID string) bool {
	for _, rec := range scope.Tasks {
		if rec.ID == projectID || rec.IsArchived() {
			continue
		}
		if a.isGenericNavRecordUnread(rec) {
			return true
		}
	}
	return false
}

func (a *App) projectScopeHasUnreadGenericCandidates(scope data.ProjectScope, cutoffNano int64) bool {
	for _, kind := range []string{data.KindLane, data.KindDocument, data.KindCommand} {
		recs, err := a.Store.ProjectUnreadCandidates(scope, kind, cutoffNano)
		if err != nil {
			continue
		}
		if a.anyUnreadOfKind(kind, recs) {
			return true
		}
	}
	return false
}

func (a *App) projectScopeHasUnreadContext(scope data.ProjectScope, cutoffNano int64) bool {
	recs, err := a.Store.ProjectUnreadCandidates(scope, data.KindContext, cutoffNano)
	if err != nil {
		return false
	}
	for _, rec := range recs {
		if a.isProjectContextUnread(rec) {
			return true
		}
	}
	return false
}

func (a *App) anyUnreadOfKind(kind string, recs []data.Record) bool {
	for _, rec := range recs {
		if a.isNavRecordUnread(kind, rec, nil) {
			return true
		}
	}
	return false
}
