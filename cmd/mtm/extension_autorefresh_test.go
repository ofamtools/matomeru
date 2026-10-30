package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rebuildFake makes the fake extension answer catalog (its catalog file)
// and changes its bin's stamp as a rebuild does: a new size and mtime.
func rebuildFake(t *testing.T, bin, catalog string, n int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "catalog.json"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(bin, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# build " + strings.Repeat("x", n) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	later := time.Now().Add(time.Duration(n) * time.Minute)
	if err := os.Chtimes(bin, later, later); err != nil {
		t.Fatal(err)
	}
}

// fakeCatalogCalls counts the fake's catalog calls.
func fakeCatalogCalls(t *testing.T, log string) int {
	n := 0
	for _, c := range calls(t, log) {
		if strings.HasSuffix(c, "|catalog") {
			n++
		}
	}
	return n
}

// fakeWithVerb is the fake catalog at version with one more item verb.
func fakeWithVerb(version, verb string) string {
	c := strings.Replace(fakeCatalog, "VERSION", version, 1)
	return strings.Replace(c, `{"name":"done",`, `{"name":"`+verb+`","kind":"application"},{"name":"done",`, 1)
}

// A rebuilt extension bin's catalog is read again by itself: when the TUI
// starts and when the extension's page opens, through extension.refresh.
// An unchanged bin is not asked; a refresh that fails keeps the old
// catalog, says why, and is not tried again until the bin changes.
func TestExtensionCatalogRefreshesWhenItsBinChanges(t *testing.T) {
	path := seeded(t)
	bin, log := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := registered(t, path, "fake", bin)
	hasVerb := func(a *App, verb string) bool {
		_, ok := a.r.Operations[a.r.CanonicalKey("fake.item."+verb)]
		return ok
	}
	stored := func() extEntry {
		var e extEntry
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "extensions", "fake.json"))
		if err != nil || json.Unmarshal(raw, &e) != nil {
			t.Fatalf("registry: %v", err)
		}
		return e
	}
	// Unchanged: starting asks nothing.
	before := fakeCatalogCalls(t, log)
	settleJobs(t, a, a.refreshStaleExtensions())
	if fakeCatalogCalls(t, log) != before {
		t.Fatal("an unchanged bin was asked its catalog")
	}

	// Rebuilt before mtm starts: the start refreshes it and the sync
	// mounts the new verb.
	rebuildFake(t, bin, fakeWithVerb("2.0", "archive"), 1)
	b := openAt(t, path)
	if hasVerb(b, "archive") {
		t.Fatal("the old catalog already had archive")
	}
	settleJobs(t, b, b.refreshStaleExtensions())
	_, cmd := b.Update(nil) // the sync the refresh asked for
	settleJobs(t, b, cmd)
	if e := stored(); e.Catalog.Version != "2.0" || e.BinStamp == nil || *e.BinStamp != *binStamp(bin) {
		t.Fatalf("start: stored %s %+v", e.Catalog.Version, e.BinStamp)
	}
	if !hasVerb(b, "archive") {
		t.Fatal("start: archive is not mounted")
	}

	// Rebuilt while mtm runs: opening its page refreshes it.
	rebuildFake(t, bin, fakeWithVerb("3.0", "star"), 2)
	openExtPage(t, b, "ext/fake")
	_, cmd = b.Update(nil)
	settleJobs(t, b, cmd)
	if stored().Catalog.Version != "3.0" || !hasVerb(b, "star") {
		t.Fatalf("page open: stored %s, star mounted %v", stored().Catalog.Version, hasVerb(b, "star"))
	}

	// A rebuild that answers no catalog: the old one stays, the status
	// line says why, and the next open does not ask again.
	rebuildFake(t, bin, "not json", 3)
	if err := b.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	b.Update(nil)
	openExtPage(t, b, "ext/fake")
	if stored().Catalog.Version != "3.0" || !hasVerb(b, "star") {
		t.Fatal("a failed refresh replaced the catalog")
	}
	if !strings.Contains(b.m.Status, "fake: o-fake catalog") {
		t.Fatalf("failure not reported: %q", b.m.Status)
	}
	asked := fakeCatalogCalls(t, log)
	if err := b.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	b.Update(nil)
	openExtPage(t, b, "ext/fake")
	if fakeCatalogCalls(t, log) != asked {
		t.Fatal("a bin whose refresh failed was asked again at the same stamp")
	}
}
