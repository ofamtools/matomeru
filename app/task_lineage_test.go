package app

import (
	"slices"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/widgets/palette"
)

func TestTaskLineageRewriteBindingAndMenus(t *testing.T) {
	a, d := demoLegacy(t)
	d.Keys("2")
	for _, id := range []string{CmdTaskRewrite, CmdTaskSplit, CmdTaskJoin} {
		if !slices.ContainsFunc(a.paletteEntries(), func(entry palette.Entry) bool { return entry.Spec.ID == id }) {
			t.Fatalf("palette has no %s", id)
		}
	}
	d.Keys(";")
	for _, id := range []string{CmdTaskRewrite, CmdTaskSplit, CmdTaskJoin} {
		if !slices.Contains(pickerIDs(a, "record.actions"), id) {
			t.Fatalf("task action picker has no %s", id)
		}
	}
	d.Keys("esc", "esc")
	d.Keys("g", "W")
	m := a.activeLaunchForm()
	if m == nil || m.Model().Title != "Rewrite Task" {
		t.Fatalf("g W opened %q, want rewrite form", a.Modal())
	}
	m.SetValue("title", "Rewritten task")
	m.SetValue("body", "New Markdown body")
	d.Keys("ctrl+s")
	rec := titled(t, a, data.KindTask, "Rewritten task")
	links, err := a.Store.Related(rec.Ref(), data.RelDerivedFrom, data.Outgoing)
	if a.Modal() != "" || err != nil || len(links) != 1 || rec.Text("description") != "New Markdown body" {
		t.Fatalf("rewrite submission: modal %q, record %+v, relations %v, err %v", a.Modal(), rec, links, err)
	}
}
