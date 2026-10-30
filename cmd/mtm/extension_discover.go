package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
// finds them) is asked `catalog`, and only that, and only when its file
// changed since the catalog on hand was read (plan); a tool that does not
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
	p := d.plan()
	d.probe(ctx, p)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.rows(p), nil
}

// extProbePlan is one discovery: the tools found, the registry, the
// catalog answers known without an exec and the bins to ask.
type extProbePlan struct {
	onPath         map[string]string // name -> bin
	names          []string          // registered, in order
	entries        map[string]extEntry
	missing        map[string]bool // registered name -> its bin is gone
	registeredBins map[string]bool // a registered bin, links resolved
	answers        map[string]extAnswer
	ask            map[string]string // bin -> the name it is asked as
	stamps         map[string]*extBinStamp
	probes         extProbes
}

// plan finds the tools and decides which bins to ask. A catalog is the
// tool's definition, not its data, so a bin is asked only when its file
// (links resolved: size and mtime, extBinStamp) is not what it was when
// the catalog on hand was read: the registry entry's (BinStamp), else the
// probe cache's (extProbes, for the tools not registered). It reads files
// and stats bins, so it runs off the loop.
func (d extDiscovery) plan() *extProbePlan {
	p := &extProbePlan{onPath: map[string]string{}, missing: map[string]bool{}, registeredBins: map[string]bool{},
		answers: map[string]extAnswer{}, ask: map[string]string{}, stamps: map[string]*extBinStamp{}, probes: d.reg.probes()}
	found, _ := oden.Discover("o", d.path, oden.OS)
	for _, ext := range found {
		if !store.ValidExtensionName(ext.Name) || slices.Contains(extHosts, ext.Name) || d.isSelf(ext.Path) {
			continue
		}
		p.onPath[ext.Name] = ext.Path
	}
	// A registry file that does not read is a notice of the mounting,
	// not a row.
	p.names, p.entries, _ = d.reg.Ordered()
	// The bins to ask: every tool on PATH, and every registered bin that
	// still exists, unless its catalog on hand is as new as the bin.
	for name, bin := range p.onPath {
		p.consider(bin, name, nil)
	}
	for _, name := range p.names {
		e := p.entries[name]
		p.registeredBins[realPath(e.Bin)] = true
		if !oden.Executable(oden.OS, e.Bin) {
			p.missing[name] = true
			continue
		}
		p.consider(e.Bin, name, &e)
	}
	return p
}

// consider files bin's answer as known (the registered catalog, else the
// probe cache's) when its stamp matches, else asks it.
func (p *extProbePlan) consider(bin, name string, e *extEntry) {
	stamp := binStamp(bin)
	p.stamps[bin] = stamp
	delete(p.answers, bin)
	delete(p.ask, bin)
	switch probe, ok := p.probes[realPath(bin)]; {
	case stamp == nil:
	case e != nil && e.BinStamp != nil && *e.BinStamp == *stamp:
		p.answers[bin] = extAnswer{doc: e.Catalog}
		return
	case ok && probe.Stamp == *stamp:
		p.answers[bin] = probe.answer()
		return
	}
	p.ask[bin] = name
}

// probe asks each bin of the plan its catalog and records the answers in
// the probe cache (not when ctx ended: an answer cut short is not the
// bin's).
func (d extDiscovery) probe(ctx context.Context, p *extProbePlan) {
	if len(p.ask) == 0 {
		return
	}
	for bin, ans := range d.askAll(ctx, p.ask) {
		p.answers[bin] = ans
		if stamp := p.stamps[bin]; stamp != nil && ctx.Err() == nil {
			p.probes[realPath(bin)] = newExtProbe(*stamp, ans)
		}
	}
	if ctx.Err() == nil {
		keep := map[string]bool{}
		for bin := range p.stamps {
			keep[realPath(bin)] = true
		}
		maps.DeleteFunc(p.probes, func(bin string, _ extProbe) bool { return !keep[bin] })
		_ = d.reg.writeProbes(p.probes) // a cache: a failed write asks again next time
	}
}

// rows merges the answers with the registry, sorted by name.
func (d extDiscovery) rows(p *extProbePlan) []extCandidate {
	rows := map[string]extCandidate{}
	for _, name := range p.names {
		e := p.entries[name]
		stored := e.Catalog
		c := extCandidate{Name: name, Bin: e.Bin, RegisteredAt: e.RegisteredAt, catalog: stored, stored: &stored}
		switch ans := p.answers[e.Bin]; {
		case p.missing[name]:
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
	for name, bin := range p.onPath {
		if _, taken := rows[name]; taken || p.registeredBins[realPath(bin)] {
			continue // registered: under this name, or the same bin under another
		}
		ans := p.answers[bin]
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
	return out
}

// extBinStamp is a bin's file, links resolved, as discovery compares it.
type extBinStamp struct {
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
}

// binStamp is bin's stamp now, nil when it does not stat.
func binStamp(bin string) *extBinStamp {
	fi, err := os.Stat(realPath(bin))
	if err != nil {
		return nil
	}
	return &extBinStamp{Size: fi.Size(), Mtime: fi.ModTime().UTC()}
}

// extProbe is the probe cache's record of one bin's catalog answer.
type extProbe struct {
	Stamp   extBinStamp      `json:"stamp"`
	Catalog *ocli.CatalogDoc `json:"catalog,omitempty"`
	Error   string           `json:"error,omitempty"`
}

func newExtProbe(stamp extBinStamp, ans extAnswer) extProbe {
	if ans.err != nil {
		return extProbe{Stamp: stamp, Error: ans.err.Error()}
	}
	doc := ans.doc
	return extProbe{Stamp: stamp, Catalog: &doc}
}

func (p extProbe) answer() extAnswer {
	if p.Catalog == nil {
		return extAnswer{err: errors.New(cmp.Or(p.Error, "no catalog"))}
	}
	return extAnswer{doc: *p.Catalog}
}

// extProbes is the probe cache by bin (links resolved): the catalog
// answers of the tools discovery asked, so it asks a bin again only once
// its file changed. It is <registry>/.probes.json (a dot name is not an
// extension); a cache, rebuilt when it does not read.
type extProbes map[string]extProbe

const extProbesFile = ".probes.json"

func (g extRegistry) probes() extProbes {
	out := extProbes{}
	if data, err := os.ReadFile(filepath.Join(g.Dir, extProbesFile)); err == nil {
		if json.Unmarshal(data, &out) != nil {
			return extProbes{}
		}
	}
	return out
}

// writeProbes writes the cache whole (temp file and rename).
func (g extRegistry) writeProbes(p extProbes) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(g.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(g.Dir, ".probes-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(g.Dir, extProbesFile))
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
		mounted[n.Name] = n.Name // pagesOf reads only which nouns mount
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
