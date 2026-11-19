package work

import (
	"encoding/json"
	"strings"
	"time"

	"ofam.dev/matomeru/store"
)

// Priority is how much a task or a project matters, on the owner's scale:
// P0 (work on it now) to P5 (not so important), and not set, the default,
// which ranks below P5 (a P6 of sorts) and shows blank. Not set stores
// nothing and reads "": a record written before priority existed, or one
// set back, is as it was.
const PriorityNone = ""

// PriorityLevel is one value of the scale: Value is what is stored and
// read, Label what a pick list shows, and Aliases the other words a write
// may give for it (case aside), among them the values of the scale before
// P0 to P5 (none, low, medium, high, urgent), which the tasks and projects
// migration rewrites the same way.
type PriorityLevel struct {
	Value   string
	Label   string
	Aliases []string
}

// PriorityLevels is the scale, most important first: the prop's enum, so a
// sort by priority ranks P0 first. Not set is last; as a blank value it
// sorts after every level in either direction. The one list every reader
// and writer of a priority goes through.
var PriorityLevels = []PriorityLevel{
	{Value: "P0", Label: "P0: work on it now", Aliases: []string{"urgent"}},
	{Value: "P1", Label: "P1: very important", Aliases: []string{"high"}},
	{Value: "P2", Label: "P2"},
	{Value: "P3", Label: "P3", Aliases: []string{"medium"}},
	{Value: "P4", Label: "P4"},
	{Value: "P5", Label: "P5: not so important", Aliases: []string{"low"}},
	{Value: PriorityNone, Label: "not set", Aliases: []string{"none", "normal", "not set", "unset", "-"}},
}

// Priorities are the levels' values in scale order (the prop's enum).
var Priorities = priorityValues()

func priorityValues() []string {
	out := make([]string, len(PriorityLevels))
	for i, l := range PriorityLevels {
		out[i] = l.Value
	}
	return out
}

// PriorityLabels are the pick-list labels of the levels by value.
func PriorityLabels() map[string]string {
	out := make(map[string]string, len(PriorityLevels))
	for _, l := range PriorityLevels {
		out[l.Value] = l.Label
	}
	return out
}

// PriorityNames is the scale as a write may name it, for messages: each
// level's value, not set as its first alias.
func PriorityNames() []string {
	out := make([]string, len(PriorityLevels))
	for i, l := range PriorityLevels {
		out[i] = l.Value
		if l.Value == "" && len(l.Aliases) > 0 {
			out[i] = l.Aliases[0]
		}
	}
	return out
}

// PriorityOf reads a written priority: the level's value for v, its value
// or one of its aliases, case and spaces aside, and whether v names one.
func PriorityOf(v string) (string, bool) {
	v = strings.TrimSpace(v)
	for _, l := range PriorityLevels {
		if strings.EqualFold(v, l.Value) || containsFold(l.Aliases, v) {
			return l.Value, true
		}
	}
	return v, false
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// priorityRewrite is the migration of one tasks or projects row to the P0
// to P5 scale: a stored priority that names a level by an alias (an old
// value: urgent P0, high P1, medium P3, low P5, none not set) is rewritten
// to the level's value; any other value is kept as it is.
func priorityRewrite(_ *store.Tx, raw json.RawMessage) (any, error) {
	row := map[string]any{}
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}
	old, ok := row["priority"].(string)
	if !ok {
		return raw, nil
	}
	v, known := PriorityOf(old)
	switch {
	case !known || v == old:
		return raw, nil
	case v == PriorityNone:
		delete(row, "priority")
	default:
		row["priority"] = v
	}
	return row, nil
}

// prioritySteps are the version steps that move tasks and projects to the
// P0 to P5 scale.
var prioritySteps = []struct {
	table    string
	from, to int
}{
	{"tasks", 1, 2},
	{"projects", 2, 3},
}

// migratePriorities moves the stored priorities to the P0 to P5 scale once,
// after a backup of the file (backups/mtm.db.bak-<time>-priority-levels).
func migratePriorities(db *store.Store) error {
	pending := false
	for _, st := range prioritySteps {
		if v, ok := db.TableVersion(st.table); ok && v < st.to {
			pending = true
		}
	}
	if !pending {
		return nil
	}
	if path := db.Path(); path != "" && path != ":memory:" {
		if _, err := db.BackupCopy("priority-levels", time.Now()); err != nil {
			return err
		}
	}
	for _, st := range prioritySteps {
		if err := db.Migrate(st.table, st.from, st.to, priorityRewrite); err != nil {
			return err
		}
	}
	return nil
}
