package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// bulkRows writes n context rows and m action rows straight into the
// tables (every 10th context row archived, every 7th starred, every 5th
// action row dismissed or snoozed), so a test can show what a read costs.
func bulkRows(t *testing.T, a *App, n, m int) {
	t.Helper()
	kinds := contextitems.Kinds
	err := a.db.Tx([]string{contextitems.Table.Name, actionitems.Table.Name}, func(tx *store.Tx) error {
		for i := range n {
			id := fmt.Sprintf("c%05d", i)
			row := map[string]any{"id": id, "kind": kinds[i%len(kinds)], "text": fmt.Sprintf("/work/item-%d.txt", i), "session": "s", "window": "w1", "pane": "p",
				"project_id": "", "task_id": "", "starred": i%7 == 0, "pinned": false, "created": int64(1000 + i), "sources": []any{}, "provenance": map[string]any{}}
			if i%10 == 0 {
				row["archived"] = true
			}
			if i%3 == 0 {
				row["last_seen"] = int64(5000 - i)
			}
			if err := tx.Put(contextitems.Table.Name, id, row); err != nil {
				return err
			}
		}
		for i := range m {
			id := fmt.Sprintf("a%05d", i)
			row := map[string]any{"id": id, "service": []string{"gh", "slack"}[i%2], "source_url": fmt.Sprintf("https://example.com/%d", i), "title": fmt.Sprintf("Item %d", i), "why": "review requested",
				"created": int64(2000 + i), "updated": int64(2000 + i)}
			switch i % 5 {
			case 1:
				row["dismissed_at"] = int64(9000 + i)
			case 2:
				row["snoozed_until"] = a.inbox.items.Now() + 100000
			case 3:
				row["seen_at"] = int64(3000 + i)
			}
			if err := tx.Put(actionitems.Table.Name, id, row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The Nouns page counts context-item (20,000 rows) and action-item (3,000)
// with one-row pages in SQLite: no full read of either table. The action
// store lists the open items by default (state), so its count is the open
// ones: 3000 less 600 dismissed and 600 snoozed.
func TestNounsPageCountsWithoutReadingTheTables(t *testing.T) {
	t.Parallel()
	a := open(t)
	bulkRows(t, a, 20000, 3000)
	ctxReads, actReads := a.db.FullReads(contextitems.Table.Name), a.db.FullReads(actionitems.Table.Name)
	if err := a.r.EnsureNounsPage(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(ochazuke.NounsPageID); err != nil {
		t.Fatal(err)
	}
	for noun, want := range map[string][2]int{contextNoun: {20000, 2000}, actionItemNoun: {1800, 0}} {
		count, archived, known, hasArchived := a.r.NounCount(noun)
		if !known || !hasArchived || count != want[0] || archived != want[1] {
			t.Fatalf("%s: count %d archived %d known %v, want %v", noun, count, archived, known, want)
		}
	}
	if got := a.db.FullReads(contextitems.Table.Name) - ctxReads; got != 0 {
		t.Fatalf("counting context-item read the table %d times", got)
	}
	if got := a.db.FullReads(actionitems.Table.Name) - actReads; got != 0 {
		t.Fatalf("counting action-item read the table %d times", got)
	}
}

// The context-item index draws its first page from 20,000 rows: one window
// read, the whole table never read, the total from SQLite.
func TestContextIndexDrawsFirstPageWithoutAFullRead(t *testing.T) {
	t.Parallel()
	a := open(t)
	bulkRows(t, a, 20000, 0)
	reads := a.db.FullReads(contextitems.Table.Name)
	if err := a.r.OpenIndex(contextNoun, ""); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	list := a.r.List(ochazuke.IndexPageID(contextNoun) + "/list")
	if list == nil || !list.Windowed() {
		t.Fatalf("index list = %v, want a windowed list", list)
	}
	if got, want := len(list.Items()), 200; got != want {
		t.Fatalf("the first page holds %d rows, want %d", got, want)
	}
	if got := list.WindowTotal(); got != 18000 {
		t.Fatalf("window total = %d, want 18000 (the archived rows hidden)", got)
	}
	if got := a.db.FullReads(contextitems.Table.Name) - reads; got != 0 {
		t.Fatalf("the index read the table %d times, want 0", got)
	}
}

// Pushing a request into SQLite and answering it from the store's List
// agree: ids, order and total, for requests of every shape.
func TestTableQueryAgreesWithTheListPath(t *testing.T) {
	t.Parallel()
	a := open(t)
	bulkRows(t, a, 400, 300)
	c := ochazuke.Ctx{}
	for _, tc := range []struct {
		noun string
		q    ochazuke.ListQuery
	}{
		{contextNoun, ochazuke.ListQuery{}},
		{contextNoun, ochazuke.ListQuery{Archived: true}},
		{contextNoun, ochazuke.ListQuery{Page: 3, PerPage: 25}},
		{contextNoun, ochazuke.ListQuery{Filters: []ochazuke.PropFilter{{Prop: "archived", Value: true}}}},
		{contextNoun, ochazuke.ListQuery{Filters: []ochazuke.PropFilter{{Prop: "type", Op: "in", Value: []any{"file", "dir"}}, {Prop: "starred", Value: false}}}},
		{contextNoun, ochazuke.ListQuery{Filters: []ochazuke.PropFilter{{Prop: "text", Op: "contains", Value: "item-1"}}, Sort: []ochazuke.SortKey{{Field: "text", Desc: true}}, PerPage: 20, Page: 2}},
		{contextNoun, ochazuke.ListQuery{Sort: []ochazuke.SortKey{{Field: "created", Desc: true}}, PerPage: 10}},
		{contextNoun, ochazuke.ListQuery{Sort: []ochazuke.SortKey{{Field: "seen"}}, PerPage: 10, Page: 4}},
		{contextNoun, ochazuke.ListQuery{Args: ochazuke.Arguments{"kind": "dir"}}},
		{contextNoun, ochazuke.ListQuery{Filters: []ochazuke.PropFilter{{Prop: "origin", Value: "prompt"}}}}, // derived: the List path
		{contextNoun, ochazuke.ListQuery{GroupBy: []string{"type"}}},
		{actionItemNoun, ochazuke.ListQuery{}},
		{actionItemNoun, ochazuke.ListQuery{Args: ochazuke.Arguments{"state": "all"}}},
		{actionItemNoun, ochazuke.ListQuery{Args: ochazuke.Arguments{"state": "snoozed"}, Archived: true}},
		{actionItemNoun, ochazuke.ListQuery{Args: ochazuke.Arguments{"state": "archived"}, Archived: true}},
		{actionItemNoun, ochazuke.ListQuery{Args: ochazuke.Arguments{"source": "slack"}, PerPage: 15, Page: 2}},
		{actionItemNoun, ochazuke.ListQuery{Filters: []ochazuke.PropFilter{{Prop: "read", Value: true}}, Args: ochazuke.Arguments{"state": "all"}}},
		{actionItemNoun, ochazuke.ListQuery{Filters: []ochazuke.PropFilter{{Prop: "archived", Value: true}}, Args: ochazuke.Arguments{"state": "all"}, Sort: []ochazuke.SortKey{{Field: "title"}}}},
		{actionItemNoun, ochazuke.ListQuery{Args: ochazuke.Arguments{"project": "nope"}}},
	} {
		n := a.r.Nouns[tc.noun]
		got, err := n.Store.Query(c, tc.q)
		if err != nil {
			t.Fatalf("%s %+v: %v", tc.noun, tc.q, err)
		}
		things, err := n.Store.List(c, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		want := a.r.QueryAll(tc.noun, things, tc.q)
		ids := func(p ochazuke.ListPage) []string {
			var out []string
			for _, th := range p.Items {
				out = append(out, fmt.Sprint(th["id"]))
			}
			return out
		}
		if got.Total != want.Total || !slices.Equal(ids(got), ids(want)) {
			t.Errorf("%s %+v:\n pushed total %d %v\n list   total %d %v", tc.noun, tc.q, got.Total, ids(got), want.Total, ids(want))
		}
	}
}

// A text query is matched word by word, without case, in the props the
// noun searches; a text the store cannot push reads like any other request.
func TestTableQueryTextMatchesWords(t *testing.T) {
	t.Parallel()
	a := open(t)
	bulkRows(t, a, 50, 300)
	page, err := a.r.Nouns[actionItemNoun].Store.Query(ochazuke.Ctx{}, ochazuke.ListQuery{Text: "ITEM 12", Archived: true, Args: ochazuke.Arguments{"state": "all"}})
	if err != nil {
		t.Fatal(err)
	}
	// Both words must appear: every title "Item N" with 12 in N.
	want := 0
	for i := range 300 {
		if strings.Contains(fmt.Sprintf("%d", i), "12") {
			want++
		}
	}
	if page.Total != want {
		t.Fatalf("total = %d, want %d", page.Total, want)
	}
	page, err = a.r.Nouns[contextNoun].Store.Query(ochazuke.Ctx{}, ochazuke.ListQuery{Text: "item-4 txt"})
	if err != nil || page.Total != 10 { // item-4, item-40 to item-49, less the archived 40
		t.Fatalf("context text: total %d err %v", page.Total, err)
	}
}

// Drawn rows and widgets declare the part kind, so the Nouns page lists them
// nowhere; the nouns with a use of their own (a command line, a service)
// stay commands, and stored nouns are records.
func TestNounKindsOfDrawnRowsAndCommands(t *testing.T) {
	t.Parallel()
	a := open(t)
	kinds := a.r.AllNounKinds()
	for _, tc := range []struct{ kind, noun string }{
		{ochazuke.NounKindPart, "turn-line"}, {ochazuke.NounKindPart, "turn-step"}, {ochazuke.NounKindPart, "turn-separator"}, {ochazuke.NounKindPart, "turn"},
		{ochazuke.NounKindPart, outlineRootNoun}, {ochazuke.NounKindPart, outlineProjectNoun}, {ochazuke.NounKindPart, outlineTaskNoun},
		{ochazuke.NounKindPart, outlineModal}, {ochazuke.NounKindPart, noteBodyNoun}, {ochazuke.NounKindPart, "panes"}, {ochazuke.NounKindPart, copyNoun},
		{ochazuke.NounKindCommand, "daemon"}, {ochazuke.NounKindCommand, "extension"}, {ochazuke.NounKindCommand, "setup"},
		{ochazuke.NounKindRecord, contextNoun}, {ochazuke.NounKindRecord, actionItemNoun}, {ochazuke.NounKindRecord, "task"},
	} {
		if kinds[tc.noun] != tc.kind {
			t.Errorf("%s is a %s, want %s", tc.noun, kinds[tc.noun], tc.kind)
		}
	}
}
