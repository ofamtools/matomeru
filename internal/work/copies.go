package work

import (
	"errors"
	"strings"
	"sync"

	"ofam.dev/matomeru/store"
)

// Copy is mtm's copy of a thing another source holds (a pane or a session
// the daemon lists): ochazuke's copy stores (CopyStore) keep their copies
// here, one row per noun and key. The first read of a thing copies it in
// (the backfill, BackfilledAt); after that the copy is the thing, and it
// outlives the source's (a pane that ended stays). Props is the thing as
// the copy store put it, a tombstone included (ochazuke.CopyTombstone).
type Copy struct {
	ID    string         `json:"id"` // noun|key
	Noun  string         `json:"noun"`
	Key   string         `json:"key"`
	Props map[string]any `json:"props"`
	// Source is where the thing was copied from (the daemon's socket for
	// a pane or a session): one database serves every mtm, on any TUIOS
	// server, so a listing speaks only for its own source's copies.
	Source       string `json:"source,omitempty"`
	BackfilledAt int64  `json:"backfilled_at"`
	Updated      int64  `json:"updated"`
}

// copiesTable is the copies' table (Tables).
const copiesTable = "copies"

// copyID is a copy's row id: a noun is one word, so the first | parts it
// from the key whatever the key holds.
func copyID(noun, key string) string { return noun + "|" + key }

// copyCache is every copy row as last read, and the copies table's version
// (store.Version) it was read at: reads answer from it while the version is
// unchanged, so a render path that asks for the copies many times a frame
// reads and decodes the table once per change. Rows leave the cache as deep
// clones; nothing outside holds the cached maps.
type copyCache struct {
	mu    sync.Mutex
	ver   string
	rows  []Copy
	index map[string]int // copyID → place in rows
}

// fresh is the cache's rows when ver is the version they were read at.
func (cc *copyCache) fresh(ver string) ([]Copy, map[string]int, bool) {
	if cc.rows == nil || cc.ver != ver {
		return nil, nil, false
	}
	return cc.rows, cc.index, true
}

// copyRows is every copy row, from the cache while the table is unchanged.
func (s *Store) copyRows() ([]Copy, error) {
	ver, err := s.db.Version(copiesTable)
	if err != nil {
		return nil, err
	}
	s.copies.mu.Lock()
	defer s.copies.mu.Unlock()
	if rows, _, ok := s.copies.fresh(ver); ok {
		return rows, nil
	}
	rows, err := all[Copy](s.db, copiesTable)
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(rows))
	for i, c := range rows {
		index[c.ID] = i
	}
	s.copies.ver, s.copies.rows, s.copies.index = ver, rows, index
	return rows, nil
}

// CopiesVersion is a token for the copies as they are now: equal answers
// mean no copy was written between them (store.Version).
func (s *Store) CopiesVersion() (string, error) { return s.db.Version(copiesTable) }

// cloneCopy is c with its props deep-copied, so a caller may change them.
func cloneCopy(c Copy) Copy {
	c.Props = cloneProps(c.Props)
	return c
}

func cloneProps(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

// cloneValue deep-copies a decoded JSON value (maps and slices; the rest
// are values).
func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneProps(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	default:
		return v
	}
}

// Copy is the copy of noun's thing key; ok is false when there is none.
// While the copies are unchanged since they were last read whole, it
// answers from that read; otherwise it reads the one row.
func (s *Store) Copy(noun, key string) (Copy, bool, error) {
	id := copyID(noun, key)
	if ver, err := s.db.Version(copiesTable); err == nil {
		s.copies.mu.Lock()
		rows, index, ok := s.copies.fresh(ver)
		var c Copy
		i, has := index[id]
		if ok && has {
			c = cloneCopy(rows[i])
		}
		s.copies.mu.Unlock()
		if ok {
			return c, has, nil
		}
	}
	var c Copy
	err := s.db.Get(copiesTable, id, &c)
	if errors.Is(err, store.ErrNotFound) {
		return Copy{}, false, nil
	}
	if err != nil {
		return Copy{}, false, err
	}
	return c, true, nil
}

// PutCopy writes the copy of noun's thing key from source: the first
// write stamps BackfilledAt and Source, a later one keeps them (and the
// row's place in Copies).
func (s *Store) PutCopy(noun, key, source string, props map[string]any) error {
	if noun == "" || strings.Contains(noun, "|") || key == "" {
		return errors.New("a copy requires a noun (one word) and a key")
	}
	id := copyID(noun, key)
	return s.db.Tx([]string{copiesTable}, func(tx *store.Tx) error {
		now := Now()
		c := Copy{ID: id, Noun: noun, Key: key, Props: props, Source: source, BackfilledAt: now, Updated: now}
		var old Copy
		switch err := tx.Get(copiesTable, id, &old); {
		case err == nil:
			c.BackfilledAt, c.Source = old.BackfilledAt, old.Source
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
		return tx.Put(copiesTable, id, c)
	})
}

// Copies are noun's copies in the order they were first written, each the
// caller's own (props deep-copied from the cache, copyRows).
func (s *Store) Copies(noun string) ([]Copy, error) {
	rows, err := s.copyRows()
	if err != nil {
		return nil, err
	}
	out := []Copy{}
	for _, c := range rows {
		if c.Noun == noun {
			out = append(out, cloneCopy(c))
		}
	}
	return out, nil
}

// CopyKeys are the keys of noun's copies in Copies' order, for a caller
// that needs no props (no clone).
func (s *Store) CopyKeys(noun string) ([]string, error) {
	rows, err := s.copyRows()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range rows {
		if c.Noun == noun {
			out = append(out, c.Key)
		}
	}
	return out, nil
}

// DeleteCopy removes the copy of noun's thing key; none is no error.
func (s *Store) DeleteCopy(noun, key string) error {
	return s.db.Tx([]string{copiesTable}, func(tx *store.Tx) error {
		return tx.Delete(copiesTable, copyID(noun, key))
	})
}
