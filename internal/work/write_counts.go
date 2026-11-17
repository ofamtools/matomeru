package work

import (
	"strconv"
	"strings"
)

// The write counts of the tables a store's reads merge in, as one token: a
// store whose things come from elsewhere (the pane and session stores list
// the daemon's listing) joins it to its own change token, so a write to one
// of these tables (in any process) moves it and nothing else does
// (store.WriteCount: one indexed read per table, no row read).

// paneRecordTables are what a pane read merges into the listing: its own
// owner, its session's, the star, and the tasks an owner reaches (its
// project).
var paneRecordTables = []string{"assignments", "session_owners", "pane_stars", "tasks"}

// sessionRecordTables are what a session read merges in: its owner and the
// tasks an owner reaches.
var sessionRecordTables = []string{"session_owners", "tasks"}

// PaneRecordsVersion is a token for the records a pane read merges in.
func (s *Store) PaneRecordsVersion() (string, error) { return s.writeCounts(paneRecordTables) }

// SessionRecordsVersion is a token for the records a session read merges
// in.
func (s *Store) SessionRecordsVersion() (string, error) { return s.writeCounts(sessionRecordTables) }

// projectRecordTables are what a project read answers from: the projects
// and their directory trees (relations).
var projectRecordTables = []string{"projects", "relations"}

// taskRecordTables are what a task read answers from: the tasks, the
// projects (whether a task may leave mtm: Exports) and the focus rows (its
// place in today's focus).
var taskRecordTables = []string{"tasks", "projects", focusTable}

// ProjectRecordsVersion is a token for what a project read answers from.
func (s *Store) ProjectRecordsVersion() (string, error) { return s.writeCounts(projectRecordTables) }

// TaskRecordsVersion is a token for what a task read answers from.
func (s *Store) TaskRecordsVersion() (string, error) { return s.writeCounts(taskRecordTables) }

// CopiesWriteCount is a token for the copies: it moves on each write of a
// copy, in any process, and on nothing else (CopiesVersion moves on any
// other process's write to the file too).
func (s *Store) CopiesWriteCount() (string, error) { return s.writeCounts([]string{copiesTable}) }

func (s *Store) writeCounts(tables []string) (string, error) {
	parts := make([]string, len(tables))
	for i, t := range tables {
		n, err := s.db.WriteCount(t)
		if err != nil {
			return "", err
		}
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, "."), nil
}
