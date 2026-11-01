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
