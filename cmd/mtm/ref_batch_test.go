package main

import (
	"fmt"
	"reflect"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
)

// A read by key of sessions (ochazuke's batch read of the things a pane's
// session names: Every, a filter on the key) answers each key as a Get of
// it does, whatever the pin says and whether the session ended: one call
// for them all.
func TestSessionKeyedReadEqualsGets(t *testing.T) {
	t.Parallel()
	a, f := sessionsApp(t)
	f.panes = append(f.panes, pane{Session: "other", SessionID: "id-other", Pane: daemon.Pane{ID: "w3", Name: "third"}})
	reload(t, a)
	if err := a.work.SetSessionOwner(work.SessionOwner{ID: "id-matomeru", Name: "matomeru", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SetSessionOwner(work.SessionOwner{ID: "id-ochazuke", Name: "ochazuke", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SetSessionOwner(work.SessionOwner{ID: "id-other", Name: "other", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "session", "list", "", ochazuke.Arguments{"all": true}) // copied in
	f.panes = []pane{f.panes[0], f.panes[2]}                               // ochazuke's session ended
	reload(t, a)
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	s := a.r.Nouns["session"].Store
	c := ochazuke.Ctx{Runtime: a.r, Noun: "session"}
	pinned, err := s.List(c, ochazuke.ListQuery{Archived: true, Args: ochazuke.Arguments{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range pinned {
		if s["id"] == "id-ochazuke" {
			t.Fatalf("the pin lists %v: the test needs an ended session outside it", s)
		}
	}
	keys := []any{"id-matomeru", "id-ochazuke", "id-other"}
	page, err := s.Query(c, ochazuke.ListQuery{Filters: []ochazuke.PropFilter{{Prop: "id", Op: "in", Value: keys}}, Archived: true, Every: true, Args: ochazuke.Arguments{}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ochazuke.Thing{}
	for _, t := range page.Items {
		got[fmt.Sprint(t["id"])] = t
	}
	for _, key := range keys {
		want, err := s.Get(c, key.(string))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got[key.(string)], want) {
			t.Errorf("%s: the keyed read answers\n%v\nGet answers\n%v", key, got[key.(string)], want)
		}
	}
	if got["id-ochazuke"]["ended"] != true {
		t.Errorf("the ended session reads %v", got["id-ochazuke"])
	}
}

// A pane list makes the same store calls (Get, List and Query of every
// noun, the reads its derived props make included) for 80 panes as for 8,
// and reads no row of the records it merges in per pane (owners, stars,
// projects: their row caches answer): nothing is read per pane or per
// session.
func TestPaneListStoreCallsBounded(t *testing.T) {
	t.Parallel()
	a, f := sessionsApp(t)
	calls := 0
	for _, n := range a.r.Nouns {
		if n == nil || n.Store == nil {
			continue
		}
		if get := n.Store.Get; get != nil {
			n.Store.Get = func(c ochazuke.Ctx, key string) (ochazuke.Thing, error) { calls++; return get(c, key) }
		}
		if list := n.Store.List; list != nil {
			n.Store.List = func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) { calls++; return list(c, q) }
		}
		if query := n.Store.Query; query != nil {
			n.Store.Query = func(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) { calls++; return query(c, q) }
		}
	}
	listOf := func(n int) int {
		t.Helper()
		f.panes = nil
		for i := range n {
			session := []string{"matomeru", "ochazuke"}[i%2]
			f.panes = append(f.panes, pane{Session: session, SessionID: "id-" + session, Pane: daemon.Pane{ID: fmt.Sprintf("w%d", i+1), Name: fmt.Sprintf("pane %d", i+1)}})
		}
		reload(t, a)
		executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true}) // copied in
		calls = 0
		tables := []string{"assignments", "session_owners", "pane_stars", "projects", "tasks", "copies"}
		before := make([]int64, len(tables))
		for i, table := range tables {
			before[i] = a.db.RowReads(table)
		}
		rows, _ := ochazuke.AnswerRows(executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true})).([]map[string]any)
		if len(rows) < n {
			t.Fatalf("%d panes listed of %d", len(rows), n)
		}
		for i, table := range tables {
			if d := a.db.RowReads(table) - before[i]; d != 0 {
				t.Errorf("a list of %d panes read %d %s rows one by one", n, d, table)
			}
		}
		return calls
	}
	few, many := listOf(8), listOf(80)
	if many != few {
		t.Fatalf("a pane list made %d store calls for 8 panes, %d for 80", few, many)
	}
}
