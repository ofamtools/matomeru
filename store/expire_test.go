package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestExpire: a file whose append-only table has the guard that refuses
// every delete (as every file before expiry) gets the expiry-aware guard at
// Open; Expire removes the rows older than the cutoff, in batches, and
// keeps the newer ones and the newest row whatever its age; a plain DELETE
// or UPDATE stays refused, through the store's connection and through a
// raw connection to the file, and a raw connection cannot write an expiry
// grant either.
func TestExpire(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	events := Table{Name: "events", Owner: "events", Key: "id", Version: 1, AppendOnly: true}
	if err := db.Declare(events); err != nil {
		t.Fatal(err)
	}
	// at: 1..10 for e01..e10, then e11 at 1 (late, out of order) and e12 at
	// 2 (the newest row).
	if err := db.Tx([]string{"events"}, func(tx *Tx) error {
		for i, at := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 1, 2} {
			id := fmt.Sprintf("e%02d", i+1)
			if err := tx.Put("events", id, map[string]any{"id": id, "at": at}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// The guard every file had before expiry existed.
	if _, err := raw.Exec(`DROP TRIGGER "__deck_events_delete"; CREATE TRIGGER "__deck_events_delete" BEFORE DELETE ON "events" BEGIN SELECT RAISE(ABORT, 'append-only table'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if db, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got, err := db.ExpireCount("events", Expiry{Field: "at", Before: 6}); err != nil || got.Rows != 12 || got.Remove != 6 || got.Oldest != 1 || got.Newest != 5 {
		t.Fatalf("the count is e01-e05 and the late e11, never the newest e12: %+v %v", got, err)
	}
	got, err := db.Expire("events", Expiry{Field: "at", Before: 6, Batch: 2, Pause: -1})
	if err != nil {
		t.Fatal(err)
	}
	// Batches of two: e01-e02, e03-e04, e05 (e06 stays), then e07-e08
	// removes nothing and the walk stops: e11 waits for a later run.
	if got.Remove != 5 || got.Batches != 3 {
		t.Fatalf("expire removes e01-e05 in three batches: %+v", got)
	}
	left := func() string {
		rows, err := raw.Query(`SELECT id FROM events ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		return strings.Join(ids, " ")
	}
	if l := left(); l != "e06 e07 e08 e09 e10 e11 e12" {
		t.Fatalf("left %s", l)
	}
	if got, err := db.Expire("events", Expiry{Field: "at", Before: 100, Pause: -1}); err != nil || got.Remove != 6 || left() != "e12" {
		t.Fatalf("every row but the newest goes: %+v %v left %s", got, err, left())
	}

	for name, conn := range map[string]*sql.DB{"store": db.db, "raw": raw} {
		for _, stmt := range []string{`DELETE FROM events`, `UPDATE events SET row = '{}'`} {
			if _, err := conn.Exec(stmt); err == nil || !strings.Contains(err.Error(), "append-only table") {
				t.Fatalf("%s: %s is refused: %v", name, stmt, err)
			}
		}
	}
	if _, err := raw.Exec(`INSERT INTO __deck_expiry (name, token, field, before, below, upto) VALUES ('events', 'x', '$.at', 1000, 0, 1000)`); err == nil {
		t.Fatal("a raw connection writes no expiry grant")
	}
	if l := left(); l != "e12" {
		t.Fatalf("left %s", l)
	}
}
