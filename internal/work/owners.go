package work

import (
	"encoding/json"
	"errors"

	"github.com/unixsuperhero/matomeru/store"
)

// Assignment's presence, even with empty ownership, overrides session inheritance.
type Assignment struct {
	Window          string `json:"window"`
	Session         string `json:"session"`
	ProjectID       string `json:"project_id"`
	TaskID          string `json:"task_id"`
	FormerProjectID string `json:"former_project_id,omitempty"`
	FormerTaskID    string `json:"former_task_id,omitempty"`
	AssignedAt      int64  `json:"assigned_at,omitempty"`
}

type SessionOwner struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	// TaskID is set when a project the session belonged to became a task
	// (DemoteProject); its panes inherit it.
	TaskID          string `json:"task_id,omitempty"`
	FormerProjectID string `json:"former_project_id,omitempty"`
}

func (s *Store) SetPaneOwner(a Assignment) error {
	return s.db.Tx([]string{"assignments"}, func(tx *store.Tx) error { return tx.Put("assignments", a.Window, a) })
}

func (s *Store) SetSessionOwner(o SessionOwner) error {
	return s.db.Tx([]string{"session_owners"}, func(tx *store.Tx) error { return tx.Put("session_owners", o.ID, o) })
}

func (s *Store) SessionOwner(id string) (SessionOwner, error) {
	var o SessionOwner
	err := s.db.Get("session_owners", id, &o)
	if errors.Is(err, store.ErrNotFound) {
		return SessionOwner{ID: id}, nil
	}
	return o, err
}

// SessionOwnerNamed is the owner of the session with this name, for a
// caller that knows only the name (a capture hook inside the pane); an
// unowned name is an empty owner.
func (s *Store) SessionOwnerNamed(name string) (SessionOwner, error) {
	owners, err := all[SessionOwner](s.db, "session_owners")
	if err != nil {
		return SessionOwner{}, err
	}
	for _, o := range owners {
		if o.Name == name && name != "" {
			return o, nil
		}
	}
	return SessionOwner{Name: name}, nil
}

func (s *Store) PaneOwner(window, sessionID string) (Assignment, error) {
	var a Assignment
	err := s.db.Get("assignments", window, &a)
	if err == nil {
		// Old assignments to tasks stored no project; follow their parent chain.
		if a.ProjectID == "" && a.TaskID != "" {
			a.ProjectID, err = s.taskProject(a.TaskID)
		}
		return a, err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return a, err
	}
	o, err := s.SessionOwner(sessionID)
	return Assignment{Window: window, Session: sessionID, ProjectID: o.ProjectID, TaskID: o.TaskID, FormerProjectID: o.FormerProjectID}, err
}

func (s *Store) taskProject(id string) (string, error) {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		t, err := s.Task(id)
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if t.ProjectID != "" {
			return t.ProjectID, nil
		}
		id = t.ParentID
	}
	return "", nil
}

// AssociateTask files the task at project's top level ("" is
// unassociated), its subtasks with it (MoveSubtree), in one write.
func (s *Store) AssociateTask(id, project string) (Task, error) {
	t, _, err := s.moveSubtree(id, project, true)
	return t, err
}

func (s *Store) Pin() (string, error) {
	var row struct {
		ProjectID string `json:"project_id"`
	}
	err := s.db.Get("active", "singleton", &row)
	return row.ProjectID, err
}

func (s *Store) SetPin(project string) error {
	return s.db.Tx([]string{"active"}, func(tx *store.Tx) error {
		row := map[string]any{}
		if err := tx.Get("active", "singleton", &row); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		row["project_id"], row["pin_initialized"], row["task_id"] = project, true, ""
		if project != "" {
			pinned, _ := row["pinned_at"].(map[string]any)
			if pinned == nil {
				pinned = map[string]any{}
			}
			pinned[project] = Now()
			row["pinned_at"] = pinned
		}
		return tx.Put("active", "singleton", row)
	})
}

// PinnedAt is when each project was last pinned, in Unix milliseconds; a
// project never pinned is absent.
func (s *Store) PinnedAt() (map[string]int64, error) {
	var row struct {
		PinnedAt map[string]int64 `json:"pinned_at"`
	}
	err := s.db.Get("active", "singleton", &row)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	if row.PinnedAt == nil {
		row.PinnedAt = map[string]int64{}
	}
	return row.PinnedAt, err
}

// importOwners runs once, atomically. Existing assignment bytes are never rewritten.
func (s *Store) importOwners() error {
	return s.db.Tx([]string{"active", "tasks", "projects", "launch_state", "assignments", "reviews"}, func(tx *store.Tx) error {
		row := map[string]any{}
		if err := tx.Get("active", "singleton", &row); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		changed := false
		if initialized, _ := row["pin_initialized"].(bool); !initialized {
			project := ""
			id, _ := row["task_id"].(string)
			seen := map[string]bool{}
			for id != "" && !seen[id] {
				seen[id] = true
				var task Task
				err := tx.Get("tasks", id, &task)
				if errors.Is(err, store.ErrNotFound) {
					break
				}
				if err != nil {
					return err
				}
				if task.ProjectID != "" {
					project = task.ProjectID
					break
				}
				id = task.ParentID
			}
			if project == "" {
				var last struct {
					ProjectID string `json:"project_id"`
				}
				if err := tx.Get("launch_state", "last", &last); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				project = last.ProjectID
			}
			if project != "" {
				var p Project
				if err := tx.Get("projects", project, &p); errors.Is(err, store.ErrNotFound) {
					project = ""
				} else if err != nil {
					return err
				}
			}
			row["project_id"], row["task_id"], row["pin_initialized"] = project, "", true
			changed = true
		}
		if imported, _ := row["owners_imported"].(bool); !imported {
			reviews, err := tx.All("reviews")
			if err != nil {
				return err
			}
			for _, raw := range reviews {
				var rv Review
				if err := json.Unmarshal(raw, &rv); err != nil {
					return err
				}
				if rv.Window == "" {
					continue
				}
				var existing json.RawMessage
				err := tx.Get("assignments", rv.Window, &existing)
				if err == nil {
					continue
				}
				if !errors.Is(err, store.ErrNotFound) {
					return err
				}
				if err := tx.Put("assignments", rv.Window, Assignment{Window: rv.Window, Session: rv.Session, ProjectID: rv.ProjectID, TaskID: rv.TaskID}); err != nil {
					return err
				}
			}
			row["owners_imported"] = true
			changed = true
		}
		if changed {
			return tx.Put("active", "singleton", row)
		}
		return nil
	})
}
