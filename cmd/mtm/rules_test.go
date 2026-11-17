package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/lexiconcheck"
	"ofam.dev/ochazuke/namecheck"
	"ofam.dev/ochazuke/rulecheck"

	"ofam.dev/matomeru/internal/actionitems"
)

// mtm holds itself to the family's rule checks (ochazuke's rulecheck), as
// subtests: deadkeys (testdata/rules/deadkeys.txt), hiddenkeys
// (testdata/rules/hiddenkeys.txt; it also walks testdata/rules/places.json,
// the places of matomeru's, the mounted tools' and the framework's nouns as
// a running mtm recorded them, reduced to noun, ancestors and modal, so the
// pages, rails, trees, forms and pickers the fixture does not build are
// walked too), names
// (testdata/namecheck.txt), refusals (testdata/refusecheck.txt), lexicon
// (testdata/lexicon-baseline.txt, words lexicon-words.tsv), nouns (no
// baseline), and the restructure's syntax checks over the whole repo
// (seeded 2026-11-17, run plan 5.4a; each testdata/rules/<id>.txt):
// declarations (Define outside the noun-file loader), gokeys (Go Bind
// rows), stringids (ids glued or cut in strings outside Ref),
// storeliterals, storeui and hookrefusal (spine S6 rules 1 to 3) and
// nounlines (the Go lines naming each stored noun, which may only fall).
// Every baseline only shrinks (RULECHECK_UPDATE=1). This test
// replaces TestNoNewNameBranches, TestNoNewRefusals, TestLexicon and
// TestNouns.
//
// The runtime is the Panes fixture with every kind of row (an agent pane
// with a subagent child, an item row under a pane, a needs-you item row, an
// item with no listed pane, the session tree) and both real extension
// catalogs mounted (o-ai's quota catalog and o-lane's), so the vocabulary
// and the lexicon read the full registry.
//
// serial: the checks pass RULECHECK_UPDATE on with t.Setenv, which a
// parallel test cannot.
func TestRules(t *testing.T) {
	path := seeded(t)
	for _, ext := range []struct{ name, catalog string }{
		{"ai", "testdata/o-ai-quota-catalog.json"},
		{"lane", "testdata/o-lane-catalog.json"},
	} {
		catalog, err := os.ReadFile(ext.catalog)
		if err != nil {
			t.Fatal(err)
		}
		bin, _ := fakeExtension(t, strings.TrimSpace(string(catalog)), "")
		mustCLI(t, openAt(t, path), "extension", "register", ext.name, "--bin", bin)
	}
	a := subagentsAppAt(t, path)
	groupBy(t, a, groupProjectSessionPane)
	setting(t, a, settingPaneChildren, []string{childKindSubagents, childKindItems})
	// An item of the agent pane (its row under the pane and its needs-you
	// row) and one with no listed pane (queue:).
	addItem(t, a, actionitems.Spec{Source: "gh", Key: "pr-2", Title: "PR 2", Why: "about the pane", Window: "w1", Session: "matomeru"})
	addItem(t, a, actionitems.Spec{Source: "gh", Key: "pr-1", Title: "PR 1", Why: "review requested", URL: "https://example.invalid/pr/1"})
	setting(t, a, settingItemsOpen, itemsOpenAll)
	reload(t, a)
	if !slices.ContainsFunc(sectionItems(a), func(it ochazuke.ListItem) bool { return it.Noun == subagentNoun }) {
		for _, it := range sectionItems(a) {
			t.Logf("%s %s", it.Noun, it.ID)
		}
		t.Fatal("the fixture has no subagent row")
	}
	for _, it := range sectionItems(a) {
		t.Logf("fixture row %s %s", it.Noun, it.ID)
	}
	places, err := rulecheck.LoadPlaces("testdata/rules/places.json")
	if err != nil {
		t.Fatal(err)
	}
	rulecheck.Check(t, rulecheck.Config{
		Runtime: a.r,
		Places:  places,
		Root:    "../..",
		Names: namecheck.Config{
			Dirs: []string{".", "../../internal/...", "../../store/..."},
		},
		Lexicon: lexiconcheck.Config{
			Baseline:  "testdata/lexicon-baseline.txt",
			MainNouns: map[string]string{"lane": "lane"},
		},
	})
}
