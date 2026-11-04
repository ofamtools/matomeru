package main

import (
	"ofam.dev/ochazuke"
	"os"
	"strings"
	"testing"
)

// A mounted page whose extension answers its state without a live noun
// (o-ai's quota) still shows the live list's rows, fetched through the
// noun's list verb when the page opens and on every reload (the tool owns
// its caching).
func TestExtensionLiveNounListFillsThroughItsVerb(t *testing.T) {
	t.Parallel()
	catalog := strings.Replace(strings.Replace(fakeCatalog, "VERSION", "1.0", 1), `"pages":[]`,
		`"state":"item.state","pages":[{"id":"main","title":"Fake Live","layout":"groups","lists":[{"id":"items","noun":"item"},{"id":"quota","noun":"quota"}]}]`, 1)
	catalog = strings.Replace(catalog, `{"name":"list","kind":"application","description":"List the items"},`,
		`{"name":"list","kind":"application","description":"List the items"},{"name":"state","kind":"application"},`, 1)
	catalog = strings.Replace(catalog, ` {"name":"app",`, ` {"name":"quota","live":true,"verbs":[{"name":"list","kind":"application","rows":"quota"}]},
 {"name":"app",`, 1)
	bin, log := fakeExtension(t, catalog, `"item state") echo '{"noun":"item","id":"i1","label":"one"}' ;;
"quota list") echo '[{"id":"quota/claude:session","label":"claude session 7% used"}]' ;;`)
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "live", "--bin", bin)
	a := openAt(t, path)
	openExtPage(t, a, "ext/live/main")
	quota := a.r.List("ext/live/quota")
	if quota == nil || quota.Len() != 1 {
		t.Fatalf("the quota list has no rows: %+v", quota)
	}
	if items := a.r.List("ext/live/items"); items == nil || items.Len() != 1 {
		t.Fatalf("the state's list lost its rows: %+v", items)
	}
	count := func() (n int) {
		for _, c := range calls(t, log) {
			if strings.Contains(c, "|quota list") {
				n++
			}
		}
		return n
	}
	x := a.extension("live")
	a.reloadExtension(x)
	settleJobs(t, a, a.r.PendingCommands())
	if count() != 2 || a.r.List("ext/live/quota").Len() != 1 {
		t.Fatalf("a reload ran quota list %d times in all", count())
	}
	a.refreshExtensionRows(x)
	settleJobs(t, a, a.r.PendingCommands())
	if count() != 3 {
		t.Fatalf("an explicit refresh ran quota list %d times in all", count())
	}
}

// o-ai's quota catalog (testdata/o-ai-quota-catalog.json: its quota and
// status nouns and its F binding, as `o-ai catalog` publishes them) is
// mounted as the ai extension, whose page mtm builds itself. The quota
// list shows the cached rows; F on a row runs quota.refresh, whose answer
// differs, and the list shows the new row. The F row is not dropped by
// keyTaken.
func TestExtensionLiveNounRefreshKeyReachesTheVerb(t *testing.T) {
	t.Parallel()
	catalog, err := os.ReadFile("testdata/o-ai-quota-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := fakeExtension(t, string(catalog), `"status state") echo '{"noun":"status","id":"status/s","label":"s"}' ;;
"quota list") if [ -f "$0.fresh" ]; then echo '[{"id":"quota/claude:session","label":"claude session 9% used (fresh)"}]'; else echo '[{"id":"quota/claude:session","label":"claude session 7% used (cached)"}]'; fi ;;
"quota refresh") touch "$0.fresh"; echo '[{"id":"quota/claude:session","label":"claude session 9% used (fresh)"}]' ;;`)
	path := seeded(t)
	out := mustCLI(t, openAt(t, path), "extension", "register", "ai", "--bin", bin)
	if !strings.Contains(out, `"name": "ai"`) {
		t.Fatalf("register answered:\n%s", out)
	}
	a := openAt(t, path)
	var page string
	for _, x := range a.exts {
		for _, v := range x.views {
			page = v.Page()
		}
	}
	openExtPage(t, a, page)
	list := a.r.List(page + "/quota")
	if list == nil || list.Len() != 1 || !strings.Contains(rowLabels(list)[0], "cached") {
		t.Fatalf("the quota group before the key: %v", list)
	}
	if err := a.r.SetFocus(list.Items()[0].ID); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "F")
	list = a.r.List(page + "/quota")
	if list.Len() != 1 || !strings.Contains(rowLabels(list)[0], "fresh") {
		t.Fatalf("F did not show the new row: %v; calls %v", rowLabels(list), calls(t, log))
	}
	var refreshed bool
	for _, c := range calls(t, log) {
		refreshed = refreshed || strings.Contains(c, "|quota refresh")
	}
	if !refreshed {
		t.Fatalf("F never reached quota.refresh: %v", calls(t, log))
	}
}

func rowLabels(l interface{ Items() []ochazuke.ListItem }) []string {
	var out []string
	for _, it := range l.Items() {
		out = append(out, it.Label)
	}
	return out
}
