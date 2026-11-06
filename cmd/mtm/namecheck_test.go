package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke/namecheck"
)

// mtm holds itself to namecheck: its non-test Go (cmd/mtm, internal and
// store) adds no comparison, case label or lookup table keyed by a noun,
// verb, mode, motion or parameter name its runtime declares, and no
// function grows or passes complexity 25. The vocabulary is the full
// runtime of the test app plus the nouns two real extension catalogs mount
// (o-ai's quota catalog and o-lane's). The accepted uses are
// testdata/namecheck.txt, which only shrinks: NAMECHECK_UPDATE=1 removes
// rows and lowers counts, a new row is a reviewed edit, and a row that is
// a real word coincidence carries its reason in the last column.
func TestNoNewNameBranches(t *testing.T) {
	t.Parallel()
	vocab := map[string][]string{}
	merge := func(a *App) {
		for name, cats := range a.r.NameVocab() {
			for _, c := range cats {
				if !slices.Contains(vocab[name], c) {
					vocab[name] = append(vocab[name], c)
				}
			}
		}
	}
	merge(open(t))
	for _, ext := range []struct{ name, catalog string }{
		{"ai", "testdata/o-ai-quota-catalog.json"},
		{"lane", "testdata/o-lane-catalog.json"},
	} {
		catalog, err := os.ReadFile(ext.catalog)
		if err != nil {
			t.Fatal(err)
		}
		bin, _ := fakeExtension(t, strings.TrimSpace(string(catalog)), "")
		path := seeded(t)
		mustCLI(t, openAt(t, path), "extension", "register", ext.name, "--bin", bin)
		merge(openAt(t, path))
	}
	namecheck.Check(t, namecheck.Config{
		Vocab:    vocab,
		Baseline: "testdata/namecheck.txt",
		Dirs:     []string{".", "../../internal/...", "../../store/..."},
	})
}
