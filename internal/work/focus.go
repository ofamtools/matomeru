package work

import (
	"cmp"
	"errors"
	"slices"

	"ofam.dev/matomeru/store"
)

// The day's focus (daily-focus plan, lane focus-2-mtm): a focus is a dated
// mark on a thing, a row of focus_items naming the thing (noun:key), the
// local day it was picked for and its place in that day's list. Today's
// list is computed on read (Today): nothing is written at midnight and no
// timer runs for the day change. Unfocusing archives the row (record
// first, never delete; undo restores it).

// focusTable is the focus marks' table (Tables).
const focusTable = "focus_items"

// Focus carry values (setting focus.carry): what an earlier day's
// unfinished focus does on a later day.
const (
	FocusCarryUnfinished = "unfinished"
	FocusCarryNone       = "none"
)

// FocusCarries are the focus.carry values, the default first.
var FocusCarries = []string{FocusCarryUnfinished, FocusCarryNone}

// FocusItem is one row of focus_items.
type FocusItem struct {
	ID       string `json:"id"`
	Thing    string `json:"thing"` // noun:key of the focused thing
	Day      string `json:"day"`   // local YYYY-MM-DD it was focused for
	Position int    `json:"position"`
	Archived bool   `json:"archived,omitempty"`
	Created  int64  `json:"created"`
	Updated  int64  `json:"updated"`
	// CreatedBy is stamped once, ChangedBy on every write (actor.go).
	CreatedBy Actor `json:"created_by,omitzero"`
	ChangedBy Actor `json:"changed_by,omitzero"`
}

// FocusItems are every focus row, archived ones included, in no order.
func (s *Store) FocusItems() ([]FocusItem, error) {
	return all[FocusItem](s.db, focusTable)
}

// FocusItem is one focus row by id.
func (s *Store) FocusItem(id string) (FocusItem, error) {
	var it FocusItem
	err := s.db.Get(focusTable, id, &it)
	return it, err
}

// PutFocusItems writes rows in one transaction, stamping a new row's id,
// creation time and maker, and every row's change time and writer.
func (s *Store) PutFocusItems(items ...FocusItem) ([]FocusItem, error) {
	now, me := Now(), s.Actor()
	out := slices.Clone(items)
	err := s.db.Tx([]string{focusTable}, func(tx *store.Tx) error {
		for i := range out {
			it := &out[i]
			if it.Thing == "" || it.Day == "" {
				return errors.New("a focus needs a thing and a day")
			}
			if it.ID == "" {
				it.ID = NewID()
			}
			if it.Created == 0 {
				it.Created = now
			}
			if it.CreatedBy.Kind == "" {
				it.CreatedBy = me
			}
			it.Updated, it.ChangedBy = now, me
			if err := tx.Put(focusTable, it.ID, *it); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// DeleteFocusItem removes a row (the undo of a create; unfocusing
// archives).
func (s *Store) DeleteFocusItem(id string) error {
	return s.db.Tx([]string{focusTable}, func(tx *store.Tx) error { return tx.Delete(focusTable, id) })
}

// FocusState is what the focused thing is now: Finished (closed or
// archived) and, when finished, the local day it last changed (so a thing
// finished today stays in today's list, flagged).
type FocusState struct {
	Finished bool
	Day      string
}

// FocusEntry is one place in a day's focus list.
type FocusEntry struct {
	FocusItem
	// Order is the place in the day's list, from 1.
	Order int `json:"order"`
	// Carried is a row of an earlier day shown by focus.carry.
	Carried bool `json:"carried,omitempty"`
	// Finished is a thing finished now; the list keeps it (flagged) for
	// the day it was finished on.
	Finished bool `json:"finished,omitempty"`
}

// FocusDay is day's focus list over rows: every live row of day by
// position; with carry, then every live row of an earlier day whose thing
// is not finished (or was finished on day), oldest day first, by position
// within a day. A thing is listed once: its row of day wins, else its
// newest earlier row. A thing finished stays listed, flagged Finished.
// state reads a thing's state; a thing it cannot read is unfinished.
func FocusDay(rows []FocusItem, day string, carry bool, state func(thing string) FocusState) []FocusEntry {
	byPlace := func(x, y FocusItem) int {
		return cmp.Or(cmp.Compare(x.Day, y.Day), cmp.Compare(x.Position, y.Position), cmp.Compare(x.Created, y.Created), cmp.Compare(x.ID, y.ID))
	}
	today := map[string]bool{}
	newest := map[string]FocusItem{}
	var own []FocusItem
	for _, r := range rows {
		switch {
		case r.Archived || r.Day > day:
		case r.Day == day:
			today[r.Thing] = true
			own = append(own, r)
		case carry:
			if old, ok := newest[r.Thing]; !ok || byPlace(old, r) < 0 {
				newest[r.Thing] = r
			}
		}
	}
	slices.SortFunc(own, byPlace)
	// A thing focused twice on one day keeps its first place.
	seen := map[string]bool{}
	own = slices.DeleteFunc(own, func(r FocusItem) bool {
		dup := seen[r.Thing]
		seen[r.Thing] = true
		return dup
	})
	var carried []FocusItem
	for thing, r := range newest {
		if !today[thing] {
			carried = append(carried, r)
		}
	}
	slices.SortFunc(carried, byPlace)
	out := make([]FocusEntry, 0, len(own)+len(carried))
	for _, r := range own {
		out = append(out, FocusEntry{FocusItem: r, Finished: state(r.Thing).Finished})
	}
	for _, r := range carried {
		st := state(r.Thing)
		if st.Finished && st.Day != day {
			continue // finished on an earlier day: it drops off
		}
		out = append(out, FocusEntry{FocusItem: r, Carried: true, Finished: st.Finished})
	}
	for i := range out {
		out[i].Order = i + 1
	}
	return out
}
