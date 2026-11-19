package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/daemon"
)

// What the Inbox page's three lists (Needs you, Panes, Working) list is
// one setting, panes.section-rows, whose values are rows of
// sectionRowModes:
//
//   - panes (the default): every row of the three lists is a pane, one row
//     per pane however many items or subagents it has. Its open items are
//     the count after its name (◆N, panes.item-count) and its subagents the
//     ◐N after it; an item whose pane is in another list is counted there;
//     the items of a pane the listing does not show (closed or archived)
//     are one row for that pane in Needs you; an item that names no pane
//     is not in these lists (the Items page, the rail and action-item list
//     have it). The pane's items are in the rail's Needs you section and
//     the Items page; x on a pane row archives them (pane.archive-items).
//   - items: the rows before panes.section-rows: a pane's items and
//     subagents as child rows (panes.children), and an item whose pane is
//     elsewhere, closed or missing a row of its own in Needs you.

const (
	settingSectionRows = "panes.section-rows"
	sectionRowsPanes   = "panes"
	sectionRowsItems   = "items"

	agentCountMark = "◐"
)

// sectionRowMode is one value of panes.section-rows: children draws a
// pane row's child rows (panes.children), paneRows makes every row a
// pane's (an item folds into its pane's row wherever that row is; one
// whose pane is not listed is a row for that pane; one with no pane has no
// row).
type sectionRowMode struct {
	name     string
	children bool
	paneRows bool
}

var sectionRowModes = []sectionRowMode{
	{name: sectionRowsPanes, children: false, paneRows: true},
	{name: sectionRowsItems, children: true, paneRows: false},
}

func sectionRowModeNames() []string {
	out := make([]string, len(sectionRowModes))
	for i, m := range sectionRowModes {
		out[i] = m.name
	}
	return out
}

func sectionRowSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingSectionRows, Type: ochazuke.SettingString, Default: sectionRowsPanes, Enum: sectionRowModeNames(),
			Description: "What the Inbox page's three lists hold: panes (one row per pane, however many items or subagents it has: the open items are the ◆ count after its name and the subagents the ◐ count; an item whose pane is in another list counts there, a closed pane's items are one row for it in Needs you, an item that names no pane is on the Items page and in the rail only) or items (a pane's items and subagents as child rows, panes.children, and an item whose pane is elsewhere a row of its own in Needs you)"},
	}
}

// sectionRowMode is the row of sectionRowModes panes.section-rows names,
// the first for a value not in the table.
func (a *App) sectionRowMode() sectionRowMode {
	value := a.r.Settings().String(settingSectionRows)
	i := max(0, slices.IndexFunc(sectionRowModes, func(m sectionRowMode) bool { return m.name == value }))
	return sectionRowModes[i]
}

// itemsUnderPanes reports whether an item belongs to its pane's row: when
// items are child rows (kinds, panes.children), and always when every row
// is a pane's.
func (m sectionRowMode) itemsUnderPanes(kinds []childKind) bool {
	return m.paneRows || slices.ContainsFunc(kinds, func(k childKind) bool { return k.name == childKindItems })
}

// foldsElsewhere reports whether an item whose pane's row is in another
// list folds into that row (its count) instead of a queue row of its own:
// legacy (one false) and every row a pane's.
func (m sectionRowMode) foldsElsewhere(one bool) bool {
	return !one || m.paneRows
}

// queueRowItems are the queue's items as the mode lists them. When every
// row is a pane's, an item whose pane the listing does not show takes that
// pane's row (unlistedPane, recorded in byWindow and section; its shownName is its
// baseName) and
// an item that names no pane has no row (the Items page and the rail list
// it); otherwise items is unchanged.
func (a *App) queueRowItems(items []queuedItem, m sectionRowMode, byWindow map[string]pane, section map[string]string) []queuedItem {
	if !m.paneRows {
		return items
	}
	out := items[:0:0]
	for _, q := range items {
		if q.it.Window == "" {
			continue
		}
		if _, listed := byWindow[q.it.Window]; !listed {
			p := a.unlistedPane(q.it)
			byWindow[p.ID], section[p.ID] = p, sectionQueue
		}
		out = append(out, q)
	}
	return out
}

// paneNeeds is, by window, what a pane's queue row says it needs: the why
// of its newest item in the queue and how many it has there.
type paneNeeds map[string]paneNeed

type paneNeed struct {
	why string
	at  int64
	n   int
}

// queueNeeds is what each pane's queue items need, when every row is a
// pane's (none otherwise: an item row says its own).
func (m sectionRowMode) queueNeeds(items []queuedItem, nowMs int64) paneNeeds {
	out := paneNeeds{}
	if !m.paneRows {
		return out
	}
	for _, q := range items {
		was := out[q.it.Window]
		was.n++
		if at := q.it.Entered(nowMs); at >= was.at {
			was.why, was.at = cmp.Or(q.it.Why, q.it.Kind), at
		}
		out[q.it.Window] = was
	}
	return out
}

// onRow puts what the pane needs in its row's last cell, after its use:
// the newest reason, with the count when it has more ("turn finished
// ×7").
func (needs paneNeeds) onRow(node ochazuke.TreeNode) ochazuke.TreeNode {
	p, _ := node.Data.(pane)
	need, ok := needs[p.ID]
	if !ok || len(node.Cells) < 3 {
		return node
	}
	text := need.why
	if need.n > 1 {
		text += fmt.Sprintf(" ×%d", need.n)
	}
	node.Cells = slices.Clone(node.Cells)
	node.Cells[2] = strings.TrimPrefix(node.Cells[2]+" · "+text, " · ")
	return node
}

// rowAgents counts a row's subagents for its list's title: its subagent
// rows, or the ones its ◐ count stands for when none are drawn.
func (a *App) rowAgents(r row, kids []ochazuke.TreeNode, mode sectionRowMode, now time.Time) int {
	if p, ok := r.node.Data.(pane); ok && r.pane && !mode.children {
		return a.subagentRows(p, now)
	}
	return countActivities(kids)
}

// unlistedPane is the pane an item names that the listing does not show:
// the daemon's pane when it is only hidden (archived), the ended copy when
// mtm keeps one, else an ended pane made from what the item kept (its
// window, session and name), so its row is a pane row every pane verb
// reads.
func (a *App) unlistedPane(it actionitems.Item) pane {
	for _, p := range a.panes {
		if p.ID == it.Window {
			return p
		}
	}
	for _, p := range a.pv.ended {
		if p.ID == it.Window {
			return p
		}
	}
	return pane{Session: it.Session, Exited: true, ended: true,
		Pane: daemon.Pane{ID: it.Window, Name: cmp.Or(it.PaneLabel, it.Window)}}
}

// agentMark is the count of a pane's subagents that follows its name
// (with a leading space) while their rows are not drawn, "" for none.
func agentMark(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" %s%d", agentCountMark, n)
}

// subagentRows is the number of subagent rows the pane would have.
func (a *App) subagentRows(p pane, now time.Time) int {
	if p.ID == "" || !slices.ContainsFunc(a.childKinds(), func(k childKind) bool { return k.name == childKindSubagents }) {
		return 0
	}
	return len(a.shownActivities(p, now))
}
