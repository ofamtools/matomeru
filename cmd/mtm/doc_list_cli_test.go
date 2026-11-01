package main

import (
	"strings"
	"testing"
)

// Exercise the outer oden dispatch as well as the extension alias: the
// shell-only helper would miss a prefix resolving doc to framework docs.
func TestDocsCLIListUsesExtensionAlias(t *testing.T) {
	t.Parallel()
	bin, _, doc := docsExtension(t)
	a := registered(t, seeded(t), "md", bin)
	for _, words := range [][]string{{"doc", "list"}, {"md", "doc", "list"}, {"md.doc", "list"}} {
		out, errOut, err := mtm(a, t.TempDir(), words...)
		if err != nil || !strings.Contains(out, doc) {
			t.Fatalf("%v: %v\n%s\n%s", words, err, out, errOut)
		}
	}
}

// A shared noun must beat the unrelated framework alias docs by exact
// word, with enough information to run either extension explicitly.
func TestAmbiguousExtensionNounCLI(t *testing.T) {
	t.Parallel()
	catalog := strings.ReplaceAll(strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "item", "doc")
	bin, _ := fakeExtension(t, catalog, `"doc list") echo '[{"id":"d1","label":"example"}]' ;;`)
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "md", "--bin", bin)
	mustCLI(t, openAt(t, path), "extension", "register", "json", "--bin", bin)
	a := openAt(t, path)
	want := "doc is ambiguous: json.doc or md.doc (use mtm json doc ... or mtm md doc ...)"
	for _, words := range [][]string{{"doc"}, {"doc", "list"}, {"doc", "rename", "--to", "new"}} {
		out, _, err := mtm(a, t.TempDir(), words...)
		if err == nil || err.Error() != want || out != "" {
			t.Fatalf("%v: out %q, error %v, want %q", words, out, err, want)
		}
	}
	for _, words := range [][]string{{"md", "doc", "list"}, {"md.doc", "list"}, {"json", "doc", "list"}, {"json.doc", "list"}} {
		out, errOut, err := mtm(a, t.TempDir(), words...)
		if err != nil || !strings.Contains(out, "example") {
			t.Fatalf("%v: %v\n%s\n%s", words, err, out, errOut)
		}
	}
	// The palette already shares the framework's noun-tail ambiguity
	// mechanism: it offers both full names and never executes docs.
	press(t, a, ":")
	typeText(t, a, "doc list")
	a.Update(keyMsg("enter")) // an ambiguity is the expected status
	if fr := frame(a); !strings.Contains(fr, "ambiguous") || !strings.Contains(fr, "md.doc") || !strings.Contains(fr, "json.doc") {
		t.Fatalf("palette lacks ambiguous candidates:\n%s", fr)
	}
	// Remounting in legacy mode removes both the bare aliases and the
	// ambiguity reservation. Switching back reserves the word again.
	if err := a.r.Settings().Set(settingExtNounNames, extNounNamesLegacy); err != nil {
		t.Fatal(err)
	}
	if got := a.commands(nil, nil, nil, nil, nil).Catalog().Lookup("doc"); got != nil {
		t.Fatalf("legacy mode keeps bare doc: %+v", got)
	}
	if err := a.r.Settings().Set(settingExtNounNames, extNounNamesNamespaced); err != nil {
		t.Fatal(err)
	}
	if got := a.commands(nil, nil, nil, nil, nil).Catalog().Lookup("doc"); got == nil || got.Summary != want {
		t.Fatalf("namespaced mode did not restore ambiguity: %+v", got)
	}
}

// An app noun or a framework alias keeps its exact word even when two
// extensions have the same tail; only unclaimed names are ambiguous.
func TestAmbiguousExtensionNounKeepsHostNames(t *testing.T) {
	t.Parallel()
	for _, noun := range []string{"task", "docs"} {
		t.Run(noun, func(t *testing.T) {
			t.Parallel()
			catalog := strings.ReplaceAll(strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "item", noun)
			bin, _ := fakeExtension(t, catalog, "")
			path := seeded(t)
			mustCLI(t, openAt(t, path), "extension", "register", "md", "--bin", bin)
			mustCLI(t, openAt(t, path), "extension", "register", "json", "--bin", bin)
			a := openAt(t, path)
			want := a.r.CanonicalNoun(noun)
			got := a.commands(nil, nil, nil, nil, nil).Catalog().Lookup(noun)
			if got == nil || got.Name != want {
				t.Fatalf("%s resolves to %+v, want host %s", noun, got, want)
			}
		})
	}
}
