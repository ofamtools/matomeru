package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
)

// A pane row's child rows are the rows of its child kinds, in the order
// panes.children gives them. Each kind is a row of childKinds: a function
// from a pane to the TreeNodes under it. Today's kinds are the pane's
// action items (items) and its running subagents (subagents); a new kind
// is a new row of the table. Leaving items out of panes.children turns
// the item rows off: items go back to queue rows of their own.
//
// An action item that names a pane still listed is a child row of that
// pane's row, whichever list the pane is in; an item whose pane is gone
// or that names none is its own row in the top list.
//
// Settings: panes.children (default items, subagents), panes.child-items
// (which states: open, open+snoozed, all), panes.item-count (the count
// after a pane's name, default on) and panes.items-open (the first fold of
// a pane row that has item rows: queue, all or none).

const (
	settingPaneChildren = "panes.children"
	settingChildItems   = "panes.child-items"
	settingItemCount    = "panes.item-count"
	settingItemsOpen    = "panes.items-open"

	childKindItems     = "items"
	childKindSubagents = "subagents"

	childItemsOpen        = "open"
	childItemsOpenSnoozed = "open+snoozed"
	childItemsAll         = "all"

	itemsOpenQueue = "queue"
	itemsOpenAll   = "all"
	itemsOpenNone  = "none"

	itemCountMark = "◆"
)

// childKind is one kind of child row of a pane row.
type childKind struct {
	name  string
	nodes func(a *App, p pane, parent string, now time.Time) []ochazuke.TreeNode
}

// childKindTable is every kind panes.children takes.
var childKindTable = []childKind{
	{name: childKindItems, nodes: (*App).itemNodes},
	{name: childKindSubagents, nodes: func(a *App, p pane, parent string, now time.Time) []ochazuke.TreeNode {
		return a.activityNodes(p, parent, now)
	}},
}

func childKindNames() []string {
	out := make([]string, len(childKindTable))
	for i, k := range childKindTable {
		out[i] = k.name
	}
	return out
}

func paneChildSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingPaneChildren, Type: ochazuke.SettingList, Default: childKindNames(),
			Description: "The child rows under a pane's row, in order: items (its action items) and subagents (its running subagents). Leave items out and an item is a row of its own in the top list",
			Validate: func(v any) error {
				for _, k := range listValue(v) {
					if !slices.Contains(childKindNames(), k) {
						return fmt.Errorf("child %q: one of %s", k, strings.Join(childKindNames(), ", "))
					}
				}
				return nil
			}},
		{Name: settingChildItems, Type: ochazuke.SettingString, Default: childItemsOpen,
			Enum:        []string{childItemsOpen, childItemsOpenSnoozed, childItemsAll},
			Description: "Which of a pane's action items are its child rows: open ones, open and snoozed, or all (archived too)"},
		{Name: settingItemCount, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Show the number of a pane's open action items after its name"},
		{Name: settingItemsOpen, Type: ochazuke.SettingString, Default: itemsOpenQueue,
			Enum:        []string{itemsOpenQueue, itemsOpenAll, itemsOpenNone},
			Description: "A pane row with item rows starts unfolded in the top list only (queue), in every list (all), or folded (none); a fold you make is kept"},
	}
}

// childKinds are the kinds panes.children names, in its order; a name not
// in the table is skipped.
func (a *App) childKinds() []childKind {
	var out []childKind
	for _, name := range a.r.Settings().Strings(settingPaneChildren) {
		if i := slices.IndexFunc(childKindTable, func(k childKind) bool { return k.name == name }); i >= 0 {
			out = append(out, childKindTable[i])
		}
	}
	return out
}

// childNodes are the child rows of the pane's row parent: each kind's, in
// order.
func (a *App) childNodes(p pane, parent string, now time.Time) []ochazuke.TreeNode {
	var out []ochazuke.TreeNode
	for _, k := range a.childKinds() {
		out = append(out, k.nodes(a, p, parent, now)...)
	}
	return out
}

// paneKidItems sorts the inbox's items by the window they name: those that
// are child rows (panes.child-items; a category panes.queue-order leaves
// out is not shown), and the count of open ones.
func (a *App) paneKidItems(nowMs int64) (map[string][]actionitems.Item, map[string]int) {
	items, counts := map[string][]actionitems.Item{}, map[string]int{}
	if a.inbox == nil {
		return items, counts
	}
	ranks := a.queueRanks()
	states := map[string]bool{actionitems.StateOpen: true}
	switch a.r.Settings().String(settingChildItems) {
	case childItemsOpenSnoozed:
		states[actionitems.StateSnoozed] = true
	case childItemsAll:
		states[actionitems.StateSnoozed], states[actionitems.StateArchived] = true, true
	}
	for _, it := range a.inbox.all {
		if it.Window == "" {
			continue
		}
		state := it.State(nowMs)
		if _, ok := ranks[itemCategory(it)]; !ok || itemCategory(it) == "" {
			continue
		}
		if state == actionitems.StateOpen {
			counts[it.Window]++
		}
		if states[state] {
			items[it.Window] = append(items[it.Window], it)
		}
	}
	return items, counts
}

// itemNodes are the pane's action items as child rows: the cells of an
// item's row in the top list (glyph and label, why, source). Every
// action-item verb works on them.
func (a *App) itemNodes(p pane, parent string, now time.Time) []ochazuke.TreeNode {
	nowMs := now.UnixMilli()
	var out []ochazuke.TreeNode
	for _, it := range a.pv.kidItems[p.ID] {
		cell := inboxGlyph(it, nowMs) + " " + it.Label()
		out = append(out, ochazuke.TreeNode{
			ID: itemRowStart + it.ID, Parent: parent, Label: it.Label(), Noun: actionItemNoun, Data: it,
			Cells: []string{cell, cmp.Or(it.Why, it.Kind), it.Source},
		})
	}
	return out
}

// itemRowStart starts the id of an item's child row.
const itemRowStart = "item:"

// itemMark is the open-item count that follows a pane's name (with a
// leading space), "" for none or with panes.item-count off.
func (a *App) itemMark(p pane) string {
	if n := a.pv.kidCounts[p.ID]; n > 0 && p.ID != "" && a.r.Settings().Bool(settingItemCount) {
		return fmt.Sprintf(" %s%d", itemCountMark, n)
	}
	return ""
}

// foldItemRows sets the first fold of each pane row that has item rows
// (panes.items-open), again when the row moves to another list. A fold
// made since stays.
func (a *App) foldItemRows() error {
	if a.pv.foldedIn == nil {
		a.pv.foldedIn = map[string]string{}
	}
	mode := a.r.Settings().String(settingItemsOpen)
	seen := map[string]bool{}
	for _, s := range a.pv.sections {
		for _, n := range s.tree.Nodes() {
			if n.Noun != actionItemNoun || !strings.HasPrefix(n.ID, itemRowStart) || n.Parent == "" {
				continue
			}
			seen[n.Parent] = true
			if a.pv.foldedIn[n.Parent] == s.name {
				continue
			}
			a.pv.foldedIn[n.Parent] = s.name
			open := mode == itemsOpenAll || mode == itemsOpenQueue && s.name == sectionQueue
			if err := s.tree.SetCollapsed(n.Parent, !open); err != nil {
				return err
			}
		}
	}
	for id := range a.pv.foldedIn {
		if !seen[id] {
			delete(a.pv.foldedIn, id)
		}
	}
	return nil
}
