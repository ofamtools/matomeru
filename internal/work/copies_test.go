package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

// A database written before the copies table existed opens with it, its
// rows unchanged, and the copies work at once.
func TestCopiesMigrateAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range Tables {
		if table.Name == copiesTable {
			continue // the database as it was before the copies
		}
		if err := db.Declare(table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Tx([]string{"projects"}, func(tx *store.Tx) error {
		return tx.Put("projects", "p1", Project{ID: "p1", Name: "kept"})
	}); err != nil {
		t.Fatal(err)
	}
	if db.Declared(copiesTable) {
		t.Fatal("the old database already has copies")
	}
	db.Close()

	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatalf("open an existing database: %v", err)
	}
	if ps, err := w.Projects(); err != nil || len(ps) != 1 || ps[0].Name != "kept" {
		t.Fatalf("projects: %v %v", ps, err)
	}
	if err := w.PutCopy("pane", "w1", "sock", map[string]any{"id": "w1", "name": "fixer"}); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := w.Copy("pane", "w1"); err != nil || !ok || c.Props["name"] != "fixer" {
		t.Fatalf("copy: %+v %v %v", c, ok, err)
	}
	// Opening again accepts the declaration it wrote.
	if _, err := Open(db); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

// Copies keep their first-write order and backfill time per noun; a key
// may hold any text; delete drops one copy.
func TestCopiesPutListDelete(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ noun, key, name string }{
		{"pane", "w2", "b"}, {"session", "s1", "main"}, {"pane", "w1|x", "a"},
	} {
		if err := w.PutCopy(c.noun, c.key, "sock", map[string]any{"id": c.key, "name": c.name}); err != nil {
			t.Fatal(err)
		}
	}
	first, _, _ := w.Copy("pane", "w2")
	if err := w.PutCopy("pane", "w2", "other", map[string]any{"id": "w2", "name": "b2"}); err != nil {
		t.Fatal(err)
	}
	panes, err := w.Copies("pane")
	if err != nil || len(panes) != 2 || panes[0].Key != "w2" || panes[0].Props["name"] != "b2" || panes[1].Key != "w1|x" {
		t.Fatalf("pane copies: %+v %v", panes, err)
	}
	if panes[0].BackfilledAt != first.BackfilledAt || panes[0].Source != "sock" {
		t.Fatalf("a rewrite moved the backfill time or the source: %+v, was %+v", panes[0], first)
	}
	if err := w.DeleteCopy("pane", "w2"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.Copy("pane", "w2"); ok {
		t.Fatal("delete kept the copy")
	}
	if err := w.DeleteCopy("pane", "none"); err != nil {
		t.Fatalf("delete of no copy: %v", err)
	}
	if ss, _ := w.Copies("session"); len(ss) != 1 {
		t.Fatalf("session copies: %+v", ss)
	}
	if err := w.PutCopy("a|b", "k", "", nil); err == nil {
		t.Fatal("a noun with | was written")
	}
}

// Reads of unchanged copies answer from the last whole read: no table read
// until a copy is written, here or by another connection to the file, and
// a caller's change to a returned copy never reaches the next read.
func TestCopiesReadOncePerChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutCopy("pane", "w1", "sock", map[string]any{"id": "w1", "name": "a", "tags": []any{"x"}}); err != nil {
		t.Fatal(err)
	}
	reads := func() int64 { return db.FullReads(copiesTable) }
	first, _ := w.Copies("pane")
	before := reads()
	for range 5 {
		got, err := w.Copies("pane")
		if err != nil || len(got) != 1 || got[0].Props["name"] != "a" {
			t.Fatalf("copies: %+v %v", got, err)
		}
		if c, ok, _ := w.Copy("pane", "w1"); !ok || c.Props["name"] != "a" {
			t.Fatalf("copy: %+v", c)
		}
		if keys, _ := w.CopyKeys("pane"); len(keys) != 1 || keys[0] != "w1" {
			t.Fatalf("keys: %v", keys)
		}
	}
	if got := reads() - before; got != 0 {
		t.Fatalf("unchanged copies read the table %d times", got)
	}
	first[0].Props["name"] = "changed"
	first[0].Props["tags"].([]any)[0] = "y"
	if c, _, _ := w.Copy("pane", "w1"); c.Props["name"] != "a" || c.Props["tags"].([]any)[0] != "x" {
		t.Fatalf("a caller's change reached the cache: %+v", c)
	}
	if err := w.PutCopy("pane", "w1", "sock", map[string]any{"id": "w1", "name": "b"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Copies("pane"); got[0].Props["name"] != "b" {
		t.Fatalf("after a write: %+v", got)
	}
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ow, err := Open(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := ow.PutCopy("pane", "w2", "sock", map[string]any{"id": "w2"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.Copies("pane"); len(got) != 2 {
		t.Fatalf("another connection's write was not seen: %+v", got)
	}
	if _, ok, _ := w.Copy("pane", "w2"); !ok {
		t.Fatal("copy of another connection's write")
	}
}
