// Package contextitems keeps the things mtm finds in panes (paths, URLs,
// PRs, branches, commits, commands): the context_items table matomeru-old
// wrote, the extraction patterns and de-duplication. It knows nothing of
// the TUI or the daemon.
package contextitems

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
)

// Table is matomeru-old's declaration, unchanged: the owner's database
// holds it with owner "context", and a different declaration stops mtm
// from starting.
var Table = store.Table{Name: "context_items", Owner: "context", Key: "id", Version: 1}

// Kinds are the item kinds, in the order the page's kind filter cycles
// them. link is a URL (matomeru-old's name, kept so its rows read as they
// were written); pr is a GitHub pull request or issue, or a GitLab merge
// request.
var Kinds = []string{"file", "dir", "link", "pr", "branch", "commit", "command", "prompt"}

// Item is one row. The fields up to Provenance are matomeru-old's shape
// and keep its JSON names; LastSeen and Exists are new and omitted when
// unset, so an old row read and written back keeps every field it had.
type Item struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Session   string `json:"session"`
	Window    string `json:"window"`
	Pane      string `json:"pane"`
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	Starred   bool   `json:"starred"`
	Pinned    bool   `json:"pinned"`
	Archived  bool   `json:"archived,omitempty"`
	// Dismissed is the item triaged away: kept and listed, left out of a
	// pane's rail (rail.dismissed); a star keeps it on the rail anyway.
	Dismissed bool `json:"dismissed,omitempty"`
	// DismissedAt is when it was dismissed (ms; 0 never): every item of one
	// bulk dismissal holds the same stamp, so that batch can be undismissed.
	DismissedAt int64      `json:"dismissed_at,omitempty"`
	Created     int64      `json:"created"`
	Sources     []Source   `json:"sources"`
	Group       string     `json:"group,omitempty"`
	Provenance  Provenance `json:"provenance"`
	// LastSeen is when the item was last printed (ms); 0 on old rows,
	// which read Created instead (Seen).
	LastSeen int64 `json:"last_seen,omitempty"`
	// Exists is whether a file or dir item named an existing path when it
	// was last extracted; nil for other kinds and old rows.
	Exists *bool `json:"exists,omitempty"`
}

// Source is one place an item was seen.
type Source struct {
	Kind      string `json:"kind"`
	EntryKind string `json:"entry_kind,omitempty"`
	Session   string `json:"session"`
	Window    string `json:"window"`
	Pane      string `json:"pane"`
	At        int64  `json:"at"`
	Cwd       string `json:"cwd,omitempty"`
}

// Provenance is where an item was extracted: matomeru-old's shape.
type Provenance struct {
	TurnID    string `json:"turn_id,omitempty"`
	Session   string `json:"session,omitempty"`
	Window    string `json:"window,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Branch    string `json:"branch,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
	At        int64  `json:"at"`
}

// Seen is when the item was last printed: LastSeen, else Created.
func (it Item) Seen() int64 { return cmp.Or(it.LastSeen, it.Created) }

// Path reports whether the item names a path (file or dir).
func (it Item) Path() bool { return it.Kind == "file" || it.Kind == "dir" }

// Owner is who an item belongs to. A task owner keeps the project empty,
// as matomeru-old stored it (the project follows from the task).
type Owner struct {
	ProjectID string
	TaskID    string
}

// key is the de-duplication key: kind, normalized text and owner.
func key(it Item) string {
	return it.Kind + "\x00" + normalize(it.Kind, it.Text) + "\x00" + it.TaskID + "\x00" + it.ProjectID
}

// normalize is the text compared for de-duplication: trimmed, a URL
// without a trailing slash.
func normalize(kind, text string) string {
	text = strings.TrimSpace(text)
	if kind == "link" || kind == "pr" {
		text = strings.TrimSuffix(text, "/")
	}
	return text
}

// Store is the context unit's view of the database.
type Store struct{ db *store.Store }

// Open declares the table (accepting matomeru-old's) and returns the unit.
func Open(db *store.Store) (*Store, error) {
	if err := db.Declare(Table); err != nil {
		return nil, err
	}
	// The index Merge looks items up by. A read-only file cannot make it;
	// Merge then still finds the rows, by a scan that decodes none.
	_ = db.Index(Table.Name, indexName, keyFields...)
	// A task's detail lists its items on every move (its related section).
	_ = db.Index(Table.Name, "task", "task_id")
	return &Store{db: db}, nil
}

// keyFields are the row fields a duplicate is looked up by (the owner is
// compared after the rows are read); indexName is their index.
var keyFields = []string{"kind", "text|trim"}

const indexName = "kind_text"

// decoded counts the rows Merge has decoded to find duplicates, so a test
// can show it reads the few rows it needs and not the table.
var decoded atomic.Int64

// All is every item, newest sighting first (starred ones are sorted by
// the caller).
func (s *Store) All() ([]Item, error) {
	rows, err := s.db.All(Table.Name)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, raw := range rows {
		var it Item
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, fmt.Errorf("context item: %w", err)
		}
		out = append(out, it)
	}
	slices.SortStableFunc(out, func(a, b Item) int { return cmp.Compare(b.Seen(), a.Seen()) })
	return out, nil
}

// Version is a cheap token for the table: equal tokens mean no write of it
// (by this process, or by another since the file was opened) in between.
func (s *Store) Version() (string, error) { return s.db.Version(Table.Name) }

// Get is one item.
func (s *Store) Get(id string) (Item, error) {
	var it Item
	if err := s.db.Get(Table.Name, id, &it); err != nil {
		return it, fmt.Errorf("context item %s: %w", id, err)
	}
	return it, nil
}

// Merge writes extracted items for owner: an item already stored with the
// same kind, text and owner is seen again (LastSeen, Exists, a new source
// appended) instead of repeated. It returns the items written, new ones
// first then the updated, and how many were new.
func (s *Store) Merge(items []Item, owner Owner, now int64) ([]Item, int, error) {
	if len(items) == 0 {
		return nil, 0, nil
	}
	var added, seen []Item
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		existing := map[string]Item{}
		looked := map[string]bool{}
		// find reads the stored rows that can share its key (same kind
		// and text, any owner) once per kind and text, by index.
		find := func(it Item) error {
			n := normalize(it.Kind, it.Text)
			if looked[it.Kind+"\x00"+n] {
				return nil
			}
			looked[it.Kind+"\x00"+n] = true
			// A stored text may keep the trailing slash normalize drops;
			// the rows are compared by key() once read, so a variant that
			// is not a match costs nothing.
			texts := []string{n, n + "/"}
			rows, err := tx.Match(Table.Name, store.JSONMatch{Field: keyFields[0], Value: it.Kind}, store.JSONMatch{Field: keyFields[1], Values: texts})
			if err != nil {
				return err
			}
			for _, raw := range rows {
				decoded.Add(1)
				var old Item
				if err := json.Unmarshal(raw, &old); err != nil {
					return err
				}
				existing[key(old)] = old
			}
			return nil
		}
		for _, it := range items {
			it.ProjectID, it.TaskID = owner.ProjectID, owner.TaskID
			if it.TaskID != "" {
				it.ProjectID = ""
			}
			if err := find(it); err != nil {
				return err
			}
			k := key(it)
			if prev, ok := existing[k]; ok {
				for _, src := range it.Sources {
					if !slices.ContainsFunc(prev.Sources, func(old Source) bool {
						return old.Kind == src.Kind && old.EntryKind == src.EntryKind && old.Session == src.Session && old.Window == src.Window && old.Pane == src.Pane
					}) {
						prev.Sources = append(prev.Sources, src)
					}
				}
				prev.LastSeen = now
				if it.Exists != nil {
					prev.Exists = it.Exists
				}
				it = prev
				seen = append(seen, it)
			} else {
				if it.ID == "" {
					it.ID = work.NewID()
				}
				if it.Created == 0 {
					it.Created = now
				}
				it.LastSeen = now
				it.Provenance = Provenance{TurnID: it.Provenance.TurnID, Session: it.Session, Window: it.Window, ProjectID: owner.ProjectID, TaskID: owner.TaskID, At: it.Created}
				added = append(added, it)
			}
			existing[k] = it
			if err := tx.Put(Table.Name, it.ID, it); err != nil {
				return err
			}
		}
		return nil
	})
	return slices.Concat(added, seen), len(added), err
}

// SetStarred stars or unstars an item.
func (s *Store) SetStarred(id string, starred bool) (Item, error) {
	var it Item
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		if err := tx.Get(Table.Name, id, &it); err != nil {
			return fmt.Errorf("context item %s: %w", id, err)
		}
		it.Starred = starred
		return tx.Put(Table.Name, id, it)
	})
	return it, err
}

// Forget removes the record only; the file, page or branch it names is
// untouched.
func (s *Store) Forget(id string) error {
	return s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		var it Item
		if err := tx.Get(Table.Name, id, &it); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("context item %s: not found", id)
			}
			return err
		}
		return tx.Delete(Table.Name, id)
	})
}

// Associate gives an item a new owner: a task (the project left empty, as
// Merge stores it; the project follows from the task), a project, or none
// (the zero Owner, unassociated). Its sightings and provenance are kept.
func (s *Store) Associate(id string, owner Owner) (Item, error) {
	var it Item
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		if err := tx.Get(Table.Name, id, &it); err != nil {
			return fmt.Errorf("context item %s: %w", id, err)
		}
		it.ProjectID, it.TaskID = owner.ProjectID, owner.TaskID
		if it.TaskID != "" {
			it.ProjectID = ""
		}
		return tx.Put(Table.Name, id, it)
	})
	return it, err
}

// Update changes one item in a transaction: change edits the stored row,
// which is written back as it leaves it (every field it does not touch,
// its sightings and provenance among them, is kept). A task owner keeps
// the project empty, as Merge stores it.
func (s *Store) Update(id string, change func(*Item)) (Item, error) {
	var it Item
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		if err := tx.Get(Table.Name, id, &it); err != nil {
			return fmt.Errorf("context item %s: %w", id, err)
		}
		change(&it)
		it.ID = id
		if it.TaskID != "" {
			it.ProjectID = ""
		}
		return tx.Put(Table.Name, id, it)
	})
	return it, err
}

// UpdateAll changes every item of ids in one transaction (a bulk
// dismissal): one write, however many items. Ids not found are skipped;
// the count is the items written.
func (s *Store) UpdateAll(ids []string, change func(*Item)) (int, error) {
	n := 0
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		for _, id := range ids {
			var it Item
			if err := tx.Get(Table.Name, id, &it); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return err
			}
			change(&it)
			it.ID = id
			if err := tx.Put(Table.Name, id, it); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// Put writes one item as it is, under its id (a new row, or one put back
// after Forget). A task owner keeps the project empty, as Merge stores it.
func (s *Store) Put(it Item) error {
	if it.ID == "" {
		return errors.New("context item: no id")
	}
	if it.TaskID != "" {
		it.ProjectID = ""
	}
	return s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error { return tx.Put(Table.Name, it.ID, it) })
}
