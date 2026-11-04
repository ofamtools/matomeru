package main

import (
	"strings"
	"testing"
)

// A mounted page whose extension answers its state without a live noun
// (o-ai's quota) still shows the live list's rows, fetched through the
// noun's list verb when the page opens; a plain reload within the live TTL
// does not ask again, an explicit refresh does.
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
	if count() != 1 || a.r.List("ext/live/quota").Len() != 1 {
		t.Fatalf("a reload inside the live TTL asked again: %d calls", count())
	}
	a.refreshExtensionRows(x)
	settleJobs(t, a, a.r.PendingCommands())
	if count() != 2 {
		t.Fatalf("an explicit refresh ran quota list %d times in all", count())
	}
}
