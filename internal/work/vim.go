package work

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/unixsuperhero/matomeru/internal/store"
)

// VimSession keeps the legacy vim_sessions shape and unknown fields intact.
// OwnedPath is set only for files created by this implementation; legacy paths
// and explicitly supplied paths are never inferred to be safe to delete.
type VimSession struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	ProjectID       string `json:"project_id"`
	TaskID          string `json:"task_id"`
	FormerProjectID string `json:"former_project_id,omitempty"`
	FormerTaskID    string `json:"former_task_id,omitempty"`
	Cwd             string `json:"cwd"`
	Created         int64  `json:"created"`
	Updated         int64  `json:"updated"`
	Session         string `json:"session"`
	WindowID        string `json:"window_id"`
	State           string `json:"state"`
	Listen          string `json:"listen,omitempty"`
	OwnedPath       string `json:"owned_path,omitempty"`
	extra           map[string]json.RawMessage
}

type vimFields VimSession

func (v *VimSession) UnmarshalJSON(data []byte) error {
	var fields vimFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*v = VimSession(fields)
	if err := json.Unmarshal(data, &v.extra); err != nil {
		return err
	}
	for _, key := range []string{"id", "name", "path", "project_id", "task_id", "former_project_id", "former_task_id", "cwd", "created", "updated", "session", "window_id", "state", "listen", "owned_path"} {
		delete(v.extra, key)
	}
	return nil
}

func (v VimSession) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(vimFields(v))
	if err != nil || len(v.extra) == 0 {
		return data, err
	}
	fields := make(map[string]json.RawMessage, len(v.extra)+16)
	for key, value := range v.extra {
		fields[key] = value
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(data, &known); err != nil {
		return nil, err
	}
	for key, value := range known {
		fields[key] = value
	}
	return json.Marshal(fields)
}

func (s *Store) VimSessions() ([]VimSession, error) {
	rows, err := all[VimSession](s.db, "vim_sessions")
	slices.SortFunc(rows, func(a, b VimSession) int { return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID)) })
	return rows, err
}

func (s *Store) VimSession(id string) (VimSession, error) {
	var row VimSession
	err := s.db.Get("vim_sessions", id, &row)
	return row, err
}

func (s *Store) PutVimSession(row VimSession) error {
	return s.db.Tx([]string{"vim_sessions"}, func(tx *store.Tx) error { return tx.Put("vim_sessions", row.ID, row) })
}

func (s *Store) ForgetVimSession(id string) error {
	return s.db.Tx([]string{"vim_sessions"}, func(tx *store.Tx) error { return tx.Delete("vim_sessions", id) })
}

func (s *Store) VimProject(row VimSession) (string, error) {
	if row.TaskID == "" {
		return row.ProjectID, nil
	}
	if _, err := s.Task(row.TaskID); err == store.ErrNotFound {
		return row.ProjectID, nil
	} else if err != nil {
		return "", err
	}
	return s.taskProject(row.TaskID)
}
