// Package work owns the project and task rows of the work page. The row
// shapes and table declarations are the ones matomeru-old persisted, so the
// existing database opens unchanged.
package work

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/unixsuperhero/matomeru/internal/store"
)

// Project is one row of the projects table.
type Project struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Theme       Theme           `json:"theme"`
	Status      string          `json:"status"`
	Created     int64           `json:"created"`
	Repo        string          `json:"repo"`
	Trees       []Tree          `json:"trees"`
	DefaultTree string          `json:"default_tree"`
	Settings    ProjectSettings `json:"settings"`
}

// Theme is the colour and glyph a project or task is drawn with.
type Theme struct {
	Color string `json:"color"`
	Glyph string `json:"glyph"`
}

// Tree is one checkout of a project: a plain directory, a clone or a worktree.
type Tree struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// ProjectSettings are the per-project launch defaults.
type ProjectSettings struct {
	Harness string `json:"harness,omitempty"`
	Preset  string `json:"preset,omitempty"`
}

// Task is one row of the tasks table.
type Task struct {
	ID           string `json:"id"`
	ParentID     string `json:"parent_id"`
	ProjectID    string `json:"project_id"`
	Title        string `json:"title"`
	Position     int64  `json:"position"`
	Theme        Theme  `json:"theme"`
	Status       string `json:"status"`
	Easy         bool   `json:"easy"`
	SnoozedUntil int64  `json:"snoozed_until,omitempty"`
	Created      int64  `json:"created"`
	Updated      int64  `json:"updated"`
}

// Tables are the declarations this package owns; they match the persisted
// metadata of matomeru-old, so Declare accepts an existing database.
var Tables = []store.Table{
	{Name: "projects", Owner: "work", Key: "id", Version: 1},
	{Name: "tasks", Owner: "work", Key: "id", Version: 1},
}

// Store is the work unit's view of the database.
type Store struct {
	db *store.Store
}

// Open declares the tables and returns the unit.
func Open(db *store.Store) (*Store, error) {
	for _, t := range Tables {
		if err := db.Declare(t); err != nil {
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

// Projects returns every project, oldest first.
func (s *Store) Projects() ([]Project, error) {
	out, err := all[Project](s.db, "projects")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Project) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// Tasks returns the tasks of one project in board order; an empty project id
// returns every task.
func (s *Store) Tasks(projectID string) ([]Task, error) {
	rows, err := all[Task](s.db, "tasks")
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, t := range rows {
		if projectID == "" || t.ProjectID == projectID {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Task) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// PutProject writes a project row.
func (s *Store) PutProject(p Project) error {
	return s.db.Tx([]string{"projects"}, func(tx *store.Tx) error { return tx.Put("projects", p.ID, p) })
}

// PutTask writes a task row.
func (s *Store) PutTask(t Task) error {
	return s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error { return tx.Put("tasks", t.ID, t) })
}

func all[T any](db *store.Store, table string) ([]T, error) {
	raws, err := db.All(table)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(raws))
	for _, raw := range raws {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
