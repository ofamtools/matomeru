package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// extPageApp is an app whose PATH holds only dir, opened on the
// Extensions page with its first discovery landed.
func extPageApp(t *testing.T, path, dir string) *App {
	t.Helper()
	a := openAt(t, path)
	a.getenv = func(k string) string {
		if k == "PATH" {
			return dir
		}
		return os.Getenv(k)
	}
	openExtPage(t, a, extensionsPage)
	if !a.extPage.discovered {
		t.Fatal("no discovery on opening the page")
	}
	return a
}

// extStatus is the status cell of name's row, "" when there is none.
func extStatus(a *App, name string) string {
	for _, item := range a.extList.Items() {
		if item.Label == name {
			return item.Cells[1]
		}
	}
	return ""
}

// The Extensions tab sits after mtm's own pages (Inbox is the last) and before the
// extensions' tabs.
func TestExtensionsPageIsTheTabBeforeTheExtensions(t *testing.T) {
	path := seeded(t)
	bin, _ := fakeExtension(t, toolCatalog("fake", "1.0"), "")
	a := registered(t, path, "fake", bin)
	pages := pageIDs(a)
	i := slices.Index(pages, extensionsPage)
	if i < 0 || pages[i-1] != inboxPage || pages[i+1] != "ext/fake" {
		t.Fatalf("tabs %v", pages)
	}
}

// On the page: n registers an available row (its tab mounts at once and
// the row reads registered), enter opens that tab, L refreshes it once
// its catalog changed (stale, then registered again, the new verb
// mounted), d d asks and removes it (the tab goes, the row is available
// again), and a missing bin's row is removed the same way. Keys that do
// not apply to a row's status do not answer there.
func TestExtensionsPageKeys(t *testing.T) {
	dir := t.TempDir()
	fakeTool(t, dir, "alpha", toolCatalog("alpha", "1.0"))
	gone := fakeTool(t, dir, "gone", toolCatalog("gone", "1.0"))
	fakeTool(t, dir, "junk", "{not json")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "gone", "--bin", gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	a := extPageApp(t, path, dir)
	if got := len(a.extList.Items()); got != 2 || extStatus(a, "alpha") != statusAvailable || extStatus(a, "gone") != statusMissing {
		t.Fatalf("rows %+v", a.extList.Items())
	}
	alpha := extListID + "/alpha"
	if err := a.r.SetFocus(alpha); err != nil {
		t.Fatal(err)
	}
	// enter and L do not answer on an available row.
	for _, key := range []string{"enter"} {
		pressJob(t, a, key)
		if a.r.Page != extensionsPage || a.r.TopModal() != "" {
			t.Fatalf("%s on an available row: page %s, modal %q", key, a.r.Page, a.r.TopModal())
		}
	}
	pressJob(t, a, "n")
	if a.r.PageOf("ext/alpha") == nil || extStatus(a, "alpha") != statusRegistered {
		t.Fatalf("n: page %v, status %q", a.r.PageOf("ext/alpha") != nil, extStatus(a, "alpha"))
	}
	if a.r.Focus != alpha {
		t.Fatalf("n moved the focus to %s", a.r.Focus)
	}
	if !strings.Contains(frame(a), "registered") {
		t.Fatalf("frame:\n%s", frame(a))
	}
	pressJob(t, a, "enter")
	if a.r.Page != "ext/alpha" {
		t.Fatalf("enter: page %s", a.r.Page)
	}
	// The tool changes: the row is stale, the detail says what changed.
	if err := os.WriteFile(filepath.Join(dir, "alpha.catalog"), []byte(strings.Replace(toolCatalog("alpha", "2.0"),
		`{"name":"done"`, `{"name":"archive","kind":"application"},{"name":"done"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	rebuilt(t, filepath.Join(dir, "o-alpha")) // a new catalog is a new bin
	openExtPage(t, a, extensionsPage)
	if extStatus(a, "alpha") != statusStale {
		t.Fatalf("status %q, want stale", extStatus(a, "alpha"))
	}
	if err := a.r.SetFocus(alpha); err != nil {
		t.Fatal(err)
	}
	if f := frame(a); !strings.Contains(f, "changed since registered") || !strings.Contains(f, "+ verb item.archive") {
		t.Fatalf("stale detail:\n%s", f)
	}
	pressJob(t, a, "L")
	if extStatus(a, "alpha") != statusRegistered || !hasOp(a, "alpha-item.archive") {
		t.Fatalf("L: status %q, archive mounted %v", extStatus(a, "alpha"), hasOp(a, "alpha-item.archive"))
	}
	// d d asks, then removes; the data file is not touched.
	press(t, a, "d", "d") // no settling between: the sequence's timer would run out
	modal := a.r.Instances[a.r.TopModal()]
	if modal == nil || !strings.HasPrefix(modal.Payload.Prompt, "Remove extension alpha?") {
		t.Fatalf("d d: no confirm: %+v", modal)
	}
	pressJob(t, a, "y")
	if a.r.PageOf("ext/alpha") != nil || extStatus(a, "alpha") != statusAvailable {
		t.Fatalf("d d y: page %v, status %q", a.r.PageOf("ext/alpha") != nil, extStatus(a, "alpha"))
	}
	// n on a missing row does nothing; d d removes it from the table.
	gone = extListID + "/gone"
	if err := a.r.SetFocus(gone); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "n")
	if extStatus(a, "gone") != statusMissing {
		t.Fatal("n answered on a missing row")
	}
	press(t, a, "d", "d")
	pressJob(t, a, "y")
	if extStatus(a, "gone") != "" || len(a.extList.Items()) != 1 {
		t.Fatalf("missing row after d d y: %+v", a.extList.Items())
	}
}

// One row of each status, the stale one selected: the table's columns and
// the detail with what changed at its top.
func TestExtensionsPageFrame(t *testing.T) {
	dir := t.TempDir()
	fakeTool(t, dir, "alpha", toolCatalog("alpha", "1.0"))
	beta := fakeTool(t, dir, "beta", toolCatalog("beta", "1.0"))
	delta := fakeTool(t, dir, "delta", toolCatalog("delta", "1.0"))
	gone := fakeTool(t, dir, "gone", toolCatalog("gone", "1.0"))
	fakeTool(t, dir, "junk", "garbage")
	path := seeded(t)
	for name, bin := range map[string]string{"beta": beta, "delta": delta, "gone": gone} {
		mustCLI(t, openAt(t, path), "extension", "register", name, "--bin", bin)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta.catalog"), []byte(strings.Replace(toolCatalog("beta", "1.1"),
		`{"name":"done"`, `{"name":"archive","kind":"application"},{"name":"done"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	rebuilt(t, filepath.Join(dir, "o-beta")) // a new catalog is a new bin
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	a := extPageApp(t, path, dir)
	a.home = func() (string, error) { return dir, nil } // bins read ~/o-NAME
	a.rediscover()
	settleJobs(t, a, a.r.PendingCommands())
	a.m.Notices = nil // the fakes' dropped key rows
	if err := a.r.SetFocus(extListID + "/beta"); err != nil {
		t.Fatal(err)
	}
	f := frame(a)
	for _, want := range []string{"name", "status", "bin", "tabs", "nouns", "verbs", "registered",
		"alpha  available", "beta   stale", "delta  registered", "gone   missing",
		"changed since registered (L refreshes)", "version 1.0 → 1.1", "+ verb item.archive",
		"nouns and verbs", "item  list, archive, done, rename", "pages", "beta (from its nouns: item)",
		"L extension.refresh", "d d extension.remove", "enter extension.open"} {
		if !strings.Contains(f, want) {
			t.Errorf("frame lacks %q", want)
		}
	}
	if strings.Contains(f, "junk") {
		t.Error("the tool answering garbage is listed")
	}
	t.Logf("frame:\n%s", f)
}

// Discovery is a quiet job off the loop: the page opens at once saying it
// is looking, and with nothing on PATH the table says what would be
// listed.
func TestExtensionsPageEmpty(t *testing.T) {
	if !opQuiet(t, "extension.available") {
		t.Fatal("extension.available is not quiet")
	}
	a := openAt(t, seeded(t))
	a.getenv = func(k string) string {
		if k == "PATH" {
			return t.TempDir()
		}
		return os.Getenv(k)
	}
	if err := a.r.OpenPage(extensionsPage); err != nil {
		t.Fatal(err)
	}
	_, cmd := a.Update(nil)
	if f := frame(a); !strings.Contains(f, "Looking for family tools on PATH") {
		t.Fatalf("before the job landed:\n%s", f)
	}
	settleJobs(t, a, cmd)
	if f := frame(a); !strings.Contains(f, "No family tools found") {
		t.Fatalf("frame:\n%s", f)
	}
}

func opQuiet(t *testing.T, key string) bool {
	t.Helper()
	return open(t).r.Operations[key].Quiet
}
