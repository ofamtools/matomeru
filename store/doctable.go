package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"sync/atomic"
	"unicode/utf8"

	"ofam.dev/ochazuke"
)

// DocTable is ochazuke's DocTable over one declared table of this store:
// the table DocStore reads and writes, so a noun declared with props and a
// table name needs no store code of its own. Each document is the table's
// JSON row under its id.
//
//   - Get answers the row decoded as a Thing (a declared prop's number as
//     its type reads it: an integer prop's as int, a number prop's as
//     float64; a list prop's strings as []string).
//   - Put writes inside one transaction of the store (Tx), so the table's
//     Version moves and every reader of it sees the change as for any
//     other write. It merges: a field given as nil is removed, every field
//     not given is kept (see Put). Merge is Put.
//   - NextKey answers a create's next numbered key with one SQL query.
//   - Delete removes the row; Version is Store.Version.
//   - Query runs in SQLite: the conditions, the archive hiding, the text
//     search, the sort keys, the groups and the page, with the count of
//     every match (see Query).
//
// It answers what ochazuke's MemDocTable answers for the same documents
// and requests (doctest), with the differences Query names.
type DocTable struct {
	s     *Store
	table string
	r     *ochazuke.Runtime
	noun  string

	reads, queries, fallbacks atomic.Int64
	// trace, when set (a test), hears every statement Query runs.
	trace func(sql string, args []any)
}

// DocTable is the DocTable over the declared table holding noun's
// documents; r is the runtime the noun is declared on (its props type the
// values). The table is checked on each call, so it may be declared later.
func (s *Store) DocTable(table string, r *ochazuke.Runtime, noun string) *DocTable {
	return &DocTable{s: s, table: table, r: r, noun: noun}
}

// Reads is how many documents Query has decoded in all, Queries how many
// times it ran, and Fallbacks how many of those ran part of the request in
// Go (Query): a test proves with them that a page is not a whole read.
func (d *DocTable) Reads() int64     { return d.reads.Load() }
func (d *DocTable) Queries() int64   { return d.queries.Load() }
func (d *DocTable) Fallbacks() int64 { return d.fallbacks.Load() }

// FieldNameError is a field name (a prop, a sort or group key, a text
// field, the archive prop) that a DocTable will not put into SQL: it is
// not letters, digits, underscores and dashes starting with a letter or an
// underscore. Nothing ran.
type FieldNameError struct {
	Table, Field string
}

func (e *FieldNameError) Error() string {
	return fmt.Sprintf("store: %s: field name %q is not letters, digits, _ and -", e.Table, e.Field)
}

// DocNotFoundError is a Delete of a key the table does not hold.
type DocNotFoundError struct {
	Table, Key string
}

func (e *DocNotFoundError) Error() string {
	return fmt.Sprintf("store: %s has no document %q", e.Table, e.Key)
}

// Unwrap makes errors.Is(err, ErrNotFound) hold.
func (e *DocNotFoundError) Unwrap() error { return ErrNotFound }

// docField is a field a DocTable reads by name; plainField one that
// needs no quoting in a JSON path (the form an Index writes, so a
// condition repeats an index's expression exactly).
var (
	docField   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	plainField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// path is the JSON path literal of a field, checked: '$.name', or
// '$."a-b"' for a name with a dash.
func (d *DocTable) path(field string) (string, error) {
	if !docField.MatchString(field) {
		return "", &FieldNameError{Table: d.table, Field: field}
	}
	if plainField.MatchString(field) {
		return `'$.` + field + `'`, nil
	}
	return `'$."` + field + `"'`, nil
}

func (d *DocTable) nounOf() *ochazuke.Noun {
	if d.r == nil {
		return nil
	}
	return d.r.Noun(d.noun)
}

// Get answers the document stored under key; false when there is none.
func (d *DocTable) Get(key string) (ochazuke.Thing, bool, error) {
	if err := d.s.declared(d.table); err != nil {
		return nil, false, err
	}
	var raw []byte
	err := d.s.db.QueryRow(`SELECT row FROM `+quote(d.table)+` WHERE id = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	t, err := decodeDoc(raw, d.nounOf())
	if err != nil {
		return nil, false, err
	}
	return t, true, nil
}

// Put writes t into the document under key, in one transaction of the
// store, as ochazuke's DocTable contract asks: a field t gives with a value
// is set to it, a field t gives as nil is removed, and every field t does
// not give is kept (DocStore hands Put the whole document, with nil for a
// cleared prop). A new key stores t's fields that have a value.
func (d *DocTable) Put(key string, t ochazuke.Thing) error {
	return d.Merge(key, t)
}

// Merge writes the given fields into the document under key (creating it
// when the table has none): a field with a value is set, a field given as
// nil is removed, and every field it is not given is kept. It is Put.
func (d *DocTable) Merge(key string, fields ochazuke.Thing) error {
	return d.s.Tx([]string{d.table}, func(tx *Tx) error {
		stored, err := tx.Raw(d.table, key)
		if err != nil {
			return err
		}
		doc := map[string]json.RawMessage{}
		if stored != nil {
			if err := json.Unmarshal(stored, &doc); err != nil {
				return err
			}
		}
		for k, v := range fields {
			if v == nil {
				delete(doc, k)
				continue
			}
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			doc[k] = raw
		}
		return tx.Put(d.table, key, doc)
	})
}

// NextKey is ochazuke's DocKeyTable: prefix and one past the largest whole
// number N of a key prefix+N the table holds (prefix+"1" when it holds
// none), from one max query in SQLite, never a read of the documents.
func (d *DocTable) NextKey(prefix string) (string, error) {
	if err := d.s.declared(d.table); err != nil {
		return "", err
	}
	n := utf8.RuneCountInString(prefix) // SQLite's substr counts characters
	var most sql.NullInt64
	err := d.s.db.QueryRow(`SELECT max(CAST(substr(id, ?) AS INTEGER)) FROM `+quote(d.table)+
		` WHERE substr(id, 1, ?) = ? AND length(id) > ? AND substr(id, ?) NOT GLOB '*[^0-9]*'`,
		n+1, n, prefix, n, n+1).Scan(&most)
	if err != nil {
		return "", err
	}
	return prefix + strconv.FormatInt(most.Int64+1, 10), nil
}

// Delete removes the document under key; a key the table does not hold is
// a DocNotFoundError.
func (d *DocTable) Delete(key string) error {
	return d.s.Tx([]string{d.table}, func(tx *Tx) error {
		stored, err := tx.Raw(d.table, key)
		if err != nil {
			return err
		}
		if stored == nil {
			return &DocNotFoundError{Table: d.table, Key: key}
		}
		return tx.Delete(d.table, key)
	})
}

// Version is the table's change token (Store.Version).
func (d *DocTable) Version() (string, error) { return d.s.Version(d.table) }

// decodeDoc reads a stored row as a Thing, typing each declared prop's
// value (under its stored field) as the prop reads it (docValue).
func decodeDoc(raw []byte, n *ochazuke.Noun) (ochazuke.Thing, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	types := map[string]string{}
	if n != nil {
		for i := range n.Props {
			types[storedField(&n.Props[i])] = n.Props[i].Type
		}
	}
	t := make(ochazuke.Thing, len(m))
	for k, v := range m {
		t[k] = docValue(v, types[k])
	}
	return t, nil
}

// docValue is a decoded JSON value as Go holds it: a number of a number
// prop as float64, any other whole number as int, else float64; a list
// prop's strings as []string; arrays and objects element by element.
func docValue(v any, typ string) any {
	switch x := v.(type) {
	case json.Number:
		return docNumber(x, kindsOf(typ).float)
	case []any:
		out := make([]any, len(x))
		strs := make([]string, 0, len(x))
		for i, e := range x {
			out[i] = docValue(e, "")
			if s, ok := out[i].(string); ok {
				strs = append(strs, s)
			}
		}
		if kindsOf(typ).list && len(strs) == len(x) {
			return strs
		}
		return out
	case map[string]any:
		out := maps.Clone(x)
		for k, e := range out {
			out[k] = docValue(e, "")
		}
		return out
	}
	return v
}

func docNumber(n json.Number, float bool) any {
	if !float {
		if i, err := n.Int64(); err == nil && int64(int(i)) == i {
			return int(i)
		}
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	return f
}
