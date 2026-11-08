package main

import (
	"os"
	"strings"
	"testing"
)

// A collection-scope verb of a mounted catalog (o-lane's `lane archive
// --finished-trees`) runs headless with no target: its optional target
// parameter reads `from: row:id`, and a blank row id is left out rather
// than refused (ochazuke v0.105.1).
func TestExtensionCollectionScopeVerbRunsWithoutTarget(t *testing.T) {
	t.Parallel()
	catalog, err := os.ReadFile("testdata/o-lane-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := fakeExtension(t, strings.TrimSpace(string(catalog)), `"lane archive") echo '{"ok":true,"trees":[]}' ;;`)
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "lane", "--bin", bin)
	out, errOut, err := cli(openAt(t, path), "", "lane", "lane", "archive", "--finished-trees", "--dry-run")
	if err != nil {
		t.Fatalf("lane lane archive --finished-trees --dry-run: %v\n%s\n%s", err, out, errOut)
	}
	var got bool
	for _, c := range calls(t, log) {
		got = got || (strings.Contains(c, "lane archive") && strings.Contains(c, `"finished-trees":true`) && !strings.Contains(c, "--target"))
	}
	if !got {
		t.Errorf("no targetless archive call; calls %v", calls(t, log))
	}
}
