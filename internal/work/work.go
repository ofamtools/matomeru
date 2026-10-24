// Package work owns the project and task rows of the work page. The row
// shapes and table declarations are the ones matomeru-old persisted, so the
// existing database opens unchanged.
package work

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

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
	{Name: "launch_state", Owner: "work", Key: "id", Version: 1},
	{Name: "reviews", Owner: "work", Key: "id", Version: 1},
}

// launchState is the one row of launch_state: the project the last launch
// went into, the fallback of launch inference.
type launchState struct {
	ProjectID string `json:"project_id"`
}

const lastLaunchID = "last"

// LastLaunched is the id of the project the last launch went into, "" when
// nothing was launched yet.
func (s *Store) LastLaunched() (string, error) {
	var st launchState
	err := s.db.Get("launch_state", lastLaunchID, &st)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return st.ProjectID, err
}

// SetLastLaunched records the project a launch went into.
func (s *Store) SetLastLaunched(projectID string) error {
	return s.db.Tx([]string{"launch_state"}, func(tx *store.Tx) error {
		return tx.Put("launch_state", lastLaunchID, launchState{ProjectID: projectID})
	})
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

// AddProject creates an open project with a name and nothing else.
func (s *Store) AddProject(name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, errors.New("a project needs a name")
	}
	p := Project{ID: NewID(), Name: name, Status: "open", Created: Now()}
	return p, s.PutProject(p)
}

// PutTask writes a task row.
func (s *Store) PutTask(t Task) error {
	return s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error { return tx.Put("tasks", t.ID, t) })
}

// Task statuses this unit writes; the database also holds "open", "active"
// and "skipped" from matomeru-old, which read unchanged.
const (
	StatusTodo = "todo"
	StatusDone = "done"
)

// Task reads one task row by id.
func (s *Store) Task(id string) (Task, error) {
	var t Task
	err := s.db.Get("tasks", id, &t)
	return t, err
}

// AddTask creates a todo task at the end of a project's board.
func (s *Store) AddTask(projectID, title string) (Task, error) {
	title = strings.TrimSpace(title)
	if projectID == "" {
		return Task{}, errors.New("a task needs a project")
	}
	if title == "" {
		return Task{}, errors.New("a task needs a title")
	}
	siblings, err := s.Tasks(projectID)
	if err != nil {
		return Task{}, err
	}
	var position int64
	for _, t := range siblings {
		position = max(position, t.Position)
	}
	now := Now()
	t := Task{ID: NewID(), ProjectID: projectID, Title: title, Position: position + 1, Status: StatusTodo, Created: now, Updated: now}
	return t, s.PutTask(t)
}

// Retitle sets a task's title.
func (s *Store) Retitle(id, title string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, errors.New("a task needs a title")
	}
	return s.patchTask(id, func(t *Task) { t.Title = title })
}

// Done marks a task done.
func (s *Store) Done(id string) (Task, error) {
	return s.patchTask(id, func(t *Task) { t.Status = StatusDone })
}

func (s *Store) patchTask(id string, change func(*Task)) (Task, error) {
	var t Task
	err := s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error {
		if err := tx.Get("tasks", id, &t); err != nil {
			return err
		}
		change(&t)
		t.Updated = Now()
		return tx.Put("tasks", t.ID, t)
	})
	return t, err
}

// NewID is a row id in the shape matomeru-old wrote: 32 hex digits.
func NewID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

// Now is a row timestamp in matomeru-old's unit, Unix milliseconds.
func Now() int64 { return time.Now().UnixMilli() }

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
