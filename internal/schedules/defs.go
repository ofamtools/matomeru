package schedules

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/store"
)

// Stored rows: the schedules a person adds with mtm schedule add (and
// edits, duplicates and deletes) live in the schedule_defs table, one row
// per schedule keyed by its name. They merge over the built-ins and
// schedules.json by name (Layer), so editing a built-in or file row
// stores a full copy that wins; deleting that copy brings the original
// back.
var DefsTable = store.Table{Name: "schedule_defs", Owner: "schedules", Key: "name", Version: 1}

// ErrNotStored is a delete of a row that is not stored (built in, or in
// schedules.json).
var ErrNotStored = errors.New("not a stored schedule")

// Stored are the stored rows by name order.
func (r *Runner) Stored() ([]Def, error) {
	var out []Def
	err := r.db.View([]string{DefsTable.Name}, func(tx *store.Tx) error {
		rows, err := tx.All(DefsTable.Name)
		if err != nil {
			return err
		}
		for _, raw := range rows {
			var d Def
			if json.Unmarshal(raw, &d) == nil && ValidName(d.Name) {
				d.Source = SourceStored
				out = append(out, d)
			}
		}
		return nil
	})
	slices.SortFunc(out, func(a, b Def) int { return strings.Compare(a.Name, b.Name) })
	return out, err
}

// Save stores d (a new row or a full replacement), stamped at now.
func (r *Runner) Save(d Def) error {
	if !ValidName(d.Name) {
		return fmt.Errorf("name %q: use letters, digits and dashes", d.Name)
	}
	if err := d.Check(); err != nil {
		return fmt.Errorf("schedule %s: %w", d.Name, err)
	}
	now := r.now()
	if d.Created == 0 {
		d.Created = now
	}
	d.Updated = now
	d.Source = ""
	return r.db.Tx([]string{DefsTable.Name}, func(tx *store.Tx) error { return tx.Put(DefsTable.Name, d.Name, d) })
}

// Delete removes the stored row name (its state and history stay, under
// the name, until a row of that name runs again).
func (r *Runner) Delete(name string) error {
	return r.db.Tx([]string{DefsTable.Name}, func(tx *store.Tx) error {
		var d Def
		if err := tx.Get(DefsTable.Name, name, &d); errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%s: %w", name, ErrNotStored)
		} else if err != nil {
			return err
		}
		return tx.Delete(DefsTable.Name, name)
	})
}

// Layer is base with the stored rows merged over it by name: a stored row
// replaces the base row of its name, any other is added.
func Layer(base, stored []Def) []Def {
	out := slices.Clone(base)
	for _, d := range stored {
		if i := slices.IndexFunc(out, func(b Def) bool { return b.Name == d.Name }); i >= 0 {
			out[i] = d
		} else {
			out = append(out, d)
		}
	}
	return out
}

// legacySchedule is a matomeru-old schedule row (its Schedule type), as it
// may sit in the schedules table of a database matomeru-old wrote.
type legacySchedule struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Interval int64  `json:"interval"` // seconds
	Timeout  int64  `json:"timeout"`  // seconds
	Created  int64  `json:"created"`
	Window   string `json:"window"`
	Run      *struct {
		Kind   string         `json:"kind"`
		Text   string         `json:"text"`
		Target map[string]any `json:"target"`
	} `json:"run"`
}

// ImportLegacy is the migration from matomeru-old: every prompt schedule
// it left in the schedules table becomes a stored prompt row of the same
// name (when that name is free), off, so nothing starts agents until the
// owner turns it on. Its pane target becomes target pane. It answers the
// names it imported. Lookup and command schedules had no equivalent and
// stay as they were. Call it once, when DefsTable is first declared.
func (r *Runner) ImportLegacy() ([]string, error) {
	var imported []string
	err := r.db.Tx([]string{Table.Name, DefsTable.Name}, func(tx *store.Tx) error {
		rows, err := tx.All(Table.Name)
		if err != nil {
			return err
		}
		for _, raw := range rows {
			var old legacySchedule
			if json.Unmarshal(raw, &old) != nil || old.Run == nil || old.Run.Kind != KindPrompt || !ValidName(old.ID) {
				continue
			}
			var existing Def
			if err := tx.Get(DefsTable.Name, old.ID, &existing); err == nil {
				continue
			}
			d := Def{Name: old.ID, Description: strings.TrimSpace(old.Title + " (imported from matomeru-old)"), Kind: KindPrompt,
				Prompt: old.Run.Text, Enabled: Off, Created: old.Created, Updated: r.now()}
			if old.Interval >= 60 {
				d.Interval = Duration(old.Interval) * Duration(1e9)
			}
			if old.Timeout > 0 {
				d.Timeout = Duration(old.Timeout) * Duration(1e9)
			}
			window := old.Window
			if w, ok := old.Run.Target["window"].(string); ok && w != "" {
				window = w
			}
			if window != "" {
				d.Agent.Target, d.Agent.Pane = TargetPane, window
			}
			if h, ok := old.Run.Target["agent"].(string); ok {
				d.Agent.Harness = h
			}
			if err := tx.Put(DefsTable.Name, d.Name, d); err != nil {
				return err
			}
			imported = append(imported, d.Name)
		}
		return nil
	})
	return imported, err
}
