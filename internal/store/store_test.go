package store

import (
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
