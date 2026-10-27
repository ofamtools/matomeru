package work

import (
	"strings"

	"github.com/unixsuperhero/matomeru/store"
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
