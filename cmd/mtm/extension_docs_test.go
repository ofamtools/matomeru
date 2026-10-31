package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
)

// docsCatalog is o-md's catalog as far as the Docs page needs it: noun doc
// with its row source, send-notes with a pane parameter, and the Docs
// page, a split whose detail is the markdown kind over the row's path; its
// key rows are o-md's: g s sends the notes, and the g esc that cancels the
// g prefix (ochazuke generates it for every sequence).
const docsCatalog = `{"schema":1,"name":"o-md","version":"0.2.0","nouns":[
 {"name":"doc","verbs":[
  {"name":"list","kind":"application","rows":"doc"},
  {"name":"send-notes","kind":"application","params":{"pane":{"type":"string","noun":"pane","free":true}},"positional":["pane"]}]}],
 "bindings":[
  {"mode":"normal","noun":"doc","request":{"verb":"leave","noun":"ochazuke.mode"},"keys":"g esc"},
  {"mode":"normal","noun":"doc","request":{"verb":"send-notes","noun":"doc"},"keys":"g s"}],
 "pages":[{"id":"docs","title":"Docs","layout":"split","lists":[{"id":"docs/docs","noun":"doc","title":"Docs"}],"detail":{"kind":"markdown","pathField":"path"}}]}`

// docsExtension is a fake o-md over one temp doc: doc list answers its
// row, and every call logs MTM_PROJECT_DIRS and its words.
func docsExtension(t *testing.T) (bin, log, doc string) {
	t.Helper()
	root := t.TempDir()
	doc = filepath.Join(root, "plan.md")
	if err := os.WriteFile(doc, []byte("# Plan\n\n- [ ] first task\n- [ ] second task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "o-md"), filepath.Join(dir, "calls.log")
	catalogFile, rowsFile := filepath.Join(dir, "catalog.json"), filepath.Join(dir, "rows.json")
	if err := os.WriteFile(catalogFile, []byte(docsCatalog), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := `[{"id":"` + doc + `","title":"Plan","path":"` + doc + `","root":"` + root + `","open_tasks":2,"open_notes":0}]`
	if err := os.WriteFile(rowsFile, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
echo "$MTM_PROJECT_DIRS|$*" >> '` + log + `'
case "$1 $2" in
"catalog ") cat '` + catalogFile + `' ;;
"doc list") cat '` + rowsFile + `' ;;
"doc send-notes") echo '{"sent":1}' ;;
*) echo "unknown $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log, doc
}

// The Docs page: o-md mounted as an extension, the selected doc drawn as
// the markdown editor (mtm registers the kind); tab moves into it, ]x x
// toggles and n adds a note, each written to the file at once. The page's
// loads carry the pinned project's directories.
func TestDocsPageEditsTheSelectedFile(t *testing.T) {
	t.Parallel()
	bin, log, doc := docsExtension(t)
	a := registered(t, seeded(t), "md", bin)
	mono(t, a)
	const page = "ext/md/docs"
	if a.r.PageOf(page) == nil {
		t.Fatal("Docs page not mounted")
	}
	openExtPage(t, a, page)
	if f := frame(a); !strings.Contains(f, "Docs (1)") || !strings.Contains(f, "first task") {
		t.Fatalf("Docs page:\n%s", f)
	}
	if got := calls(t, log); !strings.HasSuffix(got[len(got)-1], "|doc list --json {}") {
		t.Fatalf("load call %q", got[len(got)-1])
	}
	press(t, a, "tab")
	frame(a)
	if !strings.Contains(a.r.Focus, "/markdown/line/") {
		t.Fatalf("tab focus %s", a.r.Focus)
	}
	press(t, a, "]", "x", "x")
	if text := readFile(t, doc); !strings.Contains(text, "- [x] first task") {
		t.Fatalf("toggle not written:\n%s", text)
	}
	press(t, a, "n")
	for _, k := range "looks good" {
		key := string(k)
		if k == ' ' {
			key = "space"
		}
		press(t, a, key)
	}
	press(t, a, "esc")
	frame(a)
	if text := readFile(t, doc); !strings.Contains(text, ": looks good") || !strings.Contains(text, "> **") {
		t.Fatalf("note not written:\n%s", text)
	}

	// The pinned project's directories reach the next load.
	projDir := t.TempDir()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "directory", Target: "matomeru", Args: ochazuke.Arguments{"path": projDir}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	a.pin = "matomeru"
	want := strings.Join(a.projectDirsOf(a.pin), string(os.PathListSeparator))
	if !strings.Contains(want, projDir) {
		t.Fatalf("project dirs %q lack %s", want, projDir)
	}
	a.reloadExtension(a.extension("md"))
	settleJobs(t, a, a.r.PendingCommands())
	if got := calls(t, log); !strings.HasPrefix(got[len(got)-1], want+"|doc list") {
		t.Fatalf("pinned load %q, want MTM_PROJECT_DIRS %q", got[len(got)-1], want)
	}
}

// doc send-notes without a pane, from the editor (the row stand-in acts
// for the selected doc), opens mtm's pane chooser instead of failing.
func TestDocsSendNotesAsksForAPane(t *testing.T) {
	t.Parallel()
	bin, log, _ := docsExtension(t)
	a := registered(t, seeded(t), "md", bin)
	openExtPage(t, a, "ext/md/docs")
	press(t, a, "tab")
	frame(a)
	if _, err := a.r.Execute(ochazuke.Request{Noun: "md-doc", Verb: "send-notes"}); err != nil {
		t.Fatal(err)
	}
	top := a.r.Instances[a.r.TopModal()]
	if top == nil || top.Noun != "ext-pane" {
		t.Fatalf("no chooser: top %+v", top)
	}
	for _, c := range calls(t, log) {
		if strings.Contains(c, "send-notes") {
			t.Fatalf("ran without a pane: %q", c)
		}
	}
}

// g s, the catalog's key row, answers on the doc list and in the markdown
// editor: mtm's pane chooser opens for the pane parameter, and the chosen
// pane reaches doc send-notes --target DOC. The row is not dropped, and
// neither is its generated g esc (a mounted prefix brings its own back).
func TestDocsGSSendsNotesFromTheListAndTheEditor(t *testing.T) {
	t.Parallel()
	bin, log, doc := docsExtension(t)
	a := registered(t, seeded(t), "md", bin)
	a.panes = []pane{{Pane: daemon.Pane{ID: "w9", Name: "worker"}, Session: "s"}}
	openExtPage(t, a, "ext/md/docs")
	if slices.ContainsFunc(a.m.Notices, func(n string) bool { return strings.Contains(n, "key rows dropped") }) {
		t.Fatalf("a key row was dropped: %q", a.m.Notices)
	}
	// The g prefix cancels with esc (the cancel loading g s brought back).
	press(t, a, "g", "esc")
	if a.r.Mode.Name != "normal" || a.r.TopModal() != "" {
		t.Fatalf("g esc: mode %q modal %q", a.r.Mode.Name, a.r.TopModal())
	}
	for _, where := range []string{"list", "editor"} {
		if where == "editor" {
			press(t, a, "tab")
		}
		frame(a)
		if inEditor := strings.Contains(a.r.Focus, "/markdown/line/"); inEditor != (where == "editor") {
			t.Fatalf("%s: focus %s", where, a.r.Focus)
		}
		before := len(calls(t, log))
		press(t, a, "g", "s")
		top := a.r.Instances[a.r.TopModal()]
		if top == nil || top.Noun != "ext-pane" {
			t.Fatalf("%s: g s opened no pane chooser: top %+v focus %s", where, top, a.r.Focus)
		}
		pressJob(t, a, "enter")
		got := calls(t, log)[before:]
		want := "|doc send-notes --target " + doc + ` --json {"pane":"w9"} --yes`
		if !slices.ContainsFunc(got, func(c string) bool { return strings.HasSuffix(c, want) }) {
			t.Fatalf("%s: calls %q, want one ending %q", where, got, want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An extension row clashes with mtm's when one key sequence is a prefix of
// the other; sharing only a prefix is fine.
func TestKeysClash(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		want bool
	}{{"g s", "g g", false}, {"g", "g g", true}, {"g g x", "g g", true}, {"j", "j", true}, {"X", "j", false}} {
		if got := keysClash(strings.Fields(c.a), strings.Fields(c.b)); got != c.want {
			t.Errorf("keysClash(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
