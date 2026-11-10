package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// Tables lists every declaration with its row count; a read-only store
// reads the same and refuses every write.
func TestTablesAndReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	journal := Table{Name: "journal", Owner: "history", Key: "id", Version: 1, AppendOnly: true}
	notes := Table{Name: "notes", Owner: "notes", Key: "id", Version: 2}
	for _, table := range []Table{notes, journal} {
		if err := db.Declare(table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Tx([]string{"journal"}, func(tx *Tx) error {
		for _, id := range []string{"a", "b"} {
			if err := tx.Put("journal", id, map[string]string{"id": id}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []TableInfo{{Table: journal, Rows: 2}, {Table: notes, Rows: 0}}
	check := func(s *Store) {
		t.Helper()
		got, err := s.Tables()
		if err != nil || len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("tables %+v %v", got, err)
		}
	}
	check(db)
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	check(ro)
	if err := ro.Declare(Table{Name: "more", Owner: "x", Key: "id", Version: 1}); err == nil {
		t.Fatal("a read-only store declared a table")
	}
	if err := ro.Tx([]string{"notes"}, func(tx *Tx) error { return tx.Put("notes", "n", map[string]string{}) }); err == nil {
		t.Fatal("a read-only store wrote a row")
	}
	if _, err := OpenReadOnly(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("a missing database opened read-only")
	}
}

func TestVersionMovesOnEveryWriteKindAndOnAnotherProcess(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "v.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"a_rows", "b_rows"} {
		if err := db.Declare(Table{Name: name, Owner: "t", Key: "id", Version: 1}); err != nil {
			t.Fatal(err)
		}
	}
	ver := func(table string) string {
		t.Helper()
		v, err := db.Version(table)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	write := func(f func(tx *Tx) error) {
		t.Helper()
		if err := db.Tx([]string{"a_rows", "b_rows"}, f); err != nil {
			t.Fatal(err)
		}
	}
	start := ver("a_rows")
	if ver("a_rows") != start {
		t.Fatal("the token moved with no write")
	}
	if err := db.View([]string{"a_rows"}, func(tx *Tx) error { _, err := tx.All("a_rows"); return err }); err != nil || ver("a_rows") != start {
		t.Fatalf("a read moved the token: %v", err)
	}
	last := start
	for name, f := range map[string]func(tx *Tx) error{
		"put":    func(tx *Tx) error { return tx.Put("a_rows", "1", map[string]any{"id": "1"}) },
		"update": func(tx *Tx) error { return tx.Put("a_rows", "1", map[string]any{"id": "1", "x": 2}) },
		"delete": func(tx *Tx) error { return tx.Delete("a_rows", "1") },
	} {
		write(f)
		if got := ver("a_rows"); got == last {
			t.Fatalf("%s did not move the token", name)
		} else {
			last = got
		}
	}
	// A write of another table leaves this table's own count alone.
	before := ver("a_rows")
	write(func(tx *Tx) error { return tx.Put("b_rows", "1", map[string]any{"id": "1"}) })
	if ver("a_rows") != before {
		t.Fatal("a write of another table moved this table's token")
	}
	// A rolled-back transaction moves nothing.
	_ = db.Tx([]string{"a_rows"}, func(tx *Tx) error {
		tx.Put("a_rows", "9", map[string]any{"id": "9"})
		return errors.New("no")
	})
	if ver("a_rows") != before {
		t.Fatal("a rolled-back write moved the token")
	}
	// Migrate rewrites rows.
	write(func(tx *Tx) error { return tx.Put("a_rows", "m", map[string]any{"id": "m"}) })
	before = ver("a_rows")
	if err := db.Migrate("a_rows", 1, 2, func(*Tx, json.RawMessage) (any, error) { return map[string]any{"id": "m", "v": 2}, nil }); err != nil {
		t.Fatal(err)
	}
	if ver("a_rows") == before {
		t.Fatal("Migrate did not move the token")
	}
	// Another connection (another process) writes the file.
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	before = ver("a_rows")
	if err := other.Tx([]string{"a_rows"}, func(tx *Tx) error { return tx.Put("a_rows", "ext", map[string]any{"id": "ext"}) }); err != nil {
		t.Fatal(err)
	}
	if ver("a_rows") == before {
		t.Fatal("another connection's write did not move the token")
	}
	if db.FullReads("a_rows") < 1 {
		t.Fatal("full reads are counted")
	}
}
