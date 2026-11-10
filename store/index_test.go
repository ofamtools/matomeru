package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func indexFixture(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Declare(Table{Name: "rels", Owner: "rels", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]string{
		{"id": "1", "from": "project:a", "kind": "directory", "to": "directory:/x"},
		{"id": "2", "from": "project:a", "kind": "see-also", "to": "pane:w"},
		{"id": "3", "from": "project:b", "kind": "directory", "to": "directory:/x"},
		{"id": "4", "from": "note:n", "kind": "about", "to": "project:a"},
	}
	err = db.Tx([]string{"rels"}, func(tx *Tx) error {
		for _, r := range rows {
			if err := tx.Put("rels", r["id"], r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestIndexIsIdempotentAndLeavesDeclarations(t *testing.T) {
	t.Parallel()
	db := indexFixture(t)
	before, err := db.Tables()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Index("rels", "from", "from", "kind"); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := db.Tables()
	if len(before) != len(after) || before[0] != after[0] {
		t.Fatalf("declarations changed: %v -> %v", before, after)
	}
	if err := db.Index("rels", "bad name", "from"); err == nil {
		t.Fatal("a bad index name is refused")
	}
	if err := db.Index("rels", "x", "a'b"); err == nil {
		t.Fatal("a bad field is refused")
	}
	if err := db.Index("nope", "x", "from"); err == nil {
		t.Fatal("an undeclared table is refused")
	}
}

func TestMatchByOneTwoFieldsAndPrefix(t *testing.T) {
	t.Parallel()
	db := indexFixture(t)
	n := func(ms ...JSONMatch) int {
		t.Helper()
		rows, err := db.Match("rels", ms...)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	if got := n(JSONMatch{Field: "from", Value: "project:a"}); got != 2 {
		t.Fatalf("one field: %d", got)
	}
	if got := n(JSONMatch{Field: "from", Value: "project:a"}, JSONMatch{Field: "kind", Value: "directory"}); got != 1 {
		t.Fatalf("two fields: %d", got)
	}
	if got := n(JSONMatch{Field: "from", Value: "project:", Prefix: true}, JSONMatch{Field: "kind", Value: "directory"}); got != 2 {
		t.Fatalf("prefix: %d", got)
	}
	if got := n(JSONMatch{Field: "from", Value: "project%", Prefix: true}); got != 0 {
		t.Fatalf("a prefix is literal, not a LIKE pattern: %d", got)
	}
	if got := n(); got != 4 {
		t.Fatalf("no match: %d", got)
	}
	err := db.Tx([]string{"rels"}, func(tx *Tx) error {
		rows, err := tx.Match("rels", JSONMatch{Field: "to", Value: "directory:/x"})
		if err != nil || len(rows) != 2 {
			t.Fatalf("tx match: %v %v", rows, err)
		}
		es, err := tx.MatchEntries("rels", JSONMatch{Field: "to", Value: "directory:/x"})
		if err != nil || len(es) != 2 || es[0].ID != "1" {
			t.Fatalf("tx entries: %v %v", es, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMatchUsesTheIndex(t *testing.T) {
	t.Parallel()
	db := indexFixture(t)
	if err := db.Index("rels", "from", "from", "kind"); err != nil {
		t.Fatal(err)
	}
	for _, ms := range [][]JSONMatch{
		{{Field: "from", Value: "project:a"}},
		{{Field: "from", Value: "project:a"}, {Field: "kind", Value: "directory"}},
		{{Field: "from", Value: "project:", Prefix: true}, {Field: "kind", Value: "directory"}},
	} {
		plan, err := db.plan("rels", ms...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, "idx_rels_from") {
			t.Fatalf("plan does not use the index: %s", plan)
		}
	}
}

func TestTableVersion(t *testing.T) {
	t.Parallel()
	db := indexFixture(t)
	if v, ok := db.TableVersion("rels"); !ok || v != 1 {
		t.Fatalf("version %d %v", v, ok)
	}
	if _, ok := db.TableVersion("nope"); ok {
		t.Fatal("undeclared")
	}
}

func TestBackupCopiesAndRefusesAnExistingFile(t *testing.T) {
	t.Parallel()
	db := indexFixture(t)
	dest := filepath.Join(t.TempDir(), "mtm.db.bak-x")
	if err := db.Backup(dest); err != nil {
		t.Fatal(err)
	}
	copy, err := OpenReadOnly(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	rows, err := copy.All("rels")
	if err != nil || len(rows) != 4 {
		t.Fatalf("backup rows %d %v", len(rows), err)
	}
	if err := db.Backup(dest); err == nil {
		t.Fatal("an existing backup is not overwritten")
	}
}
