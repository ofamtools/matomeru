package work

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// StatusDef is one row of the task status table (the tasks.statuses
// setting): the status's name as stored, the one character drawn inside
// the checkbox brackets, its place in the task tables (lower first;
// statuses the table does not name rank 3) and whether it closes the task
// (a closed task is not counted open and is hidden from pickers that skip
// done work).
type StatusDef struct {
	Name   string `json:"name"`
	Glyph  string `json:"glyph"`
	Rank   int    `json:"rank"`
	Closed bool   `json:"closed,omitempty"`
}

// DefaultStatusesJSON is the default status table. Stored names are kept
// (todo, active, blocked, done); waiting and cancelled are new rows.
const DefaultStatusesJSON = `[{"name":"todo","glyph":" ","rank":1},` +
	`{"name":"active","glyph":">","rank":0},` +
	`{"name":"waiting","glyph":"~","rank":2},` +
	`{"name":"blocked","glyph":"!","rank":2},` +
	`{"name":"done","glyph":"x","rank":4,"closed":true},` +
	`{"name":"cancelled","glyph":"-","rank":4,"closed":true}]`

// ParseStatuses reads a status table: unique names, one-character glyphs,
// the todo and done rows present.
func ParseStatuses(text string) ([]StatusDef, error) {
	var rows []StatusDef
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, fmt.Errorf("tasks.statuses: %w", err)
	}
	seen := map[string]bool{}
	for i, r := range rows {
		if strings.TrimSpace(r.Name) == "" || r.Name != strings.TrimSpace(r.Name) {
			return nil, fmt.Errorf("tasks.statuses: row %d needs a name", i+1)
		}
		if seen[r.Name] {
			return nil, fmt.Errorf("tasks.statuses: %q is listed twice", r.Name)
		}
		seen[r.Name] = true
		if n := utf8.RuneCountInString(r.Glyph); n > 1 {
			return nil, fmt.Errorf("tasks.statuses: %q glyph %q is %d characters: use one", r.Name, r.Glyph, n)
		}
	}
	for _, need := range []string{StatusTodo, StatusDone} {
		if !seen[need] {
			return nil, fmt.Errorf("tasks.statuses: the %q row is needed (x toggles between todo and done)", need)
		}
	}
	return rows, nil
}

// DefaultStatuses is the default table, parsed.
func DefaultStatuses() []StatusDef {
	rows, err := ParseStatuses(DefaultStatusesJSON)
	if err != nil {
		panic(err)
	}
	return rows
}

// SetStatusSource makes the store read its status table from source (the
// setting, read on each use); nil is the default table.
func (s *Store) SetStatusSource(source func() []StatusDef) { s.statuses = source }

// StatusTable is the task status table in force.
func (s *Store) StatusTable() []StatusDef {
	if s.statuses != nil {
		if rows := s.statuses(); len(rows) > 0 {
			return rows
		}
	}
	return DefaultStatuses()
}

// StatusNames are the status names a task can be set to, in table order.
func (s *Store) StatusNames() []string {
	rows := s.StatusTable()
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

// ValidStatus reports whether status is one a task can be set to.
func (s *Store) ValidStatus(status string) bool {
	return slices.Contains(s.StatusNames(), status)
}

func (s *Store) statusRow(status string) (StatusDef, bool) {
	status = NormalStatus(status)
	for _, r := range s.StatusTable() {
		if r.Name == status {
			return r, true
		}
	}
	return StatusDef{}, false
}

// StatusGlyph is a status's checkbox: its row's glyph inside brackets, [?]
// for a value the table does not name (an older mtm's).
func (s *Store) StatusGlyph(status string) string {
	r, ok := s.statusRow(status)
	if !ok {
		return "[?]"
	}
	if r.Glyph == "" {
		return "[ ]"
	}
	return "[" + r.Glyph + "]"
}

// StatusRank orders statuses for the task tables (the table's rank; a
// value it does not name ranks 3).
func (s *Store) StatusRank(status string) int {
	if r, ok := s.statusRow(status); ok {
		return r.Rank
	}
	return 3
}

// StatusClosed reports whether the status closes a task (done, cancelled).
func (s *Store) StatusClosed(status string) bool {
	r, ok := s.statusRow(status)
	return ok && r.Closed
}
