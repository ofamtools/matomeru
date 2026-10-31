package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// A path's project is held on a path-component boundary; among several
// projects holding it the pinned one wins, else the longest directory; ~
// is expanded on both sides, and a directory attached through a symlink
// also holds the paths it resolves to.
func TestPathProjectPinThenLongest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	for _, d := range []string{filepath.Join(home, "proj", "h"), real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	a := &App{projects: []work.Project{
		{ID: "outer", DefaultTree: "/a"},
		{ID: "inner", Trees: []work.Tree{{Kind: "dir", Path: "/a/inner/"}}},
		{ID: "foo", DefaultTree: "/x/foo"},
		{ID: "tilde", DefaultTree: "~/proj/h"},
		{ID: "linked", Trees: []work.Tree{{Kind: "dir", Path: link}}},
	}}
	a.home = func() (string, error) { return home, nil }
	a.refreshProjectPrefixes()
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"/a":                       "outer",
		"/a/notes.md":              "outer",
		"/a/inner":                 "inner",
		"/a/inner/docs/plan.md":    "inner",
		"/a/innerx/plan.md":        "outer",
		"/x/foo/README.md":         "foo",
		"/x/foobar/README.md":      "",
		"/x":                       "",
		"/elsewhere/a/notes.md":    "",
		"/a/../x/foo/z.md":         "foo",
		"~/proj/h/README.md":       "tilde",
		home + "/proj/h/docs/x.md": "tilde",
		"~/proj/hh/README.md":      "",
		link + "/doc.md":           "linked",
		resolved + "/doc.md":       "linked",
	}
	for path, want := range cases {
		if got := a.pathProject(path); got != want {
			t.Errorf("no pin: pathProject(%q) = %q, want %q", path, got, want)
		}
	}
	// Pinned outer: it wins inside inner's directory too; elsewhere the
	// longest still does.
	a.pin = "outer"
	for path, want := range map[string]string{
		"/a/inner/docs/plan.md": "outer",
		"/a/notes.md":           "outer",
		"/x/foo/README.md":      "foo",
		"/x/foobar/README.md":   "",
	} {
		if got := a.pathProject(path); got != want {
			t.Errorf("pinned outer: pathProject(%q) = %q, want %q", path, got, want)
		}
	}
	// Pinned foo, which holds none of them: the longest wins.
	a.pin = "foo"
	if got := a.pathProject("/a/inner/docs/plan.md"); got != "inner" {
		t.Errorf("pinned foo: %q, want inner", got)
	}

	for field, want := range map[string]string{"path": "/p", "dir": "/d", "cwd": "/c", "repo": "/r", "root": "/o"} {
		if got, ok := rowPath(map[string]any{field: want, "title": "x"}); !ok || got != want {
			t.Errorf("rowPath(%s) = %q %v", field, got, ok)
		}
	}
	if got, _ := rowPath(map[string]any{"root": "/o", "path": "/p", "dir": "/d"}); got != "/p" {
		t.Errorf("rowPath order: %q, want /p", got)
	}
	if _, ok := rowPath(map[string]any{"title": "x", "path": ""}); ok {
		t.Error("an empty path field is no path")
	}
	if _, ok := rowPath("scalar"); ok {
		t.Error("a scalar row has no path")
	}
}

// docsScopeCatalog is docsCatalog with doc new.
var docsScopeCatalog = strings.Replace(docsCatalog, `{"name":"list","kind":"application","rows":"doc"},`,
	`{"name":"list","kind":"application","rows":"doc"},
  {"name":"new","kind":"application","params":{"name":{"type":"string"}},"positional":["name"]},
  {"name":"touch","kind":"application"},`, 1)

// scopedDocsExtension is a fake o-md answering four docs as JSON Lines:
// one in each of two project directories, one in no project's, one
// without any path. doc new NAME makes NAME.md under the first of
// MTM_PROJECT_DIRS (else the temp root) and answers its row, which the
// list then holds; every call logs MTM_PROJECT, MTM_PROJECT_LABEL and
// MTM_PROJECT_DIRS.
func scopedDocsExtension(t *testing.T) (bin, log, root string) {
	t.Helper()
	root = t.TempDir()
	mtmDir, ochazukeDir := filepath.Join(root, "matomeru"), filepath.Join(root, "ochazuke")
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "o-md"), filepath.Join(dir, "calls.log")
	catalogFile, rowsFile := filepath.Join(dir, "catalog.json"), filepath.Join(dir, "rows.jsonl")
	if err := os.WriteFile(catalogFile, []byte(docsScopeCatalog), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := `{"id":"mine","title":"Mine","path":"` + mtmDir + `/docs/plan.md"}
{"id":"theirs","title":"Theirs","root":"` + ochazukeDir + `"}
{"id":"stray","title":"Stray","path":"` + root + `/matomeru-old/x.md"}
{"id":"loose","title":"Loose","open_tasks":1}
`
	if err := os.WriteFile(rowsFile, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
echo "$MTM_PROJECT|$MTM_PROJECT_LABEL|$MTM_PROJECT_DIRS|$*" >> '` + log + `'
case "$1 $2" in
"catalog ") cat '` + catalogFile + `' ;;
"doc list") cat '` + rowsFile + `' ;;
"doc touch") echo '{"touched":true}' ;;
"doc new")
  name=$(printf '%s' "$*" | sed 's/.*"name":"\([^"]*\)".*/\1/')
  d=$(printf '%s' "$MTM_PROJECT_DIRS" | cut -d: -f1)
  [ -n "$d" ] || d='` + root + `/loose'
  mkdir -p "$d" && printf '# %s\n' "$name" > "$d/$name.md"
  row="{\"id\":\"$d/$name.md\",\"title\":\"$name\",\"path\":\"$d/$name.md\"}"
  echo "$row" >> '` + rowsFile + `'
  echo "$row" ;;
*) echo "unknown $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log, root
}

// scopeApp is mtm with the fake o-md registered and the seeded projects
// at root/ochazuke and root/matomeru.
func scopeApp(t *testing.T) (a *App, log, root string) {
	t.Helper()
	bin, log, root := scopedDocsExtension(t)
	a = registered(t, seeded(t), "md", bin)
	mono(t, a)
	for _, p := range []work.Project{
		{ID: "ochazuke", Name: "ochazuke", Created: 1, DefaultTree: filepath.Join(root, "ochazuke")},
		{ID: "matomeru", Name: "matomeru", Created: 2, DefaultTree: filepath.Join(root, "matomeru")},
	} {
		if err := a.work.PutProject(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	return a, log, root
}

const docsList = "ext/md/docs/docs"

// docsShown are the Docs list's rows as drawn: id -> its cells.
func docsShown(t *testing.T, a *App) map[string][]string {
	t.Helper()
	_, v := a.extViewOf("ext/md/docs")
	if v == nil {
		t.Fatal("Docs page not mounted")
	}
	out := map[string][]string{}
	for _, item := range v.List(docsList).Items() {
		out[strings.TrimPrefix(item.ID, docsList+"/")] = item.Cells
	}
	return out
}

func shownIDs(t *testing.T, a *App) []string {
	t.Helper()
	rows := docsShown(t, a)
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func projectCell(t *testing.T, a *App, id string) string {
	t.Helper()
	cells := docsShown(t, a)[id]
	if len(cells) != 2 {
		t.Fatalf("%s cells %q", id, cells)
	}
	return cells[1]
}

// focusOn opens the page holding id and puts the focus on it.
func focusOn(t *testing.T, a *App, id string) {
	t.Helper()
	path := a.r.InstancePath(id)
	if len(path) < 2 {
		t.Fatalf("no instance %s", id)
	}
	if page := path[1]; a.r.PageOf(page) != nil && a.r.Page != page {
		if err := a.r.OpenPage(page); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.r.SetFocus(id); err != nil {
		t.Fatal(err)
	}
}

func pin(t *testing.T, a *App, project string) {
	t.Helper()
	req := ochazuke.Request{Noun: "project", Verb: "unpin"}
	if project != "" {
		req = ochazuke.Request{Noun: "project", Verb: "pin", Args: ochazuke.Arguments{"project": project}}
	}
	if _, err := a.r.Execute(req); err != nil {
		t.Fatal(err)
	}
}

// The context project is the innermost project the focus stack names: a
// project row, a task row, a note, a pane row, the live pane, an
// extension row's recorded owner, an open modal's return focus; else the
// pin, else none.
func TestContextProjectWalksTheFocusStack(t *testing.T) {
	t.Parallel()
	a, _, root := scopeApp(t)
	focus := func(id string) {
		t.Helper()
		focusOn(t, a, id)
	}
	want := func(what, project string) {
		t.Helper()
		if got := a.contextProject(); got != project {
			t.Errorf("%s: context project %q, want %q", what, got, project)
		}
	}
	// No project on the stack, no pin: none; with a pin: the pin.
	openExtPage(t, a, "ext/md/docs")
	focus(docsList + "/loose")
	want("unrecorded ext row, no pin", "")
	pin(t, a, "ochazuke")
	focus(docsList + "/loose")
	want("unrecorded ext row, pinned", "ochazuke")
	pin(t, a, "")

	// An extension row with a recorded owner.
	if err := a.work.SetExtRowOwner(work.ExtRowOwner{Extension: "md", Noun: "doc", Row: "loose", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil { // owners read again
		t.Fatal(err)
	}
	focus(docsList + "/loose")
	want("recorded ext row", "matomeru")
	// A path match names its project too, over the pin.
	focus(docsList + "/theirs")
	want("derived ext row", "ochazuke")
	pin(t, a, "matomeru")
	scopeAll := func() {
		t.Helper()
		if _, err := a.r.Execute(ochazuke.Request{Noun: "scope", Verb: "all"}); err != nil {
			t.Fatal(err)
		}
	}
	scopeAll() // S shows theirs and stray beside the pin
	focus(docsList + "/theirs")
	want("derived ext row under another pin", "ochazuke")
	focus(docsList + "/stray")
	want("ext row whose path no project holds, pinned", "matomeru")
	scopeAll()
	pin(t, a, "")

	// A project row.
	focus("ochazuke")
	want("project row", "ochazuke")
	// A modal's return focus: the picker over the project row.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "pick"}); err != nil {
		t.Fatal(err)
	}
	if a.r.TopModal() == "" {
		t.Fatal("no picker")
	}
	want("picker over the project row", "ochazuke")
	press(t, a, "esc")

	// A task row: its task's project.
	if _, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "project", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	focus("t1")
	want("task row", "matomeru")

	// A note: its owner's project.
	note := work.Note{ID: "n1", Text: "a note", ProjectID: "ochazuke", Created: 1, Updated: 1}
	if err := a.work.PutNote(note); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	focus("n1")
	want("note", "ochazuke")

	// A pane row and the live pane: the pane's owner.
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "work", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	w1 := pane{Session: "work", SessionID: "s1", Pane: daemon.Pane{ID: "w1", Name: "Terminal 1a2b3c4d", Cwd: root}}
	a.setPanes([]pane{w1}, nil)
	focus(w1.rowID())
	want("pane row", "ochazuke")
	a.attached = w1
	a.r.Instance(paneTermID, ochazuke.Parent(panesPage))
	focus(paneTermID)
	want("live pane", "ochazuke")
}

// doc new from a pane of project X (no pin): the exec is told X and X's
// directories, the doc is made there, and its row is recorded as X's.
// Acting on a row with no owner records the context project too; a
// recorded row keeps its owner.
func TestDocNewFromAPaneIsTheProjectsDoc(t *testing.T) {
	t.Parallel()
	a, log, root := scopeApp(t)
	openExtPage(t, a, "ext/md/docs")
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "work", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	w1 := pane{Session: "work", SessionID: "s1", Pane: daemon.Pane{ID: "w1", Name: "Terminal 1a2b3c4d", Cwd: root}}
	a.setPanes([]pane{w1}, nil)
	focusOn(t, a, w1.rowID())
	if _, err := a.r.Execute(ochazuke.Request{Noun: "md-doc", Verb: "new", Args: ochazuke.Arguments{"name": "fresh"}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	dir := filepath.Join(root, "ochazuke")
	doc := filepath.Join(dir, "fresh.md")
	if _, err := os.Stat(doc); err != nil {
		t.Fatalf("doc not made under ochazuke's directory: %v", err)
	}
	var newCall string
	for _, c := range calls(t, log) {
		if strings.Contains(c, "|doc new") {
			newCall = c
		}
	}
	if !strings.HasPrefix(newCall, "ochazuke|ochazuke|"+dir+"|") {
		t.Fatalf("doc new told %q", newCall)
	}
	if got := a.extRowProject("md", "doc", doc); got != "ochazuke" {
		t.Fatalf("recorded owner %q", got)
	}
	if got := projectCell(t, a, doc); got != "ochazuke" {
		t.Fatalf("new doc's project cell %q (recorded: no mark)", got)
	}
	// Recorded beats path: attaching the doc's directory to matomeru (the
	// longer prefix) does not move it.
	if err := a.work.PutProject(work.Project{ID: "matomeru", Name: "matomeru", Created: 2, DefaultTree: filepath.Join(root, "matomeru"), Trees: []work.Tree{{Kind: "dir", Path: dir}}}); err != nil {
		t.Fatal(err)
	}
	pin(t, a, "matomeru")
	if slices.Contains(shownIDs(t, a), doc) {
		t.Fatal("matomeru's pin shows ochazuke's recorded doc")
	}
	pin(t, a, "")

	// Acting on stray (no owner) from the ochazuke project row records it.
	focusOn(t, a, "ochazuke")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "md-doc", Verb: "touch", Target: docsList + "/stray"}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if got := a.extRowProject("md", "doc", "stray"); got != "ochazuke" {
		t.Fatalf("acted-on row owner %q", got)
	}
	// Acting on it again from matomeru keeps ochazuke.
	focusOn(t, a, "matomeru")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "md-doc", Verb: "touch", Target: docsList + "/stray"}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if got := a.extRowProject("md", "doc", "stray"); got != "ochazuke" {
		t.Fatalf("recorded owner moved to %q", got)
	}
}

// No context project: the doc is made, unassociated by record (its path
// may still tell its project), and nothing is refused.
func TestDocNewWithNoProjectRecordsNothing(t *testing.T) {
	t.Parallel()
	a, log, root := scopeApp(t)
	openExtPage(t, a, "ext/md/docs")
	focusOn(t, a, docsList+"/loose") // a row with no project
	if _, err := a.r.Execute(ochazuke.Request{Noun: "md-doc", Verb: "new", Args: ochazuke.Arguments{"name": "nowhere"}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	doc := filepath.Join(root, "loose", "nowhere.md")
	if _, err := os.Stat(doc); err != nil {
		t.Fatalf("doc not made: %v", err)
	}
	if got := calls(t, log); !strings.HasPrefix(got[len(got)-2], "|||doc new") {
		t.Fatalf("doc new told %q", got[len(got)-2])
	}
	if got := a.extRowProject("md", "doc", doc); got != "" {
		t.Fatalf("recorded %q with no project", got)
	}
	if got := projectCell(t, a, doc); got != "unassociated ·path" {
		t.Fatalf("project cell %q", got)
	}
}

// The Docs page end to end: recorded owners, derived owners (marked),
// unassociated rows and rows without a path; the pin shows its own rows
// and the ones without a path; S and no pin show every row; attaching a
// directory moves a derived row, never a recorded one; ext.md.scope off
// draws the rows as the tool answered them.
func TestDocsRowsFollowThePinnedProject(t *testing.T) {
	t.Parallel()
	a, _, root := scopeApp(t)
	// theirs sits in ochazuke's directory but was recorded as matomeru's.
	if err := a.work.SetExtRowOwner(work.ExtRowOwner{Extension: "md", Noun: "doc", Row: "theirs", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	openExtPage(t, a, "ext/md/docs")
	if got := shownIDs(t, a); !slices.Equal(got, []string{"loose", "mine", "stray", "theirs"}) {
		t.Fatalf("no pin shows %v", got)
	}
	for id, want := range map[string]string{"mine": "matomeru ·path", "theirs": "matomeru", "stray": "unassociated ·path", "loose": "unassociated"} {
		if got := projectCell(t, a, id); got != want {
			t.Errorf("%s project %q, want %q", id, got, want)
		}
	}
	_, v := a.extViewOf("ext/md/docs")
	cols := v.List(docsList).Columns()
	if len(cols) != 2 || cols[0].Title != "doc" || cols[1].Title != "project" {
		t.Fatalf("columns %+v", cols)
	}
	if f := frame(a); !strings.Contains(f, "project") || !strings.Contains(f, "matomeru ·path") {
		t.Fatalf("Docs page:\n%s", f)
	}

	// The pin: its rows (recorded and derived) and the rows with no path.
	pin(t, a, "matomeru")
	if got := shownIDs(t, a); !slices.Equal(got, []string{"loose", "mine", "theirs"}) {
		t.Fatalf("pinned matomeru shows %v", got)
	}
	pin(t, a, "ochazuke")
	if got := shownIDs(t, a); !slices.Equal(got, []string{"loose"}) {
		t.Fatalf("pinned ochazuke shows %v (theirs is recorded as matomeru's)", got)
	}
	pin(t, a, "matomeru")
	// A reload under the pin is scoped as it lands.
	a.reloadExtension(a.extension("md"))
	settleJobs(t, a, a.r.PendingCommands())
	if got := shownIDs(t, a); !slices.Equal(got, []string{"loose", "mine", "theirs"}) {
		t.Fatalf("reloaded under the pin: %v", got)
	}
	// S: every project, the pin kept; S again: back to the pin.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "scope", Verb: "all"}); err != nil {
		t.Fatal(err)
	}
	if got := shownIDs(t, a); len(got) != 4 {
		t.Fatalf("all projects shows %v", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "scope", Verb: "all"}); err != nil {
		t.Fatal(err)
	}

	// Attaching matomeru-old to ochazuke moves stray (derived) to it;
	// theirs (recorded) stays matomeru's.
	if err := a.work.PutProject(work.Project{ID: "ochazuke", Name: "ochazuke", Created: 1, DefaultTree: filepath.Join(root, "ochazuke"), Trees: []work.Tree{{Kind: "dir", Path: filepath.Join(root, "matomeru-old")}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	pin(t, a, "ochazuke")
	if got := projectCell(t, a, "stray"); got != "ochazuke ·path" {
		t.Fatalf("stray after attaching: %q", got)
	}
	if got := shownIDs(t, a); !slices.Equal(got, []string{"loose", "stray"}) {
		t.Fatalf("pinned ochazuke after attaching: %v", got)
	}

	// Off: the rows as the tool answers them, no project column, the pin
	// filters nothing.
	if err := a.r.Settings().Set(extScopeSetting("md"), extScopeOff); err != nil {
		t.Fatal(err)
	}
	if got := shownIDs(t, a); len(got) != 4 {
		t.Fatalf("scope off shows %v", got)
	}
	if cols := v.List(docsList).Columns(); len(cols) != 0 {
		t.Fatalf("scope off columns %+v", cols)
	}
	for id, cells := range docsShown(t, a) {
		if len(cells) != 0 {
			t.Errorf("scope off: %s cells %q", id, cells)
		}
	}
	// Off records nothing either.
	focusOn(t, a, "matomeru")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "md-doc", Verb: "touch", Target: docsList + "/loose"}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if got := a.extRowProject("md", "doc", "loose"); got != "" {
		t.Fatalf("scope off recorded %q", got)
	}
	if err := a.r.Settings().Set(extScopeSetting("md"), extScopeAuto); err != nil {
		t.Fatal(err)
	}
	if got := shownIDs(t, a); !slices.Equal(got, []string{"loose", "stray"}) {
		t.Fatalf("scope auto again: %v", got)
	}

	// Unpinned: every row.
	pin(t, a, "")
	if got := shownIDs(t, a); len(got) != 4 {
		t.Fatalf("unpinned shows %v", got)
	}
}

// A deleted project leaves the rows recorded as its own unassociated.
func TestExtRowOwnerClearedWithItsProject(t *testing.T) {
	t.Parallel()
	a, _, _ := scopeApp(t)
	if err := a.work.SetExtRowOwner(work.ExtRowOwner{Extension: "md", Noun: "doc", Row: "loose", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.work.Delete("project", "ochazuke"); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	if got := a.extRowProject("md", "doc", "loose"); got != "" {
		t.Fatalf("owner after delete %q", got)
	}
	if got := a.extRowOwners[work.ExtRowKey("md", "doc", "loose")].FormerProjectID; got != "ochazuke" {
		t.Fatalf("former project %q", got)
	}
}

// The scope setting is declared per extension, auto by default, in the
// extension's own section of the Settings page.
func TestExtScopeSettingDeclaredPerExtension(t *testing.T) {
	t.Parallel()
	a, _, _ := scopeApp(t)
	s, err := a.r.Settings().Get("ext.md.scope")
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != extScopeAuto || !slices.Equal(s.Enum, []string{extScopeAuto, extScopeOff}) {
		t.Fatalf("ext.md.scope %+v", s)
	}
	if err := a.r.Settings().Set("ext.md.scope", "sometimes"); err == nil {
		t.Fatal("a value outside auto and off was taken")
	}
}

// lastListCall is the project context the last doc list was told.
func lastListCall(t *testing.T, log string) string {
	t.Helper()
	got := ""
	for _, c := range calls(t, log) {
		if ctx, ok := strings.CutSuffix(c, "|doc list --json {}"); ok {
			got = ctx
		}
	}
	return got
}

// Page loads are told the pin's project, never the focus's: moving
// between rows recorded to different projects never changes what a reload
// asks for; S (all projects) and no pin tell none.
func TestDocsReloadIgnoresTheCursor(t *testing.T) {
	t.Parallel()
	a, log, root := scopeApp(t)
	for row, project := range map[string]string{"mine": "matomeru", "theirs": "ochazuke"} {
		if err := a.work.SetExtRowOwner(work.ExtRowOwner{Extension: "md", Noun: "doc", Row: row, ProjectID: project}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	openExtPage(t, a, "ext/md/docs")
	reloadOn := func(row string) string {
		t.Helper()
		focusOn(t, a, docsList+"/"+row)
		a.reloadExtension(a.extension("md"))
		settleJobs(t, a, a.r.PendingCommands())
		return lastListCall(t, log)
	}
	for _, row := range []string{"mine", "theirs", "loose"} {
		if got := reloadOn(row); got != "||" {
			t.Fatalf("no pin, on %s: doc list told %q", row, got)
		}
	}
	mtmDir := filepath.Join(root, "matomeru")
	pin(t, a, "matomeru")
	for _, row := range []string{"mine", "loose", "mine"} {
		if got := reloadOn(row); got != "matomeru|matomeru|"+mtmDir {
			t.Fatalf("pinned, on %s: doc list told %q", row, got)
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "scope", Verb: "all"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"mine", "theirs", "loose"} {
		if got := reloadOn(row); got != "||" {
			t.Fatalf("all projects, on %s: doc list told %q", row, got)
		}
	}
	// A verb still runs with the context project: the row's own.
	focusOn(t, a, docsList+"/theirs")
	if got := a.contextProject(); got != "ochazuke" {
		t.Fatalf("verb context on theirs %q", got)
	}
}

// Acting on a doc from its own row records the row's path project, not
// the pin: pin matomeru, focus a doc under ochazuke's directory, act.
func TestActingOnADerivedDocRecordsItsPathProject(t *testing.T) {
	t.Parallel()
	a, log, root := scopeApp(t)
	pin(t, a, "matomeru")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "scope", Verb: "all"}); err != nil { // theirs is shown
		t.Fatal(err)
	}
	openExtPage(t, a, "ext/md/docs")
	focusOn(t, a, docsList+"/theirs")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "md-doc", Verb: "touch"}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if got := a.extRowProject("md", "doc", "theirs"); got != "ochazuke" {
		t.Fatalf("theirs recorded to %q, want ochazuke", got)
	}
	var touch string
	for _, c := range calls(t, log) {
		if strings.Contains(c, "|doc touch") {
			touch = c
		}
	}
	if want := "ochazuke|ochazuke|" + filepath.Join(root, "ochazuke") + "|doc touch --target theirs"; !strings.HasPrefix(touch, want) {
		t.Fatalf("doc touch told %q, want %q", touch, want)
	}
	if got := projectCell(t, a, "theirs"); got != "ochazuke" {
		t.Fatalf("theirs now reads %q (recorded: no mark)", got)
	}
}
