package work

import (
	"errors"
	"fmt"
	"strings"

	"ofam.dev/matomeru/store"
)

// ExtRowOwner is the project an extension's row was created in: mtm records
// it when one of the extension's verbs answers a new row while a project is
// in context, and scopes the row by it. The extension's own data never
// holds it. A deleted project leaves the row unassociated (its id moves to
// FormerProjectID, as for every owned row).
type ExtRowOwner struct {
	ID              string `json:"id"` // ExtRowKey(Extension, Noun, Row)
	Extension       string `json:"extension"`
	Noun            string `json:"noun"` // the extension's catalog noun
	Row             string `json:"row"`  // the row's own id, as the extension answers it
	ProjectID       string `json:"project_id"`
	FormerProjectID string `json:"former_project_id,omitempty"`
	AssignedAt      int64  `json:"assigned_at,omitempty"`
}

// ExtRowKey is the id of an extension row's owner record. Extension and
// noun names hold no NUL; a row id may hold anything.
func ExtRowKey(extension, noun, row string) string {
	return strings.Join([]string{extension, noun, row}, "\x00")
}

// SetExtRowOwner records the project of an extension's row.
func (s *Store) SetExtRowOwner(o ExtRowOwner) error {
	o.ID = ExtRowKey(o.Extension, o.Noun, o.Row)
	return s.db.Tx([]string{"ext_rows"}, func(tx *store.Tx) error { return tx.Put("ext_rows", o.ID, o) })
}

// RekeyExtRowOwner moves a legacy selector's assignment to its durable row
// key in one transaction. A conflicting assignment is retained and reported.
func (s *Store) RekeyExtRowOwner(extension, noun, old, key string) error {
	if old == key {
		return nil
	}
	return s.db.Tx([]string{"ext_rows"}, func(tx *store.Tx) error {
		before := ExtRowKey(extension, noun, old)
		var owner ExtRowOwner
		if err := tx.Get("ext_rows", before, &owner); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return err
		}
		after := ExtRowKey(extension, noun, key)
		var current ExtRowOwner
		if err := tx.Get("ext_rows", after, &current); err == nil {
			if current.ProjectID != owner.ProjectID || current.FormerProjectID != owner.FormerProjectID {
				return fmt.Errorf("conflicting extension row owners for %s and %s", old, key)
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		} else {
			owner.ID, owner.Row = after, key
			if err := tx.Put("ext_rows", after, owner); err != nil {
				return err
			}
		}
		return tx.Delete("ext_rows", before)
	})
}

// ExtRowOwners is every recorded extension row owner, by ID.
func (s *Store) ExtRowOwners() (map[string]ExtRowOwner, error) {
	rows, err := all[ExtRowOwner](s.db, "ext_rows")
	if err != nil {
		return nil, err
	}
	out := make(map[string]ExtRowOwner, len(rows))
	for _, o := range rows {
		out[o.ID] = o
	}
	return out, nil
}
