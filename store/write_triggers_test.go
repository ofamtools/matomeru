package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// countFixture is a file with two declared tables and a counter reader.
func countFixture(t *testing.T) (*Store, string, func(table string) int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, name := range []string{"a_rows", "b_rows"} {
		if err := db.Declare(Table{Name: name, Owner: "t", Key: "id", Version: 1}); err != nil {
			t.Fatal(err)
		}
	}
	return db, path, func(table string) int64 {
		t.Helper()
		n, err := db.WriteCount(table)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
}

// Every writer of the file moves a table's write count, through the
// table's triggers: a Put, a raw SQL statement (an older build, a sqlite3
// shell), Migrate's rewrite and what the rewrite writes into another table,
// a Rename (both names), and a second process.
func TestWriteCountMovesForEveryWriter(t *testing.T) {
	t.Parallel()
	db, path, count := countFixture(t)
	step := func(name, table string, write func()) {
		t.Helper()
		before := count(table)
		write()
		if count(table) == before {
			t.Errorf("%s did not move %s's write count", name, table)
		}
	}
	step("Put", "a_rows", func() {
		if err := db.Tx([]string{"a_rows"}, func(tx *Tx) error { return tx.Put("a_rows", "1", map[string]any{"id": "1"}) }); err != nil {
			t.Fatal(err)
		}
	})
	step("a raw UPDATE", "a_rows", func() {
		if _, err := db.db.Exec(`UPDATE a_rows SET row = '{"id":"1","raw":true}' WHERE id = '1'`); err != nil {
			t.Fatal(err)
		}
	})
	step("a raw DELETE", "a_rows", func() {
		if _, err := db.db.Exec(`DELETE FROM a_rows WHERE id = '1'`); err != nil {
			t.Fatal(err)
		}
	})
	if err := db.Tx([]string{"a_rows"}, func(tx *Tx) error { return tx.Put("a_rows", "m", map[string]any{"id": "m"}) }); err != nil {
		t.Fatal(err)
	}
	a, b := count("a_rows"), count("b_rows")
	if err := db.Migrate("a_rows", 1, 2, func(tx *Tx, _ json.RawMessage) (any, error) {
		return map[string]any{"id": "m", "v": 2}, tx.Put("b_rows", "from-migrate", map[string]any{"id": "from-migrate"})
	}); err != nil {
		t.Fatal(err)
	}
	if count("a_rows") == a || count("b_rows") == b {
		t.Errorf("Migrate: a_rows %d -> %d, b_rows %d -> %d", a, count("a_rows"), b, count("b_rows"))
	}
	old := count("b_rows")
	if err := db.Tx([]string{"b_rows"}, func(tx *Tx) error { return tx.Rename("b_rows", "c_rows") }); err != nil {
		t.Fatal(err)
	}
	if count("b_rows") == old || count("c_rows") == 0 {
		t.Errorf("Rename: b_rows %d -> %d, c_rows %d", old, count("b_rows"), count("c_rows"))
	}
	step("a write to the renamed table", "c_rows", func() {
		if _, err := db.db.Exec(`INSERT INTO c_rows (id, row) VALUES ('x', '{}')`); err != nil {
			t.Fatal(err)
		}
	})
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	step("another process", "a_rows", func() {
		if err := other.Tx([]string{"a_rows"}, func(tx *Tx) error { return tx.Put("a_rows", "2", map[string]any{"id": "2"}) }); err != nil {
			t.Fatal(err)
		}
	})
}

// A file whose tables have no write triggers (declared by an older build),
// or whose triggers an older build's rename left on the renamed table,
// gets them right at the next Open.
func TestOpenRepairsWriteTriggers(t *testing.T) {
	t.Parallel()
	db, path, _ := countFixture(t)
	if err := dropWriteTriggers(db.db, "a_rows"); err != nil {
		t.Fatal(err)
	}
	// An older build's rename: the table and its declaration move, the
	// triggers go with the table still counting the old name.
	for _, q := range []string{`ALTER TABLE b_rows RENAME TO d_rows`, `UPDATE __deck_tables SET name = 'd_rows' WHERE name = 'b_rows'`} {
		if _, err := db.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	for _, table := range []string{"a_rows", "d_rows"} {
		before, _ := again.WriteCount(table)
		if _, err := again.db.Exec(`INSERT INTO ` + table + ` (id, row) VALUES ('n', '{}')`); err != nil {
			t.Fatal(err)
		}
		if after, _ := again.WriteCount(table); after != before+1 {
			t.Errorf("%s: write count %d -> %d after one insert", table, before, after)
		}
	}
	var stale int
	if err := again.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE '__deckcount_b_rows_%'`).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("the old name's triggers stayed: %d (%v)", stale, err)
	}
}

// The write cost: 1,000 single-row transactions counted by the triggers,
// by a Go-side bump in each transaction (how v0.45.0 to v0.47.0 counted),
// and not at all, logged.
func TestWriteTriggerCost(t *testing.T) {
	if testing.Short() {
		t.Skip("measurement")
	}
	db, _, _ := countFixture(t)
	run := func(bump bool) time.Duration {
		start := time.Now()
		for i := range 1000 {
			if err := db.Tx([]string{"a_rows"}, func(tx *Tx) error {
				if err := tx.Put("a_rows", "k", map[string]any{"id": "k", "i": i}); err != nil {
					return err
				}
				if bump {
					_, err := tx.tx.Exec(bumpWrites, "a_rows")
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		return time.Since(start)
	}
	triggers := run(false)
	if err := dropWriteTriggers(db.db, "a_rows"); err != nil {
		t.Fatal(err)
	}
	goBump, none := run(true), run(false)
	t.Logf("1,000 writes: %v by the triggers, %v by a Go-side bump, %v uncounted", triggers, goBump, none)
}
