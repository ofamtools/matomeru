package work

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/unixsuperhero/matomeru/store"
)

// Companion links a named side window to its parent pane.
// Server is the TUIOS daemon socket the windows are on: one database
// serves every mtm, on any server, so a listing reconciles only its own
// server's links (ReconcileCompanions). "" is a link written before the
// server was recorded: unknown.
type Companion struct {
	Window string `json:"window"`
	Parent string `json:"parent"`
	Kind   string `json:"kind"`
	Server string `json:"server,omitempty"`
}

func (s *Store) Companions() ([]Companion, error) {
	return all[Companion](s.db, "companions")
}

// PutCompanion permits at most one window of each kind per parent.
func (s *Store) PutCompanion(row Companion) error {
	if row.Window == "" || row.Parent == "" || row.Window == row.Parent {
		return errors.New("companion requires distinct nonempty window and parent ids")
	}
	if row.Kind == "" {
		return errors.New("companion kind must be nonempty")
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

// ReconcileCompanions removes server's dead links without changing pane
// ownership, given a complete listing of server (live: window id to not
// exited), and returns server's surviving links. A surviving companion
// whose parent exited becomes an ordinary pane. Another server's links are
// kept and not returned. A link of unknown server is server's when the
// listing has either of its windows (window ids are unique across
// servers): it is recorded as server's and reconciled; else it is kept.
func (s *Store) ReconcileCompanions(server string, live map[string]bool) ([]Companion, error) {
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
			if row.Server != server {
				_, window := live[row.Window]
				_, parent := live[row.Parent]
				if row.Server != "" || !window && !parent {
					continue
				}
				row.Server = server
				if err := tx.Put("companions", row.Window, row); err != nil {
					return err
				}
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
