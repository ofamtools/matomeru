package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func recordExtension(t *testing.T) string {
	t.Helper()
	r := ochazuke.NewRuntime()
	r.Config().Disable()
	r.DefineOwn("item").Props(ochazuke.PropSpec{Name: "id", Key: true}, ochazuke.PropSpec{Name: "label", Role: ochazuke.RoleLabel}, ochazuke.PropSpec{Name: "file", Human: true, Parameter: ochazuke.Parameter{Type: ochazuke.PropPath, Path: "file"}}, ochazuke.PropSpec{Name: "content"}).Store(ochazuke.Store{
		Get:   func(ochazuke.Ctx, string) (ochazuke.Thing, error) { return nil, nil },
		List:  func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) { return nil, nil },
		Patch: func(ochazuke.Ctx, string, ochazuke.Thing) (ochazuke.Thing, error) { return nil, nil },
	}).Verb("open", func(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done(nil) }).OpensPage("things")
	op, _ := r.Operation("item.open")
	op.Parameters = map[string]ochazuke.Parameter{"id": {Type: "string", Noun: "item", Free: true}, "path": {Type: ochazuke.PropPath, Path: "file"}}
	op.Positional = []string{"path"}
	r.Operations.Set(op)
	raw, err := ocli.Catalog(r, "fixture", "1")
	if err != nil {
		t.Fatal(err)
	}
	var doc ocli.CatalogDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Namespace = "recordfixture"
	page := `{"id":"things","title":"Things","layout":"split","lists":[{"id":"things/items","noun":"item","title":"Things"}],"detail":{"fields":["content"]}}`
	var spec ocli.CatalogPage
	if err := json.Unmarshal([]byte(page), &spec); err != nil {
		t.Fatal(err)
	}
	doc.Pages = []ocli.CatalogPage{spec}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := fakeExtension(t, string(raw), `"item get") echo '{"id":"i2","label":"two","file":"/fixture/current","content":"Selected record two"}' ;;`)
	named := filepath.Join(filepath.Dir(bin), "o-recordfixture")
	if err := os.Rename(bin, named); err != nil {
		t.Fatal(err)
	}
	return named
}

func TestExtensionNativeOpenSelectsAndHydratesRequestedRecord(t *testing.T) {
	t.Parallel()
	a := registered(t, seeded(t), "recordfixture", recordExtension(t))
	mono(t, a)
	if _, err := a.r.Execute(ochazuke.Request{Noun: "recordfixture.item", Verb: "open", Args: ochazuke.Arguments{"id": "i2"}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	frame(a)
	settleJobs(t, a, a.r.PendingCommands())
	f := frame(a)
	if !strings.Contains(a.r.FocusID(), "/i2") || !strings.Contains(f, "Selected record two") {
		t.Fatalf("open did not select/hydrate second row: focus %s\n%s", a.r.FocusID(), f)
	}
	if a.r.Page != "ext/recordfixture/things" {
		t.Fatalf("opened %s", a.r.Page)
	}
}

func TestExtensionCanonicalGetMigratesUncachedLegacyReferences(t *testing.T) {
	t.Parallel()
	a := registered(t, seeded(t), "recordfixture", recordExtension(t))
	old := "recordfixture.item:/fixture/current"
	note, err := a.work.AddNote("ochazuke", "", "legacy reference")
	if err != nil {
		t.Fatal(err)
	}
	note.About = old
	if err := a.work.PutNote(note); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Relate(ochazuke.Ctx{Runtime: a.r}, ochazuke.Relation{From: "project:matomeru", To: old, Text: "keep"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SetExtRowOwner(work.ExtRowOwner{Extension: "recordfixture", Noun: "item", Row: "/fixture/current", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	a.pin = "matomeru"
	// No mounted page was opened and no row cache exists.
	mustCLI(t, a, "recordfixture.item", "get", "--id", "/fixture/current")
	got, err := a.work.Note(note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.About != "recordfixture.item:i2" || got.Text != "legacy reference" {
		t.Fatalf("note not migrated across scope: %+v", got)
	}
	rels, err := a.r.RelationsOf(ochazuke.RelationQuery{Thing: "recordfixture.item:i2"})
	if err != nil || len(rels) != 1 || rels[0].Text != "keep" {
		t.Fatalf("relations: %+v %v", rels, err)
	}
	owners, err := a.work.ExtRowOwners()
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 1 || owners[work.ExtRowKey("recordfixture", "item", "i2")].ProjectID != "ochazuke" {
		t.Fatalf("owners: %+v", owners)
	}
}

func TestExtensionNativeOpenUsesDeclaredPathBeforeSelectedRow(t *testing.T) {
	t.Parallel()
	bin := recordExtension(t)
	a := registered(t, seeded(t), "recordfixture", bin)
	mono(t, a)
	openExtPage(t, a, "ext/recordfixture/things")
	if err := a.r.SetFocus("ext/recordfixture/things/items/i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "recordfixture.item", Verb: "open", Args: ochazuke.Arguments{"path": "/fixture/current"}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if got := readFile(t, filepath.Join(filepath.Dir(bin), "calls.log")); !strings.Contains(got, `item get --json {"id":"/fixture/current"}`) {
		t.Fatalf("declared input was not resolved: %s", got)
	}
	if !strings.HasSuffix(a.r.FocusID(), "/i2") {
		t.Fatalf("old selection won: %s", a.r.FocusID())
	}
}

func TestExtensionCollectionCallDoesNotInheritRowTarget(t *testing.T) {
	t.Parallel()
	a := registered(t, seeded(t), "recordfixture", recordExtension(t))
	op, ok := a.r.Operation("recordfixture.item.list")
	if !ok {
		t.Fatal("generated mounted list is missing")
	}
	if got := a.rowTargetUnlessSubjectGiven(op, nil, "selected-document"); got != "" {
		t.Fatalf("collection inherited row %q", got)
	}
}
