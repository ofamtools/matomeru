package main

import (
	"os"
	"strings"
	"testing"
)

// o-df's shape: list rows carry id noun/key (culprit/NAME), the delete
// verb takes the thing by a positional name its param fills from the row
// (from row:name) and resolves a bare name only. d d on a row must reach
// the tool's delete with a target and a name it resolves, not the
// noun-prefixed list id as the name.
const idPrefixedCatalog = `{"schema":1,"name":"o-fake","version":"1.0","nouns":[
 {"name":"thing","columns":[{"title":"thing"}],"verbs":[
  {"name":"list","kind":"application","rows":"thing"},
  {"name":"delete","kind":"application","positional":["name"],"confirm":"Delete {{label}}?","params":{"name":{"type":"string","free":true,"from":"row:name"}}}]}],
 "bindings":[],"pages":[]}`

const idPrefixedArms = `"thing list") echo '[{"noun":"thing","id":"thing/alpha","label":"alpha","cells":["alpha"],"data":{"name":"alpha"}},{"noun":"thing","id":"thing/beta","label":"beta","cells":["beta"],"data":{"name":"beta"}}]' ;;
"thing delete") echo '{"ok":true}' ;;`

func TestExtensionDeleteKeyPassesWhatTheToolResolves(t *testing.T) {
	t.Parallel()
	bin, log := fakeExtension(t, idPrefixedCatalog, idPrefixedArms)
	a := registered(t, seeded(t), "fake", bin)
	openExtPage(t, a, "ext/fake")
	if err := a.r.SetFocus("ext/fake/thing/thing/beta"); err != nil {
		t.Fatal(err)
	}
	n := len(calls(t, log))
	pressJob(t, a, "d")
	pressJob(t, a, "d")
	pressJob(t, a, "y")
	var got []string
	for _, c := range calls(t, log)[n:] {
		if strings.Contains(c, "thing delete") {
			got = append(got, c[strings.LastIndex(c, "|")+1:])
		}
	}
	t.Logf("calls: %v", got)
	if len(got) != 1 {
		t.Fatalf("delete calls: %v", got)
	}
	if want := `thing delete --json {"name":"beta"} --yes`; got[0] != want {
		t.Fatalf("delete call %q, want %q", got[0], want)
	}
}

// The real o-df catalog (testdata/o-df-catalog.json, `o-df catalog`): rows
// id culprit/NAME, a delete whose name param is from row:name.
func TestExtensionDeleteKeyOnTheRealODfCatalog(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/o-df-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	arms := `"culprit list") echo '[{"noun":"culprit","id":"culprit/go-build-cache","label":"go-build-cache","cells":["go-build-cache","1 GB","yes","","delete"],"data":{"name":"go-build-cache","title":"Go build cache"}},{"noun":"culprit","id":"culprit/npm-cache","label":"npm-cache","cells":["npm-cache","1 GB","yes","","delete"],"data":{"name":"npm-cache"}}]' ;;
"culprit delete") echo '{"ok":true}' ;;
"culprit names") echo '[{"id":"go-build-cache","label":"go-build-cache"}]' ;;`
	bin, log := fakeExtension(t, string(raw), arms)
	a := registered(t, seeded(t), "df", bin)
	openExtPage(t, a, "ext/df/df")
	if err := a.r.SetFocus("ext/df/df/culprits/culprit/go-build-cache"); err != nil {
		t.Fatal(err)
	}
	n := len(calls(t, log))
	pressJob(t, a, "d")
	pressJob(t, a, "d")
	pressJob(t, a, "y")
	var got []string
	for _, c := range calls(t, log)[n:] {
		if strings.Contains(c, "culprit delete") {
			got = append(got, c[strings.LastIndex(c, "|")+1:])
		}
	}
	if want := `culprit delete --json {"name":"go-build-cache"} --yes`; len(got) != 1 || got[0] != want {
		t.Fatalf("delete calls %q, want %q (the tool resolves a bare name, not the list id culprit/NAME)", got, want)
	}
}
