package work

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/store"
)

// The session's key is its id (spine S5 rule 1, run plan 5.2): a stored
// reference to a session holds the id, never the name, which is the
// session's label. Older rows stored the name where the id belongs: the
// session field of assignments, the key of session_owners rows written
// before the daemon gave ids, a saved pane's missing session id, and the
// session prop of a pane's copy. sessionIDSteps rewrite them once, on open
// (S13: a migration row per table, a version step each), after a backup of
// the file; PlanSessionIDs is the dry run that says what they would change.
//
// A name is read as an id through the session records the file holds (the
// session copies, the session owners, the saved panes): a name that one
// session held becomes its id, and a window whose saved pane recorded its
// session's id takes that id; a name several sessions held (a session
// ended and another took its name) is kept, as is a name no record knows:
// which session it meant is not in the file. Kept values stay readable as before. Vim sessions and pane recipes
// keep their session name: it is where they open, not a reference (a name
// opens a session that has ended, an id cannot); a star's session is a
// label (stars.go).

// SessionIDChange is one value the migration rewrites or keeps.
type SessionIDChange struct {
	Table string `json:"table"`
	Row   string `json:"row"`
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to,omitempty"`
	// Kept says why a value stays: "ambiguous" (several sessions held the
	// name), "unknown" (no record knows it). Empty for a rewrite.
	Kept string `json:"kept,omitempty"`
}

// SessionIDPlan is what the migration changes (Changes) and keeps (Kept).
type SessionIDPlan struct {
	Changes []SessionIDChange `json:"changes"`
	Kept    []SessionIDChange `json:"kept"`
	// Done lists the tables already migrated: nothing of theirs changes.
	Done []string `json:"done,omitempty"`
}

// Counts are the plan's totals per table: rewritten and kept.
func (p SessionIDPlan) Counts() map[string][2]int {
	out := map[string][2]int{}
	for _, c := range p.Changes {
		n := out[c.Table]
		n[0]++
		out[c.Table] = n
	}
	for _, c := range p.Kept {
		n := out[c.Table]
		n[1]++
		out[c.Table] = n
	}
	return out
}

// sessionIDStep is one table's migration row: the version it moves from,
// and the rewrite of one row (the plan's resolver; nil change: unchanged).
type sessionIDStep struct {
	table    string
	from, to int
	// noun limits a copies step to one noun's copies ("" every row).
	noun    string
	rewrite func(r sessionResolver, tx *store.Tx, row map[string]any, plan *SessionIDPlan) (bool, error)
}

var sessionIDSteps = []sessionIDStep{
	{"assignments", 1, 2, "", rewriteAssignment},
	{"session_owners", 1, 2, "", rewriteSessionOwner},
	{"saved_panes", 1, 2, "", rewriteSavedPane},
	{copiesTable, 2, 3, "pane", rewritePaneCopy},
}

// applies reports whether the step rewrites row (a row of its noun).
func (st sessionIDStep) applies(row map[string]any) bool {
	return st.noun == "" || text(row, "noun") == st.noun
}

// sessionCopyNoun is the noun whose copies name sessions by id and name.
const sessionCopyNoun = "session"

// sessionResolver reads a stored session value as an id.
type sessionResolver struct {
	ids    map[string]bool     // every known session id
	byName map[string][]string // name → the ids that held it
	window map[string]string   // window → the session id a saved pane recorded
}

// resolve is the id text names, or why it is kept.
func (r sessionResolver) resolve(text string) (id, kept string) {
	switch {
	case text == "" || r.ids[text]:
		return text, ""
	case len(r.byName[text]) == 1:
		return r.byName[text][0], ""
	case len(r.byName[text]) > 1:
		return text, "ambiguous"
	}
	return text, "unknown"
}

// note records a value's rewrite or why it stays; it reports a rewrite.
func (r sessionResolver) note(plan *SessionIDPlan, table, row, field, from, to, kept string) bool {
	c := SessionIDChange{Table: table, Row: row, Field: field, From: from, To: to, Kept: kept}
	switch {
	case kept != "":
		c.To = ""
		plan.Kept = append(plan.Kept, c)
	case to != from:
		plan.Changes = append(plan.Changes, c)
		return true
	}
	return false
}

// sessionResolverOf reads the file's session records.
func sessionResolverOf(db *store.Store) (sessionResolver, error) {
	r := sessionResolver{ids: map[string]bool{}, byName: map[string][]string{}, window: map[string]string{}}
	add := func(id, name string) {
		if id == "" {
			return
		}
		r.ids[id] = true
		if name != "" && !slices.Contains(r.byName[name], id) {
			r.byName[name] = append(r.byName[name], id)
		}
	}
	copies, err := copiesOf(db, sessionCopyNoun)
	if err != nil {
		return r, err
	}
	for _, c := range copies {
		name, _ := c.Props["name"].(string)
		add(c.Key, name)
	}
	var owners []SessionOwner
	if err := allRows(db, "session_owners", &owners); err != nil {
		return r, err
	}
	for _, o := range owners {
		add(o.ID, o.Name)
	}
	var saved []SavedPane
	if err := allRows(db, "saved_panes", &saved); err != nil {
		return r, err
	}
	for _, s := range saved {
		add(s.SessionID, s.Session)
		if s.Window != "" && s.SessionID != "" {
			r.window[s.Window] = s.SessionID
		}
	}
	return r, nil
}

// allRows decodes every row of a table the file holds (none when it does
// not hold the table yet).
func allRows[T any](db *store.Store, table string, out *[]T) error {
	if _, ok := db.TableVersion(table); !ok {
		return nil
	}
	raws, err := db.All(table)
	if err != nil {
		return err
	}
	for _, raw := range raws {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		*out = append(*out, v)
	}
	return nil
}

// copiesOf are the copies of one noun the file holds.
func copiesOf(db *store.Store, noun string) ([]Copy, error) {
	var all []Copy
	if err := allRows(db, copiesTable, &all); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(c Copy) bool { return c.Noun != noun }), nil
}

func text(row map[string]any, field string) string { s, _ := row[field].(string); return s }

func rewriteAssignment(r sessionResolver, _ *store.Tx, row map[string]any, plan *SessionIDPlan) (bool, error) {
	window, from := text(row, "window"), text(row, "session")
	if from == "" {
		return false, nil
	}
	to, kept := r.resolve(from)
	if id := r.window[window]; id != "" && !r.ids[from] {
		to, kept = id, "" // the window's own record says which session
	}
	if r.note(plan, "assignments", window, "session", from, to, kept) {
		row["session"] = to
		return true, nil
	}
	return false, nil
}

func rewriteSessionOwner(r sessionResolver, tx *store.Tx, row map[string]any, plan *SessionIDPlan) (bool, error) {
	id, name := text(row, "id"), text(row, "name")
	if id != "" {
		return false, nil // keyed by its id (SetSessionOwner)
	}
	to, kept := r.resolve(name)
	if !r.note(plan, "session_owners", name, "id", name, to, kept) || tx == nil {
		return false, nil
	}
	row["id"] = to
	var held json.RawMessage
	if err := tx.Get("session_owners", to, &held); err == nil {
		// The id has its own row (written later): it wins; the name's row
		// stays in the backup.
		return false, tx.Delete("session_owners", name)
	}
	if err := tx.Put("session_owners", to, row); err != nil {
		return false, err
	}
	return false, tx.Delete("session_owners", name)
}

func rewriteSavedPane(r sessionResolver, _ *store.Tx, row map[string]any, plan *SessionIDPlan) (bool, error) {
	name := text(row, "session")
	if text(row, "session_id") != "" || name == "" {
		return false, nil
	}
	to, kept := r.resolve(name)
	if to == name {
		to = "" // a name that is no id stays only in session
	}
	if r.note(plan, "saved_panes", text(row, "id"), "session_id", "", to, kept) {
		row["session_id"] = to
		return true, nil
	}
	return false, nil
}

func rewritePaneCopy(r sessionResolver, _ *store.Tx, row map[string]any, plan *SessionIDPlan) (bool, error) {
	props, _ := row["props"].(map[string]any)
	from := text(props, "session")
	if from == "" {
		return false, nil
	}
	to, kept := r.resolve(from)
	if id := r.window[text(row, "key")]; id != "" && !r.ids[from] {
		to, kept = id, ""
	}
	if r.note(plan, copiesTable, text(row, "id"), "props.session", from, to, kept) {
		props["session"] = to
		return true, nil
	}
	return false, nil
}

// PlanSessionIDs is the migration's dry run: what it would rewrite and
// keep, read from the file, which it does not change.
func PlanSessionIDs(db *store.Store) (SessionIDPlan, error) {
	var plan SessionIDPlan
	r, err := sessionResolverOf(db)
	if err != nil {
		return plan, err
	}
	for _, step := range sessionIDSteps {
		if v, ok := db.TableVersion(step.table); !ok || v >= step.to {
			plan.Done = append(plan.Done, step.table)
			continue
		}
		var rows []map[string]any
		if err := allRows(db, step.table, &rows); err != nil {
			return plan, err
		}
		for _, row := range rows {
			if !step.applies(row) {
				continue
			}
			if _, err := step.rewrite(r, nil, row, &plan); err != nil {
				return plan, err
			}
		}
	}
	return plan, nil
}

// migrateSessionIDs runs the steps once: a file with a table still at its
// old version is copied first (backups/mtm.db.bak-<time>-session-ids,
// store.BackupCopy), then each
// table moves a version in its own transaction (store.Migrate). A fresh
// file and a migrated one do nothing.
func migrateSessionIDs(db *store.Store) (SessionIDPlan, error) {
	var plan SessionIDPlan
	pending := false
	for _, step := range sessionIDSteps {
		if v, ok := db.TableVersion(step.table); ok && v < step.to {
			pending = true
		}
	}
	if !pending {
		return plan, nil
	}
	r, err := sessionResolverOf(db)
	if err != nil {
		return plan, err
	}
	if path := db.Path(); path != "" && path != ":memory:" {
		if _, err := db.BackupCopy("session-ids", time.Now()); err != nil {
			return plan, err
		}
	}
	for _, step := range sessionIDSteps {
		err := db.Migrate(step.table, step.from, step.to, func(tx *store.Tx, raw json.RawMessage) (any, error) {
			row := map[string]any{}
			if err := json.Unmarshal(raw, &row); err != nil {
				return nil, err
			}
			if !step.applies(row) {
				return raw, nil
			}
			changed, err := step.rewrite(r, tx, row, &plan)
			if err != nil || !changed {
				return raw, err
			}
			return row, nil
		})
		if err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// String is the plan as lines: one per table with its counts, then each
// rewrite and each kept value.
func (p SessionIDPlan) String() string {
	var b strings.Builder
	counts := p.Counts()
	var tables []string
	for t := range counts {
		tables = append(tables, t)
	}
	slices.Sort(tables)
	for _, t := range tables {
		fmt.Fprintf(&b, "%s: %d rewritten, %d kept\n", t, counts[t][0], counts[t][1])
	}
	for _, t := range p.Done {
		fmt.Fprintf(&b, "%s: already migrated\n", t)
	}
	for _, c := range p.Changes {
		fmt.Fprintf(&b, "rewrite %s %s %s: %q -> %q\n", c.Table, c.Row, c.Field, c.From, c.To)
	}
	for _, c := range p.Kept {
		fmt.Fprintf(&b, "keep %s %s %s: %q (%s)\n", c.Table, c.Row, c.Field, cmp.Or(c.From, "-"), c.Kept)
	}
	return b.String()
}
