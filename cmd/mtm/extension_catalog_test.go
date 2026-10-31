package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// The catalog can change without a new bin stamp: wrappers load external
// files, and an install can preserve a binary's size and modification time.
func TestExtensionCatalogChangeWithoutBinChange(t *testing.T) {
	for _, trigger := range []string{"start", "extensions", "tab"} {
		t.Run(trigger, func(t *testing.T) {
			path := seeded(t)
			bin, log := fakeExtension(t, toolCatalog("fake", "1.0"), "")
			a := registered(t, path, "fake", bin)
			stamp := *binStamp(bin)
			rewriteCatalog(t, bin, `{"name":"done"`, `{"name":"archive","kind":"application"},{"name":"done"`)
			if *binStamp(bin) != stamp {
				t.Fatal("fixture changed the executable")
			}
			// CLI discovery is the same source of stale status as the UI.
			cli := openAt(t, path)
			cli.getenv = func(string) string { return "" }
			if got := discovered(t, cli)["fake"].Status; got != statusStale {
				t.Fatalf("available: %s, want stale", got)
			}
			switch trigger {
			case "start":
				before := fakeCatalogCalls(t, log)
				cmd := a.refreshStaleExtensions()
				if fakeCatalogCalls(t, log) != before || hasOp(a, "fake.item.archive") {
					t.Fatal("catalog work ran on the UI loop")
				}
				settleJobs(t, a, cmd)
			case "extensions":
				a.getenv = func(string) string { return "" }
				openExtPage(t, a, extensionsPage)
			case "tab":
				openExtPage(t, a, "ext/fake")
			}
			if !hasOp(a, "fake.item.archive") {
				t.Fatal("new catalog was not mounted")
			}
			if !slices.ContainsFunc(a.m.Notices, func(s string) bool { return strings.Contains(s, "fake") && strings.Contains(s, "refreshed") }) {
				t.Fatalf("no named refresh notice: %v", a.m.Notices)
			}
		})
	}
}

func TestExtensionCatalogAutoRefreshOptOut(t *testing.T) {
	path := seeded(t)
	bin, _ := fakeExtension(t, toolCatalog("fake", "1.0"), "")
	a := registered(t, path, "fake", bin)
	if !a.r.Settings().Bool(settingExtAutoRefresh) {
		t.Fatal("auto refresh must default on")
	}
	if _, err := a.r.Settings().SetSession(settingExtAutoRefresh, false); err != nil {
		t.Fatal(err)
	}
	rewriteCatalog(t, bin, `{"name":"done"`, `{"name":"archive","kind":"application"},{"name":"done"`)
	settleJobs(t, a, a.refreshStaleExtensions())
	openExtPage(t, a, "ext/fake")
	a.getenv = func(string) string { return "" }
	openExtPage(t, a, extensionsPage)
	if hasOp(a, "fake.item.archive") || extStatus(a, "fake") != statusStale {
		t.Fatal("opt out did not keep the old catalog marked stale")
	}
	if err := a.r.SetFocus(extListID + "/fake"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(frame(a), "extension.refresh") {
		t.Fatal("stale row offers no refresh verb")
	}
	pressJob(t, a, "L")
	if !hasOp(a, "fake.item.archive") || extStatus(a, "fake") != statusRegistered {
		t.Fatal("explicit refresh stopped working with auto refresh off")
	}
}

func TestExtensionCatalogCheckDoesNotOverwriteNewerRegistration(t *testing.T) {
	path := seeded(t)
	bin, _ := fakeExtension(t, toolCatalog("fake", "1.0"), "")
	a := registered(t, path, "fake", bin)
	rewriteCatalog(t, bin, `"version":"1.0"`, `"version":"2.0"`)
	d := a.discovery()
	d.registeredOnly = true
	rows, err := d.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// A manual refresh lands while this check's completion is queued.
	rewriteCatalog(t, bin, `"version":"2.0"`, `"version":"3.0"`)
	if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "refresh", Args: ochazuke.Arguments{"name": "fake"}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	a.autoRefreshCatalogs(rows)
	e, err := a.extRegistry().Read("fake")
	if err != nil || e.Catalog.Version != "3.0" {
		t.Fatalf("old check replaced a newer catalog: %+v, %v", e, err)
	}
}

func TestExtensionCatalogFailureRecoversWithoutBinChange(t *testing.T) {
	path := seeded(t)
	bin, _ := fakeExtension(t, toolCatalog("fake", "1.0"), "")
	a := registered(t, path, "fake", bin)
	file := filepath.Join(filepath.Dir(bin), "catalog.json")
	if err := os.WriteFile(file, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.refreshStaleExtensions())
	if !hasOp(a, "fake.item.done") || !strings.Contains(strings.Join(a.m.Notices, " "), "fake") {
		t.Fatalf("failed refresh lost the cache or notice: %v", a.m.Notices)
	}
	if err := os.WriteFile(file, []byte(fakeWithVerb("1.0", "archive")), 0o644); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.refreshStaleExtensions())
	if !hasOp(a, "fake.item.archive") {
		t.Fatal("recovery required a changed executable")
	}
}

// The reported bug changed only detail Sections, not the version or verbs.
// Render the actual remounted page so catalog equality covers that case.
func TestExtensionCatalogDetailSectionsChange(t *testing.T) {
	page := `"pages":[{"id":"items","title":"Catalog fixture","lists":[{"id":"items/list","noun":"item","source":"item.list"}],"detail":{"fields":["body"]}}]`
	old := strings.Replace(toolCatalog("fake", "1.0"), `"pages":[]`, page, 1)
	bin, _ := fakeExtension(t, old, "")
	script, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), `"label":"one","cells"`, `"label":"one","data":{"body":"Readable catalog body"},"cells"`, 1))
	if err := os.WriteFile(bin, script, 0o755); err != nil {
		t.Fatal(err)
	}
	a := registered(t, seeded(t), "fake", bin)
	if a.r.PageOf("ext/fake/items") == nil {
		t.Fatalf("pages: %v; notices: %v", pageIDs(a), a.m.Notices)
	}
	openExtPage(t, a, "ext/fake/items")
	if strings.Contains(frame(a), "Fresh catalog section") {
		t.Fatal("fixture already has sections")
	}
	rewriteCatalog(t, bin, `"fields":["body"]`, `"sections":[{"kind":"markdown","title":"Fresh catalog section","field":"body"}]`)
	openExtPage(t, a, "work")
	openExtPage(t, a, "ext/fake/items")
	if f := frame(a); !strings.Contains(f, "Fresh catalog section") || !strings.Contains(f, "Readable catalog body") {
		t.Fatalf("new detail sections were not drawn:\n%s", f)
	}
}
