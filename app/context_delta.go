package app

import (
	"strings"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

// contextTaskShape is what the context index needs of a task to name an
// item's project: the parent it hangs under ("" for a root) and its title.
type contextTaskShape struct{ parent, title string }

// contextRoot is the project (root task) a context item belongs to.
type contextRoot struct{ id, title string }

func contextTaskShapeOf(schema data.Schema, task data.Record) contextTaskShape {
	shape := contextTaskShape{title: views.Title(schema, task)}
	if p, ok := task.RefField("parent"); ok && p.Kind == data.KindTask {
		shape.parent = p.ID
	}
	return shape
}

// loadPersistedContextIndex brings the in-memory index up to the store's
// newest change set. A cold start, an undo that moved the store backwards and
// a reparented task read every context record; any other write merges only the
// records changed since the revision the index already holds.
func (a *App) loadPersistedContextIndex(idx *ContextController) {
	if a.Store == nil {
		return
	}
	rev, _ := a.Store.LatestChangeSetNumber()
	idx.mu.RLock()
	prevLoaded, prevRev := idx.loadedStored, idx.storedRev
	idx.mu.RUnlock()
	if prevLoaded && prevRev == rev {
		return
	}
	if prevLoaded && rev > prevRev {
		if !contextKindsTouchedSince(a.Store, prevRev, rev) {
			idx.mu.Lock()
			if idx.loadedStored && idx.storedRev == prevRev {
				idx.storedRev = rev
			}
			idx.mu.Unlock()
			return
		}
		if a.mergeContextIndexDelta(idx, prevRev, rev) {
			return
		}
	}
	a.loadContextIndexFull(idx, rev)
}

// loadContextIndexFull reads every context record, and the panes and tasks
// they name in two bulk queries, and merges them into the index.
func (a *App) loadContextIndexFull(idx *ContextController, rev int64) {
	res, err := a.Store.Query(data.Query{Kind: data.KindContext, IncludeArchived: true})
	if err != nil {
		return
	}
	paneSchema, taskSchema := a.schema(data.KindPane), a.schema(data.KindTask)
	paneTitles := map[string]string{}
	if panes, perr := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: true}); perr == nil {
		paneTitles = make(map[string]string, len(panes.Items))
		for _, p := range panes.Items {
			paneTitles[p.ID] = views.Title(paneSchema, p)
		}
	}
	tasks := map[string]contextTaskShape{}
	if all, terr := a.Store.Query(data.Query{Kind: data.KindTask, IncludeArchived: true}); terr == nil {
		tasks = make(map[string]contextTaskShape, len(all.Items))
		for _, t := range all.Items {
			tasks[t.ID] = contextTaskShapeOf(taskSchema, t)
		}
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.loadedStored && idx.storedRev == rev {
		return
	}
	idx.paneTitles, idx.tasks = paneTitles, tasks
	changed := false
	roots := map[string]contextRoot{}
	for _, rec := range res.Items {
		if idx.mergeStoredRecord(rec, roots) {
			changed = true
		}
	}
	idx.storedRev, idx.loadedStored = rev, true
	if changed {
		idx.gen++
	}
}

// mergeContextIndexDelta merges the context, pane and task records written
// after prevRev into an index loaded at prevRev. It reports false, leaving the
// index for a full load, when it cannot: a query failed, the index moved under
// it, or a task changed parent (which can move a whole subtree's items to
// another project).
func (a *App) mergeContextIndexDelta(idx *ContextController, prevRev, rev int64) bool {
	contexts, err := a.Store.QueryChangedSince(data.KindContext, prevRev, true)
	if err != nil {
		return false
	}
	panes, err := a.Store.QueryChangedSince(data.KindPane, prevRev, true)
	if err != nil {
		return false
	}
	tasks, err := a.Store.QueryChangedSince(data.KindTask, prevRev, true)
	if err != nil {
		return false
	}
	paneSchema, taskSchema := a.schema(data.KindPane), a.schema(data.KindTask)

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if !idx.loadedStored || idx.storedRev != prevRev || idx.paneTitles == nil || idx.tasks == nil {
		return idx.loadedStored && idx.storedRev == rev
	}
	projectTitles := map[string]string{}
	for _, t := range tasks {
		shape := contextTaskShapeOf(taskSchema, t)
		old, known := idx.tasks[t.ID]
		if known && old.parent != shape.parent {
			return false
		}
		idx.tasks[t.ID] = shape
		if known && old.title != shape.title && shape.parent == "" {
			projectTitles[t.ID] = shape.title
		}
	}
	paneRetitled := map[string]string{}
	for _, p := range panes {
		title := views.Title(paneSchema, p)
		if old, known := idx.paneTitles[p.ID]; !known || old != title {
			idx.paneTitles[p.ID] = title
			paneRetitled[p.ID] = title
		}
	}
	changed := false
	if len(projectTitles) > 0 || len(paneRetitled) > 0 {
		for _, it := range idx.items {
			if t, ok := paneRetitled[it.PaneID]; ok && it.PaneID != "" && it.PaneTitle != t {
				it.PaneTitle, changed = t, true
			}
			if t, ok := projectTitles[it.ProjectID]; ok && it.Project != t {
				it.Project, changed = t, true
			}
		}
	}
	roots := map[string]contextRoot{}
	for _, rec := range contexts {
		if idx.mergeStoredRecord(rec, roots) {
			changed = true
		}
	}
	idx.storedRev = rev
	if changed {
		idx.gen++
	}
	return true
}

// taskRoot walks id's parents to the project it belongs to. roots remembers
// answers for one merge. The caller holds idx.mu.
func (idx *ContextController) taskRoot(id string, roots map[string]contextRoot) (contextRoot, bool) {
	if r, ok := roots[id]; ok {
		return r, true
	}
	cur, ok := idx.tasks[id]
	if !ok {
		return contextRoot{}, false
	}
	rootID, seen := id, map[string]bool{id: true}
	for range 32 {
		parent, exists := idx.tasks[cur.parent]
		if cur.parent == "" || seen[cur.parent] || !exists {
			break
		}
		rootID, cur = cur.parent, parent
		seen[rootID] = true
	}
	r := contextRoot{id: rootID, title: cur.title}
	roots[id] = r
	return r, true
}

// mergeStoredRecord folds one stored context record into the index and reports
// whether what the index shows changed. The caller holds idx.mu.
func (idx *ContextController) mergeStoredRecord(rec data.Record, roots map[string]contextRoot) bool {
	val := strings.TrimSpace(rec.Text("value"))
	kind := rec.Text("contextKind")
	if kind == "" {
		kind = "url"
	}
	canon := strings.TrimSpace(rec.Text("canonKey"))
	if canon == "" {
		canon = contextCanonKey(kind, val)
	}
	if canon == "" {
		return false
	}
	paneID := ""
	if pRef, ok := rec.RefField("pane"); ok {
		paneID = pRef.ID
	}
	key := paneID + "\x00" + canon
	idx.persisted[key] = true
	if rec.IsArchived() {
		if cur, exists := idx.items[key]; exists && cur.RecordID == rec.ID {
			delete(idx.items, key)
			return true
		}
		return false
	}
	if val == "" {
		return false
	}
	paneTitle := ""
	if paneID != "" {
		if t, ok := idx.paneTitles[paneID]; ok {
			paneTitle = t
		} else {
			paneTitle = data.RefText(core.Ref{Kind: data.KindPane, ID: paneID})
		}
	}
	projID, projTitle := "", unassignedProjectLabel
	if tRef, ok := rec.RefField("task"); ok && tRef.Kind == data.KindTask {
		if root, found := idx.taskRoot(tRef.ID, roots); found {
			projID, projTitle = root.id, root.title
		}
	}
	path := ""
	if kind == contextitems.KindFile || kind == "doc" {
		path, _, _ = splitContextFileLoc(val)
	}
	next := &CollectedContextItem{
		RecordID: rec.ID, Key: key, CanonKey: canon, Kind: kind, Value: val, Path: path,
		Label: rec.Text("title"), Source: rec.Text("source"), Line: recordIntField(rec, "line"), Col: recordIntField(rec, "col"),
		PaneID: paneID, PaneTitle: paneTitle, ProjectID: projID, Project: projTitle,
		Starred: rec.IsStarred() || idx.starred[canon], Official: rec.Bool("official"), Pinned: rec.Bool("pinned"),
		Notes: rec.Text("notes"), SeenAt: rec.UpdatedAt,
	}
	cur, exists := idx.items[key]
	idx.items[key] = next
	return !exists || !contextItemVisibleEqual(cur, next)
}
