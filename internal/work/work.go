// Package work owns the project and task rows of the work page. The row
// shapes and table declarations are the ones matomeru-old persisted, so the
// existing database opens unchanged.
package work

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/matomeru/store"
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
	// Origin is the tool that created or adopted the project ("o-proj");
	// rows written before it existed, and by mtm itself, leave it empty.
	Origin string `json:"origin,omitempty"`
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
	ID              string `json:"id"`
	ParentID        string `json:"parent_id"`
	ProjectID       string `json:"project_id"`
	FormerProjectID string `json:"former_project_id,omitempty"`
	FormerTaskID    string `json:"former_task_id,omitempty"`
	Title           string `json:"title"`
	Position        int64  `json:"position"`
	Theme           Theme  `json:"theme"`
	Status          string `json:"status"`
	Easy            bool   `json:"easy"`
	SnoozedUntil    int64  `json:"snoozed_until,omitempty"`
	Created         int64  `json:"created"`
	Updated         int64  `json:"updated"`
}

// Tables are the declarations this package owns; they match the persisted
// metadata of matomeru-old, so Declare accepts an existing database.
var Tables = []store.Table{
	{Name: "projects", Owner: "work", Key: "id", Version: 1},
	{Name: "tasks", Owner: "work", Key: "id", Version: 1},
	{Name: "launch_state", Owner: "work", Key: "id", Version: 1},
	{Name: "reviews", Owner: "work", Key: "id", Version: 1},
	{Name: "dir_recent", Owner: "work", Key: "path", Version: 1},
	{Name: "assignments", Owner: "work", Key: "window", Version: 1},
	{Name: "active", Owner: "work", Key: "id", Version: 1},
	{Name: "session_owners", Owner: "work", Key: "id", Version: 1},
	{Name: "notes", Owner: "notes", Key: "id", Version: 1},
	{Name: "saved_panes", Owner: "work", Key: "id", Version: 1},
	{Name: "pane_recipes", Owner: "work", Key: "window", Version: 1},
	{Name: "companions", Owner: "work", Key: "window", Version: 1},
	{Name: "vim_sessions", Owner: "vim", Key: "id", Version: 1},
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
	s := &Store{db: db}
	if err := s.importOwners(); err != nil {
		return nil, err
	}
	return s, nil
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
		t.Status = NormalStatus(t.Status)
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
	p := Project{ID: NewID(), Name: name, Status: "open", Created: Now()}
	return p, s.PutProject(p)
}

// PutTask writes a task row.
func (s *Store) PutTask(t Task) error {
	return s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error { return tx.Put("tasks", t.ID, t) })
}

// Task statuses this unit writes, in the order the task tables sort them.
// The database also holds values from matomeru-old: "open" reads as todo
// (and is written back as todo on the task's next change), "" reads as
// todo, and any other value (such as "skipped") is kept and shown as-is.
const (
	StatusActive  = "active"
	StatusTodo    = "todo"
	StatusBlocked = "blocked"
	StatusDone    = "done"
)

// Statuses are the statuses a task can be set to, in table order.
var Statuses = []string{StatusTodo, StatusActive, StatusBlocked, StatusDone}

// NormalStatus is a stored status as it reads: legacy "open" and "" are
// todo; every other value is kept.
func NormalStatus(status string) string {
	if status == "" || status == "open" {
		return StatusTodo
	}
	return status
}

// ValidStatus reports whether status is one a task can be set to.
func ValidStatus(status string) bool { return slices.Contains(Statuses, status) }

// StatusGlyph is a status's checkbox: [ ] todo, [>] active, [!] blocked,
// [x] done, [?] a value this unit does not write.
func StatusGlyph(status string) string {
	switch NormalStatus(status) {
	case StatusTodo:
		return "[ ]"
	case StatusActive:
		return "[>]"
	case StatusBlocked:
		return "[!]"
	case StatusDone:
		return "[x]"
	}
	return "[?]"
}

// StatusRank orders statuses for the task tables: active, todo, blocked,
// any other value, done.
func StatusRank(status string) int {
	switch NormalStatus(status) {
	case StatusActive:
		return 0
	case StatusTodo:
		return 1
	case StatusBlocked:
		return 2
	case StatusDone:
		return 4
	}
	return 3
}

// Task reads one task row by id, its status normalized (NormalStatus).
func (s *Store) Task(id string) (Task, error) {
	var t Task
	err := s.db.Get("tasks", id, &t)
	t.Status = NormalStatus(t.Status)
	return t, err
}

// AddTask creates a todo task at the end of a project's board.
func (s *Store) AddTask(projectID, title string) (Task, error) {
	title = strings.TrimSpace(title)
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
	return s.patchTask(id, func(t *Task) { t.Title = title })
}

// Done marks a task done.
func (s *Store) Done(id string) (Task, error) {
	return s.SetStatus(id, StatusDone)
}

// ToggleDone marks a task done, or a done task todo.
func (s *Store) ToggleDone(id string) (Task, error) {
	return s.patchTask(id, func(t *Task) {
		if t.Status == StatusDone {
			t.Status = StatusTodo
		} else {
			t.Status = StatusDone
		}
	})
}

// SetStatus sets a task's status to one of Statuses.
func (s *Store) SetStatus(id, status string) (Task, error) {
	if !ValidStatus(status) {
		return Task{}, fmt.Errorf("status %q is not one of %s", status, strings.Join(Statuses, ", "))
	}
	return s.patchTask(id, func(t *Task) { t.Status = status })
}

// RestoreStatus puts back a status the task had before, as it was: an
// undo restores a legacy or unknown value unchanged (record first).
func (s *Store) RestoreStatus(id, status string) (Task, error) {
	return s.patchTask(id, func(t *Task) { t.Status = status })
}

// AddSubtask creates a todo task under parent (a task of the same
// project, or "" for the top level), last among its siblings.
func (s *Store) AddSubtask(projectID, parentID, title string) (Task, error) {
	title = strings.TrimSpace(title)
	siblings, err := s.Tasks(projectID)
	if err != nil {
		return Task{}, err
	}
	var position int64
	for _, t := range siblings {
		if t.ProjectID == projectID && t.ParentID == parentID {
			position = max(position, t.Position)
		}
	}
	now := Now()
	t := Task{ID: NewID(), ProjectID: projectID, ParentID: parentID, Title: title, Position: position + 1, Status: StatusTodo, Created: now, Updated: now}
	return t, s.PutTask(t)
}

func (s *Store) patchTask(id string, change func(*Task)) (Task, error) {
	var t Task
	err := s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error {
		if err := tx.Get("tasks", id, &t); err != nil {
			return err
		}
		t.Status = NormalStatus(t.Status)
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
