package app

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

const maxCachedPreviewFiles = 64

type cachedFileEntry struct {
	modTime int64
	size    int64
	content string
}

// projectPerfCache holds memoized Project Page section table/tree cache keys
// and a bounded (path, mtime, size) preview file cache scoped to one App.
type projectPerfCache struct {
	sectionTableKeys map[string]string
	sectionTreeKeys  map[string]string

	fileMu    sync.Mutex
	files     map[string]cachedFileEntry
	fileOrder []string
}

func (a *App) perfCache() *projectPerfCache {
	idx := a.ensureContextIndex()
	if idx.perf == nil {
		idx.perf = &projectPerfCache{
			sectionTableKeys: map[string]string{},
			sectionTreeKeys:  map[string]string{},
		}
	}
	return idx.perf
}

func (a *App) clearProjectSectionCache() {
	if a == nil || a.ctx == nil || a.ctx.perf == nil {
		return
	}
	clear(a.ctx.perf.sectionTableKeys)
	clear(a.ctx.perf.sectionTreeKeys)
}

func (a *App) cachedProjectTable(tableID, key string) (*table.Model, bool) {
	if key == "" {
		return nil, false
	}
	cache := a.perfCache()
	if cache.sectionTableKeys[tableID] != key {
		return nil, false
	}
	pc := a.projectCtrl()
	tbl := pc.tables[tableID]
	if tbl == nil {
		return nil, false
	}
	if navTbl := a.nav.tables[tableID]; navTbl != nil && navTbl != tbl {
		return nil, false
	}
	a.pageSize(tbl, tableID)
	a.nav.tables[tableID] = tbl
	return tbl, true
}

func (a *App) storeProjectTableKey(tableID, key string) {
	if key == "" {
		return
	}
	a.perfCache().sectionTableKeys[tableID] = key
}

func (a *App) invalidateProjectTableCache(tableID string) {
	if a == nil || a.ctx == nil || a.ctx.perf == nil || tableID == "" {
		return
	}
	delete(a.ctx.perf.sectionTableKeys, tableID)
}

func (a *App) cachedProjectTree(treeID, key string) (*tree.Model, bool) {
	if key == "" {
		return nil, false
	}
	cache := a.perfCache()
	if cache.sectionTreeKeys[treeID] != key {
		return nil, false
	}
	pc := a.projectCtrl()
	tm := pc.trees[treeID]
	if tm == nil {
		return nil, false
	}
	tm.ScrollOff, tm.PageSize = a.scrollOff(), a.treeViewportHeight(treeID)
	return tm, true
}

func (a *App) storeProjectTreeKey(treeID, key string) {
	if key == "" {
		return
	}
	a.perfCache().sectionTreeKeys[treeID] = key
}

// readFileCached reads path through a bounded (path, mtime, size) cache so
// previewing or stepping across Markdown files in project.nav avoids redundant
// synchronous os.ReadFile calls while immediately seeing on-disk edits.
func (a *App) readFileCached(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		if a.opts.ReadFile != nil {
			return a.readFile(path)
		}
		return "", statErr
	}
	modNano := info.ModTime().UnixNano()
	size := info.Size()

	cache := a.perfCache()
	cache.fileMu.Lock()
	if entry, ok := cache.files[path]; ok && entry.modTime == modNano && entry.size == size {
		cache.fileMu.Unlock()
		return entry.content, nil
	}
	cache.fileMu.Unlock()

	content, err := a.readFile(path)
	if err != nil {
		return "", err
	}
	cache.fileMu.Lock()
	if cache.files == nil {
		cache.files = make(map[string]cachedFileEntry, 8)
	}
	if _, exists := cache.files[path]; !exists {
		if len(cache.fileOrder) >= maxCachedPreviewFiles {
			evict := cache.fileOrder[0]
			cache.fileOrder = cache.fileOrder[1:]
			delete(cache.files, evict)
		}
		cache.fileOrder = append(cache.fileOrder, path)
	}
	cache.files[path] = cachedFileEntry{modTime: modNano, size: size, content: content}
	cache.fileMu.Unlock()
	return content, nil
}

// --- Pure context-index value helpers -------------------------------------------

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
	case contextitems.KindCode:
		return data.CodeContextCanonKey(val)
	default:
		return kind + "\x00" + val
	}
}

func singleLineContextText(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\r\n", "\n")), " ")
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

func contextItemVisibleEqual(a, b *CollectedContextItem) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.RecordID == b.RecordID &&
		a.Kind == b.Kind &&
		a.Value == b.Value &&
		a.Path == b.Path &&
		a.Line == b.Line &&
		a.Col == b.Col &&
		a.Label == b.Label &&
		a.Language == b.Language &&
		a.Source == b.Source &&
		a.PaneID == b.PaneID &&
		a.PaneTitle == b.PaneTitle &&
		a.ProjectID == b.ProjectID &&
		a.Project == b.Project &&
		a.Starred == b.Starred &&
		a.Official == b.Official &&
		a.Pinned == b.Pinned &&
		a.Notes == b.Notes
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

// contextKindsTouchedSince reports whether any change set strictly newer than
// sinceRev (up to latestRev) touched a kind that affects the persisted context
// index (KindContext, KindPane, KindTask). Returns true conservatively when the
// gap is large or History fails.
func contextKindsTouchedSince(store *data.Store, sinceRev, latestRev int64) bool {
	if store == nil || latestRev <= sinceRev {
		return false
	}
	delta := latestRev - sinceRev
	if delta > 16 {
		return true
	}
	hist, err := store.History(nil, int(delta))
	if err != nil {
		return true
	}
	for _, cs := range hist {
		if cs.Number <= sinceRev {
			break
		}
		for _, ch := range cs.Changes {
			switch ch.Ref.Kind {
			case data.KindContext, data.KindPane, data.KindTask:
				return true
			}
		}
	}
	return false
}
