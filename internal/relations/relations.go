// Package relations is mtm's relation unit: one table, `relations`, in mtm.db
// that keeps ochazuke's Relation rows (anything points to anything: two
// any-noun ends, a kind, free text, a JSON data object, a position). It is
// plain storage, the ochazuke.RelationStore the runtime is given
// (Runtime.SetRelationStore): ochazuke enforces kinds, positions, archive
// and detach on top. Each tool keeps its own store; this one holds the
// relations mtm makes (project directories first).
package relations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// Table is the relations table's name.
const Table = "relations"

// Tables is the declaration this package owns.
var Tables = []store.Table{{Name: Table, Owner: "relations", Key: "id", Version: 1}}

// Row is one stored relation. ID is the relation's identity (kind and ends,
// ochazuke.Relation.ID); Key is the short word that names it on the command
// line (the same identity, hashed).
type Row struct {
	ID         string         `json:"id"`
	Key        string         `json:"key"`
	Kind       string         `json:"kind"`
	From       string         `json:"from"`
	To         string         `json:"to"`
	Text       string         `json:"text,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Position   int            `json:"position,omitempty"`
	Archived   bool           `json:"archived,omitempty"`
	FormerFrom string         `json:"former_from,omitempty"`
	FormerTo   string         `json:"former_to,omitempty"`
	Created    int64          `json:"created,omitempty"`
	Updated    int64          `json:"updated,omitempty"`
}

// KeyOf is the short key of a relation: twelve hex digits of its identity's
// hash. Deleting an end changes the identity (the end becomes a former end),
// so the key changes with it.
func KeyOf(x ochazuke.Relation) string {
	sum := sha256.Sum256([]byte(x.ID()))
	return hex.EncodeToString(sum[:6])
}

// Relation is the row as ochazuke reads it.
func (r Row) Relation() ochazuke.Relation {
	return ochazuke.Relation{Kind: r.Kind, From: r.From, To: r.To, Text: r.Text, Data: r.Data, Position: r.Position,
		Archived: r.Archived, FormerFrom: r.FormerFrom, FormerTo: r.FormerTo}
}

// RowOf is the stored form of x, stamped at now (created is kept by Put).
func RowOf(x ochazuke.Relation, now int64) Row {
	return Row{ID: x.ID(), Key: KeyOf(x), Kind: x.Kind, From: x.From, To: x.To, Text: x.Text, Data: x.Data, Position: x.Position,
		Archived: x.Archived, FormerFrom: x.FormerFrom, FormerTo: x.FormerTo, Created: now, Updated: now}
}

// Declare declares the table and its indexes; it is idempotent and runs
// before any unit that migrates rows into the table.
func Declare(db *store.Store) error {
	for _, t := range Tables {
		if err := db.Declare(t); err != nil {
			return err
		}
	}
	// A thing's relations, either direction, are one indexed read; so is every
	// relation of one kind from a noun (project directories at startup).
	for _, ix := range []struct {
		name   string
		fields []string
	}{{"from", []string{"from", "kind", "position"}}, {"to", []string{"to", "kind"}}, {"key", []string{"key"}}} {
		if err := db.Index(Table, ix.name, ix.fields...); err != nil {
			return err
		}
	}
	return nil
}

// Store is the relation table as an ochazuke.RelationStore.
type Store struct{ db *store.Store }

var _ ochazuke.RelationStore = (*Store)(nil)

// Open declares the table and returns the store.
func Open(db *store.Store) (*Store, error) {
	if err := Declare(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Relations answers the relations that meet q, by position (ties in the
// order they were stored). Kind, From and To are matched in the database;
// Thing is a read of From and one of To.
func (s *Store) Relations(q ochazuke.RelationQuery) ([]ochazuke.Relation, error) {
	rows, err := s.Rows(q)
	if err != nil {
		return nil, err
	}
	out := make([]ochazuke.Relation, len(rows))
	for i, r := range rows {
		out[i] = r.Relation()
	}
	return out, nil
}

// Rows is Relations with the stored rows (keys and stamps included).
func (s *Store) Rows(q ochazuke.RelationQuery) ([]Row, error) {
	var out []Row
	if q.Thing == "" {
		raws, err := s.db.Match(Table, matches(q, q.From, q.To)...)
		if err != nil {
			return nil, err
		}
		out = decode(raws, q, nil)
	} else {
		seen := map[string]bool{}
		for _, ends := range [][2]string{{q.Thing, ""}, {"", q.Thing}} {
			raws, err := s.db.Match(Table, matches(q, ends[0], ends[1])...)
			if err != nil {
				return nil, err
			}
			for _, r := range decode(raws, q, seen) {
				out = append(out, r)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Row) int { return a.Position - b.Position })
	return out, nil
}

// matches are the database matches of a query for the given ends.
func matches(q ochazuke.RelationQuery, from, to string) []store.JSONMatch {
	var out []store.JSONMatch
	if from != "" {
		out = append(out, store.JSONMatch{Field: "from", Value: from})
	}
	if q.Kind != "" {
		out = append(out, store.JSONMatch{Field: "kind", Value: q.Kind})
	}
	if to != "" {
		out = append(out, store.JSONMatch{Field: "to", Value: to})
	}
	return out
}

func decode(raws []json.RawMessage, q ochazuke.RelationQuery, seen map[string]bool) []Row {
	var out []Row
	for _, raw := range raws {
		var r Row
		if json.Unmarshal(raw, &r) != nil || !q.Match(r.Relation()) || seen != nil && seen[r.ID] {
			continue
		}
		if seen != nil {
			seen[r.ID] = true
		}
		out = append(out, r)
	}
	return out
}

// ByKey is the stored relation with that short key.
func (s *Store) ByKey(key string) (Row, error) {
	raws, err := s.db.Match(Table, store.JSONMatch{Field: "key", Value: key})
	if err != nil {
		return Row{}, err
	}
	for _, raw := range raws {
		var r Row
		if json.Unmarshal(raw, &r) == nil {
			return r, nil
		}
	}
	return Row{}, store.ErrNotFound
}

// Put inserts the relation or replaces the row with its identity (the
// created time is kept).
func (s *Store) Put(x ochazuke.Relation) error {
	return s.db.Tx([]string{Table}, func(tx *store.Tx) error { return PutTx(tx, x, time.Now().UnixMilli()) })
}

// PutTx is Put inside a transaction that holds the relations table, at now.
func PutTx(tx *store.Tx, x ochazuke.Relation, now int64) error {
	row := RowOf(x, now)
	var old Row
	switch err := tx.Get(Table, row.ID, &old); {
	case err == nil:
		if old.Created != 0 {
			row.Created = old.Created
		}
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	return tx.Put(Table, row.ID, row)
}

// Remove deletes the row with the relation's identity.
func (s *Store) Remove(x ochazuke.Relation) error {
	return s.db.Tx([]string{Table}, func(tx *store.Tx) error { return tx.Delete(Table, x.ID()) })
}
