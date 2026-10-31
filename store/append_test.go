package store

import (
	"path/filepath"
	"testing"
)

func TestAppendCursor(t *testing.T) {
	t.Parallel()
	db, err := Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	immutable := Table{Name: "events", Owner: "events", Key: "id", Version: 1, AppendOnly: true}
	if err := db.Declare(immutable); err != nil {
		t.Fatal(err)
	}
	put := func(id string, n int) {
		t.Helper()
		if err := db.Tx([]string{"events"}, func(tx *Tx) error { return tx.Put("events", id, map[string]any{"id": id, "sequence": n}) }); err != nil {
			t.Fatal(err)
		}
	}
	put("first", 100)
	rows, err := db.After("events", 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v %v", rows, err)
	}
	seq := rows[0].Sequence
	put("second", 0)
	rows, err = db.After("events", seq)
	if err != nil || len(rows) != 1 || rows[0].Sequence <= seq {
		t.Fatalf("new rows=%v %v", rows, err)
	}
	rows, err = db.After("events", rows[0].Sequence)
	if err != nil || len(rows) != 0 {
		t.Fatalf("idle rows=%v %v", rows, err)
	}
	immutable.Name = "mutable"
	immutable.AppendOnly = false
	if err := db.Declare(immutable); err != nil {
		t.Fatal(err)
	}
	if _, err := db.After("mutable", 0); err == nil {
		t.Fatal("mutable table accepted an insertion cursor")
	}
	if _, err := db.After("unknown", 0); err == nil {
		t.Fatal("unknown table accepted")
	}
}
