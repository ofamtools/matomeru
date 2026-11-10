package relations

import (
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

func open(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func rel(kind, from, to string, pos int) ochazuke.Relation {
	return ochazuke.Relation{Kind: kind, From: from, To: to, Position: pos}
}

func eq(a, b []string) bool { return slices.Equal(a, b) }

func TestStoreRoundTripQueriesAndOrder(t *testing.T) {
	t.Parallel()
	s, _ := open(t)
	for _, x := range []ochazuke.Relation{
		rel("directory", "project:a", "directory:/b", 2048),
		{Kind: "directory", From: "project:a", To: "directory:/a", Position: 1024, Text: "main", Data: map[string]any{"default": true}},
		rel("directory", "project:b", "directory:/a", 1024),
		rel("see-also", "project:a", "pane:w", 1024),
		{Kind: "directory", From: "project:a", To: "directory:/gone", Position: 3072, Archived: true},
		{Kind: "directory", To: "directory:/orphan", FormerFrom: "project:x", Position: 1024},
	} {
		if err := s.Put(x); err != nil {
			t.Fatal(err)
		}
	}
	got := func(q ochazuke.RelationQuery) []string {
		t.Helper()
		xs, err := s.Relations(q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range xs {
			out = append(out, x.To)
		}
		return out
	}
	if g := got(ochazuke.RelationQuery{Kind: "directory", From: "project:a"}); !eq(g, []string{"directory:/a", "directory:/b"}) {
		t.Fatalf("by position, archived left out: %v", g)
	}
	if g := got(ochazuke.RelationQuery{Kind: "directory", From: "project:a", Archived: true}); len(g) != 3 {
		t.Fatalf("archived included: %v", g)
	}
	if g := got(ochazuke.RelationQuery{Thing: "directory:/a"}); !eq(g, []string{"directory:/a", "directory:/a"}) {
		t.Fatalf("thing matches either end: %v", g)
	}
	if g := got(ochazuke.RelationQuery{Thing: "project:a"}); len(g) != 3 {
		t.Fatalf("thing at the from end: %v", g)
	}
	if g := got(ochazuke.RelationQuery{Detached: true}); !eq(g, []string{"directory:/orphan"}) {
		t.Fatalf("detached: %v", g)
	}
	if g := got(ochazuke.RelationQuery{To: "pane:w"}); !eq(g, []string{"pane:w"}) {
		t.Fatalf("to: %v", g)
	}
	x, err := s.Relations(ochazuke.RelationQuery{Kind: "directory", From: "project:a"})
	if err != nil || x[0].Text != "main" || !x[0].Flag("default") {
		t.Fatalf("text and data kept: %+v %v", x, err)
	}
}

func TestPutReplacesByIdentityKeepsCreatedAndByKey(t *testing.T) {
	t.Parallel()
	s, db := open(t)
	x := rel("directory", "project:a", "directory:/a", 1024)
	if err := s.Put(x); err != nil {
		t.Fatal(err)
	}
	first, err := s.ByKey(KeyOf(x))
	if err != nil || first.Created == 0 {
		t.Fatalf("by key: %+v %v", first, err)
	}
	if err := db.Tx([]string{Table}, func(tx *store.Tx) error {
		first.Created = 5
		return tx.Put(Table, first.ID, first)
	}); err != nil {
		t.Fatal(err)
	}
	x.Text = "changed"
	if err := s.Put(x); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Rows(ochazuke.RelationQuery{})
	if len(rows) != 1 || rows[0].Text != "changed" || rows[0].Created != 5 {
		t.Fatalf("replaced in place, created kept: %+v", rows)
	}
	if err := s.Remove(x); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(x); err != nil {
		t.Fatal("removing a gone row is no error")
	}
	if _, err := s.ByKey(KeyOf(x)); err == nil {
		t.Fatal("removed")
	}
}

func TestOpenIsIdempotentAndIndexed(t *testing.T) {
	t.Parallel()
	_, db := open(t)
	if _, err := Open(db); err != nil {
		t.Fatal(err)
	}
}
