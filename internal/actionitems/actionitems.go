// Package actionitems owns the inbox: action items, everything that needs
// the user, from any source (a GitHub review request, a Slack mention, a
// blocked agent), in one table. Outside tools push items in through the
// CLI (mtm action-item add); the TUI lists, opens, snoozes, archives and
// promotes them. It knows nothing of the TUI or the daemon.
//
// The table and the row shape are matomeru-old's (internal/actions), so the
// owner's database, which already declares action_items, opens unchanged:
// service is the source, source_url the URL, seen_at the read mark and
// dismissed_at the archive mark. Key, why, window, session and
// snoozed_until are new and omitted when unset.
package actionitems

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
)

// Table is matomeru-old's declaration, unchanged: a different one stops
// mtm from starting on the owner's database.
var Table = store.Table{Name: "action_items", Owner: "actions", Key: "id", Version: 1}

// States an item can be in. An item is open until it is archived; a
// snoozed item is open again once its time passes.
const (
	StateOpen     = "open"
	StateSnoozed  = "snoozed"
	StateArchived = "archived"
)

// States are the states in the order the inbox shows them.
var States = []string{StateOpen, StateSnoozed, StateArchived}

// ManualSource is the source of an item added with none named.
const ManualSource = "manual"

// Item is one row. The JSON names up to Extra are matomeru-old's.
type Item struct {
	ID string `json:"id"`
	// Source is the tool or service the item came from (gh, slack, ci,
	// agent, manual); matomeru-old called it service.
	Source string `json:"service"`
	URL    string `json:"source_url"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	// Kind is the item's type (review-request, mention, ci-failed, …);
	// matomeru-old's action_type.
	Kind     string `json:"action_type"`
	Category string `json:"category,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	// ProjectID is the item's project; with a task it is the task's.
	ProjectID      string         `json:"project_id,omitempty"`
	ScheduleID     string         `json:"schedule_id,omitempty"`
	Created        int64          `json:"created"`
	Updated        int64          `json:"updated"`
	ReadAt         int64          `json:"seen_at"`
	ArchivedAt     int64          `json:"dismissed_at"`
	PromotedTodoID string         `json:"promoted_todo_id,omitempty"`
	PromotedTaskID string         `json:"promoted_task_id,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`

	// Key is the caller's de-duplication key, unique per source; without
	// one the source and URL are the key.
	Key string `json:"key,omitempty"`
	// Why is the one line saying what the item wants of the user ("review
	// requested", "CI failed").
	Why string `json:"why,omitempty"`
	// Window and Session are the pane the item is about.
	Window       string `json:"window,omitempty"`
	Session      string `json:"session,omitempty"`
	SnoozedUntil int64  `json:"snoozed_until,omitempty"`
}

// State is the item's state at now (Unix ms).
func (it Item) State(now int64) string {
	switch {
	case it.ArchivedAt > 0:
		return StateArchived
	case it.SnoozedUntil > now:
		return StateSnoozed
	}
	return StateOpen
}

// Read reports whether the item has been read.
func (it Item) Read() bool { return it.ReadAt > 0 }

// Promoted is the id of the task the item was promoted to, "" for none.
func (it Item) Promoted() string { return cmp.Or(it.PromotedTaskID, it.PromotedTodoID) }

// Associated reports whether the item has a project, task or pane.
func (it Item) Associated() bool { return it.ProjectID != "" || it.TaskID != "" || it.Window != "" }

// Spec is what add is given. Every field is optional: an item is recorded
// whatever it lacks (an item with no association is unassociated).
type Spec struct {
	Source    string         `json:"source"`
	URL       string         `json:"url"`
	Key       string         `json:"key"`
	Title     string         `json:"title"`
	Why       string         `json:"why"`
	Text      string         `json:"text"`
	Kind      string         `json:"kind"`
	ProjectID string         `json:"project_id"`
	TaskID    string         `json:"task_id"`
	Window    string         `json:"window"`
	Session   string         `json:"session"`
	Extra     map[string]any `json:"extra"`
	// Reopen makes an archived or snoozed duplicate open and unread again.
	Reopen bool `json:"reopen"`
}

// Owner is what an item is associated with; the zero Owner unassociates.
type Owner struct {
	ProjectID string
	TaskID    string
	Window    string
	Session   string
}

// Store is the inbox's view of the database.
type Store struct {
	db  *store.Store
	now func() int64
}

// Open declares the table (accepting matomeru-old's) and returns the unit.
func Open(db *store.Store) (*Store, error) {
	if err := db.Declare(Table); err != nil {
		return nil, err
	}
	return &Store{db: db, now: work.Now}, nil
}

// SetClock replaces the clock (Unix ms); tests use it.
func (s *Store) SetClock(now func() int64) { s.now = now }

// Now is the store's clock.
func (s *Store) Now() int64 { return s.now() }

// line is text as one line: newlines become spaces, runs of space one.
func line(text string) string { return strings.Join(strings.Fields(text), " ") }

// Add records an item, or refreshes the one with the same source and key
// (else the same source and URL), so repeated polls never pile up. It
// reports whether a row was added. A refresh rewrites what the spec gives
// and keeps the read, snooze and archive marks unless Reopen is set.
func (s *Store) Add(spec Spec) (Item, bool, error) {
	spec.Source = cmp.Or(strings.ToLower(line(spec.Source)), ManualSource)
	spec.URL = strings.TrimSpace(spec.URL)
	spec.Key = strings.TrimSpace(spec.Key)
	spec.Title, spec.Why = line(spec.Title), line(spec.Why)
	spec.Kind = strings.ToLower(line(spec.Kind))
	var out Item
	created := false
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		now := s.now()
		existing, err := duplicate(tx, spec)
		if err != nil {
			return err
		}
		if existing != nil {
			it := *existing
			set := func(dst *string, v string) {
				if v != "" {
					*dst = v
				}
			}
			set(&it.URL, spec.URL)
			set(&it.Title, spec.Title)
			set(&it.Why, spec.Why)
			set(&it.Text, spec.Text)
			set(&it.Kind, spec.Kind)
			set(&it.ProjectID, spec.ProjectID)
			set(&it.TaskID, spec.TaskID)
			set(&it.Window, spec.Window)
			set(&it.Session, spec.Session)
			if len(spec.Extra) > 0 {
				it.Extra = spec.Extra
			}
			if spec.Reopen {
				it.ArchivedAt, it.ReadAt, it.SnoozedUntil = 0, 0, 0
			}
			it.Updated = now
			out = it
			return tx.Put(Table.Name, it.ID, it)
		}
		out = Item{ID: work.NewID(), Source: spec.Source, URL: spec.URL, Key: spec.Key, Title: spec.Title, Why: spec.Why, Text: spec.Text,
			Kind: spec.Kind, ProjectID: spec.ProjectID, TaskID: spec.TaskID, Window: spec.Window, Session: spec.Session, Extra: spec.Extra,
			Created: now, Updated: now}
		created = true
		return tx.Put(Table.Name, out.ID, out)
	})
	return out, created, err
}

// duplicate is the row spec refreshes: the same source and key, else (with
// no key) the same source and URL; nil when there is none or the spec has
// neither a key nor a URL.
func duplicate(tx *store.Tx, spec Spec) (*Item, error) {
	if spec.Key == "" && spec.URL == "" {
		return nil, nil
	}
	rows, err := tx.All(Table.Name)
	if err != nil {
		return nil, err
	}
	for _, raw := range rows {
		var it Item
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, fmt.Errorf("action item: %w", err)
		}
		if it.Source != spec.Source {
			continue
		}
		if spec.Key != "" && it.Key == spec.Key || spec.Key == "" && it.Key == "" && it.URL == spec.URL {
			return &it, nil
		}
	}
	return nil, nil
}

// All is every item in inbox order: open unread, open read, snoozed (the
// soonest awake first), archived; newest first within each.
func (s *Store) All() ([]Item, error) {
	rows, err := s.db.All(Table.Name)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, raw := range rows {
		var it Item
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, fmt.Errorf("action item: %w", err)
		}
		out = append(out, it)
	}
	now := s.now()
	slices.SortStableFunc(out, func(a, b Item) int {
		if c := cmp.Compare(section(a, now), section(b, now)); c != 0 {
			return c
		}
		if a.State(now) == StateSnoozed {
			if c := cmp.Compare(a.SnoozedUntil, b.SnoozedUntil); c != 0 {
				return c
			}
		}
		return cmp.Or(cmp.Compare(b.Created, a.Created), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func section(it Item, now int64) int {
	switch it.State(now) {
	case StateArchived:
		return 3
	case StateSnoozed:
		return 2
	}
	if it.Read() {
		return 1
	}
	return 0
}

// Counts are the open items and the unread ones among them.
type Counts struct {
	Open   int `json:"open"`
	Unread int `json:"unread"`
}

// Counts counts the open and unread items now.
func (s *Store) Counts() (Counts, error) {
	items, err := s.All()
	if err != nil {
		return Counts{}, err
	}
	now := s.now()
	var c Counts
	for _, it := range items {
		if it.State(now) != StateOpen {
			continue
		}
		c.Open++
		if !it.Read() {
			c.Unread++
		}
	}
	return c, nil
}

// Get is one item.
func (s *Store) Get(id string) (Item, error) {
	var it Item
	err := s.db.Get(Table.Name, id, &it)
	if errors.Is(err, store.ErrNotFound) {
		return it, fmt.Errorf("action item %s: %w", id, err)
	}
	return it, err
}

// Update changes one item in a transaction and stamps it.
func (s *Store) Update(id string, change func(*Item) error) (Item, error) {
	var it Item
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		if err := tx.Get(Table.Name, id, &it); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("action item %s: %w", id, err)
			}
			return err
		}
		if err := change(&it); err != nil {
			return err
		}
		it.Updated = s.now()
		return tx.Put(Table.Name, id, it)
	})
	return it, err
}

// SetRead marks an item read, or unread.
func (s *Store) SetRead(id string, read bool) (Item, error) {
	return s.Update(id, func(it *Item) error {
		switch {
		case !read:
			it.ReadAt = 0
		case it.ReadAt == 0:
			it.ReadAt = s.now()
		}
		return nil
	})
}

// Archive archives an item (and reads it): it leaves the open items.
func (s *Store) Archive(id string) (Item, error) {
	return s.Update(id, func(it *Item) error {
		now := s.now()
		if it.ReadAt == 0 {
			it.ReadAt = now
		}
		it.ArchivedAt, it.SnoozedUntil = now, 0
		return nil
	})
}

// Restore makes an archived or snoozed item open again; its read mark and
// any promotion are kept.
func (s *Store) Restore(id string) (Item, error) {
	return s.Update(id, func(it *Item) error {
		it.ArchivedAt, it.SnoozedUntil = 0, 0
		return nil
	})
}

// Snooze hides an item until until (Unix ms); a time already past wakes it.
func (s *Store) Snooze(id string, until int64) (Item, error) {
	return s.Update(id, func(it *Item) error {
		it.SnoozedUntil, it.ArchivedAt = until, 0
		return nil
	})
}

// Associate sets what the item is about; the zero Owner unassociates it.
func (s *Store) Associate(id string, o Owner) (Item, error) {
	return s.Update(id, func(it *Item) error {
		it.ProjectID, it.TaskID, it.Window, it.Session = o.ProjectID, o.TaskID, o.Window, o.Session
		return nil
	})
}

// Promoted records the task an item was promoted to (as a todo or a task),
// associates the item with it and archives the item.
func (s *Store) Promoted(id, to string, t work.Task) (Item, error) {
	return s.Update(id, func(it *Item) error {
		if to == "todo" {
			it.PromotedTodoID = t.ID
		} else {
			it.PromotedTaskID = t.ID
		}
		now := s.now()
		it.TaskID, it.ProjectID = t.ID, t.ProjectID
		if it.ReadAt == 0 {
			it.ReadAt = now
		}
		it.ArchivedAt, it.SnoozedUntil = now, 0
		return nil
	})
}

// Put writes an item as it is, under its id (a new id when it has none):
// one row of its own, never merged with a duplicate as Add merges (the
// standard create, and an undone delete putting a row back). Its source is
// manual when it names none; a new row is stamped.
func (s *Store) Put(it Item) (Item, error) {
	it.Source = cmp.Or(strings.ToLower(line(it.Source)), ManualSource)
	if it.ID == "" {
		it.ID = work.NewID()
	}
	now := s.now()
	if it.Created == 0 {
		it.Created = now
	}
	it.Updated = now
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error { return tx.Put(Table.Name, it.ID, it) })
	return it, err
}

// Delete removes an item.
func (s *Store) Delete(id string) error {
	return s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		var it Item
		if err := tx.Get(Table.Name, id, &it); err != nil {
			return fmt.Errorf("action item %s: %w", id, err)
		}
		return tx.Delete(Table.Name, id)
	})
}

// Label is the item's one line: its title, else why, else the URL.
func (it Item) Label() string {
	return cmp.Or(it.Title, it.Why, it.URL, "(untitled "+it.Source+" item)")
}
