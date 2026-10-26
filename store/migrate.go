package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
)

// Rewrite turns one row of the old format into the new one. It runs inside
// the migration's transaction; tx reaches every declared table of the file,
// so a rewrite may read (or write) related rows. The returned value is
// encoded as Put encodes it; returning the row unchanged keeps it.
type Rewrite func(tx *Tx, row json.RawMessage) (any, error)

// Migrate moves a declared table from version from to version to, running
// rewrite once over every row inside one write transaction. The declaration's
// version changes only when every rewrite succeeds; any error rolls back the
// rows and the version together.
//
// Call it before Declare with the new version, once per step (1->2, 2->3):
//   - the table is not declared yet (a fresh file): nothing to migrate, nil;
//   - the declared version is already to or newer: a no-op, so a re-run or an
//     older step of a chain does nothing;
//   - the declared version is from: the rewrite runs and the version becomes to;
//   - any other version is an error.
//
// An append-only table keeps its rows as written: its version may move, but a
// rewrite that changes a row fails the migration. A nil rewrite moves the
// version alone (a format change every old row already satisfies).
func (s *Store) Migrate(table string, from, to int, rewrite Rewrite) error {
	if from < 1 || to <= from {
		return fmt.Errorf("store: migrate %q: versions must rise from a positive version (%d -> %d)", table, from, to)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	declared, err := declaredTables(tx)
	if err != nil {
		return err
	}
	current, ok := declared[table]
	if !ok || current.Version >= to {
		return nil
	}
	if current.Version != from {
		return fmt.Errorf("store: migrate %q: table is at version %d, migration starts at %d", table, current.Version, from)
	}
	if rewrite != nil {
		scope := &Tx{tx: tx, tables: declared}
		rows, err := tx.Query(`SELECT id, row FROM ` + quote(table) + ` ORDER BY rowid`)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", table, err)
		}
		type stored struct {
			id  string
			row []byte
		}
		var all []stored
		for rows.Next() {
			var r stored
			if err := rows.Scan(&r.id, &r.row); err != nil {
				rows.Close()
				return err
			}
			all = append(all, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range all {
			next, err := rewrite(scope, json.RawMessage(r.row))
			if err != nil {
				return fmt.Errorf("migrate %s/%s v%d -> v%d: %w", table, r.id, from, to, err)
			}
			data, err := json.Marshal(next)
			if err != nil {
				return fmt.Errorf("migrate %s/%s: encode: %w", table, r.id, err)
			}
			if same(data, r.row) {
				continue
			}
			if current.AppendOnly {
				return fmt.Errorf("store: migrate %q: append-only table refuses row rewrites (%s)", table, r.id)
			}
			if _, err := tx.Exec(`UPDATE `+quote(table)+` SET row = ? WHERE id = ?`, string(data), r.id); err != nil {
				return fmt.Errorf("migrate %s/%s: %w", table, r.id, err)
			}
		}
	}
	if _, err := tx.Exec(`UPDATE __deck_tables SET version = ? WHERE name = ?`, to, table); err != nil {
		return fmt.Errorf("migrate %s: %w", table, err)
	}
	return tx.Commit()
}

// same reports whether two JSON documents hold the same value, so a rewrite
// that only re-encodes a row (key order, spacing) does not count as a change.
func same(a, b []byte) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(va, vb)
}

func declaredTables(tx *sql.Tx) (map[string]Table, error) {
	rows, err := tx.Query(`SELECT name, owner, key, version, append_only FROM __deck_tables`)
	if err != nil {
		return nil, fmt.Errorf("store tables: %w", err)
	}
	defer rows.Close()
	out := map[string]Table{}
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Name, &t.Owner, &t.Key, &t.Version, &t.AppendOnly); err != nil {
			return nil, err
		}
		out[t.Name] = t
	}
	return out, rows.Err()
}
