package main

import (
	"slices"
	"strings"
	"testing"
)

// rowGuardCatalog is crudCatalog with a mark verb and key rows guarded by
// the row: a row guard (row:!item) on the mark key and on d d, and an
// application guard the tool alone defines on M.
func rowGuardCatalog() string {
	c := crudCatalogNamed("create", "edit", "delete")
	c = strings.Replace(c, `{"name":"list","kind":"application"},`, `{"name":"list","kind":"application"},{"name":"mark","kind":"application"},`, 1)
	return strings.Replace(c, `"bindings":[`, `"bindings":[`+
		`{"noun":"thing","mode":"normal","keys":"g m","when":"row:!item","request":{"noun":"thing","verb":"mark"}},`+
		`{"noun":"thing","mode":"normal","keys":"Z","when":"tool-only-guard","request":{"noun":"thing","verb":"mark"}},`+
		`{"noun":"thing","mode":"normal","keys":"d d","when":"row:!item","request":{"noun":"thing","verb":"delete"}},`, 1)
}

// A catalog key row with a row guard keeps it when mtm mounts it: on a row
// whose record has item (a checklist item under a question) neither the
// guarded key nor d d acts; on the other row both do. An application
// guard is still left off (mtm cannot judge it) and its key runs anywhere.
func TestExtensionRowGuardKeepsKeysOffNonMatchingRows(t *testing.T) {
	t.Parallel()
	bin, log := fakeExtension(t, rowGuardCatalog(), `"thing list") echo '[{"id":"t1","label":"one","cells":["one"],"data":{"title":"one","item":"i1"}},{"id":"t2","label":"two","cells":["two"],"data":{"title":"two"}}]' ;;
"thing mark"|"thing delete") echo '{"ok":true}' ;;`)
	a := registered(t, seeded(t), "fake", bin)
	openExtPage(t, a, "ext/fake")
	if l := a.r.List("ext/fake/thing"); l == nil || l.Len() != 2 {
		t.Fatalf("rows not loaded: %+v", l)
	}
	if slices.ContainsFunc(a.m.Notices, func(s string) bool { return strings.Contains(s, " on thing:") && strings.Contains(s, "z ") }) {
		t.Fatalf("g m dropped: %q", a.m.Notices)
	}
	focus := func(id string) {
		t.Helper()
		if err := a.r.SetFocus(id); err != nil {
			t.Fatal(err)
		}
	}

	focus("ext/fake/thing/t1")
	n := len(calls(t, log))
	press(t, a, "g")
	pressJob(t, a, "m")
	press(t, a, "d", "d")
	if modal := a.r.Instances[a.r.TopModal()]; modal != nil && modal.Payload.Prompt != "" {
		t.Fatalf("d d on an item row asked %q", modal.Payload.Prompt)
	}
	if got := since(t, log, n); len(got) != 0 {
		t.Fatalf("guarded keys ran on the item row: %q", got)
	}
	pressJob(t, a, "Z")
	if got := since(t, log, n); len(got) != 1 || got[0] != "thing mark --target t1 --json {} --yes" {
		t.Fatalf("an application guard is left off, Z runs: %q", got)
	}

	focus("ext/fake/thing/t2")
	n = len(calls(t, log))
	press(t, a, "g")
	pressJob(t, a, "m")
	if got := since(t, log, n); len(got) != 1 || got[0] != "thing mark --target t2 --json {} --yes" {
		t.Fatalf("g m on a matching row: %q", got)
	}
	press(t, a, "d", "d")
	if modal := a.r.Instances[a.r.TopModal()]; modal == nil || modal.Payload.Prompt != "Delete two?" {
		t.Fatalf("d d on a matching row: no confirm: %+v", modal)
	}
	pressJob(t, a, "y")
	if got := since(t, log, n); len(got) != 2 || got[1] != "thing delete --target t2 --json {} --yes" {
		t.Fatalf("d d on a matching row: %q", got)
	}
}
