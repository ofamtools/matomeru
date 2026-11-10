package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// settingExtMountCache turns off the mount cache (below).
const settingExtMountCache = "extensions.mount-cache"

var extMountCacheSetting = ochazuke.Setting{Name: settingExtMountCache, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection,
	Type: ochazuke.SettingBoolean, Default: true,
	Description: "Keep a slim copy of the extension registry (<data>/extensions/.mounts.json) so a start decodes kilobytes, not each tool's whole catalog; off reads every registry file in full"}

// The mount cache holds, per registry file, the entry as mounting needs it:
// the catalog with each framework (builtin) verb cut down to its name and
// the few fields lookups read (slimEntry). A tool's catalog lists every
// framework verb, most of its size, and mounting never uses one (AppNouns).
// Each record is valid while its registry file's size and modification time
// are those it was read at, so a register, refresh or remove (which write
// the file) drops it. A cache that does not read, has another version, or
// holds a stale record is rebuilt from the registry files; it is never the
// source of truth, and a failed write only costs the next start the read.
const extMountCacheFile = ".mounts.json"

const extMountCacheVersion = 1

type extMountRecord struct {
	Size  int64    `json:"size"`
	Mtime int64    `json:"mtime"`
	Entry extEntry `json:"entry"`
}

type extMountCache struct {
	Version int                       `json:"version"`
	Records map[string]extMountRecord `json:"records"`
}

// slimEntry cuts the weight mounting never reads from an entry's catalog:
// a noun with only builtin verbs goes (one stays, so the catalog still marks
// builtin) with the key rows on it (they mount on a mounted noun only), and a builtin verb keeps its identity, kind and rows (what
// lookups and the builtin marker read), not its parameters, examples or
// texts. It changes e in place; call it on an entry just decoded.
func slimEntry(e extEntry) extEntry {
	kept := e.Catalog.Nouns[:0:0]
	appNouns := map[string]bool{}
	marked := false // one noun holding a builtin verb stays: the catalog marks builtin
	for _, n := range e.Catalog.Nouns {
		builtin, app := false, false
		for _, v := range n.Verbs {
			if v.Builtin {
				builtin = true
			} else {
				app = true
			}
		}
		if app {
			appNouns[n.Name] = true
		}
		if builtin && !app {
			// A framework noun: it mounts nothing (AppNouns).
			if marked {
				continue
			}
			marked = true
			n.Verbs = n.Verbs[:1:1]
		}
		kept = append(kept, n)
	}
	e.Catalog.Nouns = kept
	// A key row mounts only on a noun the tool mounts (loadExtBindings).
	rows := e.Catalog.Bindings[:0:0]
	for _, b := range e.Catalog.Bindings {
		if appNouns[b.Noun] {
			rows = append(rows, b)
		}
	}
	e.Catalog.Bindings = rows
	if len(rows) == 0 {
		e.Catalog.Bindings = nil // as a decode of the cache gives, so entryJSON agrees
	}
	for i := range e.Catalog.Nouns {
		verbs := e.Catalog.Nouns[i].Verbs
		for j := range verbs {
			v := &verbs[j]
			if !v.Builtin {
				continue
			}
			*v = ocli.CatalogVerb{Name: v.Name, Kind: v.Kind, Builtin: true, Rows: v.Rows, History: v.History,
				Agent: v.Agent, Destructive: v.Destructive}
		}
	}
	return e
}

// OrderedSlim is Ordered with the slim entries, through the mount cache
// when use is set.
func (g extRegistry) OrderedSlim(use bool) ([]string, map[string]extEntry, []error) {
	names, err := g.Names()
	if err != nil {
		return nil, nil, []error{err}
	}
	var cache extMountCache
	if use {
		cache = g.mountCache()
	}
	fresh := extMountCache{Version: extMountCacheVersion, Records: map[string]extMountRecord{}}
	entries := map[string]extEntry{}
	var errs []error
	var ok []string
	dirty := len(cache.Records) != len(names)
	for _, name := range names {
		fi, serr := os.Stat(g.path(name))
		if rec, hit := cache.Records[name]; use && serr == nil && hit && rec.Size == fi.Size() && rec.Mtime == fi.ModTime().UnixNano() {
			entries[name] = rec.Entry
			fresh.Records[name] = rec
			ok = append(ok, name)
			continue
		}
		dirty = true
		e, err := g.Read(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		e = slimEntry(e)
		entries[name] = e
		ok = append(ok, name)
		if serr == nil {
			fresh.Records[name] = extMountRecord{Size: fi.Size(), Mtime: fi.ModTime().UnixNano(), Entry: e}
		}
	}
	if use && dirty {
		g.writeMountCache(fresh)
	}
	sort.SliceStable(ok, func(i, j int) bool {
		a, b := entries[ok[i]].RegisteredAt, entries[ok[j]].RegisteredAt
		if !a.Equal(b) {
			return a.Before(b)
		}
		return ok[i] < ok[j]
	})
	return ok, entries, errs
}

func (g extRegistry) mountCache() extMountCache {
	var c extMountCache
	data, err := os.ReadFile(filepath.Join(g.Dir, extMountCacheFile))
	if err != nil || json.Unmarshal(data, &c) != nil || c.Version != extMountCacheVersion {
		return extMountCache{}
	}
	return c
}

// writeMountCache writes the cache whole (temp file and rename); an error
// leaves the old file.
func (g extRegistry) writeMountCache(c extMountCache) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(g.Dir, ".mounts-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if os.Rename(tmp.Name(), filepath.Join(g.Dir, extMountCacheFile)) != nil {
		os.Remove(tmp.Name())
	}
}

// orderedForMount is the registry as a start or a sync mounts it.
func (a *App) orderedForMount() ([]string, map[string]extEntry, []error) {
	return a.extRegistry().OrderedSlim(a.r.Settings().Bool(settingExtMountCache))
}

// registrySnapshot is the registry as mountExtensions read it at start; the
// CLI's changed-bin check takes it instead of reading the registry again.
type registrySnapshot struct {
	names   []string
	entries map[string]extEntry
}
