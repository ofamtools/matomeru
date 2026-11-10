package main

import (
	"cmp"
	"slices"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
)

// The Inbox page's rows are drawn in groups, a labelled header over each
// run (ListItem.Section), by the setting inbox.group: a row of
// inboxGroupTable. A group's rows keep the store's order (open unread,
// open read, snoozed, archived; newest first); the groups follow their
// class, then the order they first appear in. none draws today's flat
// table.
//
//   - owner: the pane the item is about while it is listed, else its task,
//     else its project, else the pane once it has closed (by the name the
//     item kept), else unassociated.
//   - pane: the listed pane, then each closed pane, then items with no pane.
//   - project: the item's project (its task's when it has one), then none.
//   - source: where the item came from.
//   - state: open, snoozed, archived.

const (
	settingInboxGroup = "inbox.group"
	groupOwner        = "owner"
	groupNone         = "none"
)

// itemGroup is the group an item is drawn in: its header and where the
// group sorts (lower first).
type itemGroup struct {
	label string
	class int
}

// inboxGrouping is one value of inbox.group; of is nil for none.
type inboxGrouping struct {
	name               string
	of                 func(a *App, it actionitems.Item, now int64) itemGroup
	labelSQL, classSQL string
}

var inboxGroupTable = []inboxGrouping{
	{name: groupOwner, of: (*App).groupByOwner,
		labelSQL: `CASE WHEN window != '' AND (pane != '' OR NOT known) THEN pane_name WHEN task_id != '' THEN task WHEN project_id != '' THEN project WHEN window != '' THEN pane_name ELSE 'unassociated' END`,
		classSQL: `CASE WHEN window != '' AND (pane != '' OR NOT known) THEN 0 WHEN task_id != '' THEN 1 WHEN project_id != '' THEN 2 WHEN window != '' THEN 3 ELSE 4 END`},
	{name: "pane", of: (*App).groupByPane,
		labelSQL: `CASE WHEN window = '' THEN 'no pane' ELSE pane_name END`,
		classSQL: `CASE WHEN window = '' THEN 2 WHEN pane != '' OR NOT known THEN 0 ELSE 1 END`},
	{name: "project", of: (*App).groupByProject,
		labelSQL: `CASE WHEN effective_project = '' THEN 'no project' ELSE project_name END`,
		classSQL: `CASE WHEN effective_project = '' THEN 1 ELSE 0 END`},
	{name: "source", of: (*App).groupBySource, labelSQL: `COALESCE(NULLIF(json_extract(item, '$.service'), ''), 'manual')`, classSQL: `0`},
	{name: "state", of: (*App).groupByState, labelSQL: `state`, classSQL: `CASE state WHEN 'open' THEN 0 WHEN 'snoozed' THEN 1 ELSE 2 END`},
	{name: groupNone, labelSQL: `''`, classSQL: `0`},
}

func inboxGroupNames() []string {
	out := make([]string, len(inboxGroupTable))
	for i, g := range inboxGroupTable {
		out[i] = g.name
	}
	return out
}

func inboxGroupSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingInboxGroup, Type: ochazuke.SettingString, Default: groupOwner, Enum: inboxGroupNames(),
			Description: "How the Inbox page's rows are grouped under headers: owner (pane, else task, else project, else unassociated), pane, project, source, state, or none for one flat table"},
	}
}

// itemPaneKnown says how the listing stands to the pane an item names:
// listed (with its label), closed (the listing is known and lacks it), or
// unknown (no listing yet).
func (a *App) itemPaneKnown(it actionitems.Item) (label string, listed, known bool) {
	if label = a.paneLabelOf(it.Window); label != "" {
		return label, true, true
	}
	return cmp.Or(it.PaneLabel, it.Window), false, len(a.panes) > 0 && a.paneErr == ""
}

// groupByPane's and groupByOwner's pane groups share one naming.
func (a *App) paneGroup(it actionitems.Item, listedClass, closedClass int) itemGroup {
	label, listed, known := a.itemPaneKnown(it)
	switch {
	case listed:
		return itemGroup{label, listedClass}
	case known:
		return itemGroup{"closed pane " + label, closedClass}
	}
	return itemGroup{"pane " + label, listedClass}
}

func (a *App) groupByOwner(it actionitems.Item, _ int64) itemGroup {
	if it.Window != "" {
		if _, listed, known := a.itemPaneKnown(it); listed || !known {
			return a.paneGroup(it, 0, 3)
		}
	}
	if it.TaskID != "" {
		return itemGroup{a.inboxOwner(it), 1}
	}
	if it.ProjectID != "" {
		return itemGroup{a.ownerLabel(it.ProjectID), 2}
	}
	if it.Window != "" {
		return a.paneGroup(it, 0, 3)
	}
	return itemGroup{"unassociated", 4}
}

func (a *App) groupByPane(it actionitems.Item, _ int64) itemGroup {
	if it.Window == "" {
		return itemGroup{"no pane", 2}
	}
	return a.paneGroup(it, 0, 1)
}

func (a *App) groupByProject(it actionitems.Item, _ int64) itemGroup {
	if id := cmp.Or(it.ProjectID, a.taskProject(it.TaskID)); id != "" {
		return itemGroup{a.ownerLabel(id), 0}
	}
	return itemGroup{"no project", 1}
}

func (a *App) groupBySource(it actionitems.Item, _ int64) itemGroup {
	return itemGroup{cmp.Or(it.Source, "manual"), 0}
}

func (a *App) groupByState(it actionitems.Item, now int64) itemGroup {
	state := it.State(now)
	return itemGroup{state, slices.Index(actionitems.States, state)}
}

// inboxGrouping is the table row inbox.group names; an unknown value
// groups by owner.
func (a *App) inboxGrouping() inboxGrouping {
	name := a.r.Settings().String(settingInboxGroup)
	if i := slices.IndexFunc(inboxGroupTable, func(g inboxGrouping) bool { return g.name == name }); i >= 0 {
		return inboxGroupTable[i]
	}
	return inboxGroupTable[0]
}

// syncInboxGrouping declares the curated view; arranging and folding belong to
// the collection, and its source query groups before selecting a bounded page.
func (a *App) syncInboxGrouping() error {
	var by []string
	var sort []ochazuke.SortKey
	if a.inboxGrouping().of != nil {
		by = []string{inboxGroupProp}
		sort = []ochazuke.SortKey{{Field: inboxGroupClassProp}}
	}
	view := a.inbox.list.ListView()
	if !slices.Equal(view.Sort, sort) {
		if err := a.inbox.list.SetSort(sort); err != nil {
			return err
		}
	}
	if !slices.Equal(view.GroupBy, by) || len(view.GroupSort) > 0 {
		return a.inbox.list.SetGroupBy(by, nil)
	}
	return nil
}

// inboxGroupSig also invalidates the source's derived group labels when the
// grouping or a listed pane's name changes.
func (a *App) inboxGroupSig(items []actionitems.Item) string {
	return a.inboxGrouping().name + ";" + a.inboxPaneSig(items)
}
