package work

import (
	"encoding/json"
	"errors"

	"ofam.dev/matomeru/store"
)

// rowCache is one table's rows by id as last read, and the table's
// version (store.Version) they were read at: a getter that a list asks
// once per thing (a pane's owner, a session's) answers from it while the
// version is unchanged, so a list of N things reads the table once per
// change, not N rows. Rows are kept as stored JSON and decoded per call,
// so no caller holds the cached values.
type rowCache struct {
	ver  string
	rows map[string]json.RawMessage
}

// cachedHas reports whether table holds a row id, from its rowCache.
func (s *Store) cachedHas(table, id string) (bool, error) {
	var row json.RawMessage
	err := s.cachedGet(table, id, &row)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// cachedGet is store.Get of table's row id into dst, answered from the
// table's rowCache (read whole when its version moved); store.ErrNotFound
// when the table holds no such row.
func (s *Store) cachedGet(table, id string, dst any) error {
	ver, err := s.db.Version(table)
	if err != nil {
		return err
	}
	s.rowsMu.Lock()
	defer s.rowsMu.Unlock()
	c := s.rows[table]
	if c == nil || c.ver != ver {
		entries, err := s.db.Entries(table)
		if err != nil {
			return err
		}
		c = &rowCache{ver: ver, rows: make(map[string]json.RawMessage, len(entries))}
		for _, e := range entries {
			c.rows[e.ID] = e.Row
		}
		if s.rows == nil {
			s.rows = map[string]*rowCache{}
		}
		s.rows[table] = c
	}
	raw, ok := c.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	return json.Unmarshal(raw, dst)
}

// HoldReads makes the store's version checks read the database once
// until release is called (store.HoldVersions): one read of many things
// answers its per-thing records from the row caches with no database read
// each. This process's writes are seen at once.
func (s *Store) HoldReads() (release func()) { return s.db.HoldVersions() }
