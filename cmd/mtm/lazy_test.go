package main

import (
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/store"
)

// New builds the Review, Notes and Context pages empty: their tables are
// not read until the page opens, a reader needs the rows, or a --target
// names a row only the page holds. A verb that reads through the store
// (context-item list) works without loading the page.
func TestPagesLoadLazily(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := contextitems.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if err := cs.Put(contextitems.Item{ID: id, Kind: "path", Text: "/tmp/" + id, ProjectID: "matomeru", Created: 1}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	a := newUnloaded(t, path)
	loads := func() [3]int { return [3]int{a.reviewsLoad.count, a.notesLoad.count, a.cx.load.count} }
	if got := loads(); got != [3]int{} {
		t.Fatalf("New loaded pages: %v", got)
	}
	out := executeA(t, a, "context-item", "list", "", ochazuke.Arguments{"all": true})
	if rows := out.(ochazuke.ListAnswer).Items; len(rows) != 3 {
		t.Fatalf("context list: %v", out)
	}
	if got := loads(); got[2] != 0 {
		t.Fatalf("context list loaded the page: %v", got)
	}
	if err := a.r.OpenPage(contextPage); err != nil {
		t.Fatal(err)
	}
	if got := loads(); got != [3]int{0, 0, 1} || a.cx.list.Len() == 0 {
		t.Fatalf("opening Context: %v rows %d", got, a.cx.list.Len())
	}
	if err := a.r.OpenPage(reviewsPage); err != nil {
		t.Fatal(err)
	}
	if got := loads(); got != [3]int{1, 0, 1} {
		t.Fatalf("opening Review: %v", got)
	}

	b := newUnloaded(t, path)
	if err := b.reveal("c1"); err != nil || b.r.Instances["c1"] == nil || b.cx.load.count != 1 {
		t.Fatalf("reveal of a context item: %v", err)
	}

	// Execute (keys, palette, MCP) resolves a target through the same hook.
	c := newUnloaded(t, path)
	got := executeA(t, c, "context-item", "get", "c2", nil)
	if c.cx.load.count != 1 || got == nil {
		t.Fatalf("get by id through Execute: count %d out %v", c.cx.load.count, got)
	}
	if _, err := c.r.Execute(ochazuke.Request{Noun: "context-item", Verb: "get", Target: "nope"}); err == nil {
		t.Fatal("unknown id accepted")
	}
}

// newUnloaded is New on the database with nothing else done to the app.
func newUnloaded(t *testing.T, path string) *App {
	t.Helper()
	env := map[string]string{"HOME": "/home/test", "MTM_O_PROJ": "none"}
	app, err := New(Options{Data: path, Headless: true, lookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app
}
