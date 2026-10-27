package work

import (
	"errors"

	"github.com/unixsuperhero/matomeru/store"
)

// PaneStar marks a pane the person starred. It is keyed by the TUIOS
// window id, the pane's identity everywhere else in the store
// (assignments, pane_recipes, companions): a daemon restart restores a
// session with its window ids, so the star survives it, while a name or a
// session can change under a live pane. A saved record that relaunches
// its pane into a new window carries the star along (MovePaneStar).
//
// Nothing drops a star but unstarring it. One database serves every mtm,
// on any TUIOS server (plain mtm runs on mtm-daemon, mtm --server default
// on the person's own daemon), so no one listing can say a window is gone:
// pruning on a listing once dropped every star when an mtm on another
// server started. Window ids are never reused, so a star whose window is
// gone marks no row; it is a stale row, never a wrong star.
type PaneStar struct {
	Window  string `json:"window"`
	Session string `json:"session,omitempty"` // what the pane was when starred, for reading the table
	Name    string `json:"name,omitempty"`
	Created int64  `json:"created"`
}

// PaneStars are the starred panes by window id.
func (s *Store) PaneStars() (map[string]PaneStar, error) {
	rows, err := all[PaneStar](s.db, "pane_stars")
	if err != nil {
		return nil, err
	}
	out := make(map[string]PaneStar, len(rows))
	for _, r := range rows {
		out[r.Window] = r
	}
	return out, nil
}

// SetPaneStar stars (or unstars) a pane.
func (s *Store) SetPaneStar(star PaneStar, starred bool) error {
	if star.Window == "" {
		return errors.New("a star requires a pane id")
	}
	return s.db.Tx([]string{"pane_stars"}, func(tx *store.Tx) error {
		if !starred {
			if err := tx.Delete("pane_stars", star.Window); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			return nil
		}
		if star.Created == 0 {
			star.Created = Now()
		}
		return tx.Put("pane_stars", star.Window, star)
	})
}

// MovePaneStar moves a star from one window to another (a saved record
// relaunched into a new window); a window with no star moves nothing.
func (s *Store) MovePaneStar(from, to string) error {
	if from == "" || to == "" || from == to {
		return nil
	}
	return s.db.Tx([]string{"pane_stars"}, func(tx *store.Tx) error {
		var star PaneStar
		if err := tx.Get("pane_stars", from, &star); errors.Is(err, store.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		if err := tx.Delete("pane_stars", from); err != nil {
			return err
		}
		star.Window = to
		return tx.Put("pane_stars", to, star)
	})
}
