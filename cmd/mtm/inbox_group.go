package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

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
	name string
	of   func(a *App, it actionitems.Item, now int64) itemGroup
}

var inboxGroupTable = []inboxGrouping{
	{name: groupOwner, of: (*App).groupByOwner},
	{name: "pane", of: (*App).groupByPane},
	{name: "project", of: (*App).groupByProject},
	{name: "source", of: (*App).groupBySource},
	{name: "state", of: (*App).groupByState},
	{name: groupNone},
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

// groupInboxRows puts rows (one per item, in the store's order) under
// group headers and in group order.
func (a *App) groupInboxRows(items []actionitems.Item, rows []ochazuke.ListItem, now int64) []ochazuke.ListItem {
	g := a.inboxGrouping()
	if g.of == nil || len(items) != len(rows) {
		return rows
	}
	groups := make([]itemGroup, len(items))
	first := map[string]int{}
	class := map[string]int{}
	count := map[string]int{}
	for i, it := range items {
		groups[i] = g.of(a, it, now)
		label := groups[i].label
		if _, ok := first[label]; !ok {
			first[label], class[label] = i, groups[i].class
		}
		count[label]++
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(x, y int) int {
		lx, ly := groups[x].label, groups[y].label
		if lx == ly {
			return 0
		}
		return cmp.Or(cmp.Compare(class[lx], class[ly]), cmp.Compare(first[lx], first[ly]))
	})
	out := make([]ochazuke.ListItem, 0, len(rows))
	for _, i := range order {
		row := rows[i]
		row.Section = fmt.Sprintf("%s (%d)", groups[i].label, count[groups[i].label])
		out = append(out, row)
	}
	return out
}

// inboxGroupSig is what the grouped rows depend on beyond the items: the
// grouping and the pane names. A change rebuilds them.
func (a *App) inboxGroupSig(items []actionitems.Item) string {
	var b strings.Builder
	b.WriteString(a.inboxGrouping().name + ";")
	b.WriteString(a.inboxPaneSig(items))
	return b.String()
}
