package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// rewriteCatalog changes the fake tool's catalog file.
func rewriteCatalog(t *testing.T, bin, from, to string) {
	t.Helper()
	file := filepath.Join(filepath.Dir(bin), "catalog.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), from, to, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pageIDs(a *App) []string {
	var out []string
	for _, p := range a.r.PageList() {
		out = append(out, p.ID)
	}
	return out
}

// extension refresh in the TUI re-mounts a changed catalog at once: the
// new verb and its key are there, the page stays open with its rows, and
// the tabs keep their order.
func TestExtensionRefreshRemountsInTheRunningTUI(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), `"item archive") echo '{"ok":true}' ;;`)
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	other, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	mustCLI(t, openAt(t, path), "extension", "register", "zzz", "--bin", other)
	a := openAt(t, path)
	before := pageIDs(a)
	openExtPage(t, a, "ext/fake")
	if hasOp(a, "fake.item.archive") {
		t.Fatal("archive before the refresh")
	}
	rewriteCatalog(t, bin, `{"name":"done"`, `{"name":"archive","kind":"application"},{"name":"done"`)
	rewriteCatalog(t, bin, `"keys":"X","request":{"noun":"item","verb":"done"}`, `"keys":"X","request":{"noun":"item","verb":"archive"}`)
	if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "refresh", Args: ochazuke.Arguments{"name": "fake"}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if a.m.Status != "" {
		t.Fatal(a.m.Status)
	}
	if !hasOp(a, "fake.item.archive") {
		t.Fatal("refresh did not mount the new verb")
	}
	if got := pageIDs(a); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("tabs %v, were %v", got, before)
	}
	if a.r.Page != "ext/fake" {
		t.Fatalf("page %s", a.r.Page)
	}
	if l := a.r.List("ext/fake/item"); l == nil || l.Len() != 2 {
		t.Fatalf("rows after the re-mount: %+v", l)
	}
	if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "X")
	if m := a.r.TopModal(); m != "" {
		t.Fatalf("X still asks done's confirm: %s", m)
	}
}

// A register or remove from the CLI (another process) shows in a running
// mtm when a page is next opened.
func TestExtensionCLIChangesShowWhenAPageOpens(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	a := openAt(t, path)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	if a.r.PageOf("ext/fake") != nil {
		t.Fatal("mounted before a page opened")
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	if a.r.PageOf("ext/fake") == nil {
		t.Fatal("not mounted after a page opened")
	}
	openExtPage(t, a, "ext/fake")
	mustCLI(t, openAt(t, path), "extension", "remove", "fake", "--yes")
	if err := a.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	if a.r.PageOf("ext/fake") != nil || a.r.Noun("fake.item") != nil || hasOp(a, "fake.item.done") {
		t.Fatal("removed extension still mounted")
	}
}

func hasOp(a *App, key string) bool { _, ok := a.r.Operations[key]; return ok }
