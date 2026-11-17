package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// overlayCatalog is a tool whose item noun only reads (a remote store with
// get and list, no set, create or delete), with its own d d and n rows.
const overlayCatalog = `{"schema":1,"name":"o-fake","version":"1","nouns":[
 {"name":"item","store":{"get":true,"list":true,"remote":true},
  "props":[{"name":"id","type":"string","key":true},{"name":"label","type":"string","role":"label"},{"name":"note","type":"string"}],
  "verbs":[{"name":"list","kind":"application","standard":"list"},
   {"name":"get","kind":"application","standard":"get","params":{"id":{"type":"string","noun":"item"}}},
   {"name":"ignore","kind":"application"},{"name":"next","kind":"application"}]}],
 "bindings":[{"noun":"item","mode":"normal","keys":"d d","request":{"noun":"item","verb":"ignore"}},
  {"noun":"item","mode":"normal","keys":"n","request":{"noun":"item","verb":"next"}}],
 "pages":[{"id":"fake","title":"Fake","lists":[{"id":"fake/items","noun":"item"}]}]}`

const overlayArms = `"item list") echo '[{"id":"i1","label":"one"},{"id":"i2","label":"two"}]' ;;
"item get") echo '{"id":"i1","label":"one"}' ;;`

// overlayApp registers the read-only tool with the config directory dir/config
// and answers a fresh app, the tool's local noun and its call log.
func overlayApp(t *testing.T) (*App, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	opts := Options{Data: filepath.Join(dir, "mtm.db"), Config: filepath.Join(dir, "config")}
	bin, log := fakeExtension(t, overlayCatalog, overlayArms)
	mustCLI(t, openWith(t, opts), "extension", "register", "fake", "--bin", bin)
	a := openWith(t, opts)
	var noun string
	for _, x := range a.exts {
		noun = x.nouns["item"]
	}
	if noun == "" {
		t.Fatal("item not mounted")
	}
	return a, noun, log, opts.Config
}

// writes are the calls after the first n that are not a read (catalog,
// list, get).
func writes(t *testing.T, log string, n int) []string {
	t.Helper()
	var out []string
	for _, c := range calls(t, log)[n:] {
		words := c[strings.LastIndex(c, "|")+1:]
		if words != "catalog" && !strings.HasPrefix(words, "item list") && !strings.HasPrefix(words, "item get") {
			out = append(out, words)
		}
	}
	return out
}

// A set on a mounted thing whose tool has no set (ochazuke v0.146.4: the
// overlay fills remote stores) lands in the changes table (run plan
// 5.4a; overlay.json before), reads back through get and list, and asks the tool for
// nothing but reads: a CLI get reads the thing twice (its target, then the
// get, as before v0.146.4), a set once more (the thing it answers).
func TestOverlaySetOnAMountedThingStaysBesideTheTool(t *testing.T) {
	t.Parallel()
	a, noun, log, config := overlayApp(t)
	n := len(calls(t, log))
	mustCLI(t, a, noun, "get", "i1")
	perGet := len(calls(t, log)) - n
	n = len(calls(t, log))
	mustCLI(t, a, noun, "set", "i1", "note", "kept-by-mtm")
	if got := len(calls(t, log)) - n; got > perGet+1 {
		t.Fatalf("set started %d tool processes, a get %d: %q", got, perGet, calls(t, log)[n:])
	}
	if got := writes(t, log, 0); len(got) != 0 {
		t.Fatalf("the tool was asked to write: %q", got)
	}
	rows, err := a.work.ChangesBackend(changeNames{a}).Rows()
	if err != nil || rows[noun]["i1"] == nil || rows[noun]["i1"].Props["note"] != "kept-by-mtm" {
		t.Fatalf("the changes table: %+v, %v", rows[noun], err)
	}
	if _, err := os.Stat(filepath.Join(config, ochazuke.OverlayFile)); err == nil {
		t.Fatalf("%s written beside settings.json", ochazuke.OverlayFile)
	}
	if out := mustCLI(t, a, noun, "get", "i1"); !strings.Contains(out, "kept-by-mtm") {
		t.Fatalf("get:\n%s", out)
	}
	n = len(calls(t, log))
	if out := mustCLI(t, a, noun, "list"); !strings.Contains(out, "kept-by-mtm") {
		t.Fatalf("list:\n%s", out)
	}
	if got := calls(t, log)[n:]; len(got) != 1 {
		t.Fatalf("list read the tool %d times: %q", len(got), got)
	}
}

// A delete on a mounted thing whose tool has no delete hides it: it leaves
// the list, get still answers it, and the tool is never asked to delete.
func TestOverlayDeleteOnAMountedThingHidesIt(t *testing.T) {
	t.Parallel()
	a, noun, log, _ := overlayApp(t)
	mustCLI(t, a, noun, "delete", "i1", "--yes")
	if got := writes(t, log, 0); len(got) != 0 {
		t.Fatalf("the tool was asked to write: %q", got)
	}
	if out := mustCLI(t, a, noun, "list"); strings.Contains(out, `"i1"`) || !strings.Contains(out, `"i2"`) {
		t.Fatalf("list:\n%s", out)
	}
	if out := mustCLI(t, a, noun, "get", "i1"); !strings.Contains(out, `"hidden": true`) {
		t.Fatalf("get:\n%s", out)
	}
}

// A core verb the overlay runs takes mtm's key only where the tool binds
// nothing on it: the tool's own d d (ignore) and n (next) stay, and the
// list gets no n for the overlay's create.
func TestOverlayVerbsLeaveTheToolsOwnKeys(t *testing.T) {
	t.Parallel()
	a, noun, _, _ := overlayApp(t)
	var dd, n []string
	for _, b := range a.r.AllBindings() {
		switch {
		case b.Noun == noun && b.Keys == "d d":
			dd = append(dd, b.Request.Key())
		case b.Keys == "n" && (b.Noun == noun || b.Instance == "ext/fake/fake/items" || b.Instance == "fake/items"):
			n = append(n, b.Request.Key())
		}
	}
	if !slices.Equal(dd, []string{noun + ".ignore"}) || !slices.Equal(n, []string{noun + ".next"}) {
		t.Fatalf("d d %q, n %q", dd, n)
	}
}
