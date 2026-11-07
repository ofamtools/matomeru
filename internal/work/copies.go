package work

import (
	"errors"
	"strings"

	"ofam.dev/matomeru/store"
)

// Copy is mtm's copy of a thing another source holds (a pane or a session
// the daemon lists): ochazuke's copy stores (CopyStore) keep their copies
// here, one row per noun and key. The first read of a thing copies it in
// (the backfill, BackfilledAt); after that the copy is the thing, and it
// outlives the source's (a pane that ended stays). Props is the thing as
// the copy store put it, a tombstone included (ochazuke.CopyTombstone).
type Copy struct {
	ID           string         `json:"id"` // noun|key
	Noun         string         `json:"noun"`
	Key          string         `json:"key"`
	Props        map[string]any `json:"props"`
	BackfilledAt int64          `json:"backfilled_at"`
	Updated      int64          `json:"updated"`
}

// copiesTable is the copies' table (Tables).
const copiesTable = "copies"

// copyID is a copy's row id: a noun is one word, so the first | parts it
// from the key whatever the key holds.
func copyID(noun, key string) string { return noun + "|" + key }

// Copy is the copy of noun's thing key; ok is false when there is none.
func (s *Store) Copy(noun, key string) (Copy, bool, error) {
	var c Copy
	err := s.db.Get(copiesTable, copyID(noun, key), &c)
	if errors.Is(err, store.ErrNotFound) {
		return Copy{}, false, nil
	}
	if err != nil {
		return Copy{}, false, err
	}
	return c, true, nil
}

// PutCopy writes the copy of noun's thing key: the first write stamps
// BackfilledAt, a later one keeps it (and the row's place in Copies).
func (s *Store) PutCopy(noun, key string, props map[string]any) error {
	if noun == "" || strings.Contains(noun, "|") || key == "" {
		return errors.New("a copy requires a noun (one word) and a key")
	}
	id := copyID(noun, key)
	return s.db.Tx([]string{copiesTable}, func(tx *store.Tx) error {
		now := Now()
		c := Copy{ID: id, Noun: noun, Key: key, Props: props, BackfilledAt: now, Updated: now}
		var old Copy
		switch err := tx.Get(copiesTable, id, &old); {
		case err == nil:
			c.BackfilledAt = old.BackfilledAt
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
		return tx.Put(copiesTable, id, c)
	})
}

// Copies are noun's copies in the order they were first written.
func (s *Store) Copies(noun string) ([]Copy, error) {
	rows, err := all[Copy](s.db, copiesTable)
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, c := range rows {
		if c.Noun == noun {
			out = append(out, c)
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
