package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// aliasesOf is the alias names that reach noun now.
func aliasesOf(a *App, noun string) []string {
	var out []string
	for _, al := range a.r.NounAliasesOf(noun) {
		out = append(out, al.Alias)
	}
	return out
}

// An extension's nouns mount as <ext>.<noun>, with the bare noun as an
// alias and the old <ext>-<noun> as a deprecated one that still runs.
func TestExtensionNounsAreNamespaced(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	a := openAt(t, path)
	if a.r.Nouns["fake.item"] == nil || a.r.Nouns["fake-item"] != nil {
		t.Fatalf("nouns: fake.item %v, fake-item %v", a.r.Nouns["fake.item"] != nil, a.r.Nouns["fake-item"] != nil)
	}
	if got := strings.Join(aliasesOf(a, "fake.item"), ","); got != "item,fake-item" {
		t.Fatalf("aliases of fake.item: %s", got)
	}
	old, ok := a.r.DeprecatedNounAlias("fake-item")
	if !ok || old.Noun != "fake.item" || old.Since != extNounNamesSince || old.RemoveAfter != extNounNamesRemoveAfter {
		t.Fatalf("fake-item: %+v %v", old, ok)
	}
	if got := a.r.DisplayNoun("fake.item"); got != "item" {
		t.Fatalf("drawn as %q", got)
	}
	for _, noun := range []string{"fake.item", "item", "fake-item"} {
		if _, err := a.r.Execute(ochazuke.Request{Noun: noun, Verb: "list"}); err != nil {
			t.Fatalf("%s list: %v", noun, err)
		}
	}
	// The CLI reads every spelling; the old one says what it is now.
	for _, args := range [][]string{{"fake.item", "list"}, {"fake", "item", "list"}, {"item", "list"}} {
		if out := mustCLI(t, openAt(t, path), args...); !strings.Contains(out, "one") {
			t.Fatalf("mtm %s: %s", strings.Join(args, " "), out)
		}
	}
	out, errOut, err := cli(openAt(t, path), "", "fake-item", "list")
	if err != nil || !strings.Contains(out, "one") || !strings.Contains(errOut, "fake-item is an old name") {
		t.Fatalf("mtm fake-item list: %v\n%s\n%s", err, out, errOut)
	}
}

// A noun name two extensions share has no bare alias (the palette offers
// both full names, palette.noun-tails); it gets one again when the other
// extension goes.
func TestExtensionBareNounNeedsAUniqueName(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	mustCLI(t, openAt(t, path), "extension", "register", "zzz", "--bin", bin)
	a := openAt(t, path)
	if a.r.Nouns["zzz.item"] == nil {
		t.Fatal("zzz.item not mounted")
	}
	if got := a.r.CanonicalNoun("item"); got != "item" {
		t.Fatalf("item names %s while two extensions have one", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "remove", Args: ochazuke.Arguments{"name": "zzz"}, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	settleJobs(t, a, a.r.PendingCommands())
	if a.r.Nouns["zzz.item"] != nil || a.r.CanonicalNoun("zzz-item") != "zzz-item" {
		t.Fatal("zzz still mounted or its old name kept")
	}
	if got := a.r.CanonicalNoun("item"); got != "fake.item" {
		t.Fatalf("item names %q after zzz went", got)
	}
}

// extensions.noun-names legacy mounts the nouns as before (<ext>-<noun>,
// no aliases), and a change remounts the running extensions.
func TestExtensionNounNamesLegacy(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	a := openAt(t, path)
	openExtPage(t, a, "ext/fake")
	if err := a.r.Settings().Set(settingExtNounNames, extNounNamesLegacy); err != nil {
		t.Fatal(err)
	}
	if a.r.Nouns["fake-item"] == nil || a.r.Nouns["fake.item"] != nil || a.r.CanonicalNoun("item") != "item" {
		t.Fatalf("legacy: fake-item %v, fake.item %v, item -> %s", a.r.Nouns["fake-item"] != nil, a.r.Nouns["fake.item"] != nil, a.r.CanonicalNoun("item"))
	}
	if !hasOp(a, "fake-item.done") || a.r.Page != "ext/fake" {
		t.Fatalf("legacy remount: done %v, page %s", hasOp(a, "fake-item.done"), a.r.Page)
	}
	if len(a.r.NounAliasesOf("fake-item")) != 0 {
		t.Fatalf("legacy aliases %v", aliasesOf(a, "fake-item"))
	}
	if err := a.r.Settings().Set(settingExtNounNames, extNounNamesNamespaced); err != nil {
		t.Fatal(err)
	}
	if a.r.Nouns["fake.item"] == nil || a.r.CanonicalNoun("item") != "fake.item" || a.r.CanonicalNoun("fake-item") != "fake.item" {
		t.Fatalf("namespaced again: fake.item %v, item -> %s, fake-item -> %s, status %q", a.r.Nouns["fake.item"] != nil, a.r.CanonicalNoun("item"), a.r.CanonicalNoun("fake-item"), a.m.Status)
	}
	// A new process reads the setting from settings.json.
	if err := a.r.Settings().Set(settingExtNounNames, extNounNamesLegacy); err != nil {
		t.Fatal(err)
	}
	if b := openAt(t, path); b.r.Nouns["fake-item"] == nil {
		v, _ := b.r.Settings().Get(settingExtNounNames)
		t.Fatalf("legacy not read at start: %+v nouns %v status %q exts %d", v.Value, nounsLike(b, "fake"), b.m.Status, len(b.exts))
	}
}

// An extension whose name ochazuke reserves as a namespace (a framework
// noun's short name) keeps the dashed spelling, with a notice.
func TestExtensionReservedNamespaceKeepsDashes(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "list", "--bin", bin)
	a := openAt(t, path)
	if a.r.Nouns["list-item"] == nil || a.r.Nouns["list.item"] != nil {
		t.Fatalf("list-item %v, list.item %v", a.r.Nouns["list-item"] != nil, a.r.Nouns["list.item"] != nil)
	}
}

func nounsLike(a *App, s string) []string {
	var out []string
	for n := range a.r.Nouns {
		if strings.Contains(n, s) {
			out = append(out, n)
		}
	}
	return out
}

// The palette reads every spelling of a mounted noun: the bare name, the
// namespace words, the full name and the old dashed one.
func TestExtensionNounSpellingsInThePalette(t *testing.T) {
	t.Parallel()
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	for _, line := range []string{"item done", "fake item done", "fake.item done", "fake-item done"} {
		t.Run(line, func(t *testing.T) {
			a := openAt(t, path)
			openExtPage(t, a, "ext/fake")
			if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
				t.Fatal(err)
			}
			press(t, a, ":")
			typeText(t, a, line)
			press(t, a, "enter")
			if fr := frame(a); !strings.Contains(fr, "Finish one?") {
				t.Fatalf("%s: no confirm (modal %q)\n%s", line, a.r.TopModal(), fr)
			}
		})
	}
}
