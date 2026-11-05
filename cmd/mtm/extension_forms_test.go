package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"ofam.dev/ochazuke"
)

// crudCatalog is a tool with a thing noun that has new, edit and delete
// verbs (or, with aliases, create, update and remove); its own n row
// would shadow mtm's new and is dropped.
const crudCatalog = `{"schema":1,"name":"o-fake","version":"1.0","nouns":[
 {"name":"thing","columns":[{"title":"thing"}],"verbs":[
  {"name":"list","kind":"application"},
  {"name":"NEW","kind":"application","params":{"title":{"type":"string","required":true},"size":{"type":"integer"}},"positional":["title"]},
  {"name":"EDIT","kind":"application","params":{"title":{"type":"string"},"size":{"type":"integer"}},"positional":["title"]},
  {"name":"DELETE","kind":"application"}]}],
 "bindings":[{"noun":"thing","mode":"normal","keys":"n","request":{"noun":"thing","verb":"list"}}],
 "pages":[]}`

// crudArms answer the thing verbs; list rows carry data.
const crudArms = `"thing list") echo '[{"id":"t1","label":"one","cells":["one"],"data":{"title":"one","size":1}},{"id":"t2","label":"two","cells":["two"],"data":{"title":"two"}}]' ;;
"thing new"|"thing create") echo '{"id":"t3"}' ;;
"thing edit"|"thing update"|"thing delete"|"thing remove") echo '{"ok":true}' ;;`

func crudCatalogNamed(newV, editV, deleteV string) string {
	return strings.NewReplacer("NEW", newV, "EDIT", editV, "DELETE", deleteV).Replace(crudCatalog)
}

// crudApp registers the CRUD tool and opens its page.
func crudApp(t *testing.T, catalog string) (*App, string) {
	t.Helper()
	bin, log := fakeExtension(t, catalog, crudArms)
	a := registered(t, seeded(t), "fake", bin)
	openExtPage(t, a, "ext/fake")
	if l := a.r.List("ext/fake/thing"); l == nil || l.Len() != 2 {
		t.Fatalf("rows not loaded: %+v", l)
	}
	return a, log
}

// since is the calls the fake tool logged after n earlier ones, without
// the row reloads.
func since(t *testing.T, log string, n int) []string {
	t.Helper()
	var out []string
	for _, c := range calls(t, log)[n:] {
		if !strings.HasSuffix(c, "|thing list --json {}") {
			_, words, _ := strings.Cut(c[strings.Index(c, "|")+1:], "|")
			out = append(out, words)
		}
	}
	return out
}

// i on a row opens a form of the tool's edit verb prefilled from the
// row's data; a change is written by running edit on the row, and the
// form leaves with esc.
func TestExtensionEditFormWritesThroughTheToolsEditVerb(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, crudCatalogNamed("new", "edit", "delete"))
	if err := a.r.SetFocus("ext/fake/thing/t1"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "i")
	form := a.r.RecordEditor(extFormID)
	if form == nil || a.r.TopModal() != extFormID {
		t.Fatalf("no form: modal %q", a.r.TopModal())
	}
	if v := form.Values(); v["title"] != "one" || v["size"] != "1" {
		t.Fatalf("prefill %v", v)
	}
	if a.r.Focus != extFormID+"/title" || a.r.Mode.Name != "insert" {
		t.Fatalf("focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	n := len(calls(t, log))
	typeText(t, a, "!")
	pressJob(t, a, "esc") // insert ends: written at once
	if got := since(t, log, n); len(got) != 1 || got[0] != `thing edit --target t1 --json {"title":"one!"} --yes` {
		t.Fatalf("calls %q", got)
	}
	pressJob(t, a, "esc") // leaves
	if a.r.Instances[extFormID] != nil || a.extForm != nil || a.r.Focus != "ext/fake/thing/t1" {
		t.Fatalf("form left: focus %s", a.r.Focus)
	}
}

// n on the list opens a form of the new verb that runs once: typing and
// leaving fields write nothing, form.run (enter) refuses until every
// required field is filled, then runs the new verb once with the values;
// the id the tool answered opens an edit form whose changes run the edit
// verb as they are made.
func TestExtensionNewFormCreatesOnRunThenEdits(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, crudCatalogNamed("create", "update", "remove"))
	if err := a.r.SetFocus("ext/fake/thing/t2"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "n")
	form := a.r.RecordEditor(extFormID)
	if form == nil || a.r.Focus != extFormID+"/title" {
		t.Fatalf("no form at its title: focus %s", a.r.Focus)
	}
	if v := ansi.Strip(a.View().Content); !strings.Contains(v, "New thing · created when run") || strings.Contains(v, "written as you type") {
		t.Fatalf("title not in view:\n%s", v)
	}
	n := len(calls(t, log))
	// size first: title is required, so the form cannot run yet.
	pressJob(t, a, "tab")
	typeText(t, a, "7")
	pressJob(t, a, "esc")
	pressJob(t, a, "k")
	if form.Runnable() {
		t.Fatal("runnable without its title")
	}
	pressJob(t, a, "a")
	typeText(t, a, "three")
	pressJob(t, a, "esc")
	if got := since(t, log, n); len(got) != 0 {
		t.Fatalf("ran before form.run: %q", got)
	}
	if form.WriteError() != "" {
		t.Fatalf("write error %q", form.WriteError())
	}
	pressJob(t, a, "enter")
	got := since(t, log, n)
	if len(got) != 1 || got[0] != `thing create --json {"size":7,"title":"three"} --yes` {
		t.Fatalf("create calls %q", got)
	}
	edit := a.r.RecordEditor(extFormID)
	if edit == nil || edit == form || a.extForm != edit {
		t.Fatal("no edit form after create")
	}
	if v := edit.Values(); v["title"] != "three" || v["size"] != "7" {
		t.Fatalf("edit form values %v", v)
	}
	pressJob(t, a, "esc")
	pressJob(t, a, "j")
	pressJob(t, a, "a")
	typeText(t, a, "0")
	pressJob(t, a, "esc")
	got = since(t, log, n)
	if len(got) != 2 || got[1] != `thing update --target t3 --json {"size":70} --yes` {
		t.Fatalf("update calls %q", got)
	}
}

// optionalNewCatalog is crudCatalog whose new verb requires nothing (as
// md doc new: every parameter optional) and, with noEdit, has no edit
// verb.
func optionalNewCatalog(noEdit bool) string {
	editV := "edit"
	if noEdit {
		editV = "touch"
	}
	return strings.Replace(crudCatalogNamed("new", editV, "delete"), `"required":true`, `"required":false`, 1)
}

// A new verb with no required parameters is runnable at once, yet typing
// a name runs nothing: no half-typed row (a file, a directory) is made.
// Leaving the form creates nothing.
func TestExtensionNewFormWithNoRequiredParamsRunsNothingAsYouType(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, optionalNewCatalog(false))
	if err := a.r.SetFocus("ext/fake/thing/t2"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "n")
	form := a.r.RecordEditor(extFormID)
	if form == nil {
		t.Fatal("no form")
	}
	n := len(calls(t, log))
	for _, s := range []string{"m", "a", "y"} {
		typeText(t, a, s)
		settleJobs(t, a, nil)
	}
	pressJob(t, a, "esc") // insert ends
	pressJob(t, a, "j")
	if got := since(t, log, n); len(got) != 0 {
		t.Fatalf("ran as you type: %q", got)
	}
	pressJob(t, a, "esc") // leaves
	if a.r.Instances[extFormID] != nil || a.extForm != nil {
		t.Fatal("form still open")
	}
	if got := since(t, log, n); len(got) != 0 {
		t.Fatalf("leaving created: %q", got)
	}
}

// With no edit verb, run creates the row once and the form stays closed:
// no later keystroke errors that it cannot be written.
func TestExtensionNewFormWithoutEditVerbClosesAfterCreating(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, optionalNewCatalog(true))
	if err := a.r.SetFocus("ext/fake/thing/t2"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "n")
	n := len(calls(t, log))
	typeText(t, a, "ma")
	pressJob(t, a, "enter")
	if got := since(t, log, n); len(got) != 1 || got[0] != `thing new --json {"title":"ma"} --yes` {
		t.Fatalf("calls %q", got)
	}
	if a.r.Instances[extFormID] != nil || a.extForm != nil {
		t.Fatal("form still open after create without an edit verb")
	}
}

// d d runs the delete verb on the row after a confirm (mtm's own when the
// tool declares none); the tool's n row is dropped with a notice, since n
// is mtm's new.
func TestExtensionDeleteKeyConfirmsAndRunsTheTool(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, crudCatalogNamed("new", "edit", "delete"))
	if !slices.ContainsFunc(a.m.Notices, func(s string) bool {
		return strings.Contains(s, "key rows dropped") && strings.Contains(s, "n on thing")
	}) {
		t.Fatalf("notices %q", a.m.Notices)
	}
	if err := a.r.SetFocus("ext/fake/thing/t2"); err != nil {
		t.Fatal(err)
	}
	n := len(calls(t, log))
	press(t, a, "d", "d") // no settling between: the sequence's timer would run out
	modal := a.r.Instances[a.r.TopModal()]
	if modal == nil || modal.Payload.Prompt != "Delete two?" {
		t.Fatalf("no confirm: %+v", modal)
	}
	if len(since(t, log, n)) != 0 {
		t.Fatal("deleted before the confirm")
	}
	pressJob(t, a, "y")
	if got := since(t, log, n); len(got) != 1 || got[0] != "thing delete --target t2 --json {} --yes" {
		t.Fatalf("calls %q", got)
	}
}

func TestExtensionDeleteKeepsCatalogRowArguments(t *testing.T) {
	t.Parallel()
	catalog := strings.Replace(crudCatalogNamed("new", "edit", "delete"), `"name":"delete","kind":"application"`, `"name":"delete","kind":"application","confirm":"Remove machine {{name}}?","params":{"name":{"type":"string"}}`, 1)
	catalog = strings.Replace(catalog, `"bindings":[`, `"bindings":[{"noun":"thing","mode":"normal","keys":"d d","request":{"noun":"thing","verb":"delete"},"from":{"name":"row:title"}},`, 1)
	a, log := crudApp(t, catalog)
	if err := a.r.SetFocus("ext/fake/thing/t1"); err != nil {
		t.Fatal(err)
	}
	n := len(calls(t, log))
	press(t, a, "d", "d")
	if modal := a.r.Instances[a.r.TopModal()]; modal == nil || modal.Payload.Prompt != "Remove machine one?" {
		t.Fatalf("confirm: %+v", modal)
	}
	pressJob(t, a, "n")
	if got := since(t, log, n); len(got) != 0 {
		t.Fatalf("decline ran: %q", got)
	}
	press(t, a, "d", "d")
	pressJob(t, a, "y")
	if got := since(t, log, n); len(got) != 1 || got[0] != `thing delete --target t1 --json {"name":"one"} --yes` {
		t.Fatalf("calls %q", got)
	}
}

// A tool without CRUD verbs gets none of the keys.
func TestExtensionWithoutCRUDVerbsBindsNoCRUDKeys(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := registered(t, seeded(t), "fake", bin)
	for _, rows := range a.r.Noun("fake.item").Bindings {
		for _, b := range rows {
			if b.Keys == "i" || b.Keys == "d d" {
				t.Fatalf("row %+v", b)
			}
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: extFormNoun, Verb: "new", Args: ochazuke.Arguments{"noun": "fake.item"}}); err == nil {
		t.Fatal("ext-form new ran on a noun without new")
	}
}

// Quitting while a change waits for its pause writes it before mtm exits:
// no job would run after the program ends, so the tool runs at once.
func TestExtensionFormWritesItsLastChangeOnQuit(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, crudCatalogNamed("new", "edit", "delete"))
	if err := a.r.SetFocus("ext/fake/thing/t2"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "i")
	n := len(calls(t, log))
	typeText(t, a, "s")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if got := since(t, log, n); len(got) != 1 || got[0] != `thing edit --target t2 --json {"title":"twos"} --yes` {
		t.Fatalf("calls %q", got)
	}
}
