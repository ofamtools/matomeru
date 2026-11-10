package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

func TestInboxFromGroupsBeforePaginationAndKeepsFullPaneCache(t *testing.T) {
	t.Parallel()
	a, _, _ := inboxApp(t)
	if err := a.db.Tx([]string{actionitems.Table.Name}, func(tx *store.Tx) error {
		for i := range 405 {
			source := "gh"
			if i%2 != 0 {
				source = "slack"
			}
			it := actionitems.Item{ID: fmt.Sprintf("window-%03d", i), Source: source, Title: fmt.Sprintf("Item %03d", i), Created: int64(i + 1), Updated: int64(i + 1), Window: "pane", Session: "session", TaskID: "task", ProjectID: "matomeru"}
			if err := tx.Put(actionitems.Table.Name, it.ID, it); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	setting(t, a, settingInboxGroup, "source")
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	list := a.inbox.list
	if list.From() != actionItemNoun || !list.Windowed() || list.WindowTotal() != 405 || len(a.inbox.all) != 405 {
		t.Fatalf("curated source=%s windowed=%v total=%d cache=%d", list.From(), list.Windowed(), list.WindowTotal(), len(a.inbox.all))
	}
	var asked []ochazuke.ListQuery
	n := a.r.Noun(actionItemNoun)
	query := n.Store.Query
	n.Store.Query = func(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) {
		asked = append(asked, q)
		return query(c, q)
	}
	reads := a.db.FullReads(actionitems.Table.Name)
	toPage(t, a, inboxPage)
	rows := list.Items()
	if len(rows) != 200 || rows[0].Label != "Item 404" || rows[199].Label != "Item 006" {
		t.Fatalf("first grouped page: %d rows, first=%+v last=%+v", len(rows), rows[0], rows[len(rows)-1])
	}
	for _, row := range rows {
		if row.Section != "gh" {
			t.Fatalf("pagination ran before grouping: %+v", row)
		}
		thing := row.Data.(ochazuke.Thing)
		for field, want := range map[string]string{"pane": "pane", "session": "session", "task": "task", "project": "matomeru"} {
			if thing[field] != want {
				t.Fatalf("row subject %s=%v, want %s", field, thing[field], want)
			}
		}
	}
	// A folded group is one collection heading, never an action-item subject.
	executeA(t, a, "ochazuke.collection", "group-fold", "", ochazuke.Arguments{"id": inboxListID})
	if row, _ := list.Selected(); list.Shown() != 1 || row.Noun != "ochazuke.collection" || !strings.Contains(row.Label, "gh") {
		t.Fatalf("folded first page: selected=%+v shown=%d", row, list.Shown())
	}
	press(t, a, "j")
	if page, _ := list.Page(); page != 2 {
		t.Fatalf("j did not cross the folded page: %d", page)
	}
	if row, _ := list.Selected(); row.Noun != "ochazuke.collection" || !strings.Contains(row.Label, "gh") {
		t.Fatalf("continued group lost its fold: %+v", row)
	}
	press(t, a, "j")
	if row, _ := list.Selected(); row.Label != "Item 403" || row.Section != "slack" {
		t.Fatalf("j did not leave the folded group: %+v", row)
	}
	press(t, a, "k", "k")
	if page, _ := list.Page(); page != 1 || list.Shown() != 1 {
		t.Fatalf("k did not return to the folded page: page=%d shown=%d", page, list.Shown())
	}
	executeA(t, a, "ochazuke.collection", "group-fold", "", ochazuke.Arguments{"id": inboxListID, "closed": false})
	if row, _ := list.Selected(); row.Label != "Item 404" || list.Shown() != 200 {
		t.Fatalf("unfold lost its selection: %+v, shown=%d", row, list.Shown())
	}
	for _, q := range asked {
		if q.PerPage != 200 || !slices.Equal(q.GroupBy, []string{inboxGroupProp}) {
			t.Fatalf("unbounded or ungrouped curated query: %+v", q)
		}
	}
	if len(asked) == 0 || a.db.FullReads(actionitems.Table.Name) != reads {
		t.Fatalf("page/fold read whole items: queries=%d full reads=%d", len(asked), a.db.FullReads(actionitems.Table.Name)-reads)
	}
	if len(a.inbox.all) != 405 {
		t.Fatalf("a visible page replaced the Panes cache: %d", len(a.inbox.all))
	}
}

func TestInboxGroupingQueriesAgreeAcrossModesAndBounds(t *testing.T) {
	t.Parallel()
	a, items := groupApp(t)
	if _, err := a.inbox.items.Archive(items["none"].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.inbox.items.Snooze(items["project"].ID, a.inbox.items.Now()+100000); err != nil {
		t.Fatal(err)
	}
	for _, mode := range inboxGroupNames() {
		setting(t, a, settingInboxGroup, mode)
		q := a.inbox.list.ListView()
		q.Page, q.PerPage = 1, 2
		things, err := a.listActionItemThings(ochazuke.Ctx{}, q)
		if err != nil {
			t.Fatal(err)
		}
		for page := 1; page <= 6; page++ {
			q.Page = page
			reads := a.db.FullReads(actionitems.Table.Name)
			got, err := a.r.Noun(actionItemNoun).Store.Query(ochazuke.Ctx{}, q)
			if err != nil {
				t.Fatalf("%s page %d: %v", mode, page, err)
			}
			want := a.r.QueryAll(actionItemNoun, things, q)
			keys := func(p ochazuke.ListPage) []string {
				var out []string
				for _, item := range p.Items {
					out = append(out, item["id"].(string))
				}
				return out
			}
			if got.Total != want.Total || !slices.Equal(keys(got), keys(want)) {
				t.Fatalf("%s page %d: got %v/%d, want %v/%d", mode, page, keys(got), got.Total, keys(want), want.Total)
			}
			if a.db.FullReads(actionitems.Table.Name) != reads {
				t.Fatalf("%s page %d fell back to a whole-table read", mode, page)
			}
		}
		if mode == groupNone && len(q.GroupBy) != 0 {
			t.Fatalf("none retained grouping: %+v", q)
		}
	}
}

func TestDomainDocsForwardGroupingBeforePagination(t *testing.T) {
	t.Parallel()
	a := open(t)
	d := domainDocs{a: a, noun: actionItemNoun, source: ochazuke.Store{List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) {
		return []ochazuke.Thing{
			{"id": "a", "source": "beta"}, {"id": "b", "source": "alpha"},
			{"id": "c", "source": "beta"}, {"id": "d", "source": "gamma"}, {"id": "e", "source": "alpha"},
		}, nil
	}}}
	page, err := d.Query(ochazuke.DocQuery{GroupBy: []string{"source"}, GroupSort: []ochazuke.SortKey{{Field: "source", Desc: true}}, Page: 2, PerPage: 2})
	if err != nil || page.Total != 5 || len(page.Items) != 2 || page.Items[0]["id"] != "c" || page.Items[1]["id"] != "b" || !slices.Equal(page.Groups, []string{"beta", "alpha"}) {
		t.Fatalf("grouped document window: %+v, %v", page, err)
	}
}

func TestInboxFromCreateAndPromoteTaskKeys(t *testing.T) {
	t.Parallel()
	a, _, clock := inboxApp(t)
	seedInbox(t, a, clock)
	toPage(t, a, inboxPage)
	press(t, a, "n")
	if a.r.RecordEditor(ochazuke.ArgsFormID) == nil {
		t.Fatalf("n on the curated From row did not open creation: modal=%s", a.r.TopModal())
	}
	typeText(t, a, "Created on Items")
	press(t, a, "enter")
	var made actionitems.Item
	for _, it := range a.inbox.all {
		if it.Title == "Created on Items" {
			made = it
		}
	}
	if made.ID == "" {
		t.Fatal("the Items creation form did not persist the record")
	}
	if err := a.inbox.list.Select(inboxListID + "/" + made.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "c")
	changed, err := a.inbox.items.Get(made.ID)
	if err != nil || changed.PromotedTaskID == "" || changed.State(*clock) != actionitems.StateArchived {
		t.Fatalf("c did not promote and archive its From row: %+v, %v", changed, err)
	}
	task, err := a.work.Task(changed.PromotedTaskID)
	if err != nil || task.Title != made.Title {
		t.Fatalf("c wrote the wrong task: %+v, %v", task, err)
	}
}

func TestInboxFromKeepsGlobalScopeAndExplicitProjectQuery(t *testing.T) {
	t.Parallel()
	a, items := groupApp(t)
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	toPage(t, a, inboxPage)
	_, rows := inboxSections(a)
	if !slices.Contains(rows["unassociated"], items["none"].ID) || a.inbox.list.WindowTotal() != len(a.inbox.all) {
		t.Fatalf("the global Items page was narrowed to the pin: %v", rows)
	}
	q := a.inbox.list.ListView()
	q.Args, q.PerPage, q.Page = ochazuke.Arguments{"project": "matomeru"}, 2, 1
	page, err := a.r.Noun(actionItemNoun).Store.Query(ochazuke.Ctx{}, q)
	if err != nil || page.Total != 4 || len(page.Items) != 2 || page.Items[0]["id"] != items["live2"].ID || page.Items[1]["id"] != items["closed-task"].ID {
		t.Fatalf("explicit project query lost task ownership, grouping or bounds: %+v, %v", page, err)
	}
}

func TestInboxFromArchiveInclusiveSourceAndWholeSourceOptOut(t *testing.T) {
	t.Parallel()
	for _, window := range []int{0, 200} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			t.Parallel()
			config := t.TempDir()
			if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(fmt.Sprintf(`{"collection.window":%d}`, window)), 0600); err != nil {
				t.Fatal(err)
			}
			a := openWith(t, Options{Data: seeded(t), Config: config})
			open := addItem(t, a, actionitems.Spec{Title: "Open item"})
			snoozed := addItem(t, a, actionitems.Spec{Title: "Snoozed item"})
			archived := addItem(t, a, actionitems.Spec{Title: "Archived item"})
			if _, err := a.inbox.items.Snooze(snoozed.ID, a.inbox.items.Now()+100000); err != nil {
				t.Fatal(err)
			}
			if _, err := a.inbox.items.Archive(archived.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.inboxLoad.refresh(); err != nil {
				t.Fatal(err)
			}
			if a.inbox.list.Windowed() != (window > 0) || len(a.inbox.list.Items()) != 3 {
				t.Fatalf("window=%d lost a source state: windowed=%v rows=%+v", window, a.inbox.list.Windowed(), a.inbox.list.Items())
			}
			for _, q := range []ochazuke.ListQuery{
				{Archived: true},
				{Archived: true, Args: ochazuke.Arguments{"state": "open"}},
				{Archived: true, Filters: []ochazuke.PropFilter{{Prop: "state", Value: "snoozed"}}},
			} {
				page, err := a.r.Noun(actionItemNoun).Store.Query(ochazuke.Ctx{}, q)
				if err != nil {
					t.Fatal(err)
				}
				switch {
				case len(q.Args) > 0:
					if page.Total != 1 || page.Items[0]["id"] != open.ID {
						t.Fatalf("explicit state did not override inclusion: %+v", page)
					}
				case len(q.Filters) > 0:
					if page.Total != 1 || page.Items[0]["id"] != snoozed.ID {
						t.Fatalf("explicit state filter did not apply: %+v", page)
					}
				default:
					if page.Total != 3 {
						t.Fatalf("archive-inclusive source omitted states: %+v", page)
					}
				}
			}
			var ordinary []actionThing
			cliRows(t, a, &ordinary, "action-item", "list")
			if len(ordinary) != 1 || ordinary[0].ID != open.ID {
				t.Fatalf("ordinary CLI list no longer defaults to open: %+v", ordinary)
			}
		})
	}
}
