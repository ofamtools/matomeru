package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// scopedCatalog is overlayCatalog's read-only tool with a project field of
// its own on each row.
var scopedCatalog = strings.Replace(overlayCatalog, `{"name":"note","type":"string"}`, `{"name":"note","type":"string"},{"name":"project","type":"string"}`, 1)

const scopedArms = `"item list") echo '[{"id":"i1","label":"one","project":"theirs"},{"id":"i2","label":"two"}]' ;;
"item get") echo '{"id":"i1","label":"one","project":"theirs"}' ;;`

// scopedApp is overlayApp over scopedCatalog: the app and the tool's
// extension.
func scopedApp(t *testing.T) (*App, *extension, Options) {
	t.Helper()
	dir := t.TempDir()
	opts := Options{Data: filepath.Join(dir, "mtm.db"), Config: filepath.Join(dir, "config")}
	bin, _ := fakeExtension(t, scopedCatalog, scopedArms)
	mustCLI(t, openWith(t, opts), "extension", "register", "fake", "--bin", bin)
	a := openWith(t, opts)
	x := a.extension("fake")
	if x == nil || x.nouns["item"] == "" {
		t.Fatal("item not mounted")
	}
	return a, x, opts
}

// The project mtm records a tool's row in is not a prop of the thing: its
// get and list answer the tool's own row byte for byte (the tool's own
// project field kept), and ochazuke.overlay lists no change of the
// owner's for it.
func TestRecordedProjectIsNotAProp(t *testing.T) {
	t.Parallel()
	a, x, _ := scopedApp(t)
	noun := x.nouns["item"]
	get, list := mustCLI(t, a, noun, "get", "i1"), mustCLI(t, a, noun, "list")
	a.recordExtOwner(x, "item", "i1", "mine")
	if got := a.extRowProject("fake", "item", "i1"); got != "mine" {
		t.Fatalf("recorded project %q", got)
	}
	if after := mustCLI(t, a, noun, "get", "i1"); after != get || !strings.Contains(after, `"theirs"`) {
		t.Fatalf("get changed:\n%s\nwas:\n%s", after, get)
	}
	if after := mustCLI(t, a, noun, "list"); after != list {
		t.Fatalf("list changed:\n%s\nwas:\n%s", after, list)
	}
	if rows := a.r.OverlayRows(noun); len(rows) != 0 {
		t.Fatalf("the overlay holds %+v", rows)
	}
}

// What mtm keeps beside a mounted thing (its recorded project, a value of
// the owner's) is keyed by the extension and its catalog noun, not the
// mounted name: switching extensions.noun-names and back keeps both.
func TestChangesFollowTheNounNaming(t *testing.T) {
	t.Parallel()
	a, x, _ := scopedApp(t)
	a.recordExtOwner(x, "item", "i1", "mine")
	mustCLI(t, a, x.nouns["item"], "set", "i1", "note", "kept")
	for _, value := range []string{extNounNamesLegacy, extNounNamesNamespaced} {
		if err := a.r.Settings().Set(settingExtNounNames, value); err != nil {
			t.Fatal(err)
		}
		x = a.extension("fake")
		noun := x.nouns["item"]
		if got := a.extRowProject("fake", "item", "i1"); got != "mine" {
			t.Fatalf("%s: recorded project %q", value, got)
		}
		if out := mustCLI(t, a, noun, "get", "i1"); !strings.Contains(out, "kept") {
			t.Fatalf("%s: %s get:\n%s", value, noun, out)
		}
	}
	_ = ochazuke.OverlayFile
}

// The scope of 1,000 tool rows read in one read pass reads the changes
// table's version once and the table once; a row another process records
// shows in the next pass.
func TestExtOwnersReadOncePerPass(t *testing.T) {
	t.Parallel()
	a, x, _ := scopedApp(t)
	a.recordExtOwner(x, "item", "i1", "mine")
	a.extRowProject("fake", "item", "i1") // the pass's one read
	versions, reads := a.db.FullReads("__deck_writes"), a.db.FullReads("changes")
	for i := range 1000 {
		a.extRowProject("fake", "item", fmt.Sprint("row-", i))
	}
	if got := a.db.FullReads("__deck_writes") - versions; got != 0 {
		t.Fatalf("1,000 rows read the version %d more times", got)
	}
	if got := a.db.FullReads("changes") - reads; got != 0 {
		t.Fatalf("1,000 rows read the table %d more times", got)
	}
	if _, err := a.work.RecordExtOwner(work.ExtRow{Extension: "fake", Noun: "item", Row: "i2"}, "other", 1); err != nil {
		t.Fatal(err) // as another process would
	}
	if got := a.extRowProject("fake", "item", "i2"); got != "" {
		t.Fatalf("read again inside the pass: %q", got)
	}
	mustCLI(t, a, x.nouns["item"], "get", "i1") // the next request
	if got := a.extRowProject("fake", "item", "i2"); got != "other" {
		t.Fatalf("the next pass: %q", got)
	}
}
