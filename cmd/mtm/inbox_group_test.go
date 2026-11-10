package main

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
)

// inboxSections are the Inbox rows' ids under each group header, in
// drawing order.
func inboxSections(a *App) (headers []string, rows map[string][]string) {
	rows = map[string][]string{}
	for _, it := range a.inbox.list.Items() {
		if len(headers) == 0 || headers[len(headers)-1] != it.Section {
			headers = append(headers, it.Section)
		}
		rows[it.Section] = append(rows[it.Section], savedRowKey(a.r.Instances[it.ID]))
	}
	return headers, rows
}

// groupApp is sectionsApp's panes with items of every owner: a listed
// pane, a closed pane (with and without a kept name), a task, a project, a
// source with nothing and a read one.
func groupApp(t *testing.T) (*App, map[string]actionitems.Item) {
	t.Helper()
	a, _ := sectionsApp(t)
	task, err := a.work.AddTask("matomeru", "ship it")
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]actionitems.Item{}
	add := func(key string, spec actionitems.Spec) {
		spec.Source, spec.Key, spec.Title = cmp.Or(spec.Source, "gh"), key, key
		items[key] = addItem(t, a, spec)
	}
	add("none", actionitems.Spec{Source: "slack"})
	add("project", actionitems.Spec{ProjectID: "matomeru"})
	add("task", actionitems.Spec{TaskID: task.ID, ProjectID: "matomeru"})
	add("live", actionitems.Spec{Window: "widle", Session: "s"})
	add("live2", actionitems.Spec{Window: "widle", Session: "s", ProjectID: "matomeru"})
	add("closed", actionitems.Spec{Window: "wgone", Session: "s", PaneLabel: "old box"})
	add("closed-task", actionitems.Spec{Window: "wgone", Session: "s", TaskID: task.ID, ProjectID: "matomeru"})
	return a, items
}

func TestInboxGroupOwner(t *testing.T) {
	t.Parallel()
	a, items := groupApp(t)
	headers, rows := inboxSections(a)
	want := []string{"s/idler", "s/waiter", "s/errs", "s/finisher", "matomeru › ship it", "matomeru", "closed pane old box", "unassociated"}
	if !slices.Equal(headers, want) {
		t.Fatalf("headers %q, want %q", headers, want)
	}
	if !slices.Contains(rows["s/idler"], items["live2"].ID) {
		t.Fatalf("a listed pane wins over the project: %v", rows)
	}
	// The closed pane falls through to its task when it has one.
	if !slices.Contains(rows["matomeru › ship it"], items["closed-task"].ID) {
		t.Fatalf("a closed pane with a task is grouped by the task: %v", rows)
	}
}

func TestInboxGroupPaneProjectSourceStateNone(t *testing.T) {
	t.Parallel()
	a, _ := groupApp(t)
	cases := []struct {
		mode string
		want []string
	}{
		{"pane", []string{"s/idler", "s/waiter", "s/errs", "s/finisher", "closed pane wgone", "closed pane old box", "no pane"}},
		{"project", []string{"matomeru", "no project"}},
		{"source", []string{"gh", "slack", "mtm"}},
		{"state", []string{"open"}},
	}
	for _, c := range cases {
		setting(t, a, settingInboxGroup, c.mode)
		if err := a.inboxLoad.refresh(); err != nil {
			t.Fatal(err)
		}
		if got, _ := inboxSections(a); !slices.Equal(got, c.want) {
			t.Errorf("inbox.group %s: %q, want %q", c.mode, got, c.want)
		}
	}
	// none is today's flat table: no headers, the store's order.
	setting(t, a, settingInboxGroup, groupNone)
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	var flat []string
	for _, it := range a.inbox.list.Items() {
		if it.Section != "" {
			t.Fatalf("a header with inbox.group none: %q", it.Section)
		}
		flat = append(flat, savedRowKey(a.r.Instances[it.ID]))
	}
	var stored []string
	for _, it := range a.inbox.all {
		stored = append(stored, it.ID)
	}
	if !slices.Equal(flat, stored) {
		t.Fatalf("flat order %v, stored %v", flat, stored)
	}
}

// A change of inbox.group or of a pane name rebuilds the rows on the next
// reload.
func TestInboxGroupFollowsSettingAndPaneNames(t *testing.T) {
	t.Parallel()
	a, _ := groupApp(t)
	setting(t, a, settingInboxGroup, "source")
	if err := a.reloadInbox(); err != nil {
		t.Fatal(err)
	}
	if headers, _ := inboxSections(a); !slices.Contains(headers, "slack") {
		t.Fatalf("a reload kept the old grouping: %q", headers)
	}
	setting(t, a, settingInboxGroup, groupOwner)
	panes := slices.Clone(a.panes)
	for i := range panes {
		if panes[i].ID == "widle" {
			panes[i].Name = "renamed"
		}
	}
	a.setPanes(panes, nil)
	headers, _ := inboxSections(a)
	if !slices.ContainsFunc(headers, func(h string) bool { return strings.Contains(h, "renamed") }) {
		t.Fatalf("a pane rename did not reach the headers: %q", headers)
	}
}

func TestInboxGroupSettingValidates(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.r.Settings().Set(settingInboxGroup, "bogus"); err == nil {
		t.Fatal("inbox.group took an unknown value")
	}
}
