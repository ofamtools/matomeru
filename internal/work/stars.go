package work

import (
	"encoding/json"
	"errors"

	"github.com/unixsuperhero/matomeru/store"
)

// PaneStar marks a pane the person starred. It is keyed by the TUIOS
// window id, the pane's identity everywhere else in the store
// (assignments, pane_recipes, companions): a daemon restart restores a
// session with its window ids, so the star survives it, while a name or a
// session can change under a live pane. A saved record that relaunches
// its pane into a new window carries the star along (MovePaneStar), and a
// star whose window is gone keeps only while a saved record still names
// that window (PrunePaneStars).
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

// PrunePaneStars drops the stars of windows not live and named by no
// saved record, and returns the windows it dropped.
func (s *Store) PrunePaneStars(live map[string]bool) ([]string, error) {
	saved, err := s.SavedPanes()
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, row := range saved {
		keep[row.Window] = true
	}
	var dropped []string
	err = s.db.Tx([]string{"pane_stars"}, func(tx *store.Tx) error {
		raws, err := tx.All("pane_stars")
		if err != nil {
			return err
		}
		for _, raw := range raws {
			var star PaneStar
			if err := json.Unmarshal(raw, &star); err != nil {
				return err
			}
			if live[star.Window] || keep[star.Window] {
				continue
			}
			if err := tx.Delete("pane_stars", star.Window); err != nil {
				return err
			}
			dropped = append(dropped, star.Window)
		}
		return nil
	})
	return dropped, err
}
