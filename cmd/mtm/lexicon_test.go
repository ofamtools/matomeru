package main

import (
	"os"
	"strings"
	"testing"

	"ofam.dev/ochazuke/lexiconcheck"
)

// mtm's runtime, with the two real extension catalogs mounted together
// (o-ai's quota catalog and o-lane's, as in the namecheck vocabulary), is held
// to the lexicon's naming rules (lexicon plan, Design 3): one word has one
// part of speech, nouns are singular, a namespace is not a noun or action
// word, and a key has at most three parts. The known violations are
// testdata/lexicon-baseline.txt, which only shrinks (LEXICONCHECK_UPDATE=1
// drops rows the registry no longer breaks; a new row is a reviewed edit).
// mtm's own renames are lane LX6; testdata/lexicon-words.tsv is a copy of the
// family word list (ochazuke's testdata/lexicon-words.tsv).
func TestLexicon(t *testing.T) {
	t.Parallel()
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
	a := openAt(t, path)
	lexiconcheck.Check(t, lexiconcheck.Config{
		Operations: &a.r.Operations,
		Words:      "testdata/lexicon-words.tsv",
		Baseline:   "testdata/lexicon-baseline.txt",
		MainNouns:  map[string]string{"lane": "lane"},
	})
}
