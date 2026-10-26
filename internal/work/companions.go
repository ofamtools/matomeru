package work

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/unixsuperhero/matomeru/internal/store"
)

// Companion links a side vim or terminal window to its parent pane.
type Companion struct {
	Window string `json:"window"`
	Parent string `json:"parent"`
	Kind   string `json:"kind"`
}

func (s *Store) Companions() ([]Companion, error) {
	return all[Companion](s.db, "companions")
}

// PutCompanion permits at most one window of each kind per parent.
func (s *Store) PutCompanion(row Companion) error {
	if row.Window == "" || row.Parent == "" || row.Window == row.Parent {
		return errors.New("companion requires distinct nonempty window and parent ids")
	}
	if row.Kind != "vim" && row.Kind != "term" {
		return errors.New("companion kind must be vim or term")
	}
	return s.db.Tx([]string{"companions"}, func(tx *store.Tx) error {
		raws, err := tx.All("companions")
		if err != nil {
			return err
		}
		for _, raw := range raws {
			var existing Companion
			if err := json.Unmarshal(raw, &existing); err != nil {
				return err
			}
			if existing.Window != row.Window && existing.Parent == row.Parent && existing.Kind == row.Kind {
				return fmt.Errorf("pane %s already has a side %s", row.Parent, row.Kind)
			}
		}
		return tx.Put("companions", row.Window, row)
	})
}

func (s *Store) DeleteCompanion(window string) error {
	return s.db.Tx([]string{"companions"}, func(tx *store.Tx) error { return tx.Delete("companions", window) })
}

// ReconcileCompanions removes dead links without changing pane ownership.
// A surviving companion whose parent exited becomes an ordinary pane.
func (s *Store) ReconcileCompanions(live map[string]bool) ([]Companion, error) {
	out := make([]Companion, 0)
	err := s.db.Tx([]string{"companions"}, func(tx *store.Tx) error {
		raws, err := tx.All("companions")
		if err != nil {
			return err
		}
		for _, raw := range raws {
			var row Companion
			if err := json.Unmarshal(raw, &row); err != nil {
				return err
			}
			if !live[row.Window] || !live[row.Parent] {
				if err := tx.Delete("companions", row.Window); err != nil {
					return err
				}
				continue
			}
			out = append(out, row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
