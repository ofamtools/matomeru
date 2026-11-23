package app

import (
	"context"
	"fmt"
	"path/filepath"
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
	"ofam.dev/ochazuke/widgets/picker"
)

// CollectedContextItem is one deduplicated high-signal context item harvested
// from a pane's prompts, agent pages, documents, or visible terminal screen.
type CollectedContextItem struct {
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
	SeenAt    time.Time
}

// contextHarvestActor persists automatically harvested context items to SQLite
// without entering the owner's undo stack.
var contextHarvestActor = core.Actor{Kind: "system", ID: "mtm.context", Label: "context"}

type contextIndex struct {
	mu              sync.RWMutex
	items           map[string]*CollectedContextItem // paneID\x00canonKey -> item
	starred         map[string]bool                  // canonKey -> starred
	persisted       map[string]bool                  // paneID\x00canonKey -> persisted in SQLite
	loadedStored    bool
	pageRevs        map[string]int64
	docRevs         map[string]int64
	screenRevs      map[string]uint64
	screenHarvestAt map[string]time.Time
}

func newContextIndex() *contextIndex {
	return &contextIndex{
		items:           map[string]*CollectedContextItem{},
		starred:         map[string]bool{},
		persisted:       map[string]bool{},
		pageRevs:        map[string]int64{},
		docRevs:         map[string]int64{},
		screenRevs:      map[string]uint64{},
		screenHarvestAt: map[string]time.Time{},
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

func (a *App) ensureContextIndex() *contextIndex {
	if a.ctxIdx == nil {
		a.ctxIdx = newContextIndex()
	}
	return a.ctxIdx
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

func (a *App) loadPersistedContextIndex(idx *contextIndex) {
	if a.Store == nil {
		return
	}
	idx.mu.RLock()
	already := idx.loadedStored
	idx.mu.RUnlock()
	if already {
		return
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindContext, IncludeArchived: true})
	if err != nil {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.loadedStored {
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
		if _, exists := idx.items[key]; !exists && !rec.IsArchived() && val != "" {
			paneTitle := ""
			if paneID != "" {
				paneTitle = a.title(core.Ref{Kind: data.KindPane, ID: paneID})
			}
			projID, projTitle := "", unassignedProjectLabel
			if tRef, ok := rec.RefField("task"); ok {
				if taskRec, err := a.Store.Get(tRef); err == nil && taskRec != nil {
					var root core.Ref
					projTitle, root = a.resolveTaskRoot(*taskRec)
					projID = root.ID
				}
			}
			idx.items[key] = &CollectedContextItem{
				Key:       key,
				CanonKey:  canon,
				Kind:      kind,
				Value:     val,
				Label:     rec.Text("title"),
				Source:    rec.Text("source"),
				Line:      recordIntField(rec, "line"),
				Col:       recordIntField(rec, "col"),
				PaneID:    paneID,
				PaneTitle: paneTitle,
				ProjectID: projID,
				Project:   projTitle,
				Starred:   rec.IsStarred() || idx.starred[canon],
				SeenAt:    rec.UpdatedAt,
			}
		}
	}
	idx.loadedStored = true
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
			Key:       key,
			CanonKey:  canon,
			Kind:      it.Kind,
			Value:     it.Value,
			Path:      p,
			Line:      it.Line,
			Col:       it.Col,
			Label:     it.Label,
			Source:    it.Source,
			PaneID:    paneID,
			PaneTitle: paneTitle,
			ProjectID: projID,
			Project:   projTitle,
			Starred:   idx.starred[canon],
			SeenAt:    now,
		}
		idx.items[key] = &collected
		if !idx.persisted[key] {
			idx.persisted[key] = true
			toPersist = append(toPersist, collected)
		}
	}
	idx.mu.Unlock()

	if a.Store == nil || len(toPersist) == 0 {
		return
	}
	for _, item := range toPersist {
		title := strings.TrimSpace(item.Label)
		if title == "" {
			title = item.Value
		}
		fields := map[string]any{
			"title":       title,
			"value":       item.Value,
			"contextKind": mapContextRecordKind(item.Kind),
			"source":      mapContextRecordSource(item.Source),
			"canonKey":    item.CanonKey,
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
		_, _, _ = a.Store.Create(data.CreateInput{Kind: data.KindContext, Fields: fields}, contextHarvestActor)
	}
}

// harvestPrompt extracts and indexes high-signal context items from a user
// prompt sent to pane.
func (a *App) harvestPrompt(pane data.Record, text string) {
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

	// 1. Agent response / UI pages.
	if pages, err := a.Store.Query(data.Query{Kind: data.KindPage, IncludeArchived: a.showArchived}); err == nil {
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
						projTitle, ref := a.resolveTaskRoot(*rec)
						projID = ref.ID
						_ = projTitle
					}
				}
			}
			if text := sb.String(); text != "" {
				extracted := contextitems.Extract(text, contextitems.Options{Source: contextitems.SourceResponse})
				a.indexContextItems(pane, projID, projTitle, extracted)
			}
			idx.mu.Lock()
			idx.pageRevs[page.ID] = page.ChangeSetNumber
			idx.mu.Unlock()
		}
	}

	// 2. Documents with filePath / path.
	if docs, err := a.Store.Query(data.Query{Kind: data.KindDocument, IncludeArchived: a.showArchived}); err == nil {
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
					Kind:   contextitems.KindFile,
					Value:  fp,
					Path:   fp,
					Label:  views.Title(a.schema(data.KindDocument), doc),
					Source: contextitems.SourceCreatedFile,
				}})
			}
			idx.mu.Lock()
			idx.docRevs[doc.ID] = doc.ChangeSetNumber
			idx.mu.Unlock()
		}
	}

	// 3. Cached live terminal screen frames.
	for paneID, frame := range a.live.frames {
		if len(frame.Lines) == 0 {
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
	slices.SortStableFunc(items, func(x, y CollectedContextItem) int {
		if x.Starred != y.Starred {
			if x.Starred {
				return -1
			}
			return 1
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

func (a *App) projectContextItems(groupName string, projRef *core.Ref) []CollectedContextItem {
	a.syncContextItems()
	paneSet := map[string]bool{}
	for _, p := range a.railGroupPanes[groupName] {
		paneSet[p.ID] = true
	}
	idx := a.ensureContextIndex()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	byCanon := map[string]CollectedContextItem{}
	for _, it := range idx.items {
		match := false
		if it.PaneID != "" && paneSet[it.PaneID] {
			match = true
		} else if projRef != nil && it.ProjectID == projRef.ID {
			match = true
		} else if projRef == nil && it.Project == groupName {
			match = true
		}
		if !match {
			continue
		}
		cp := *it
		cp.Starred = idx.starred[cp.CanonKey]
		if existing, dup := byCanon[cp.CanonKey]; !dup || cp.SeenAt.After(existing.SeenAt) {
			byCanon[cp.CanonKey] = cp
		}
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
		cp.Starred = idx.starred[cp.CanonKey]
		if existing, dup := byCanon[cp.CanonKey]; !dup || cp.SeenAt.After(existing.SeenAt) {
			byCanon[cp.CanonKey] = cp
		}
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

// openContextValue opens a context item target through App.Handlers.
func (a *App) openContextValue(s core.State, kind, value string, pane *data.Record) core.StepResult {
	target := contextTargetPath(kind, value, pane)
	if target == "" {
		a.say("empty context target")
		return core.Next(s)
	}
	rules := a.Handlers.Match(target)
	if len(rules) == 0 {
		a.say("no handler opens %s", target)
		return core.Next(s)
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
	a.contextTarget = target

	if len(all) == 0 {
		a.say("no context items collected yet")
		return core.Next(s)
	}
	items := make([]picker.Item, len(all))
	for i, it := range all {
		subParts := []string{}
		if it.Label != "" && it.Label != it.Value {
			subParts = append(subParts, it.Label)
		}
		if it.PaneTitle != "" {
			subParts = append(subParts, it.PaneTitle)
		}
		if it.Project != "" {
			subParts = append(subParts, it.Project)
		}
		badges := []ui.Badge{{Text: it.Kind, Tone: "accent"}}
		if it.Source != "" {
			badges = append(badges, ui.Badge{Text: it.Source, Tone: "muted"})
		}
		if it.Starred {
			badges = append(badges, ui.Badge{Text: "★", Tone: "warning"})
		}
		items[i] = picker.Item{
			ID:       it.Key,
			Title:    it.Value,
			Subtitle: strings.Join(subParts, " · "),
			Badges:   badges,
			Keywords: []string{it.Kind, it.Source, it.PaneTitle, it.Project, it.Label, it.Path},
			Data:     it,
		}
	}
	pm := picker.New("Context Items", items)
	pm.Placeholder = "search URLs, files, PRs · enter open · y yank · a attach"
	return core.Next(a.openPicker(s, modalContext, pm))
}

func (a *App) contextPickerAction(s core.State, action string) core.StepResult {
	if topModal(s) != modalContext {
		return core.Next(s)
	}
	pm := a.pickers[modalContext]
	if pm == nil {
		return core.Next(s)
	}
	picked, ok := pm.Focused()
	if !ok {
		a.say("no context item matches")
		return core.Next(s)
	}
	it, ok := picked.Data.(CollectedContextItem)
	if !ok {
		return core.Next(s)
	}
	s = closeModal(s, modalContext)
	target := a.contextTarget
	a.contextTarget = nil
	switch action {
	case "yank":
		a.yank(it.Value, it.Kind)
		return core.Next(s)
	case "attach":
		if target == nil && it.PaneID != "" {
			r := core.Ref{Kind: data.KindPane, ID: it.PaneID}
			target = &r
		}
		return core.Next(a.attachContextItem(s, it, target))
	default: // open
		var pane *data.Record
		if it.PaneID != "" {
			if rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: it.PaneID}); err == nil {
				pane = rec
			}
		}
		return a.openContextValue(s, it.Kind, it.Value, pane)
	}
}
