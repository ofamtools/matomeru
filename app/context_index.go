package app

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// CollectedContextItem is one deduplicated high-signal context item harvested
// from final agent responses, pages, documents, or shell terminal text,
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
	Language  string
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
// from final agent responses, agent pages, documents, and shell terminal text.
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
	perf            *projectPerfCache
	// paneTitles and tasks are what the stored-record merge needs of the other
	// kinds, kept from the last full load so a delta merge reads only what changed.
	paneTitles map[string]string
	tasks      map[string]contextTaskShape
}

func newContextController() *ContextController {
	return &ContextController{
		items: map[string]*CollectedContextItem{}, starred: map[string]bool{}, persisted: map[string]bool{},
		pageRevs: map[string]int64{}, docRevs: map[string]int64{}, screenRevs: map[string]uint64{}, screenHarvestAt: map[string]time.Time{},
	}
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
	case contextitems.KindCode:
		return "code"
	case "doc":
		return "doc"
	default:
		return "url"
	}
}

func (a *App) contextExtractOptions(source string, wrapWidth int) contextitems.Options {
	return contextitems.Options{
		Source:            source,
		WrapWidth:         wrapWidth,
		DisableCodeBlocks: !a.extractCodeBlocksEnabled(),
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

	changed := false
	allowCode := a.extractCodeBlocksEnabled()
	idx.mu.Lock()
	for _, it := range extracted {
		if it.Kind == contextitems.KindCode {
			if !allowCode {
				continue
			}
			it.Value = panesearch.MaskSecrets(it.Value)
			if strings.TrimSpace(it.Label) == "" {
				it.Label = data.CodeContextTitle(it.Value, it.Language)
			}
		}
		canon := contextCanonKey(it.Kind, it.Value)
		if canon == "" {
			continue
		}
		key := paneID + "\x00" + canon
		if cur, exists := idx.items[key]; exists {
			if cur.Label == "" && it.Label != "" {
				cur.Label = it.Label
				changed = true
			}
			if cur.Language == "" && it.Language != "" {
				cur.Language = it.Language
				changed = true
			}
			if cur.Line == 0 && it.Line > 0 {
				cur.Line, cur.Col, cur.Value = it.Line, it.Col, it.Value
				changed = true
			}
			if cur.Path == "" && it.Path != "" {
				cur.Path = it.Path
				changed = true
			}
			if cur.PaneTitle != paneTitle {
				cur.PaneTitle = paneTitle
				changed = true
			}
			if cur.ProjectID != projID {
				cur.ProjectID = projID
				changed = true
			}
			if cur.Project != projTitle {
				cur.Project = projTitle
				changed = true
			}
			if nextStar := cur.Starred || idx.starred[canon]; cur.Starred != nextStar {
				cur.Starred = nextStar
				changed = true
			}
			cur.SeenAt = now
			continue
		}
		p := it.Path
		if p == "" && it.Kind == contextitems.KindFile {
			p, _, _ = splitContextFileLoc(it.Value)
		}
		collected := CollectedContextItem{
			Key: key, CanonKey: canon, Kind: it.Kind, Value: it.Value, Path: p,
			Line: it.Line, Col: it.Col, Label: it.Label, Language: it.Language, Source: it.Source,
			PaneID: paneID, PaneTitle: paneTitle, ProjectID: projID, Project: projTitle,
			Starred: idx.starred[canon], SeenAt: now,
		}
		idx.items[key] = &collected
		changed = true
		if !idx.persisted[key] {
			idx.persisted[key] = true
			toPersist = append(toPersist, collected)
		}
	}
	if changed {
		idx.gen++
	}
	idx.mu.Unlock()

	if a.Store == nil || len(toPersist) == 0 {
		return
	}
	beforeRev, _ := a.Store.LatestChangeSetNumber()
	persistedN := 0
	for _, item := range toPersist {
		title := strings.TrimSpace(item.Label)
		if title == "" {
			if item.Kind == contextitems.KindCode {
				title = data.CodeContextTitle(item.Value, item.Language)
			} else {
				title = item.Value
			}
		}
		fields := map[string]any{
			"title": title, "value": item.Value, "contextKind": mapContextRecordKind(item.Kind),
			"source": mapContextRecordSource(item.Source), "canonKey": item.CanonKey,
		}
		if item.Language != "" {
			fields["language"] = item.Language
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

func stripInjectedPromptSections(text string) string {
	for _, marker := range []string{"\n\n## Official Project Context\n", "\n## Official Project Context\n", "\n\n[Attachments]\n"} {
		if idx := strings.Index(text, marker); idx >= 0 {
			text = text[:idx]
		}
	}
	return text
}

// Agent panes publish verified answers through their hooks. Raw terminal text
// cannot distinguish those answers from progress, reasoning, or tool output.
func (a *App) canHarvestTerminalContext(pane data.Record) bool {
	name := strings.TrimSpace(pane.Text("harness"))
	entry, ok := a.lookupHarness(name)
	return name == "" || ok && entry.Shell
}

func (a *App) harvestPrompt(pane data.Record, text string) {
	if !a.canHarvestTerminalContext(pane) {
		return
	}
	text = stripInjectedPromptSections(text)
	if strings.TrimSpace(text) == "" {
		return
	}
	items := contextitems.Extract(text, a.contextExtractOptions(contextitems.SourcePrompt, 0))
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

	if !a.canHarvestTerminalContext(pane) {
		return
	}

	extractOpts := a.contextExtractOptions(contextitems.SourceScreen, wrapWidth)
	if a.opts.Live {
		paneCopy := pane
		linesCopy := slices.Clone(lines)
		a.background(func(_ context.Context) func(core.State) core.State {
			items := contextitems.ExtractLines(linesCopy, extractOpts)
			if len(items) > 0 {
				a.indexContextItems(&paneCopy, "", "", items)
			}
			return nil
		})
		return
	}

	items := contextitems.ExtractLines(lines, extractOpts)
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
					a.indexContextItems(pane, projID, projTitle, contextitems.Extract(text, a.contextExtractOptions(contextitems.SourceResponse, 0)))
				}
				idx.mu.Lock()
				idx.pageRevs[page.ID] = page.ChangeSetNumber
				idx.mu.Unlock()
			}
		}
		// 2. Documents with filePath / path.
		if docs, err := a.Store.Query(data.Query{Kind: data.KindDocument, IncludeArchived: a.nav.showArchived}); err == nil {
			for _, doc := range docs.Items {
				if doc.Text("source") == data.DocumentSourceTool {
					continue // The file was registered as a document by a tool hook.
				}
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

func (a *App) projectContextItems(groupName string, projRef *core.Ref, paneSet map[string]bool) []CollectedContextItem {
	a.syncContextItems()
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

func (a *App) isContextStarred(canon string) bool {
	if canon == "" || a == nil || a.ctx == nil {
		return false
	}
	a.ctx.mu.RLock()
	defer a.ctx.mu.RUnlock()
	return a.ctx.starred[canon]
}

func (a *App) setContextStar(itemCanon, kind, value string, next bool) bool {
	idx := a.ensureContextIndex()
	computed := contextCanonKey(kind, value)
	if itemCanon == "" {
		itemCanon = computed
	}
	if itemCanon == "" && computed == "" {
		return false
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	keys := []string{}
	if itemCanon != "" {
		keys = append(keys, itemCanon)
	}
	if computed != "" && computed != itemCanon {
		keys = append(keys, computed)
	}
	for _, k := range keys {
		if next {
			idx.starred[k] = true
		} else {
			delete(idx.starred, k)
		}
	}
	for _, it := range idx.items {
		itComputed := contextCanonKey(it.Kind, it.Value)
		for _, k := range keys {
			if it.CanonKey == k || (itComputed != "" && itComputed == k) {
				it.Starred = next
				break
			}
		}
	}
	idx.gen++
	return next
}

func (a *App) toggleContextStar(kind, value string) bool {
	idx := a.ensureContextIndex()
	canon := contextCanonKey(kind, value)
	if canon == "" {
		return false
	}
	idx.mu.RLock()
	cur := idx.starred[canon]
	idx.mu.RUnlock()
	return a.setContextStar(canon, kind, value, !cur)
}

// contextTargetPath converts a harvested context item value into the path or
// URL understood by handlers.Registry. Code snippets have no filesystem path
// or URL and return "".
func contextTargetPath(kind, value string, pane *data.Record) string {
	if kind == contextitems.KindCode {
		return ""
	}
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
		p := cleanLocalTargetPath(base)
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
		return cleanLocalTargetPath(val)
	}
}

func (a *App) openCodeContext(s core.State, title, code, language string, from *core.Ref) core.StepResult {
	if strings.TrimSpace(code) == "" {
		a.say("empty code snippet")
		return core.Next(s)
	}
	if strings.TrimSpace(title) == "" {
		title = data.CodeContextTitle(code, language)
	}
	return core.Next(a.openReader(s, openTarget{
		title: title,
		body:  data.FormatCodeMarkdownBlock(code, language),
		from:  from,
	}))
}

func (a *App) openCollectedContextItem(s core.State, it CollectedContextItem, pane *data.Record) core.StepResult {
	if it.Kind == contextitems.KindCode {
		var from *core.Ref
		if it.RecordID != "" {
			r := core.Ref{Kind: data.KindContext, ID: it.RecordID}
			from = &r
		} else if pane != nil {
			r := pane.Ref()
			from = &r
		}
		return a.openCodeContext(s, it.Label, it.Value, it.Language, from)
	}
	return a.openContextValue(s, it.Kind, it.Value, pane)
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
// Code snippets open inline in the Markdown reader instead of calling the OS opener.
func (a *App) revealContextValue(s core.State, kind, value string, pane *data.Record) core.StepResult {
	if kind == contextitems.KindCode {
		var from *core.Ref
		if pane != nil {
			r := pane.Ref()
			from = &r
		}
		return a.openCodeContext(s, "", value, "", from)
	}
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
// back to the OS opener when no explicit handler rule matches. Code snippets
// always open inline in the built-in Markdown reader.
func (a *App) openContextValue(s core.State, kind, value string, pane *data.Record) core.StepResult {
	if kind == contextitems.KindCode {
		var from *core.Ref
		if pane != nil {
			r := pane.Ref()
			from = &r
		}
		return a.openCodeContext(s, "", value, "", from)
	}
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
		title := it.Value
		if it.Kind == contextitems.KindCode {
			title = strings.TrimSpace(it.Label)
			if title == "" {
				title = data.CodeContextTitle(it.Value, it.Language)
			}
			title = singleLineContextText(title)
		}
		var subParts []string
		for _, s := range []string{it.Label, it.PaneTitle, it.Project} {
			s = singleLineContextText(s)
			if s != "" && s != title && s != it.Value {
				subParts = append(subParts, s)
			}
		}
		badges := []ui.Badge{{Text: it.Kind, Tone: "accent"}}
		if it.Language != "" {
			badges = append(badges, ui.Badge{Text: it.Language, Tone: "info"})
		}
		if it.Source != "" {
			badges = append(badges, ui.Badge{Text: it.Source, Tone: "muted"})
		}
		if it.Starred {
			badges = append(badges, ui.Badge{Text: "★", Tone: "warning"})
		}
		items[i] = picker.Item{
			ID: it.Key, Title: title, Subtitle: strings.Join(subParts, " · "), Badges: badges,
			Keywords: []string{it.Kind, it.Language, it.Source, it.PaneTitle, it.Project, it.Label, it.Path, it.Value}, Data: it,
		}
	}
	pm := picker.New("Context Items", items)
	pm.Placeholder = "search URLs, files, PRs, code · enter open · y yank · a attach"
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
					return a.openCollectedContextItem(st, it, pane).State
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
