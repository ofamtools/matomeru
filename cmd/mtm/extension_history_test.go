package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// historyCatalog is fakeCatalog with a history kind on item list (fetch)
// and on item rename (one the framework does not know), and the
// framework's outline verbs as every ochazuke tool's catalog lists them:
// kind application, because the tree's handlers are application handlers.
var historyCatalog = strings.NewReplacer(
	`{"name":"list","kind":"application","description":"List the items"}`,
	`{"name":"list","kind":"application","description":"List the items","history":"fetch"}`,
	`"positional":["to"]}`,
	`"positional":["to"],"history":"poll"}`,
	`{"name":"app","verbs":[{"name":"quit","kind":"interaction"}]}`,
	`{"name":"app","verbs":[{"name":"quit","kind":"interaction"}]},
 {"name":"outline","verbs":[{"name":"expand","kind":"application"},{"name":"undo","kind":"application"}]},
 {"name":"history","verbs":[{"name":"list","kind":"interaction"},{"name":"capture","kind":"application"}]}`,
	"VERSION", "1.0",
).Replace(fakeCatalog)

// A mounted verb records the kind its catalog declares; one that declares
// none stays the framework default (change); an unknown kind is a notice
// and the default. The framework's own outline verbs never mount; the
// tool's own verb on a framework noun (history capture) does.
func TestExtensionVerbsKeepTheCatalogHistory(t *testing.T) {
	t.Parallel()
	if !strings.Contains(historyCatalog, `"history":"fetch"`) || !strings.Contains(historyCatalog, `"outline"`) || !strings.Contains(historyCatalog, `"poll"`) {
		t.Fatalf("catalog not rewritten:\n%s", historyCatalog)
	}
	path := seeded(t)
	bin, _ := fakeExtension(t, historyCatalog, "")
	cliApp := openAt(t, path)
	mustCLI(t, cliApp, "extension", "register", "fake", "--bin", bin)
	a := openAt(t, path)
	for key, want := range map[string]string{
		"fake.item.list":       ochazuke.HistoryFetch,
		"fake.item.done":       ochazuke.HistoryChange,
		"fake.item.rename":     ochazuke.HistoryChange,
		"fake.history.capture": ochazuke.HistoryChange,
	} {
		op, ok := a.r.Operations[key]
		if !ok {
			t.Errorf("%s not mounted", key)
			continue
		}
		if got := op.HistoryKindOf(); got != want {
			t.Errorf("%s records %q, want %q", key, got, want)
		}
	}
	if a.r.Noun("fake.outline") != nil {
		t.Fatal("the framework's outline verbs mounted as fake-outline")
	}
	if !slices.ContainsFunc(a.m.Notices, func(n string) bool { return strings.Contains(n, `item rename declares history "poll"`) }) {
		t.Fatalf("no notice for the unknown kind: %q", a.m.Notices)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "fake.item", Verb: "list"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range a.r.HistoryOf(0, ochazuke.HistoryAction, ochazuke.HistoryChange) {
		if e.Request.Key() == "fake.item.list" {
			t.Fatalf("the fetch fake-item.list is with the user's entries: %+v", e)
		}
	}
	if !slices.ContainsFunc(a.r.HistoryOf(0, ochazuke.HistoryFetch), func(e ochazuke.HistoryEntry) bool { return e.Request.Key() == "fake.item.list" }) {
		t.Fatal("fake.item.list is not a fetch entry")
	}
}

// A catalog that marks builtin verbs (ochazuke v0.8.0 and later) is
// mounted from its AppNouns: a builtin verb never mounts, even one this
// mtm's framework does not register (a newer tool's), and a nonbuiltin
// application verb mounts. A catalog that marks none falls back to the
// keys a fresh runtime registers.
func TestExtPlanMountsTheCatalogsAppNouns(t *testing.T) {
	t.Parallel()
	app := ochazuke.KindApplication
	marked := ocli.CatalogDoc{Nouns: []ocli.CatalogNoun{
		{Name: "app", Verbs: []ocli.CatalogVerb{{Name: "quit", Kind: ochazuke.KindInteraction, Builtin: true}}},
		{Name: "outline", Verbs: []ocli.CatalogVerb{{Name: "expand", Kind: app, Builtin: true}, {Name: "fold-all", Kind: app, Builtin: true}}},
		{Name: "history", Verbs: []ocli.CatalogVerb{{Name: "list", Kind: ochazuke.KindInteraction, Builtin: true}, {Name: "capture-hook", Kind: app}}},
		{Name: "item", Verbs: []ocli.CatalogVerb{{Name: "list", Kind: app}, {Name: "focus", Kind: ochazuke.KindInteraction}}},
	}}
	keys := func(plan []ocli.CatalogNoun) []string {
		var out []string
		for _, n := range plan {
			for _, v := range n.Verbs {
				out = append(out, n.Name+"."+v.Name)
			}
		}
		return out
	}
	if got, want := keys(extPlan(marked)), []string{"history.capture-hook", "item.list"}; !slices.Equal(got, want) {
		t.Fatalf("marked catalog mounts %v, want %v", got, want)
	}
	old := ocli.CatalogDoc{Nouns: []ocli.CatalogNoun{
		{Name: "outline", Verbs: []ocli.CatalogVerb{{Name: "expand", Kind: app}}},
		{Name: "item", Verbs: []ocli.CatalogVerb{{Name: "list", Kind: app}}},
	}}
	if got, want := keys(extPlan(old)), []string{"item.list"}; !slices.Equal(got, want) {
		t.Fatalf("unmarked catalog mounts %v, want %v", got, want)
	}
}
