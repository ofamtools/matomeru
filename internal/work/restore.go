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

// taskLinked are the tables whose rows name a task by task_id (or a
// project by project_id), with their key columns: the rows Delete detaches
// that a snapshot keeps.
var taskLinked = []struct{ table, key string }{
	{"notes", "id"}, {"assignments", "window"}, {"session_owners", "id"},
	{"reviews", "id"}, {"saved_panes", "id"}, {"pane_recipes", "window"},
	{"vim_sessions", "id"}, {ChangesTable, "id"},
}

// SnapshotTask reads the task and every row that names it, before it is
// deleted.
func (s *Store) SnapshotTask(id string) (DeletedTask, error) {
	var d DeletedTask
	err := s.db.View(linkedTables("tasks"), func(tx *store.Tx) error {
		if err := tx.Get("tasks", id, &d.Task); err != nil {
			return err
		}
		rows, err := linkedRows(tx, "task_id", id)
		d.Rows = rows
		return err
	})
	return d, err
}

// DeletedProject is a project as it was before Delete removed it: its
// record and the rows Delete detached from it (notes, pane owners and the
// like), kept so an undone delete can put everything back, as DeletedTask
// is for a task.
type DeletedProject struct {
	Project Project
	Rows    []DetachedRow
}

// SnapshotProject reads the project and every row that names it, before
// it is deleted.
func (s *Store) SnapshotProject(id string) (DeletedProject, error) {
	var d DeletedProject
	err := s.db.View(linkedTables("projects"), func(tx *store.Tx) error {
		if err := tx.Get("projects", id, &d.Project); err != nil {
			return err
		}
		rows, err := linkedRows(tx, "project_id", id)
		d.Rows = rows
		return err
	})
	return d, err
}

// RestoreProject writes a deleted project back under the id it had, with
// the name the caller gives it (the record is otherwise as it was:
// status, colour, repository, settings, stamps), and re-attaches the rows
// Delete detached while they are still detached. An empty snapshot (no
// project) writes nothing.
func (s *Store) RestoreProject(d DeletedProject, name string) (Project, error) {
	p := d.Project
	if p.ID == "" {
		return p, nil
	}
	p.Name = name
	err := s.db.Tx(linkedTables("projects"), func(tx *store.Tx) error {
		if err := s.mayShareProject(tx, p); err != nil {
			return err
		}
		if err := tx.Put("projects", p.ID, p); err != nil {
			return err
		}
		return reattach(tx, d.Rows, "project", p.ID)
	})
	return p, err
}

// linkedTables are the record's table and the tables whose rows name it.
func linkedTables(table string) []string {
	tables := []string{table}
	for _, l := range taskLinked {
		tables = append(tables, l.table)
	}
	return tables
}

// linkedRows are the rows of the linked tables whose field (task_id,
// project_id) names id, as they are stored.
func linkedRows(tx *store.Tx, field, id string) ([]DetachedRow, error) {
	var out []DetachedRow
	for _, l := range taskLinked {
		rows, err := tx.All(l.table)
		if err != nil {
			return nil, err
		}
		for _, raw := range rows {
			row := map[string]any{}
			if err := json.Unmarshal(raw, &row); err != nil {
				return nil, err
			}
			if ownerOf(l.table, row, field) != id {
				continue
			}
			key, _ := row[l.key].(string)
			out = append(out, DetachedRow{Table: l.table, Key: key, Row: slicesClone(raw)})
		}
	}
	return out, nil
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
	err := s.db.Tx(linkedTables("tasks"), func(tx *store.Tx) error {
		if err := s.putTask(tx, &t); err != nil {
			return err
		}
		return reattach(tx, d.Rows, "task", t.ID)
	})
	return t, err
}

// reattach puts back what Delete detached from the record of kind (task,
// project) with id: each row that is still detached from it (NAME_id
// empty, former_NAME_id the record) names it again, with its earlier
// former_NAME_id. A row removed or attached elsewhere since is left alone.
func reattach(tx *store.Tx, rows []DetachedRow, kind, id string) error {
	field, former := kind+"_id", "former_"+kind+"_id"
	for _, r := range rows {
		now := map[string]any{}
		if err := tx.Get(r.Table, r.Key, &now); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return err
		}
		if ownerOf(r.Table, now, field) != "" {
			continue
		}
		if ownerOf(r.Table, now, former) != id {
			continue
		}
		was := map[string]any{}
		if err := json.Unmarshal(r.Row, &was); err != nil {
			return err
		}
		setOwner(r.Table, now, field, id)
		if v, ok := ownerHeld(r.Table, was, former); ok {
			setOwner(r.Table, now, former, v)
		} else {
			clearOwner(r.Table, now, former)
		}
		if err := tx.Put(r.Table, r.Key, now); err != nil {
			return err
		}
	}
	return nil
}
