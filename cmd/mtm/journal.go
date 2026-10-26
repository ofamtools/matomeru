package main

import (
	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// The history journal (internal/history): what the agents in the panes
// did, journaled as it happens, and the turns projected from it.

// journalOwner is the owner the journal gives an entry: the pane's own
// assignment, else its session's owner (looked up by name, since a
// capture hook knows only the session's name). It reads the store only,
// so it is safe off the loop.
func journalOwner(w *work.Store) history.Owner {
	return func(session, window string) (string, string, error) {
		s, err := w.SessionOwnerNamed(session)
		if err != nil {
			return "", "", err
		}
		o, err := w.PaneOwner(window, s.ID)
		return o.TaskID, o.ProjectID, err
	}
}
