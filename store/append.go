package store

import (
	"encoding/json"
	"fmt"
)

// SequencedRow is an immutable row and its database insertion sequence.
// This sequence is independent of any sequence inside the JSON document.
type SequencedRow struct {
	Sequence int64
	Row      json.RawMessage
}

// After reads only rows inserted after sequence in an append-only table.
// Updating existing rows is forbidden by the table contract.
func (s *Store) After(table string, sequence int64) ([]SequencedRow, error) {
	var appendOnly bool
	if err := s.db.QueryRow(`SELECT append_only FROM __deck_tables WHERE name = ?`, table).Scan(&appendOnly); err != nil {
		return nil, err
	}
	if !appendOnly {
		return nil, fmt.Errorf("store: %q is not append-only", table)
	}
	rows, err := s.db.Query(`SELECT rowid, row FROM `+quote(table)+` WHERE rowid > ? ORDER BY rowid`, sequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SequencedRow
	for rows.Next() {
		var r SequencedRow
		var data []byte
		if err := rows.Scan(&r.Sequence, &data); err != nil {
			return nil, err
		}
		r.Row = data
		out = append(out, r)
	}
	return out, rows.Err()
}

// JSONMatch selects rows by a JSON string field; Prefix compares a literal
// prefix (no LIKE wildcards), otherwise the whole value must match.
type JSONMatch struct {
	Field, Value string
	Prefix       bool
}

// MaxNumber reads an aggregate without materializing or decoding documents.
func (s *Store) MaxNumber(table, field string, matches ...JSONMatch) (uint64, error) {
	if err := s.declared(table); err != nil {
		return 0, err
	}
	query := `SELECT COALESCE(MAX(CAST(json_extract(row, ?) AS INTEGER)), 0) FROM ` + quote(table) + ` WHERE 1=1`
	args := []any{"$." + field}
	for _, m := range matches {
		if m.Prefix {
			query += ` AND substr(json_extract(row, ?), 1, length(?)) = ?`
			args = append(args, "$."+m.Field, m.Value, m.Value)
		} else {
			query += ` AND json_extract(row, ?) = ?`
			args = append(args, "$."+m.Field, m.Value)
		}
	}
	var n uint64
	err := s.db.QueryRow(query, args...).Scan(&n)
	return n, err
}
