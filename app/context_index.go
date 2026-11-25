package app

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// CollectedContextItem is one deduplicated high-signal context item harvested
// from a pane's prompts, agent pages, documents, or visible terminal screen,
// or curated as official project context.
type CollectedContextItem struct {
	RecordID  string
	Key       string
	CanonKey  string
	Kind      string
	Value     string
	Path      string
	Line      int
	Col       int
	Label     string
	Source    string
	PaneID    string
	PaneTitle string
	ProjectID string
	Project   string
	Starred   bool
	Official  bool
	Pinned    bool
	Notes     string
	SeenAt    time.Time
}

// contextHarvestActor persists automatically harvested context items to SQLite
// without entering the owner's undo stack.
var contextHarvestActor = core.Actor{Kind: "system", ID: "mtm.context", Label: "context"}

// ContextController owns the deduplicated high-signal context index harvested
// from prompts, agent pages, documents, and visible terminal screens.
type ContextController struct {
	mu              sync.RWMutex
	items           map[string]*CollectedContextItem // paneID\x00canonKey -> item
	starred         map[string]bool                  // canonKey -> starred
	persisted       map[string]bool                  // paneID\x00canonKey -> persisted in SQLite
	loadedStored    bool
	storedRev       int64
	syncedStored    bool
	syncRev         int64
	syncShowArch    bool
	gen             uint64
	compRev         int64
	compTaskID      string
	compLoaded      bool
	compItems       []composerCompletion
	pageRevs        map[string]int64
	docRevs         map[string]int64
	screenRevs      map[string]uint64
	screenHarvestAt map[string]time.Time
}

func newContextController() *ContextController {
	return &ContextController{
		items: map[string]*CollectedContextItem{}, starred: map[string]bool{}, persisted: map[string]bool{},
		pageRevs: map[string]int64{}, docRevs: map[string]int64{}, screenRevs: map[string]uint64{}, screenHarvestAt: map[string]time.Time{},
	}
}

func contextCanonKey(kind, value string) string {
	val := strings.TrimSpace(value)
	if val == "" {
		return ""
	}
	switch kind {
	case contextitems.KindURL, contextitems.KindOSC8:
		return "uri\x00" + val
	case contextitems.KindFile:
		base, _, _ := splitContextFileLoc(val)
		return "file\x00" + base
	default:
		return kind + "\x00" + val
	}
}

func splitContextFileLoc(raw string) (path string, line, col int) {
	path = raw
	// Parse optional trailing :line or :line:col.
	parts := strings.Split(raw, ":")
	if len(parts) >= 2 {
		if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil && n > 0 {
			if len(parts) >= 3 {
				if l, err2 := strconv.Atoi(parts[len(parts)-2]); err2 == nil && l > 0 {
					return strings.Join(parts[:len(parts)-2], ":"), l, n
				}
			}
			return strings.Join(parts[:len(parts)-1], ":"), n, 0
		}
	}
	return path, 0, 0
}

func (a *App) ensureContextIndex() *ContextController {
	if a.ctx == nil {
		a.ctx = newContextController()
	}
	return a.ctx
}

func mapContextRecordKind(kind string) string {
	switch kind {
	case contextitems.KindPR:
		return "pr"
	case contextitems.KindFile:
		return "file"
	case "doc":
		return "doc"
	default:
		return "url"
	}
}

func mapContextRecordSource(src string) string {
	switch src {
	case contextitems.SourcePrompt:
		return "prompt"
	case contextitems.SourceResponse:
		return "response"
	case contextitems.SourceScreen:
		return "screen"
	default:
		return "tool"
	}
}

func (a *App) contextIndexGen() uint64 {
	if a == nil || a.ctx == nil {
		return 0
	}
	a.ctx.mu.RLock()
	g := a.ctx.gen
	a.ctx.mu.RUnlock()
	return g
}

func (a *App) invalidateContextIndex() {
	if a.ctx == nil {
		return
	}
	a.ctx.mu.Lock()
	a.ctx.loadedStored, a.ctx.syncedStored, a.ctx.compLoaded = false, false, false
	a.ctx.gen++
	a.ctx.mu.Unlock()
}

func (a *App) loadPersistedContextIndex(idx *ContextController) {
	if a.Store == nil {
		return
	}
	rev, _ := a.Store.LatestChangeSetNumber()
	idx.mu.RLock()
	already := idx.loadedStored && idx.storedRev == rev
	idx.mu.RUnlock()
	if already {
		return
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindContext, IncludeArchived: true})
	if err != nil {
		return
	}
	// Preload panes and tasks in 2 bulk queries to avoid O(N) SQLite Store.Get calls.
	paneSchema, taskSchema := a.schema(data.KindPane), a.schema(data.KindTask)
	paneTitles := map[string]string{}
	if panesRes, perr := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: true}); perr == nil {
		paneTitles = make(map[string]string, len(panesRes.Items))
		for _, p := range panesRes.Items {
			paneTitles[p.ID] = views.Title(paneSchema, p)
		}
	}
	taskByID := map[string]data.Record{}
	if tasksRes, terr := a.Store.Query(data.Query{Kind: data.KindTask, IncludeArchived: true}); terr == nil {
		taskByID = make(map[string]data.Record, len(tasksRes.Items))
		for _, t := range tasksRes.Items {
			taskByID[t.ID] = t
		}
	}
	type rootInfo struct{ id, title string }
	rootOf := make(map[string]rootInfo, len(taskByID))
	resolveRoot := func(id string) (string, string, bool) {
		if r, ok := rootOf[id]; ok {
			return r.id, r.title, true
		}
		cur, ok := taskByID[id]
		if !ok {
			return "", "", false
		}
		chain, seen := []string{cur.ID}, map[string]bool{cur.ID: true}
		for range 32 {
			pRef, has := cur.RefField("parent")
			if !has || pRef.Kind != data.KindTask || pRef.ID == "" || seen[pRef.ID] {
				break
			}
			if r, cached := rootOf[pRef.ID]; cached {
				for _, cid := range chain {
					rootOf[cid] = r
				}
				return r.id, r.title, true
			}
			parent, exists := taskByID[pRef.ID]
			if !exists {
				break
			}
			seen[parent.ID] = true
			chain = append(chain, parent.ID)
			cur = parent
		}
		r := rootInfo{id: cur.ID, title: views.Title(taskSchema, cur)}
		for _, cid := range chain {
			rootOf[cid] = r
		}
		return r.id, r.title, true
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.loadedStored && idx.storedRev == rev {
		return
	}
	for _, rec := range res.Items {
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
			continue
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
			}
			continue
		}
		if val == "" {
			continue
		}
		paneTitle := ""
		if paneID != "" {
			if t, ok := paneTitles[paneID]; ok {
				paneTitle = t
			} else {
				paneTitle = data.RefText(core.Ref{Kind: data.KindPane, ID: paneID})
			}
		}
		projID, projTitle := "", unassignedProjectLabel
		if tRef, ok := rec.RefField("task"); ok && tRef.Kind == data.KindTask {
			if rid, rtitle, found := resolveRoot(tRef.ID); found {
				projID, projTitle = rid, rtitle
			}
		}
		path := ""
		if kind == contextitems.KindFile || kind == "doc" {
			path, _, _ = splitContextFileLoc(val)
		}
		idx.items[key] = &CollectedContextItem{
			RecordID: rec.ID, Key: key, CanonKey: canon, Kind: kind, Value: val, Path: path,
			Label: rec.Text("title"), Source: rec.Text("source"), Line: recordIntField(rec, "line"), Col: recordIntField(rec, "col"),
			PaneID: paneID, PaneTitle: paneTitle, ProjectID: projID, Project: projTitle,
			Starred: rec.IsStarred() || idx.starred[canon], Official: rec.Bool("official"), Pinned: rec.Bool("pinned"),
			Notes: rec.Text("notes"), SeenAt: rec.UpdatedAt,
		}
	}
	idx.storedRev = rev
	idx.loadedStored = true
	idx.gen++
}

func (a *App) indexContextItems(pane *data.Record, projID, projTitle string, extracted []contextitems.Item) {
	if len(extracted) == 0 {
		return
	}
	idx := a.ensureContextIndex()
	a.loadPersistedContextIndex(idx)
	paneID, paneTitle := "", ""
	var taskRef *core.Ref
	if pane != nil {
		paneID = pane.ID
		paneTitle = views.Title(a.schema(data.KindPane), *pane)
		if tRef, hasTask := pane.RefField("task"); hasTask && tRef.Kind == data.KindTask && tRef.ID != "" {
			r := tRef
			taskRef = &r
		}
		if projTitle == "" {
			var ref *core.Ref
			projTitle, ref = a.resolvePaneProject(*pane)
			if ref != nil {
				projID = ref.ID
			}
		}
	} else if projID != "" {
		r := core.Ref{Kind: data.KindTask, ID: projID}
		taskRef = &r
	}
	if projTitle == "" {
		projTitle = unassignedProjectLabel
	}
	now := a.now()

	var toPersist []CollectedContextItem

	idx.mu.Lock()
	for _, it := range extracted {
		canon := contextCanonKey(it.Kind, it.Value)
		if canon == "" {
			continue
		}
		key := paneID + "\x00" + canon
		if cur, exists := idx.items[key]; exists {
			if cur.Label == "" && it.Label != "" {
				cur.Label = it.Label
			}
			if cur.Line == 0 && it.Line > 0 {
				cur.Line, cur.Col, cur.Value = it.Line, it.Col, it.Value
			}
			if cur.Path == "" && it.Path != "" {
				cur.Path = it.Path
			}
			cur.PaneTitle = paneTitle
			cur.ProjectID = projID
			cur.Project = projTitle
			cur.Starred = idx.starred[canon]
			cur.SeenAt = now
			continue
		}
		p := it.Path
		if p == "" && it.Kind == contextitems.KindFile {
			p, _, _ = splitContextFileLoc(it.Value)
		}
		collected := CollectedContextItem{
			Key: key, CanonKey: canon, Kind: it.Kind, Value: it.Value, Path: p,
			Line: it.Line, Col: it.Col, Label: it.Label, Source: it.Source,
			PaneID: paneID, PaneTitle: paneTitle, ProjectID: projID, Project: projTitle,
			Starred: idx.starred[canon], SeenAt: now,
		}
		idx.items[key] = &collected
		if !idx.persisted[key] {
			idx.persisted[key] = true
			toPersist = append(toPersist, collected)
		}
	}
	idx.gen++
	idx.mu.Unlock()

	if a.Store == nil || len(toPersist) == 0 {
		return
	}
	beforeRev, _ := a.Store.LatestChangeSetNumber()
	persistedN := 0
	for _, item := range toPersist {
		title := strings.TrimSpace(item.Label)
		if title == "" {
			title = item.Value
		}
		fields := map[string]any{
			"title": title, "value": item.Value, "contextKind": mapContextRecordKind(item.Kind),
			"source": mapContextRecordSource(item.Source), "canonKey": item.CanonKey,
		}
		if item.Line > 0 {
			fields["line"] = item.Line
		}
		if item.Col > 0 {
			fields["col"] = item.Col
		}
		if pane != nil {
			fields["pane"] = pane.Ref()
		}
		if taskRef != nil {
			fields["task"] = *taskRef
		}
		if created, _, err := a.Store.Create(data.CreateInput{Kind: data.KindContext, Fields: fields}, contextHarvestActor); err == nil && created != nil {
			persistedN++
			idx.mu.Lock()
			if cur, ok := idx.items[item.Key]; ok && cur.RecordID == "" {
				cur.RecordID = created.ID
			}
			idx.mu.Unlock()
		}
	}
	if persistedN > 0 {
		afterRev, _ := a.Store.LatestChangeSetNumber()
		idx.mu.Lock()
		if idx.loadedStored && idx.storedRev == beforeRev && afterRev == beforeRev+int64(persistedN) {
			idx.storedRev = afterRev
		}
		idx.compLoaded = false
		idx.gen++
		idx.mu.Unlock()
	}
}

// harvestPrompt extracts and indexes high-signal context items from a user
// prompt sent to pane.
func stripInjectedPromptSections(text string) string {
	for _, marker := range []string{"\n\n## Official Project Context\n", "\n## Official Project Context\n", "\n\n[Attachments]\n"} {
		if idx := strings.Index(text, marker); idx >= 0 {
			text = text[:idx]
		}
	}
	return text
}

func (a *App) harvestPrompt(pane data.Record, text string) {
	text = stripInjectedPromptSections(text)
	if strings.TrimSpace(text) == "" {
		return
	}
	items := contextitems.Extract(text, contextitems.Options{Source: contextitems.SourcePrompt})
	a.indexContextItems(&pane, "", "", items)
}

const liveScreenHarvestInterval = 2 * time.Second

// harvestScreenLines extracts and indexes high-signal context items from
// visible terminal screen lines (deduplicated per pane revision when rev > 0).
// In live mode, harvesting is throttled per pane and executed asynchronously so
// interactive typing and high-FPS terminal output never block on regex scans or
// SQLite writes on the UI goroutine.
func (a *App) harvestScreenLines(pane data.Record, lines []string, wrapWidth int, rev uint64) {
	if len(lines) == 0 {
		return
	}
	idx := a.ensureContextIndex()
	now := a.now()
	idx.mu.RLock()
	lastRev := idx.screenRevs[pane.ID]
	lastAt := idx.screenHarvestAt[pane.ID]
	idx.mu.RUnlock()
	if rev > 0 && lastRev == rev {
		return
	}
	if a.opts.Live && !lastAt.IsZero() && now.Sub(lastAt) < liveScreenHarvestInterval {
		return
	}
	idx.mu.Lock()
	if rev > 0 {
		idx.screenRevs[pane.ID] = rev
	}
	idx.screenHarvestAt[pane.ID] = now
	idx.mu.Unlock()

	if a.opts.Live {
		paneCopy := pane
		linesCopy := slices.Clone(lines)
		a.background(func(_ context.Context) func(core.State) core.State {
			items := contextitems.ExtractLines(linesCopy, contextitems.Options{
				Source:    contextitems.SourceScreen,
				WrapWidth: wrapWidth,
			})
			if len(items) > 0 {
				a.indexContextItems(&paneCopy, "", "", items)
			}
			return nil
		})
		return
	}

	items := contextitems.ExtractLines(lines, contextitems.Options{
		Source:    contextitems.SourceScreen,
		WrapWidth: wrapWidth,
	})
	a.indexContextItems(&pane, "", "", items)
}

// recordVisibleMatches records matches returned by contextitems.ExtractVisibleMatches
// directly into the pane's context index.
func (a *App) recordVisibleMatches(pane data.Record, matches []contextitems.Match) {
	if len(matches) == 0 {
		return
	}
	items := make([]contextitems.Item, 0, len(matches))
	for _, m := range matches {
		it := contextitems.Item{
			Kind:   m.Kind,
			Value:  m.Value,
			Label:  m.Label,
			Source: contextitems.SourceScreen,
		}
		if m.Kind == contextitems.KindFile {
			it.Path, it.Line, it.Col = splitContextFileLoc(m.Value)
		}
		items = append(items, it)
	}
	a.indexContextItems(&pane, "", "", items)
}

// collectViewNodeText walks a ui.page ViewNode tree and gathers its readable
// text for high-signal context item extraction.
func collectViewNodeText(n *ui.ViewNode, out *strings.Builder) {
	if n == nil {
		return
	}
	if n.Title != "" {
		out.WriteString(n.Title)
		out.WriteByte('\n')
	}
	if n.Props != nil {
		for _, k := range []string{"markdown", "text", "summary", "url", "filePath", "path"} {
			if s, ok := n.Props[k].(string); ok && s != "" {
				out.WriteString(s)
				out.WriteByte('\n')
			}
		}
	}
	for i := range n.Children {
		collectViewNodeText(&n.Children[i], out)
	}
}

// syncContextItems harvests context items from stored ui.page records (including
// Response pages), created documents, and current live terminal frames, caching
// by record ChangeSetNumber / frame Revision so repeated calls are fast.
func (a *App) syncContextItems() {
	if a.Store == nil {
		return
	}
	idx := a.ensureContextIndex()
	a.loadPersistedContextIndex(idx)

	rev, _ := a.Store.LatestChangeSetNumber()
	idx.mu.RLock()
	synced := idx.syncedStored && idx.syncRev == rev && idx.syncShowArch == a.nav.showArchived
	idx.mu.RUnlock()
	if !synced {
		// 1. Agent response / UI pages.
		if pages, err := a.Store.Query(data.Query{Kind: data.KindPage, IncludeArchived: a.nav.showArchived}); err == nil {
			for _, page := range pages.Items {
				idx.mu.RLock()
				seenRev, ok := idx.pageRevs[page.ID]
				idx.mu.RUnlock()
				if ok && seenRev == page.ChangeSetNumber {
					continue
				}
				var sb strings.Builder
				if root, err := mcpui.PageRoot(page); err == nil {
					collectViewNodeText(&root, &sb)
				}
				var pane *data.Record
				if paneRef, hasPane := page.RefField("pane"); hasPane {
					if rec, err := a.Store.Get(paneRef); err == nil {
						pane = rec
					}
				}
				projID, projTitle := "", ""
				if pane == nil {
					if taskRef, hasTask := page.RefField("task"); hasTask {
						if rec, err := a.Store.Get(taskRef); err == nil {
							var ref core.Ref
							projTitle, ref = a.resolveTaskRoot(*rec)
							projID = ref.ID
						}
					}
				}
				if text := sb.String(); text != "" {
					a.indexContextItems(pane, projID, projTitle, contextitems.Extract(text, contextitems.Options{Source: contextitems.SourceResponse}))
				}
				idx.mu.Lock()
				idx.pageRevs[page.ID] = page.ChangeSetNumber
				idx.mu.Unlock()
			}
		}
		// 2. Documents with filePath / path.
		if docs, err := a.Store.Query(data.Query{Kind: data.KindDocument, IncludeArchived: a.nav.showArchived}); err == nil {
			for _, doc := range docs.Items {
				idx.mu.RLock()
				seenRev, ok := idx.docRevs[doc.ID]
				idx.mu.RUnlock()
				if ok && seenRev == doc.ChangeSetNumber {
					continue
				}
				fp := strings.TrimSpace(doc.Text("filePath"))
				if fp == "" {
					fp = strings.TrimSpace(doc.Text("path"))
				}
				if fp != "" {
					projID, projTitle := "", unassignedProjectLabel
					if taskRef, hasTask := doc.RefField("task"); hasTask {
						if rec, err := a.Store.Get(taskRef); err == nil {
							var root core.Ref
							projTitle, root = a.resolveTaskRoot(*rec)
							projID = root.ID
						}
					}
					a.indexContextItems(nil, projID, projTitle, []contextitems.Item{{
						Kind: contextitems.KindFile, Value: fp, Path: fp,
						Label: views.Title(a.schema(data.KindDocument), doc), Source: contextitems.SourceCreatedFile,
					}})
				}
				idx.mu.Lock()
				idx.docRevs[doc.ID] = doc.ChangeSetNumber
				idx.mu.Unlock()
			}
		}
		postRev, _ := a.Store.LatestChangeSetNumber()
		idx.mu.Lock()
		idx.syncRev, idx.syncShowArch, idx.syncedStored = postRev, a.nav.showArchived, true
		idx.mu.Unlock()
	}

	// 3. Cached live terminal screen frames (skip SQLite Get when frame already harvested).
	now := a.now()
	for paneID, frame := range a.rt.live.frames {
		if len(frame.Lines) == 0 {
			continue
		}
		idx.mu.RLock()
		lastRev, lastAt := idx.screenRevs[paneID], idx.screenHarvestAt[paneID]
		idx.mu.RUnlock()
		if (frame.Revision > 0 && lastRev == frame.Revision) || (a.opts.Live && !lastAt.IsZero() && now.Sub(lastAt) < liveScreenHarvestInterval) {
			continue
		}
		if pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID}); err == nil {
			a.harvestScreenLines(*pane, frame.Lines, frame.Cols, frame.Revision)
		}
	}
}

func (a *App) resolveTaskRoot(task data.Record) (string, core.Ref) {
	cur := &task
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
	return views.Title(a.schema(data.KindTask), *cur), cur.Ref()
}

func sortCollectedContext(items []CollectedContextItem) {
	boolCmp := func(a, b bool) int {
		if a == b {
			return 0
		}
		if a {
			return -1
		}
		return 1
	}
	slices.SortStableFunc(items, func(x, y CollectedContextItem) int {
		if c := boolCmp(x.Official, y.Official); c != 0 {
			return c
		}
		if c := boolCmp(x.Pinned, y.Pinned); c != 0 {
			return c
		}
		if c := boolCmp(x.Starred, y.Starred); c != 0 {
			return c
		}
		if c := y.SeenAt.Compare(x.SeenAt); c != 0 {
			return c
		}
		return strings.Compare(x.Value, y.Value)
	})
}

func (a *App) paneContextItems(paneID string) []CollectedContextItem {
	a.syncContextItems()
	idx := a.ensureContextIndex()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var out []CollectedContextItem
	for _, it := range idx.items {
		if it.PaneID == paneID {
			cp := *it
			cp.Starred = idx.starred[cp.CanonKey]
			out = append(out, cp)
		}
	}
	sortCollectedContext(out)
	return out
}

func mergeCollectedByCanon(byCanon map[string]CollectedContextItem, cp CollectedContextItem) {
	if existing, dup := byCanon[cp.CanonKey]; !dup || (cp.Official && !existing.Official) || (cp.Official == existing.Official && cp.SeenAt.After(existing.SeenAt)) {
		if dup {
			if cp.RecordID == "" {
				cp.RecordID = existing.RecordID
			}
			cp.Pinned = cp.Pinned || existing.Pinned
			cp.Starred = cp.Starred || existing.Starred
			if cp.Notes == "" {
				cp.Notes = existing.Notes
			}
		}
		byCanon[cp.CanonKey] = cp
	}
}

func (a *App) projectContextItems(groupName string, projRef *core.Ref) []CollectedContextItem {
	a.syncContextItems()
	paneSet := map[string]bool{}
	for _, p := range a.panes.railGroupPanes[groupName] {
		paneSet[p.ID] = true
	}
	idx := a.ensureContextIndex()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	byCanon := map[string]CollectedContextItem{}
	for _, it := range idx.items {
		if !((it.PaneID != "" && paneSet[it.PaneID]) || (projRef != nil && it.ProjectID == projRef.ID) || (projRef == nil && it.Project == groupName)) {
			continue
		}
		cp := *it
		cp.Starred = cp.Starred || idx.starred[cp.CanonKey]
		mergeCollectedByCanon(byCanon, cp)
	}
	out := make([]CollectedContextItem, 0, len(byCanon))
	for _, it := range byCanon {
		out = append(out, it)
	}
	sortCollectedContext(out)
	return out
}

func (a *App) allContextItems() []CollectedContextItem {
	a.syncContextItems()
	idx := a.ensureContextIndex()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	byCanon := map[string]CollectedContextItem{}
	for _, it := range idx.items {
		cp := *it
		cp.Starred = cp.Starred || idx.starred[cp.CanonKey]
		mergeCollectedByCanon(byCanon, cp)
	}
	out := make([]CollectedContextItem, 0, len(byCanon))
	for _, it := range byCanon {
		out = append(out, it)
	}
	sortCollectedContext(out)
	return out
}

func (a *App) toggleContextStar(kind, value string) bool {
	idx := a.ensureContextIndex()
	canon := contextCanonKey(kind, value)
	if canon == "" {
		return false
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	next := !idx.starred[canon]
	if next {
		idx.starred[canon] = true
	} else {
		delete(idx.starred, canon)
	}
	for _, it := range idx.items {
		if it.CanonKey == canon {
			it.Starred = next
		}
	}
	idx.gen++
	return next
}

// contextTargetPath converts a harvested context item value into the path or
// URL understood by handlers.Registry.
func contextTargetPath(kind, value string, pane *data.Record) string {
	val := strings.TrimSpace(value)
	switch kind {
	case contextitems.KindPR:
		// owner/repo#123 -> https://github.com/owner/repo/pull/123
		if repo, num, ok := strings.Cut(val, "#"); ok && repo != "" && num != "" {
			return "https://github.com/" + repo + "/pull/" + num
		}
		return val
	case contextitems.KindURL, contextitems.KindOSC8:
		return val
	case contextitems.KindFile:
		base, _, _ := splitContextFileLoc(val)
		p := expandHome(base)
		if !filepath.IsAbs(p) && pane != nil {
			if cwd := expandHome(pane.Text("cwd")); cwd != "" {
				p = filepath.Join(cwd, p)
			}
		}
		return p
	default:
		if handlers.IsURL(val) {
			return val
		}
		return expandHome(val)
	}
}

// systemOpenTarget opens target directly via the OS opener (`open` on macOS,
// `xdg-open` elsewhere).
func (a *App) systemOpenTarget(s core.State, target string) core.StepResult {
	target = strings.TrimSpace(target)
	if target == "" {
		a.say("empty context target")
		return core.Next(s)
	}
	return core.Next(a.startShell(s, "open "+target, systemOpener()+" "+handlers.Quote(target), ""))
}

// revealContextValue reveals a local path in the OS file manager (`open -R` on
// macOS, opening the parent directory elsewhere), falling back to opening URLs.
func (a *App) revealContextValue(s core.State, kind, value string, pane *data.Record) core.StepResult {
	target := contextTargetPath(kind, value, pane)
	if target == "" {
		a.say("empty context target")
		return core.Next(s)
	}
	if handlers.IsURL(target) {
		return a.systemOpenTarget(s, target)
	}
	cmd := systemOpener() + " " + handlers.Quote(filepath.Dir(target))
	if runtime.GOOS == "darwin" {
		cmd = "open -R " + handlers.Quote(target)
	}
	return core.Next(a.startShell(s, "reveal "+target, cmd, ""))
}

// openContextValue opens a context item target through App.Handlers, falling
// back to the OS opener when no explicit handler rule matches.
func (a *App) openContextValue(s core.State, kind, value string, pane *data.Record) core.StepResult {
	target := contextTargetPath(kind, value, pane)
	if target == "" {
		a.say("empty context target")
		return core.Next(s)
	}
	rules := a.cfg.Handlers.Match(target)
	if len(rules) == 0 {
		return a.systemOpenTarget(s, target)
	}
	var from *core.Ref
	if pane != nil {
		r := pane.Ref()
		from = &r
	}
	return core.Next(a.runHandler(s, rules[0], openTarget{
		path:  target,
		title: value,
		from:  from,
		rules: rules,
	}))
}

// attachContextItem attaches item to target via data.RelContext.
func (a *App) attachContextItem(s core.State, item CollectedContextItem, target *core.Ref) core.State {
	if target == nil || target.ID == "" {
		a.say("no focused task or pane to attach context to")
		return s
	}
	ctxRef := core.Ref{Kind: "context." + item.Kind, ID: item.Value}
	meta := map[string]any{"kind": item.Kind, "source": item.Source}
	if item.Label != "" {
		meta["label"] = item.Label
	}
	_, cs, err := a.Store.Relate(data.RelateInput{
		From: *target,
		Kind: data.RelContext,
		To:   ctxRef,
		Meta: meta,
	}, User)
	a.wrote(fmt.Sprintf("attached %s to %s", clip(item.Value, 36), a.title(*target)), cs, err)
	return s
}

// contextPick opens the searchable Context Picker over all collected context
// items (enter opens via handlers, y yanks, a attaches via RelContext).
func (a *App) contextPick(s core.State, c core.Command) core.StepResult {
	all := a.allContextItems()
	var target *core.Ref
	if ref, ok := a.subject(s, c); ok {
		r := ref
		target = &r
	} else if _, projRef, onProj := a.selectedRailProject(); onProj && projRef != nil {
		r := *projRef
		target = &r
	} else if pane, ok := a.SelectedPane(); ok {
		r := pane.Ref()
		target = &r
	}
	if len(all) == 0 {
		a.say("no context items collected yet")
		return core.Next(s)
	}
	items := make([]picker.Item, len(all))
	for i, it := range all {
		var subParts []string
		for _, s := range []string{it.Label, it.PaneTitle, it.Project} {
			if s != "" && s != it.Value {
				subParts = append(subParts, s)
			}
		}
		badges := []ui.Badge{{Text: it.Kind, Tone: "accent"}}
		if it.Source != "" {
			badges = append(badges, ui.Badge{Text: it.Source, Tone: "muted"})
		}
		if it.Starred {
			badges = append(badges, ui.Badge{Text: "★", Tone: "warning"})
		}
		items[i] = picker.Item{
			ID: it.Key, Title: it.Value, Subtitle: strings.Join(subParts, " · "), Badges: badges,
			Keywords: []string{it.Kind, it.Source, it.PaneTitle, it.Project, it.Label, it.Path}, Data: it,
		}
	}
	pm := picker.New("Context Items", items)
	pm.Placeholder = "search URLs, files, PRs · enter open · y yank · a attach"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "context",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			if it, ok := picked.Data.(CollectedContextItem); ok {
				var pane *data.Record
				if it.PaneID != "" {
					if rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: it.PaneID}); err == nil {
						pane = rec
					}
				}
				a.afterModal(func(st core.State) core.State {
					return a.openContextValue(st, it.Kind, it.Value, pane).State
				})
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
		OnCommand: func(p *picker.Model, cmd core.Command) (modal.Result, bool) {
			if cmd.ID != CmdContextYank && cmd.ID != CmdContextAttach {
				return modal.Result{}, false
			}
			picked, ok := p.Focused()
			if !ok {
				return modal.Result{Handled: true, Status: "no context item matches", SetStatus: true}, true
			}
			it, ok := picked.Data.(CollectedContextItem)
			if !ok {
				return modal.Result{Handled: true}, true
			}
			switch cmd.ID {
			case CmdContextYank:
				a.yank(it.Value, it.Kind)
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
			case CmdContextAttach:
				tgt := target
				if tgt == nil && it.PaneID != "" {
					r := core.Ref{Kind: data.KindPane, ID: it.PaneID}
					tgt = &r
				}
				a.afterModal(func(st core.State) core.State {
					return a.attachContextItem(st, it, tgt)
				})
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
			}
			return modal.Result{}, false
		},
	})
	return core.Next(a.pushModal(s, m))
}
