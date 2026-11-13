package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/store"
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
	// These tests are about the owner grouping (needs-you is the default).
	setting(t, a, settingInboxGroup, groupOwner)
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

// The group projection reads pane and project names from lookup tables;
// every grouping gives the rows, order, labels and classes the earlier
// per-row JSON lookups gave (jsonLookupGroupProjection, kept here as the
// reference).
func TestInboxGroupSQLMatchesJSONLookups(t *testing.T) {
	t.Parallel()
	a, _ := groupApp(t)
	addItem(t, a, actionitems.Spec{Source: "gh", Key: "gone-project", Title: "gone-project", ProjectID: "no-such-project"})
	addItem(t, a, actionitems.Spec{Source: "gh", Key: "gone-task", Title: "gone-task", TaskID: "no-such-task"})
	for _, mode := range inboxGroupNames() {
		setting(t, a, settingInboxGroup, mode)
		q := ochazuke.ListQuery{Archived: true, GroupBy: []string{inboxGroupProp}, Sort: []ochazuke.SortKey{{Field: inboxGroupClassProp}}, Page: 1, PerPage: 1000}
		rows := func(project func() []store.QueryProjection) []string {
			tq := a.actionTableQuery()
			tq.projections[inboxGroupField] = project
			page, err := tq.Query(ochazuke.Ctx{}, q)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, th := range page.Items {
				out = append(out, fmt.Sprint(th["id"], "|", th[inboxGroupProp], "|", th[inboxGroupClassProp]))
			}
			return out
		}
		got, want := rows(a.inboxGroupProjection), rows(func() []store.QueryProjection { return jsonLookupGroupProjection(a) })
		if len(want) < 9 || !slices.Equal(got, want) {
			t.Errorf("inbox.group %s:\n got %q\nwant %q", mode, got, want)
		}
	}
}

// jsonLookupGroupProjection is the group projection as it was before the
// lookup tables: names searched in one JSON argument for each row.
func jsonLookupGroupProjection(a *App) []store.QueryProjection {
	panes, projects := map[string]string{}, map[string]string{}
	for _, p := range a.panes {
		panes[p.ID] = p.label()
	}
	for _, p := range a.projects {
		projects[p.ID] = projectName(p)
	}
	names, _ := json.Marshal(map[string]any{
		"panes": panes, "projects": projects,
		"known": len(a.panes) > 0 && a.paneErr == "", "now": a.inbox.items.Now(),
	})
	label := func(id string) string {
		return `CASE WHEN ` + id + ` = '' THEN 'unassociated' ELSE COALESCE((SELECT value FROM json_each(metadata, '$.projects') WHERE key = ` + id + `), ` + id + `) END`
	}
	g := a.inboxGrouping()
	expr := `json((SELECT json_object('label', ` + g.labelSQL + `, 'class', ` + g.classSQL + `) FROM (
SELECT *,
 CASE WHEN pane != '' THEN pane WHEN known THEN 'closed pane ' || COALESCE(NULLIF(pane_label, ''), window) ELSE 'pane ' || COALESCE(NULLIF(pane_label, ''), window) END AS pane_name,
 ` + label("effective_project") + ` AS project_name
FROM (
 SELECT *, COALESCE(NULLIF(project_id, ''), json_extract(task_row, '$.project_id'), '') AS effective_project,
 ` + label("project_id") + ` AS project,
 CASE WHEN task_row IS NULL THEN 'task ' || task_id ELSE ` + label("COALESCE(json_extract(task_row, '$.project_id'), '')") + ` || ' › ' || json_extract(task_row, '$.title') END AS task
 FROM (
  SELECT item, metadata,
   COALESCE(json_extract(item, '$.window'), '') AS window,
   COALESCE(json_extract(item, '$.pane_label'), '') AS pane_label,
   COALESCE(json_extract(item, '$.task_id'), '') AS task_id,
   COALESCE(json_extract(item, '$.project_id'), '') AS project_id,
   (SELECT t.row FROM tasks AS t WHERE t.id = json_extract(item, '$.task_id')) AS task_row,
   COALESCE((SELECT value FROM json_each(metadata, '$.panes') WHERE key = json_extract(item, '$.window')), '') AS pane,
   json_extract(metadata, '$.known') AS known,
   CASE WHEN COALESCE(json_extract(item, '$.dismissed_at'), 0) > 0 THEN 'archived' WHEN COALESCE(json_extract(item, '$.snoozed_until'), 0) > json_extract(metadata, '$.now') THEN 'snoozed' ELSE 'open' END AS state
  FROM (SELECT source.row AS item, ? AS metadata)
 )
)
)))`
	return []store.QueryProjection{{Field: inboxGroupField, SQL: expr, Args: []any{string(names)}}}
}
