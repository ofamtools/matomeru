package work

import (
	"errors"
	"slices"

	"github.com/unixsuperhero/matomeru/store"
)

// PaneSessions are the agent sessions a pane has run, oldest first, keyed
// by the TUIOS window id: tuios reports only the current one
// (list-agents' agent_session_id), so a /clear, which starts a new
// session, would otherwise lose the turns before it. The Turns tab chains
// them.
type PaneSessions struct {
	Window   string   `json:"window"`
	Harness  string   `json:"harness,omitempty"`
	Sessions []string `json:"sessions"`
	Updated  int64    `json:"updated"`
}

// PaneSessions is the pane's sessions; none recorded is an empty record.
func (s *Store) PaneSessions(window string) (PaneSessions, error) {
	var row PaneSessions
	err := s.db.Get("pane_sessions", window, &row)
	if errors.Is(err, store.ErrNotFound) {
		return PaneSessions{Window: window}, nil
	}
	return row, err
}

// AddPaneSession records that the pane runs session now, after the ones it
// ran before; a session already recorded is left where it is. It reports
// whether the record changed.
func (s *Store) AddPaneSession(window, harness, session string) (bool, error) {
	if window == "" || session == "" {
		return false, errors.New("a pane session requires a pane id and a session id")
	}
	changed := false
	err := s.db.Tx([]string{"pane_sessions"}, func(tx *store.Tx) error {
		row := PaneSessions{Window: window}
		if err := tx.Get("pane_sessions", window, &row); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if slices.Contains(row.Sessions, session) {
			return nil
		}
		row.Harness, row.Sessions, row.Updated = harness, append(row.Sessions, session), Now()
		changed = true
		return tx.Put("pane_sessions", window, row)
	})
	return changed, err
}

// AllPaneSessions is every pane's sessions record, closed panes' too.
func (s *Store) AllPaneSessions() ([]PaneSessions, error) {
	return all[PaneSessions](s.db, "pane_sessions")
}
