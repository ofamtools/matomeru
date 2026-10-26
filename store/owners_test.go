package store_test

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

// mtm's own units declare their tables through the exported package and a
// reopened file accepts the same declarations unchanged, rows intact.
func TestOwnerDeclarationsReopenUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	open := func() *store.Store {
		t.Helper()
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := work.Open(db); err != nil {
			t.Fatal(err)
		}
		if _, err := history.Open(db, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := contextitems.Open(db); err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := open()
	if err := db.Tx([]string{"notes"}, func(tx *store.Tx) error {
		return tx.Put("notes", "n1", map[string]string{"id": "n1", "text": "keep"})
	}); err != nil {
		t.Fatal(err)
	}
	before, err := db.Tables()
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	db = open()
	defer db.Close()
	after, err := db.Tables()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) || len(after) != len(work.Tables)+3 {
		t.Fatalf("tables before %d after %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("table %d changed: %+v -> %+v", i, before[i], after[i])
		}
	}
	var note map[string]string
	if err := db.Get("notes", "n1", &note); err != nil || note["text"] != "keep" {
		t.Fatalf("note %v %v", note, err)
	}
	changed := history.Table
	changed.Version++
	if err := db.Declare(changed); err == nil {
		t.Fatal("a changed owner declaration was accepted")
	}
}
