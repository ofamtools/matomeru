package work

import (
	"encoding/json"
	"fmt"

	"github.com/unixsuperhero/matomeru/store"
)

type DeleteCounts struct {
	Tasks       int `json:"tasks"`
	Notes       int `json:"notes"`
	Assignments int `json:"assignments"`
	Sessions    int `json:"sessions"`
	Reviews     int `json:"reviews"`
	Saved       int `json:"saved"`
	Vim         int `json:"vim"`
}

func (c DeleteCounts) String() string {
	return fmt.Sprintf("%d tasks, %d notes, %d pane owners, %d session owners, %d reviews, %d saved panes, %d Vim sessions", c.Tasks, c.Notes, c.Assignments, c.Sessions, c.Reviews, c.Saved, c.Vim)
}

// DeletionImpact and Delete use the same ownership walk. Delete repeats it under
// the write transaction so no child can be left referencing the removed owner.
func (s *Store) DeletionImpact(noun, id string) (DeleteCounts, error) {
	return s.deleteRecord(noun, id, false)
}

func (s *Store) Delete(noun, id string) (DeleteCounts, error) {
	return s.deleteRecord(noun, id, true)
}

func (s *Store) deleteRecord(noun, id string, apply bool) (DeleteCounts, error) {
	var counts DeleteCounts
	if noun != "project" && noun != "task" {
		return counts, fmt.Errorf("cannot delete %s", noun)
	}
	tables := []string{"projects", "tasks", "notes", "assignments", "session_owners", "reviews", "active", "saved_panes", "pane_recipes", "vim_sessions", "ext_rows"}
	run := s.db.View
	if apply {
		run = s.db.Tx
	}
	err := run(tables, func(tx *store.Tx) error {
		table := noun + "s"
		var target json.RawMessage
		if err := tx.Get(table, id, &target); err != nil {
			return err
		}
		if apply && noun == "task" {
			var t Task
			if err := json.Unmarshal(target, &t); err != nil {
				return err
			}
			if err := s.mayChangeTask(tx, t, nil); err != nil {
				return err
			}
		}
		raws, err := tx.All("tasks")
		if err != nil {
			return err
		}
		tasks := map[string]Task{}
		for _, raw := range raws {
			var task Task
			if err := json.Unmarshal(raw, &task); err != nil {
				return err
			}
			tasks[task.ID] = task
		}
		projectOf := func(taskID string) string {
			seen := map[string]bool{}
			for taskID != "" && !seen[taskID] {
				seen[taskID] = true
				t := tasks[taskID]
				if t.ProjectID != "" {
					return t.ProjectID
				}
				taskID = t.ParentID
			}
			return ""
		}
		for _, child := range []struct {
			name, key string
			count     *int
		}{
			{"tasks", "id", &counts.Tasks}, {"notes", "id", &counts.Notes},
			{"assignments", "window", &counts.Assignments}, {"session_owners", "id", &counts.Sessions},
			{"reviews", "id", &counts.Reviews}, {"active", "id", nil},
			{"saved_panes", "id", &counts.Saved}, {"pane_recipes", "window", nil},
			{"vim_sessions", "id", &counts.Vim}, {"ext_rows", "id", nil},
		} {
			rows, err := tx.All(child.name)
			if err != nil {
				return err
			}
			for _, raw := range rows {
				row := map[string]any{}
				if err := json.Unmarshal(raw, &row); err != nil {
					return err
				}
				key, _ := row[child.key].(string)
				if child.name == "active" {
					key = "singleton"
				}
				if child.name == table && key == id {
					continue
				}
				project, _ := row["project_id"].(string)
				task, _ := row["task_id"].(string)
				parent, _ := row["parent_id"].(string)
				if project == "" {
					if child.name == "tasks" {
						project = projectOf(key)
					} else {
						project = projectOf(task)
					}
				}
				changed := false
				if noun == "project" && project == id {
					row["project_id"], row["former_project_id"] = "", id
					changed = true
				}
				if noun == "task" && (task == id || (child.name == "tasks" && parent == id)) {
					row["former_task_id"] = id
					if task == id {
						row["task_id"] = ""
					}
					if child.name == "tasks" {
						row["parent_id"] = ""
					}
					// Preserve an inherited project when only its task is removed.
					if project != "" {
						row["project_id"] = project
					}
					changed = true
				}
				if changed {
					if child.count != nil {
						*child.count++
					}
					if apply {
						if err := tx.Put(child.name, key, row); err != nil {
							return err
						}
					}
				}
			}
		}
		if apply {
			return tx.Delete(table, id)
		}
		return nil
	})
	return counts, err
}
