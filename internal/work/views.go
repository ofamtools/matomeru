package work

import (
	"errors"

	"ofam.dev/matomeru/store"
)

// PaneView is a pane's own view in mtm's pane detail (pane.view): which
// tab or layout it opens on (turns, terminal or both) and, for both, the
// percent of the width the turns take. It beats the pane's harness
// profile (harnesses.json "view") and the agent.view settings. It is
// keyed by the TUIOS window id, as a star is (PaneStar says why), and
// nothing but pane.view drops it.
type PaneView struct {
	Window  string `json:"window"`
	View    string `json:"view,omitempty"`
	Split   int    `json:"split,omitempty"`
	Updated int64  `json:"updated"`
}

// PaneViews are the panes' own views by window id.
func (s *Store) PaneViews() (map[string]PaneView, error) {
	rows, err := all[PaneView](s.db, "pane_views")
	if err != nil {
		return nil, err
	}
	out := make(map[string]PaneView, len(rows))
	for _, r := range rows {
		out[r.Window] = r
	}
	return out, nil
}

// SetPaneView records a pane's own view; one with neither a view nor a
// split removes it (the pane follows its harness and the settings again).
func (s *Store) SetPaneView(v PaneView) error {
	if v.Window == "" {
		return errors.New("a pane view requires a pane id")
	}
	return s.db.Tx([]string{"pane_views"}, func(tx *store.Tx) error {
		if v.View == "" && v.Split == 0 {
			if err := tx.Delete("pane_views", v.Window); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			return nil
		}
		v.Updated = Now()
		return tx.Put("pane_views", v.Window, v)
	})
}
