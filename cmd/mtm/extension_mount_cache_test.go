package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocli "ofam.dev/ochazuke/cli"
)

// registryWith writes a registry holding one fake extension and returns it
// with the data path; the catalog carries framework verbs to slim.
func registryWith(t *testing.T) (extRegistry, string) {
	t.Helper()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	return extRegistry{Dir: filepath.Join(filepath.Dir(path), extensionsDir)}, path
}

func TestMountCacheHitMissInvalidate(t *testing.T) {
	t.Parallel()
	reg, _ := registryWith(t)
	cache := filepath.Join(reg.Dir, extMountCacheFile)
	if _, err := os.Stat(cache); err == nil {
		os.Remove(cache)
	}
	names, first, errs := reg.OrderedSlim(true)
	if len(errs) != 0 || len(names) != 1 {
		t.Fatalf("miss: %v %v", names, errs)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("a miss writes the cache: %v", err)
	}
	// A hit comes from the cache: a record planted in it is served as is.
	c := reg.mountCache()
	rec := c.Records["fake"]
	rec.Entry.Bin = "/planted"
	c.Records["fake"] = rec
	reg.writeMountCache(c)
	if _, hit, _ := reg.OrderedSlim(true); hit["fake"].Bin != "/planted" {
		t.Fatalf("hit read the file: %q", hit["fake"].Bin)
	}
	// Writing the registry file drops the record.
	e, err := reg.Read("fake")
	if err != nil {
		t.Fatal(err)
	}
	e.Bin = first["fake"].Bin
	e.Catalog.Version = "9.9"
	if err := reg.write("fake", e); err != nil {
		t.Fatal(err)
	}
	_, after, _ := reg.OrderedSlim(true)
	if after["fake"].Bin != first["fake"].Bin || after["fake"].Catalog.Version != "9.9" {
		t.Fatalf("a changed file was not re-read: %+v", after["fake"])
	}
	// Removing the registration drops it from the cache.
	if err := reg.remove("fake"); err != nil {
		t.Fatal(err)
	}
	if names, _, _ := reg.OrderedSlim(true); len(names) != 0 {
		t.Fatalf("removed extension still listed: %v", names)
	}
	if got := reg.mountCache(); len(got.Records) != 0 {
		t.Fatalf("cache keeps a removed record: %v", got.Records)
	}
}

func TestMountCacheCorruptIsRebuilt(t *testing.T) {
	t.Parallel()
	reg, _ := registryWith(t)
	reg.OrderedSlim(true)
	cache := filepath.Join(reg.Dir, extMountCacheFile)
	for _, bad := range []string{"{not json", `{"version":99,"records":{"fake":{}}}`, ""} {
		if err := os.WriteFile(cache, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		names, entries, errs := reg.OrderedSlim(true)
		if len(errs) != 0 || len(names) != 1 || entries["fake"].Bin == "" {
			t.Fatalf("cache %q: %v %v", bad, names, errs)
		}
		if got := reg.mountCache(); got.Version != extMountCacheVersion || got.Records["fake"].Entry.Bin == "" {
			t.Fatalf("cache %q was not rewritten: %+v", bad, got)
		}
	}
}

func TestMountCacheOptOutWritesNothing(t *testing.T) {
	t.Parallel()
	reg, _ := registryWith(t)
	os.Remove(filepath.Join(reg.Dir, extMountCacheFile))
	if _, entries, _ := reg.OrderedSlim(false); entries["fake"].Bin == "" {
		t.Fatal("no entry without the cache")
	}
	if _, err := os.Stat(filepath.Join(reg.Dir, extMountCacheFile)); err == nil {
		t.Fatal("the opt-out wrote a cache")
	}
	// The setting reaches the mount: off, a start leaves no cache file.
	path := filepath.Join(filepath.Dir(reg.Dir), "mtm.db")
	a := openAt(t, path)
	if err := a.r.Settings().Set(settingExtMountCache, false); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(reg.Dir, extMountCacheFile))
	a.mountExtensions()
	if _, err := os.Stat(filepath.Join(reg.Dir, extMountCacheFile)); err == nil {
		t.Fatal("mounting with the setting off wrote a cache")
	}
}

// Slimming keeps what mounting reads: the same mounted nouns and verbs,
// and a catalog that still marks builtin.
func TestSlimEntryMountsTheSame(t *testing.T) {
	t.Parallel()
	reg, _ := registryWith(t)
	full := mustReadEntry(t, reg, "fake")
	full.Catalog.Nouns = append(full.Catalog.Nouns,
		ocli.CatalogNoun{Name: "ochazuke.setting", Verbs: []ocli.CatalogVerb{{Name: "get", Kind: "application", Builtin: true, Description: "long"}}},
		ocli.CatalogNoun{Name: "ochazuke.other", Verbs: []ocli.CatalogVerb{{Name: "get", Kind: "application", Builtin: true}}})
	slim := slimEntry(mustReadEntry(t, reg, "fake")) // a catalog with no framework nouns
	if len(extPlan(slim.Catalog)) == 0 {
		t.Fatal("slim lost the app nouns")
	}
	full.Catalog.Nouns = full.Catalog.Nouns[:len(full.Catalog.Nouns):len(full.Catalog.Nouns)]
	slim = slimEntry(full)
	if !catalogMarksBuiltin(full.Catalog) || !catalogMarksBuiltin(slim.Catalog) {
		t.Fatal("builtin marker lost")
	}
	want, got := extPlan(full.Catalog), extPlan(slim.Catalog)
	if len(want) == 0 || len(want) != len(got) {
		t.Fatalf("plan: %d vs %d", len(want), len(got))
	}
	for i := range want {
		if want[i].Name != got[i].Name || len(want[i].Verbs) != len(got[i].Verbs) {
			t.Fatalf("noun %s differs", want[i].Name)
		}
	}
	if len(slim.Catalog.Nouns) >= len(full.Catalog.Nouns) {
		t.Fatalf("nothing slimmed: %d nouns of %d", len(slim.Catalog.Nouns), len(full.Catalog.Nouns))
	}
	var _ ocli.CatalogDoc = slim.Catalog
}

func mustReadEntry(t *testing.T, reg extRegistry, name string) extEntry {
	t.Helper()
	e, err := reg.Read(name)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A start mounts every extension with one reload of the user's actions and
// bindings, not one per extension; a lone mount still reloads.
func TestMountBatchReloadsOnce(t *testing.T) {
	t.Parallel()
	_, path := registryWith(t)
	other, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	mustCLI(t, openAt(t, path), "extension", "register", "zzz", "--bin", other)
	a := newUnloaded(t, path)
	if len(a.exts) != 2 || a.mountReloads != 1 {
		t.Fatalf("mounted %d of 2 with %d reloads", len(a.exts), a.mountReloads)
	}
	a.mountExtension("lone", extEntry{Bin: other, Catalog: a.exts[0].doc})
	if a.mountReloads != 2 {
		t.Fatalf("a single mount reloads once: %d", a.mountReloads)
	}
}

// A headless New leaves the Inbox rows unloaded and does not add the
// store-fed Inbox and Context lists (each would read its store as it is
// added); a row lookup or the TUI start builds and loads them.
func TestHeadlessNewDoesNotLoadInbox(t *testing.T) {
	t.Parallel()
	a := newUnloaded(t, seeded(t))
	if a.inboxLoad.count != 0 || a.inboxLoad.loaded {
		t.Fatalf("New loaded the inbox: %d", a.inboxLoad.count)
	}
	for _, id := range []string{inboxListID, contextListID} {
		if a.r.Instances[id] != nil {
			t.Fatalf("a headless New added %s", id)
		}
	}
	if err := a.loadPagesFor("no-such-row"); err != nil || a.inboxLoad.count != 1 || a.inbox.list == nil || a.cx.list == nil {
		t.Fatalf("a lookup miss loads the inbox: %v %d", err, a.inboxLoad.count)
	}
	if p := a.r.PageOf(inboxPage); p.Focus != inboxListID {
		t.Fatalf("the built Inbox starts on %q", p.Focus)
	}
}
