package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

type pinV1 struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type pinV2 struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths"`
}

func migrateFixture(t *testing.T, table Table, ids ...string) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "ext.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Declare(table); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx([]string{table.Name}, func(tx *Tx) error {
		for _, id := range ids {
			if err := tx.Put(table.Name, id, pinV1{ID: id, Path: "/" + id}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

func version(t *testing.T, db *Store, name string) int {
	t.Helper()
	tables, err := db.Tables()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table.Name == name {
			return table.Version
		}
	}
	t.Fatalf("table %q is not declared", name)
	return 0
}

func toV2(_ *Tx, row json.RawMessage) (any, error) {
	var old pinV1
	if err := json.Unmarshal(row, &old); err != nil {
		return nil, err
	}
	return pinV2{ID: old.ID, Paths: []string{old.Path}}, nil
}

// A migration rewrites every row once, then the new declaration opens and
// the old one is refused.
func TestMigrateRewritesRowsAndBumpsVersion(t *testing.T) {
	v1 := Table{Name: "ai_pins", Owner: "ai", Key: "id", Version: 1}
	db := migrateFixture(t, v1, "a", "b")
	if err := db.Migrate("ai_pins", 1, 2, toV2); err != nil {
		t.Fatal(err)
	}
	if got := version(t, db, "ai_pins"); got != 2 {
		t.Fatalf("version %d, want 2", got)
	}
	var pin pinV2
	if err := db.Get("ai_pins", "b", &pin); err != nil || len(pin.Paths) != 1 || pin.Paths[0] != "/b" {
		t.Fatalf("row %+v %v", pin, err)
	}
	v2 := v1
	v2.Version = 2
	if err := db.Declare(v2); err != nil {
		t.Fatalf("declare v2 after migrating: %v", err)
	}
	if err := db.Declare(v1); err == nil {
		t.Fatal("the v1 declaration opened a migrated table")
	}
	rows, err := db.All("ai_pins")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %d %v", len(rows), err)
	}
}

// A failing rewrite rolls back every row it already rewrote and the version.
func TestMigrateFailureRollsBack(t *testing.T) {
	db := migrateFixture(t, Table{Name: "ai_pins", Owner: "ai", Key: "id", Version: 1}, "a", "b")
	boom := errors.New("bad row")
	calls := 0
	err := db.Migrate("ai_pins", 1, 2, func(tx *Tx, row json.RawMessage) (any, error) {
		calls++
		if calls == 2 {
			return nil, boom
		}
		return toV2(tx, row)
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error %v, want the rewrite's", err)
	}
	if got := version(t, db, "ai_pins"); got != 1 {
		t.Fatalf("version %d after a failed migration, want 1", got)
	}
	var pin pinV1
	if err := db.Get("ai_pins", "a", &pin); err != nil || pin.Path != "/a" {
		t.Fatalf("first row was not rolled back: %+v %v", pin, err)
	}
}

// Migrate runs once: a re-run, an older step of a chain and a fresh file
// are no-ops; a version the step does not start from is an error.
func TestMigrateRunsOnce(t *testing.T) {
	db := migrateFixture(t, Table{Name: "ai_pins", Owner: "ai", Key: "id", Version: 1}, "a")
	calls := 0
	count := func(tx *Tx, row json.RawMessage) (any, error) { calls++; return toV2(tx, row) }
	for range 2 {
		if err := db.Migrate("ai_pins", 1, 2, count); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("rewrite ran %d times, want 1", calls)
	}
	if err := db.Migrate("ai_pins", 2, 3, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate("ai_pins", 1, 2, count); err != nil || calls != 1 {
		t.Fatalf("an older step ran again: calls %d, %v", calls, err)
	}
	if err := db.Migrate("ai_pins", 4, 5, count); err == nil {
		t.Fatal("a step from version 4 ran on a table at version 3")
	}
	if err := db.Migrate("never_declared", 1, 2, count); err != nil || calls != 1 {
		t.Fatalf("an undeclared table migrated: calls %d, %v", calls, err)
	}
	if err := db.Migrate("ai_pins", 3, 3, nil); err == nil {
		t.Fatal("a migration that does not raise the version was accepted")
	}
}

// An append-only table may move its version but never rewrites a row.
func TestMigrateAppendOnlyRefusesRewrites(t *testing.T) {
	db := migrateFixture(t, Table{Name: "ai_log", Owner: "ai", Key: "id", Version: 1, AppendOnly: true}, "a")
	if err := db.Migrate("ai_log", 1, 2, toV2); err == nil {
		t.Fatal("an append-only row was rewritten")
	}
	if got := version(t, db, "ai_log"); got != 1 {
		t.Fatalf("version %d after a refused migration, want 1", got)
	}
	keep := func(_ *Tx, row json.RawMessage) (any, error) {
		var v map[string]any
		err := json.Unmarshal(row, &v)
		return v, err
	}
	if err := db.Migrate("ai_log", 1, 2, keep); err != nil {
		t.Fatalf("an unchanged append-only row was refused: %v", err)
	}
	if got := version(t, db, "ai_log"); got != 2 {
		t.Fatalf("version %d, want 2", got)
	}
}
