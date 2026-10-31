package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// sourceCatalog: item move --to has its choices from place list (mounted);
// item tag --name names a source mtm cannot mount.
const sourceCatalog = `{"schema":1,"name":"o-fake","version":"1.0","nouns":[
 {"name":"item","verbs":[
  {"name":"list","kind":"application"},
  {"name":"move","kind":"application","params":{"to":{"type":"string","required":true,"source":"place.list","enum":["nowhere"]}}},
  {"name":"tag","kind":"application","params":{"name":{"type":"string","source":"label.list"}}}]},
 {"name":"place","verbs":[{"name":"list","kind":"application"}]}],
 "bindings":[],"pages":[]}`

const sourceArms = `"place list") echo '[{"id":"p1","label":"Kitchen"},{"id":"p2","label":"Shed"}]' ;;
"item move") echo '{"ok":true}' ;;`

// A sourced parameter's pick list is the source verb's rows, loaded off
// the loop with the page's rows; it only suggests: a value outside it
// runs the tool, which checks it.
func TestExtensionSourcedParameterGetsSuggestions(t *testing.T) {
	t.Parallel()
	bin, log := fakeExtension(t, sourceCatalog, sourceArms)
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	a := openAt(t, path)
	if !slices.ContainsFunc(a.m.Notices, func(s string) bool {
		return strings.Contains(s, "item tag --name: choices source label.list is not mounted")
	}) {
		t.Fatalf("notices %q", a.m.Notices)
	}
	p := a.r.Operations["fake.item.move"].Parameters["to"]
	if !p.Free || p.Source != "" || len(p.Enum) != 1 {
		t.Fatalf("before a load: %+v", p)
	}
	n := len(calls(t, log))
	openExtPage(t, a, "ext/fake")
	if got := calls(t, log)[n:]; !slices.ContainsFunc(got, func(c string) bool { return strings.HasSuffix(c, "|place list --json {}") }) {
		t.Fatalf("no source load: %q", got)
	}
	p = a.r.Operations["fake.item.move"].Parameters["to"]
	if !p.Free || !slices.Equal(p.Enum, []string{"nowhere", "p1", "p2"}) {
		t.Fatalf("after the load: %+v", p)
	}
	choices, err := a.r.Choices(a.r.Context(), p)
	if err != nil || len(choices) != 3 {
		t.Fatalf("choices %v %v", choices, err)
	}
	// Suggestions only: a value outside them runs the tool.
	if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "fake.item", Verb: "move", Args: ochazuke.Arguments{"to": "attic"}}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if got := calls(t, log); !slices.ContainsFunc(got, func(c string) bool {
		return strings.HasSuffix(c, `|item move --target i1 --json {"to":"attic"} --yes`)
	}) {
		t.Fatalf("calls %q", got)
	}
}
