package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
)

// fakeCatalog is a family tool's catalog: an item noun with a row source
// (list), a confirmed verb, a verb with a parameter and key rows (X runs
// done; j is the list's motion, so mtm drops it); app is a framework noun
// with interaction verbs only, never mounted.
const fakeCatalog = `{"schema":1,"name":"o-fake","version":"VERSION","nouns":[
 {"name":"item","columns":[{"title":"item"},{"title":"state"}],"verbs":[
  {"name":"list","kind":"application","description":"List the items"},
  {"name":"done","kind":"application","confirm":"Finish {{label}}?"},
  {"name":"rename","kind":"application","params":{"to":{"type":"string","required":true}},"positional":["to"]}]},
 {"name":"app","verbs":[{"name":"quit","kind":"interaction"}]}],
 "bindings":[{"noun":"item","mode":"normal","keys":"X","request":{"noun":"item","verb":"done"}},
  {"noun":"item","mode":"normal","keys":"j","request":{"noun":"item","verb":"done"}}],
 "pages":[]}`

// fakeExtension writes a fake extension bin into a fresh directory: it
// answers catalog, item list and the item verbs, and logs every call (its
// MTM_EXTENSION and MTM_DATA, then its words) to the returned log. extra
// is more shell case arms.
func fakeExtension(t *testing.T, catalog, extra string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "o-fake"), filepath.Join(dir, "calls.log")
	catalogFile := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(catalogFile, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
echo "$MTM_EXTENSION|$MTM_DATA|$*" >> '` + log + `'
case "$1 $2" in
"catalog ") cat '` + catalogFile + `' ;;
"item list") echo '[{"id":"i1","label":"one","cells":["one","open"]},{"id":"i2","label":"two","cells":["two","done"]}]' ;;
"item done"|"item rename") echo '{"ok":true}' ;;
` + extra + `
*) echo "unknown $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// calls is the fake extension's log, a call per line.
func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// settleJobs runs cmd's commands and delivers every job completion (an
// exec of the fake bin takes a few milliseconds); timers are dropped.
func settleJobs(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- cmd() }()
		var msg tea.Msg
		select {
		case msg = <-ch:
		case <-time.After(5 * time.Second):
			return // a timer
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				run(c)
			}
		case ochazuke.Completion:
			_, next := a.Update(msg)
			run(next)
		}
	}
	run(cmd)
}

// pressJob presses one key and settles the jobs it started.
func pressJob(t *testing.T, a *App, k string) {
	t.Helper()
	_, cmd := a.Update(keyMsg(k))
	settleJobs(t, a, cmd)
	if a.m.Status != "" {
		t.Fatalf("key %q: %s", k, a.m.Status)
	}
}

// openExtPage opens an extension's page as the TUI does (the page-open
// hook reloads its rows) and settles the load.
func openExtPage(t *testing.T, a *App, page string) {
	t.Helper()
	if err := a.r.OpenPage(page); err != nil {
		t.Fatal(err)
	}
	_, cmd := a.Update(nil)
	settleJobs(t, a, cmd)
}

// registered registers the fake extension through the CLI of an app at
// path and returns the app a fresh mtm opens on the same data.
func registered(t *testing.T, path, name, bin string) *App {
	t.Helper()
	cliApp := openAt(t, path)
	out := mustCLI(t, cliApp, "extension", "register", name, "--bin", bin)
	if !strings.Contains(out, `"name": "`+name+`"`) || !strings.Contains(out, `"nouns": 1`) {
		t.Fatalf("register answered:\n%s", out)
	}
	return openAt(t, path)
}

// mtm extension register reads the tool's catalog into mtm's own registry
// (under the data directory, never nabe's), and the next mtm shows its
// page as a tab after mtm's pages, its rows from the tool as a table.
func TestExtensionRegisterMountsATabAfterMtmsPages(t *testing.T) {
	path := seeded(t)
	bin, log := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := registered(t, path, "fake", bin)
	file := filepath.Join(filepath.Dir(path), "extensions", "fake.json")
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("registry file: %v", err)
	}
	if got := calls(t, log); len(got) != 1 || got[0] != "fake|"+filepath.Dir(path)+"|catalog" {
		t.Fatalf("register calls %q", got)
	}
	var pages []string
	for _, p := range a.r.PageList() {
		pages = append(pages, p.ID)
	}
	if i := slices.Index(pages, "ext/fake"); i != len(pages)-1 || pages[i-1] != extensionsPage {
		t.Fatalf("pages %v: the extension's tab is not after mtm's", pages)
	}
	if a.r.Noun("fake-app") != nil {
		t.Fatal("a framework noun of the tool was mounted")
	}
	if len(calls(t, log)) != 1 {
		t.Fatal("mounting ran the tool")
	}
	openExtPage(t, a, "ext/fake")
	list := a.r.List("ext/fake/item")
	if list == nil || list.Len() != 2 {
		t.Fatalf("rows not loaded: %+v", list)
	}
	if got := calls(t, log); got[len(got)-1] != "fake|"+filepath.Dir(path)+"|item list --json {}" {
		t.Fatalf("load call %q", got[len(got)-1])
	}
	f := frame(a)
	for _, want := range []string{"fake", "item", "state", "one", "open", "two"} {
		if !strings.Contains(f, want) {
			t.Fatalf("frame lacks %q:\n%s", want, f)
		}
	}
	listing := strings.ReplaceAll(mustCLI(t, a, "extension", "list"), " ", "")
	if !strings.Contains(listing, `"name":"fake"`) || !strings.Contains(listing, `"version":"1.0"`) {
		t.Fatalf("extension list:\n%s", listing)
	}
}

// A verb on a row runs the tool: its own confirm first, then `<bin> item
// done --target ID --json {} --yes`; its answer is the page's status and
// the rows reload. The tool's key row X answers on the row; its j would
// shadow the list's motion and is dropped with a notice.
func TestExtensionVerbOnARowRunsTheToolAfterItsConfirm(t *testing.T) {
	path := seeded(t)
	bin, log := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := registered(t, path, "fake", bin)
	openExtPage(t, a, "ext/fake")
	if !slices.ContainsFunc(a.m.Notices, func(n string) bool {
		return strings.Contains(n, "fake: key rows dropped") && strings.Contains(n, "j on item")
	}) {
		t.Fatalf("no notice for the dropped j row: %q", a.m.Notices)
	}
	if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
		t.Fatal(err)
	}
	before := len(calls(t, log))
	pressJob(t, a, "X")
	modal := a.r.Instances[a.r.TopModal()]
	if modal == nil || modal.Payload.Prompt != "Finish one?" {
		t.Fatalf("no confirm: %+v", modal)
	}
	if len(calls(t, log)) != before {
		t.Fatal("the tool ran before the confirm")
	}
	pressJob(t, a, "y")
	got := calls(t, log)[before:]
	if len(got) < 2 || !strings.HasSuffix(got[0], "|item done --target i1 --json {} --yes") || !strings.HasSuffix(got[1], "|item list --json {}") {
		t.Fatalf("calls after y %q", got)
	}
	if f := frame(a); !strings.Contains(f, `item done: {"ok":true}`) {
		t.Fatalf("no status line:\n%s", f)
	}
	// j still moves the list.
	pressJob(t, a, "j")
	if a.r.Focus != "ext/fake/item/i2" {
		t.Fatalf("j: focus %s", a.r.Focus)
	}
}

// From the CLI a row's id as the TUI shows it is a --target: mtm proxies
// the tool's own id, with the arguments as JSON.
func TestExtensionCLIProxiesTheTarget(t *testing.T) {
	path := seeded(t)
	bin, log := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := registered(t, path, "fake", bin)
	out := mustCLI(t, a, "fake-item", "rename", "--target", "ext/fake/item/i2", "--to", "deux")
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("answer:\n%s", out)
	}
	got := calls(t, log)
	if last := got[len(got)-1]; !strings.HasSuffix(last, `|item rename --target i2 --json {"to":"deux"} --yes`) {
		t.Fatalf("call %q", last)
	}
	// A confirmed verb asks on the CLI too; --yes answers it.
	if _, _, err := cli(a, "n\n", "fake-item", "done", "--target", "ext/fake/item/i1"); err == nil {
		t.Fatal("declined confirm ran")
	}
	if last := calls(t, log); strings.Contains(last[len(last)-1], "item done") {
		t.Fatal("the tool ran without the confirm")
	}
	mustCLI(t, a, "fake-item", "done", "--target", "ext/fake/item/i1", "--yes")
	if last := calls(t, log); !strings.HasSuffix(last[len(last)-1], "|item done --target i1 --json {} --yes") {
		t.Fatalf("call %q", last[len(last)-1])
	}
}

// refresh reads the catalog again; remove unregisters it, and --purge also
// deletes the extension's own data file (store.OpenExtension's), never
// mtm's database.
func TestExtensionRefreshAndRemovePurge(t *testing.T) {
	path := seeded(t)
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := registered(t, path, "fake", bin)
	// The tool's catalog changes: refresh writes the new one.
	data, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "catalog.json"), []byte(strings.Replace(string(data), `"1.0"`, `"2.0"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, a, "extension", "refresh", "fake")
	var e extEntry
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "extensions", "fake.json"))
	if err != nil || json.Unmarshal(raw, &e) != nil || e.Catalog.Version != "2.0" {
		t.Fatalf("refresh: %v %+v", err, e.Catalog.Version)
	}
	ext, err := store.OpenExtension(filepath.Dir(path), "fake")
	if err != nil {
		t.Fatal(err)
	}
	ext.Close()
	file, _ := store.ExtensionPath(filepath.Dir(path), "fake")
	mustCLI(t, a, "extension", "remove", "fake", "--purge", "--yes")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("data file survived the purge: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("mtm's database: %v", err)
	}
	if out := mustCLI(t, a, "extension", "list"); strings.Contains(out, "fake") {
		t.Fatalf("still listed:\n%s", out)
	}
	if b := openAt(t, path); b.r.Instances["ext/fake"] != nil {
		t.Fatal("a removed extension is still mounted")
	}
	// In the TUI, remove closes the page at once.
	a = registered(t, path, "fake", bin)
	executeA(t, a, "extension", "remove", "", ochazuke.Arguments{"name": "fake"})
	if a.r.Instances["ext/fake"] != nil {
		t.Fatal("remove left the page")
	}
}

// A tool that declares a data page and a state verb gets that page (its
// ids under ext/<name>/) with every list's rows from one state call.
func TestExtensionDeclaredPageLoadsFromState(t *testing.T) {
	catalog := strings.Replace(strings.Replace(fakeCatalog, "VERSION", "1.0", 1), `"pages":[]`,
		`"state":"item.state","pages":[{"id":"main","title":"Fake Two","layout":"split","lists":[{"id":"items","noun":"item"}],"detail":{"fields":["size"]}}]`, 1)
	catalog = strings.Replace(catalog, `{"name":"list","kind":"application","description":"List the items"},`,
		`{"name":"list","kind":"application","description":"List the items"},{"name":"state","kind":"application"},`, 1)
	bin, log := fakeExtension(t, catalog, `"item state") printf '%s\n%s\n' '{"noun":"item","id":"s1","label":"from state","cells":["from state","open"],"data":{"size":3}}' 'not json' ;;`)
	a := registered(t, seeded(t), "two", bin)
	if a.r.PageOf("ext/two/main") == nil {
		t.Fatal("declared page not mounted")
	}
	openExtPage(t, a, "ext/two/main")
	list := a.r.List("ext/two/items")
	if list == nil || list.Len() != 1 || list.Items()[0].ID != "ext/two/items/s1" {
		t.Fatalf("state rows: %+v", list)
	}
	if got := calls(t, log); !strings.HasSuffix(got[len(got)-1], "|item state --json {}") {
		t.Fatalf("load call %q", got[len(got)-1])
	}
	if f := frame(a); !strings.Contains(f, "Fake Two") || !strings.Contains(f, "from state") || !strings.Contains(f, "size") {
		t.Fatalf("frame:\n%s", f)
	}
}

// A registry file that does not parse is a notice, never a failed start.
func TestExtensionBadRegistryFileIsANotice(t *testing.T) {
	path := seeded(t)
	dir := filepath.Join(filepath.Dir(path), "extensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := openAt(t, path)
	if !slices.ContainsFunc(a.m.Notices, func(n string) bool { return strings.HasPrefix(n, "extension: ") && strings.Contains(n, "broken.json") }) {
		t.Fatalf("notices %q", a.m.Notices)
	}
}

// Registered from the TUI (the palette), the extension's tab is there at
// once, without a restart.
func TestExtensionRegisteredInTheTUIMountsAtOnce(t *testing.T) {
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := openAt(t, seeded(t))
	if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "register", Args: ochazuke.Arguments{"name": "fake", "bin": bin}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if a.m.Status != "" || a.r.PageOf("ext/fake") == nil {
		t.Fatalf("not mounted: status %q", a.m.Status)
	}
	openExtPage(t, a, "ext/fake")
	if a.r.List("ext/fake/item").Len() != 2 {
		t.Fatal("rows not loaded")
	}
}
