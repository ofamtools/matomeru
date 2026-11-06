package main

import (
	"slices"
	"strings"
	"testing"
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

// optionalCreateCatalog is crudCatalog whose create verb requires nothing
// (as md doc new: every parameter optional).
func optionalCreateCatalog() string {
	return strings.Replace(crudCatalogNamed("create", "edit", "delete"), `"required":true`, `"required":false`, 1)
}

// n on a list runs the rows' noun's create: a verb named create, never a
// guessed one (new, add). Without required parameters it runs at once.
func TestExtensionCreateKeyRunsCreate(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, optionalCreateCatalog())
	if err := a.r.SetFocus("ext/fake/thing/t2"); err != nil {
		t.Fatal(err)
	}
	n := len(calls(t, log))
	pressJob(t, a, "n")
	if got := since(t, log, n); len(got) != 1 || got[0] != `thing create --target t2 --json {} --yes` {
		t.Fatalf("calls %q", got)
	}
}

// A tool whose create is named new gets no n from mtm: mtm no longer
// guesses CRUD verbs by other names (the tool's own n row stands).
func TestExtensionNewVerbIsNotCreate(t *testing.T) {
	t.Parallel()
	a, _ := crudApp(t, crudCatalogNamed("new", "edit", "delete"))
	for _, b := range a.r.AllBindings() {
		if b.Keys == "n" && b.Instance == "ext/fake/thing" {
			t.Fatalf("n bound: %+v", b)
		}
	}
}

// d d runs the delete verb on the row after a confirm (mtm's own when the
// tool declares none); the tool's n row is dropped with a notice, since n
// is mtm's create.
func TestExtensionDeleteKeyConfirmsAndRunsTheTool(t *testing.T) {
	t.Parallel()
	a, log := crudApp(t, crudCatalogNamed("create", "edit", "delete"))
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
}
