package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// Discovery: the family tools mtm could mount. Every executable o-NAME on
// PATH (the first on PATH for each name wins, as oden's command discovery
// finds them) is asked `catalog`, and only that; a tool that does not
// answer a valid catalog in extDiscoverTimeout is skipped. The answers are
// merged with the registry into one row per name and a status:
//
//	available   answers catalog, not registered
//	registered  registered, and its bin answers the stored catalog
//	stale       registered, but its bin answers another catalog (or none)
//	missing     registered, and its bin no longer exists
//
// A registered extension's own bin is asked, wherever it is (--bin may name
// one off PATH). mtm itself and the tools that are hosts (nabe, o-tui) are
// never listed. Discovery runs off the loop.

// Extension statuses.
const (
	statusAvailable  = "available"
	statusRegistered = "registered"
	statusStale      = "stale"
	statusMissing    = "missing"
)

// extDiscoverTimeout bounds each tool's catalog answer during discovery.
const extDiscoverTimeout = 3 * time.Second

// extDiscoverWorkers is how many tools are asked at once.
const extDiscoverWorkers = 8

// extHosts are the o- tools that host extensions themselves: never one.
var extHosts = []string{"mtm", "matomeru", "nabe", "tui"}

// extCandidate is one row of discovery: a tool on PATH, a registered
// extension, or both.
type extCandidate struct {
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	Bin          string    `json:"bin"`
	Version      string    `json:"version,omitempty"`
	Tabs         int       `json:"tabs"`
	Nouns        int       `json:"nouns"`
	Verbs        int       `json:"verbs"`
	RegisteredAt time.Time `json:"registeredAt,omitzero"`
	// Changes says what differs between the stored catalog and the bin's
	// current one (stale only).
	Changes []string `json:"changes,omitempty"`

	// catalog is the bin's current catalog (the stored one when missing or
	// when the bin did not answer).
	catalog ocli.CatalogDoc
	// stored is the registered catalog, nil when not registered.
	stored *ocli.CatalogDoc
}

// extDiscovery is what one discovery reads; the App fills it on the loop
// and run reads nothing else.
type extDiscovery struct {
	path    string // the PATH to search
	self    string // mtm's own executable, never listed
	reg     extRegistry
	env     func(name string) []string
	timeout time.Duration
}

// discovery is the App's discovery: its PATH, its registry, its env.
func (a *App) discovery() extDiscovery {
	self, _ := os.Executable()
	if self != "" {
		self = realPath(self)
	}
	dataDir := a.dataDir()
	return extDiscovery{
		path: a.getenv("PATH"),
		self: self,
		reg:  a.extRegistry(),
		env: func(name string) []string {
			return []string{"MTM_DATA=" + dataDir, "MTM_EXTENSION=" + name}
		},
		timeout: extDiscoverTimeout,
	}
}

// answer is one bin's catalog answer.
type extAnswer struct {
	doc ocli.CatalogDoc
	err error
}

// run discovers the rows, sorted by name.
func (d extDiscovery) run(ctx context.Context) ([]extCandidate, error) {
	found, _ := oden.Discover("o", d.path, oden.OS)
	onPath := map[string]string{} // name -> bin
	for _, ext := range found {
		if !store.ValidExtensionName(ext.Name) || slices.Contains(extHosts, ext.Name) || d.isSelf(ext.Path) {
			continue
		}
		onPath[ext.Name] = ext.Path
	}
	// A registry file that does not read is a notice of the mounting,
	// not a row.
	names, entries, _ := d.reg.Ordered()
	// The bins to ask: every tool on PATH, and every registered bin that
	// still exists.
	ask := map[string]string{} // bin -> name it is asked as
	for name, bin := range onPath {
		ask[bin] = name
	}
	missing := map[string]bool{}
	registeredBins := map[string]bool{}
	for _, name := range names {
		bin := entries[name].Bin
		registeredBins[realPath(bin)] = true
		if !oden.Executable(oden.OS, bin) {
			missing[name] = true
			continue
		}
		ask[bin] = name
	}
	answers := d.askAll(ctx, ask)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := map[string]extCandidate{}
	for _, name := range names {
		e := entries[name]
		stored := e.Catalog
		c := extCandidate{Name: name, Bin: e.Bin, RegisteredAt: e.RegisteredAt, catalog: stored, stored: &stored}
		switch ans := answers[e.Bin]; {
		case missing[name]:
			c.Status = statusMissing
		case ans.err != nil:
			c.Status, c.Changes = statusStale, []string{"catalog: " + ans.err.Error()}
		case catalogEqual(stored, ans.doc):
			c.Status, c.catalog = statusRegistered, ans.doc
		default:
			c.Status, c.catalog, c.Changes = statusStale, ans.doc, catalogChanges(stored, ans.doc)
		}
		rows[name] = c
	}
	for name, bin := range onPath {
		if _, taken := rows[name]; taken || registeredBins[realPath(bin)] {
			continue // registered: under this name, or the same bin under another
		}
		ans := answers[bin]
		if ans.err != nil {
			continue // not a family tool, or a broken one
		}
		rows[name] = extCandidate{Name: name, Status: statusAvailable, Bin: bin, catalog: ans.doc}
	}
	out := make([]extCandidate, 0, len(rows))
	for _, c := range rows {
		c.count()
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// isSelf reports whether bin is mtm's own executable.
func (d extDiscovery) isSelf(bin string) bool {
	return d.self != "" && realPath(bin) == d.self
}

// realPath is path absolute with its links resolved, else path.
func realPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// askAll asks each bin `catalog`, a few at once, each bounded by the
// timeout.
func (d extDiscovery) askAll(ctx context.Context, ask map[string]string) map[string]extAnswer {
	var mu sync.Mutex
	out := map[string]extAnswer{}
	sem := make(chan struct{}, extDiscoverWorkers)
	var wg sync.WaitGroup
	for bin, name := range ask {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, d.timeout)
			defer cancel()
			data, err := execExt(c, bin, d.env(name), "catalog")
			var doc ocli.CatalogDoc
			if err == nil {
				doc, err = parseExtCatalog(filepath.Base(bin), data)
			}
			mu.Lock()
			out[bin] = extAnswer{doc, err}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// count fills the row's counts from its catalog: the tabs mtm would mount,
// the nouns and verbs it would proxy.
func (c *extCandidate) count() {
	plan := extPlan(c.catalog)
	mounted := map[string]string{}
	c.Verbs = 0
	for _, n := range plan {
		mounted[n.Name] = extNoun(c.Name, n.Name)
		c.Verbs += len(n.Verbs)
	}
	c.Nouns = len(plan)
	c.Tabs = len(pagesOf(c.Name, c.catalog, plan, mounted))
	c.Version = c.catalog.Version
}

// catalogEqual compares two catalogs as JSON.
func catalogEqual(a, b ocli.CatalogDoc) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// catalogChanges says what changed from the stored catalog to the current
// one: the version, nouns and verbs added (+), removed (-) or changed (~),
// pages added or removed, the key rows.
func catalogChanges(old, cur ocli.CatalogDoc) []string {
	var out []string
	if old.Version != cur.Version {
		out = append(out, fmt.Sprintf("version %s → %s", orNone(old.Version), orNone(cur.Version)))
	}
	verbs := func(doc ocli.CatalogDoc) (map[string]bool, map[string][]byte) {
		nouns, vs := map[string]bool{}, map[string][]byte{}
		for _, n := range doc.Nouns {
			nouns[n.Name] = true
			for _, v := range n.Verbs {
				data, _ := json.Marshal(v)
				vs[n.Name+"."+v.Name] = data
			}
		}
		return nouns, vs
	}
	oldNouns, oldVerbs := verbs(old)
	curNouns, curVerbs := verbs(cur)
	for _, n := range sortedKeys(curNouns) {
		if !oldNouns[n] {
			out = append(out, "+ noun "+n)
		}
	}
	for _, n := range sortedKeys(oldNouns) {
		if !curNouns[n] {
			out = append(out, "- noun "+n)
		}
	}
	for _, v := range sortedKeys(curVerbs) {
		if was, ok := oldVerbs[v]; !ok {
			out = append(out, "+ verb "+v)
		} else if !bytes.Equal(was, curVerbs[v]) {
			out = append(out, "~ verb "+v)
		}
	}
	for _, v := range sortedKeys(oldVerbs) {
		if _, ok := curVerbs[v]; !ok {
			out = append(out, "- verb "+v)
		}
	}
	pages := func(doc ocli.CatalogDoc) map[string][]byte {
		m := map[string][]byte{}
		for _, p := range doc.Pages {
			data, _ := json.Marshal(p)
			m[p.ID] = data
		}
		return m
	}
	oldPages, curPages := pages(old), pages(cur)
	for _, p := range sortedKeys(curPages) {
		if was, ok := oldPages[p]; !ok {
			out = append(out, "+ page "+p)
		} else if !bytes.Equal(was, curPages[p]) {
			out = append(out, "~ page "+p)
		}
	}
	for _, p := range sortedKeys(oldPages) {
		if _, ok := curPages[p]; !ok {
			out = append(out, "- page "+p)
		}
	}
	x, _ := json.Marshal(old.Bindings)
	y, _ := json.Marshal(cur.Bindings)
	if !bytes.Equal(x, y) {
		out = append(out, fmt.Sprintf("key rows %d → %d", len(old.Bindings), len(cur.Bindings)))
	}
	if len(out) == 0 {
		out = append(out, "other catalog fields changed")
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// availableExtensions is extension.available: discovery off the loop, its
// rows the answer.
func (a *App) availableExtensions(ochazuke.Ctx) ochazuke.Result {
	d := a.discovery()
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return d.run(c)
	})
}
