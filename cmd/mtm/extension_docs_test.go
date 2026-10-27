package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// docsCatalog is o-md's catalog as far as the Docs page needs it: noun doc
// with its row source, send-notes with a pane parameter, and the Docs
// page, a split whose detail is the markdown kind over the row's path.
const docsCatalog = `{"schema":1,"name":"o-md","version":"0.2.0","nouns":[
 {"name":"doc","verbs":[
  {"name":"list","kind":"application","rows":"doc"},
  {"name":"send-notes","kind":"application","params":{"pane":{"type":"string","noun":"pane","free":true}},"positional":["pane"]}]}],
 "bindings":[],
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
