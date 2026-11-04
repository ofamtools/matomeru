package work

import (
	"encoding/json"
	"errors"

	"ofam.dev/matomeru/store"
)

// DeletedTask is a task as it was before Delete removed it: its record
// and the rows Delete detached from it (notes, pane owners, reviews and
// the like), kept so an undone delete can put everything back.
type DeletedTask struct {
	Task Task
	Rows []DetachedRow
}

// DetachedRow is one row that referenced a deleted task (task_id), as it
// was stored.
type DetachedRow struct {
	Table, Key string
	Row        json.RawMessage
}

// taskLinked are the tables whose rows name a task by task_id, with their
// key columns: the rows Delete("task") detaches.
var taskLinked = []struct{ table, key string }{
	{"notes", "id"}, {"assignments", "window"}, {"session_owners", "id"},
	{"reviews", "id"}, {"saved_panes", "id"}, {"pane_recipes", "window"},
	{"vim_sessions", "id"}, {"ext_rows", "id"},
}

// SnapshotTask reads the task and every row that names it, before it is
// deleted.
func (s *Store) SnapshotTask(id string) (DeletedTask, error) {
	var d DeletedTask
	tables := []string{"tasks"}
	for _, l := range taskLinked {
		tables = append(tables, l.table)
	}
	err := s.db.View(tables, func(tx *store.Tx) error {
		if err := tx.Get("tasks", id, &d.Task); err != nil {
			return err
		}
		for _, l := range taskLinked {
			rows, err := tx.All(l.table)
			if err != nil {
				return err
			}
			for _, raw := range rows {
				row := map[string]any{}
				if err := json.Unmarshal(raw, &row); err != nil {
					return err
				}
				if task, _ := row["task_id"].(string); task != id {
					continue
				}
				key, _ := row[l.key].(string)
				d.Rows = append(d.Rows, DetachedRow{Table: l.table, Key: key, Row: slicesClone(raw)})
			}
		}
		return nil
	})
	return d, err
}

func slicesClone(b json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), b...) }

// RestoreTask writes a deleted task back under the id it had, with the
// parent, title and project the caller gives it (the record is otherwise
// as it was: status, easy, private, position, stamps), and re-attaches
// the rows Delete detached while they are still detached (a row that was
// removed or attached elsewhere since is left alone).
func (s *Store) RestoreTask(d DeletedTask, parent, project, title string) (Task, error) {
	t := d.Task
	if t.ID == "" {
		return t, errors.New("nothing to restore")
	}
	t.ParentID, t.ProjectID, t.Title = parent, project, title
	tables := []string{"tasks"}
	for _, l := range taskLinked {
		tables = append(tables, l.table)
	}
	err := s.db.Tx(tables, func(tx *store.Tx) error {
		if err := s.putTask(tx, &t); err != nil {
			return err
		}
		for _, r := range d.Rows {
			now := map[string]any{}
			if err := tx.Get(r.Table, r.Key, &now); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return err
			}
			if task, _ := now["task_id"].(string); task != "" {
				continue
			}
			if former, _ := now["former_task_id"].(string); former != t.ID {
				continue
			}
			was := map[string]any{}
			if err := json.Unmarshal(r.Row, &was); err != nil {
				return err
			}
			now["task_id"] = t.ID
			if v, ok := was["former_task_id"]; ok {
				now["former_task_id"] = v
			} else {
				delete(now, "former_task_id")
			}
			if err := tx.Put(r.Table, r.Key, now); err != nil {
				return err
			}
		}
		return nil
	})
	return t, err
}
