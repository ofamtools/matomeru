package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
)

// childrenOf are the ids of the rows under a pane row, in any list.
func childrenOf(a *App, parent string) []string {
	var out []string
	for _, s := range a.pv.sections {
		for _, n := range s.tree.Nodes() {
			if n.Parent == parent {
				out = append(out, n.ID)
			}
		}
	}
	return out
}

func sectionOfRow(a *App, id string) string {
	for _, s := range a.pv.sections {
		for _, n := range s.tree.Nodes() {
			if n.ID == id {
				return s.name
			}
		}
	}
	return ""
}

func rowNode(a *App, id string) (ochazuke.TreeNode, bool) {
	for _, s := range a.pv.sections {
		for _, n := range s.tree.Nodes() {
			if n.ID == id {
				return n, true
			}
		}
	}
	return ochazuke.TreeNode{}, false
}

// A pane with several open items is one row, in the top list while one is
// unread, and its items are child rows (AIP1).
func TestPaneItemsAreChildRowsOfOnePaneRow(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	one := addItem(t, a, actionitems.Spec{Source: "gh", Key: "idler-1", Title: "ask one", Window: "widle", Session: "s"})
	two := addItem(t, a, actionitems.Spec{Source: "gh", Key: "idler-2", Title: "ask two", Window: "widle", Session: "s"})
	row := "pane:s/widle"
	if got := sectionOfRow(a, row); got != sectionQueue {
		t.Fatalf("a pane with an unread outside item is in %q, want the queue", got)
	}
	if kids := childrenOf(a, row); !slices.Equal(kids, []string{itemRowStart + two.ID, itemRowStart + one.ID}) && !slices.Equal(kids, []string{itemRowStart + one.ID, itemRowStart + two.ID}) {
		t.Fatalf("children of %s: %v", row, kids)
	}
	for _, id := range []string{"queue:" + one.ID, "queue:" + two.ID} {
		if _, ok := rowNode(a, id); ok {
			t.Fatalf("a repeated row %s", id)
		}
	}
	n := 0
	for _, s := range a.pv.sections {
		for _, node := range s.tree.Nodes() {
			if node.ID == row {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("pane row drawn %d times", n)
	}
	if node, _ := rowNode(a, row); !strings.Contains(node.Cells[0], itemCountMark+"2") {
		t.Fatalf("no count after the name: %q", node.Cells[0])
	}
	if a.r.Instances[itemRowStart+one.ID].Noun != actionItemNoun {
		t.Fatal("a child row is not an action-item")
	}
}

// Once every item is read the pane goes back to its list and keeps its
// items as child rows there, folded (panes.items-open queue).
func TestPaneItemsStayWithTheirPaneWhenRead(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	it := addItem(t, a, actionitems.Spec{Source: "gh", Key: "idler-1", Title: "ask one", Window: "widle", Session: "s"})
	row := "pane:s/widle"
	if _, err := a.inbox.items.Update(it.ID, func(i *actionitems.Item) error { i.ReadAt = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	if got := sectionOfRow(a, row); got != sectionPanes {
		t.Fatalf("a pane with only read items is in %q, want panes", got)
	}
	if kids := childrenOf(a, row); len(kids) != 1 {
		t.Fatalf("children %v", kids)
	}
	tree := a.pv.sections[1].tree
	if !tree.Collapsed(row) {
		t.Fatal("a pane row's items are open in Panes with panes.items-open queue")
	}
	setting(t, a, settingItemsOpen, itemsOpenAll)
	// A new fold is made for rows that move; force it by clearing the memo.
	a.pv.foldedIn = nil
	if err := a.rebuildPanes(); err != nil {
		t.Fatal(err)
	}
	if tree.Collapsed(row) {
		t.Fatal("panes.items-open all leaves the row folded")
	}
}

// panes.children without items is the old shape: each item its own row in
// the top list, the first one standing for its pane.
func TestPaneChildrenWithoutItemsKeepsQueueRows(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	setting(t, a, settingPaneChildren, []string{childKindSubagents})
	one := addItem(t, a, actionitems.Spec{Source: "gh", Key: "idler-1", Title: "ask one", Window: "widle", Session: "s"})
	if _, ok := rowNode(a, "queue:"+one.ID); !ok {
		t.Fatal("the item is not a row of its own with panes.children without items")
	}
	if _, ok := rowNode(a, itemRowStart+one.ID); ok {
		t.Fatal("an item child row with items left out of panes.children")
	}
	if got := sectionOfRow(a, "pane:s/widle"); got != sectionPanes {
		t.Fatalf("the pane moved to %q", got)
	}
}

func TestPaneItemCountSetting(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	addItem(t, a, actionitems.Spec{Source: "gh", Key: "idler-1", Title: "ask one", Window: "widle", Session: "s"})
	setting(t, a, settingItemCount, false)
	if err := a.rebuildPanes(); err != nil {
		t.Fatal(err)
	}
	if node, _ := rowNode(a, "pane:s/widle"); strings.Contains(node.Cells[0], itemCountMark) {
		t.Fatalf("count shown with panes.item-count off: %q", node.Cells[0])
	}
}

func TestPaneChildItemsStates(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	it := addItem(t, a, actionitems.Spec{Source: "gh", Key: "idler-1", Title: "ask one", Window: "widle", Session: "s"})
	until := a.clock().Add(1e9 * 3600).UnixMilli()
	if _, err := a.inbox.items.Update(it.ID, func(i *actionitems.Item) error { i.SnoozedUntil = until; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	row := "pane:s/widle"
	if kids := childrenOf(a, row); len(kids) != 0 {
		t.Fatalf("a snoozed item is a child with panes.child-items open: %v", kids)
	}
	setting(t, a, settingChildItems, childItemsOpenSnoozed)
	if err := a.rebuildPanes(); err != nil {
		t.Fatal(err)
	}
	if kids := childrenOf(a, row); len(kids) != 1 {
		t.Fatalf("open+snoozed: %v", kids)
	}
}

func TestPaneChildrenValidates(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.r.Settings().Set(settingPaneChildren, []string{"items", "bogus"}); err == nil {
		t.Fatal("panes.children took an unknown kind")
	}
	if err := a.r.Settings().Set(settingPaneChildren, []string{"subagents", "items"}); err != nil {
		t.Fatal(err)
	}
}

// The Inbox page's for text names the pane (AIP2): the listed pane's own
// label, a closed pane by the name the item kept, and the item remembers
// the name from the listing when it is added or its pane is set.
func TestItemRemembersPaneLabelAndNamesClosedPanes(t *testing.T) {
	t.Parallel()
	a, _ := sectionsApp(t)
	out, _, err := cli(a, "", "action-item", "add", "--source", "gh", "--key", "named", "--title", "named ask", "--pane", "widle", "--session", "s")
	if err != nil {
		t.Fatalf("add: %s %v", out, err)
	}
	it := itemByKey(t, a, "named")
	if it.PaneLabel != "s/idler" {
		t.Fatalf("pane label %q", it.PaneLabel)
	}
	if got := a.itemPaneText(it); got != "s/idler" {
		t.Fatalf("listed pane text %q", got)
	}
	// The pane closes: the item names it as closed, by the kept name.
	a.setPanes(slices.DeleteFunc(slices.Clone(a.panes), func(p pane) bool { return p.ID == "widle" }), nil)
	if got := a.itemPaneText(it); got != "closed pane s/idler" {
		t.Fatalf("closed pane text %q", got)
	}
	if got := a.itemPaneText(actionitems.Item{Window: "wgone"}); got != "closed pane wgone" {
		t.Fatalf("item without a kept name: %q", got)
	}
	// The prop is read-only and shows in get.
	th, err := a.actionItemStore().Get(ochazuke.Ctx{}, it.ID)
	if err != nil || th["pane-label"] != "s/idler" {
		t.Fatalf("pane-label prop: %v %v", th, err)
	}
	// Setting pane stamps the new pane's name; clearing it drops the name.
	executeA(t, a, actionItemNoun, "set", "", ochazuke.Arguments{"id": it.ID, "props": map[string]any{"pane": "wterm"}})
	if got := itemByKey(t, a, "named"); got.PaneLabel != "s/term" {
		t.Fatalf("after set pane: %q", got.PaneLabel)
	}
	executeA(t, a, actionItemNoun, "set", "", ochazuke.Arguments{"id": it.ID, "props": map[string]any{"pane": ""}})
	if got := itemByKey(t, a, "named"); got.PaneLabel != "" {
		t.Fatalf("after clearing pane: %q", got.PaneLabel)
	}
}
