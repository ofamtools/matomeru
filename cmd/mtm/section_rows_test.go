package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/panesignals"
)

// sectionRowsApp is sectionsApp (a waiting, an errored, a finished, two
// working, an idle agent and a terminal, an outside item with no pane)
// with five more finished turns of the finisher (six in all), an unread
// finished turn of a working pane and one of a pane that closed, under
// panes.section-rows mode.
func sectionRowsApp(t *testing.T, mode string) *App {
	t.Helper()
	a, _ := sectionsApp(t)
	setting(t, a, settingSectionRows, mode)
	for i := range 5 {
		addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: fmt.Sprintf("u%d", i), Kind: panesignals.Done, Title: "finisher finished", Window: "wdone", Session: "s"})
	}
	addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: "o", Kind: panesignals.Done, Title: "oldest finished", Window: "wold", Session: "s"})
	addItem(t, a, actionitems.Spec{Source: panesignals.Source, Key: "g", Kind: panesignals.Done, Title: "gone finished", Window: "wgone", Session: "s", PaneLabel: "goner"})
	return a
}

// panes.section-rows: panes makes every row of the Inbox page's three
// lists a pane's, one per pane (the finisher's six turns are one row
// counting them, the closed pane's item is that pane's row, the working
// pane's item is its Working row's count, the outside item with no pane
// is in none), and the titles and header count those rows; items keeps a
// pane's items as child rows and an item whose pane is elsewhere or none a
// row of its own.
func TestSectionRows(t *testing.T) {
	t.Parallel()
	t.Run(sectionRowsPanes, func(t *testing.T) {
		t.Parallel()
		a := sectionRowsApp(t, sectionRowsPanes)
		for i, s := range a.pv.sections {
			for _, n := range s.tree.Nodes() {
				// The middle list's session and project headings group its
				// panes; every other row is a pane's, and no row has a pane
				// row for its parent.
				heading := i == 1 && (n.Noun == "session" || n.Noun == "pane-project")
				if n.Noun != "pane" && !heading || strings.HasPrefix(n.Parent, "pane:") {
					t.Fatalf("%s holds %s row %s (parent %q)", s.name, n.Noun, n.ID, n.Parent)
				}
			}
		}
		queue := sectionIDs(a, 0)
		want := []string{"pane:s/wwait", "pane:s/werr", "pane:s/wdone", "pane:s/wgone"}
		if !slices.Equal(queue, want) || a.pv.sections[0].rows != len(want) || needsYouHead(a) != len(want) {
			t.Fatalf("queue %v (title %d, head %q), want %v", queue, a.pv.sections[0].rows, a.pv.view.sectionsHead(), want)
		}
		// The row says what the pane needs: its newest reason and the count.
		for _, n := range a.pv.sections[0].tree.Nodes() {
			if n.ID == "pane:s/wdone" && !strings.HasSuffix(n.Cells[2], " ×6") {
				t.Fatalf("finisher's row cells %q", n.Cells)
			}
		}
		// The closed pane's row is an ended pane's: enter says it ended
		// (pane.gone), never attaching another pane.
		if _, ended := a.endedAt(a.r.Instances["pane:s/wgone"]); !ended {
			t.Fatal("the closed pane's row is not an ended row")
		}
		fr := frameWH(a, 120, 40)
		left := strings.Join(leftColumn(t, fr), "\n")
		if strings.Count(left, "finisher") != 1 || !strings.Contains(left, "finisher "+itemCountMark+"6") ||
			!strings.Contains(left, "oldest "+itemCountMark+"1") || !strings.Contains(left, "goner") || strings.Contains(left, "PR 1") {
			t.Fatalf("left side:\n%s", left)
		}
	})
	t.Run(sectionRowsItems, func(t *testing.T) {
		t.Parallel()
		a := sectionRowsApp(t, sectionRowsItems)
		if kids := childrenOf(a, "pane:s/wdone"); len(kids) != 6 {
			t.Fatalf("finisher's child rows %v", kids)
		}
		own := 0
		for _, id := range sectionIDs(a, 0) {
			if strings.HasPrefix(id, queueRowStart) {
				own++
			}
		}
		// The outside item, the working pane's and the closed pane's.
		if own != 3 {
			t.Fatalf("queue %v: %d item rows of their own, want 3", sectionIDs(a, 0), own)
		}
	})
}

// x on a pane row (pane.archive-items) archives every open item of the
// pane, so the pane's row leaves Needs you; the items stay archived in the
// store (the Items page lists them).
func TestPaneArchiveItems(t *testing.T) {
	t.Parallel()
	a := sectionRowsApp(t, sectionRowsPanes)
	if err := a.selectPaneRow("pane:s/wdone"); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus("pane:s/wdone"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "x")
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(sectionIDs(a, 0), "pane:s/wdone") {
		t.Fatalf("the finisher is still in Needs you: %v", sectionIDs(a, 0))
	}
	all, err := a.inbox.items.All()
	if err != nil {
		t.Fatal(err)
	}
	archived := 0
	for _, it := range all {
		if it.Window == "wdone" && it.State(a.inbox.items.Now()) == actionitems.StateArchived {
			archived++
		}
	}
	if archived != 6 {
		t.Fatalf("%d of the finisher's 6 items archived", archived)
	}
	// Again, by --pane: nothing open is nothing to do, never a refusal.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "archive-items", Args: ochazuke.Arguments{"pane": "wdone"}}); err != nil {
		t.Fatalf("again: %v", err)
	}
}
