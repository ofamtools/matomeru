package main

import (
	"slices"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// ochazuke v0.100.0 (list.filter-keeps-children): filtering the Settings
// page by "task" keeps agents.owner-task-props with its item rows, and enter
// on an item opens "item 1 of 2".
func TestSettingsFilterKeepsListItems(t *testing.T) {
	t.Parallel()
	a, _, _ := scopeApp(t)
	executeA(t, a, "ochazuke.setting", "open", "", nil)
	a.m.Width, a.m.Height = 120, 40
	plainView(a)
	sections := a.r.List("__settings/sections")
	if sections == nil {
		t.Fatal("no settings sections list")
	}
	if err := sections.Focus(); err != nil {
		t.Fatal(err)
	}
	sections.SetFilter("task")
	all := a.r.List("__settings/list//all")
	if all == nil {
		t.Fatal("no All section list")
	}
	var ids []string
	for _, it := range all.Items() {
		ids = append(ids, it.ID)
	}
	want := []string{"__settings/all/row/agents.owner-task-props", "__settings/all/item/agents.owner-task-props/0", "__settings/all/item/agents.owner-task-props/1"}
	i := slices.Index(ids, want[0])
	if i < 0 || !slices.Equal(ids[i:i+3], want) {
		t.Fatalf("shown %v, want %v in a row", ids, want)
	}
	if err := a.r.SetFocus(want[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "ochazuke.setting-item", Verb: "edit"}); err != nil {
		t.Fatal(err)
	}
	if f := xansi.Strip(frameView(a).Content); !strings.Contains(f, "item 1 of 2") {
		t.Fatalf("frame:\n%s", f)
	}
}
