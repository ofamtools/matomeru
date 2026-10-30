package main

import (
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// The verbs renamed when ochazuke refused dotted verbs keep their old
// names as deprecated aliases (Keep actions), so a host holding an old
// catalog (nabe's registry: dirs base.add) still reaches them.
func TestRenamedVerbsKeepTheirOldNames(t *testing.T) {
	a := openWith(t, Options{Data: seeded(t)})
	for old, canonical := range map[string]string{
		"dirs.base.add":            "dirs.add-base",
		"dirs.base.remove":         "dirs.remove-base",
		"project.delete.confirm":   "project.delete",
		"task.delete.confirm":      "task.delete",
		"project.directory.edit":   "project.edit-directory",
		"project.directory.remove": "project.remove-directory",
	} {
		alias, ok := a.r.ActionAlias(old)
		if !ok || alias.Action != canonical || !alias.Deprecated() {
			t.Errorf("%s: %+v %v, want a deprecated alias of %s", old, alias, ok, canonical)
		}
		if op, ok := a.r.Operation(old); !ok || op.Key() != canonical {
			t.Errorf("%s resolves to %s", old, op.Key())
		}
	}
	if q := a.r.CanonicalRequest(ochazuke.Request{Noun: "dirs", Verb: "base.add"}); q.Key() != "dirs.add-base" || q.Via != "dirs.base.add" {
		t.Fatalf("request %+v", q)
	}
}
