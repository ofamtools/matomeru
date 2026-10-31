package work

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/unixsuperhero/matomeru/store"
)

// A task's subtasks belong to its project: a move to another project
// (task.move, task set project= or parent=, m, the outline's move across a
// project row, AssociateTask) writes the task and gives every task under
// it the same project in one transaction, and an undo of the move (which
// writes the task's old project back through the same path) takes them
// back. Their parents stay. The subtree walk is the one make-project uses
// (convert.go) and tuios-deck's subtree (internal/ui/tasks.go).

// taskForest is every task row by id with its children in board order.
type taskForest struct {
	tasks    map[string]Task
	children map[string][]string
}

func newTaskForest(rows []Task) taskForest {
	f := taskForest{tasks: make(map[string]Task, len(rows)), children: map[string][]string{}}
	slices.SortFunc(rows, func(a, b Task) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	for _, t := range rows {
		f.tasks[t.ID] = t
	}
	for _, t := range rows {
		if t.ParentID != "" && t.ParentID != t.ID {
			f.children[t.ParentID] = append(f.children[t.ParentID], t.ID)
		}
	}
	return f
}

// below is every task under id, level by level, each level in board
// order; a parent loop stops at the first task seen again.
func (f taskForest) below(id string) []string {
	seen := map[string]bool{id: true}
	var out []string
	for level := []string{id}; len(level) > 0; {
		var next []string
		for _, p := range level {
			for _, c := range f.children[p] {
				if !seen[c] {
					seen[c] = true
					out = append(out, c)
					next = append(next, c)
				}
			}
		}
		level = next
	}
	return out
}

func txTasks(tx *store.Tx) (taskForest, error) {
	raws, err := tx.All("tasks")
	if err != nil {
		return taskForest{}, err
	}
	rows := make([]Task, 0, len(raws))
	for _, raw := range raws {
		var t Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return taskForest{}, err
		}
		rows = append(rows, t)
	}
	return newTaskForest(rows), nil
}

// putTree writes t and gives every task under it t's project; it answers
// how many tasks under t it changed.
func (s *Store) putTree(tx *store.Tx, t Task) (int, error) {
	if err := s.putTask(tx, &t); err != nil {
		return 0, err
	}
	f, err := txTasks(tx)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, id := range f.below(t.ID) {
		d := f.tasks[id]
		if d.ProjectID == t.ProjectID {
			continue
		}
		d.ProjectID, d.Updated = t.ProjectID, t.Updated
		if err := s.putTask(tx, &d); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// Subtree is the task and every task under it: the task first, then each
// level in board order (statuses normalized).
func (s *Store) Subtree(id string) ([]Task, error) {
	rows, err := all[Task](s.db, "tasks")
	if err != nil {
		return nil, err
	}
	f := newTaskForest(rows)
	root, ok := f.tasks[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	out := []Task{root}
	for _, d := range f.below(id) {
		out = append(out, f.tasks[d])
	}
	for i := range out {
		out[i].Status = NormalStatus(out[i].Status)
	}
	return out, nil
}

// PutTaskSubtree writes t as PutTask does and gives every task under it
// t's project, in one transaction (one write, so one undo of the move
// restores them all). It answers how many tasks under t changed project.
func (s *Store) PutTaskSubtree(t Task) (int, error) {
	var moved int
	err := s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error {
		var err error
		moved, err = s.putTree(tx, t)
		return err
	})
	return moved, err
}

// MoveSubtree moves a task and every task under it to project ("" is
// unassociated, record first) in one transaction. The task keeps its
// parent when the parent is in project, else it goes to the project's top
// level. It answers the task as written and how many tasks under it
// changed project.
func (s *Store) MoveSubtree(id, project string) (Task, int, error) {
	return s.moveSubtree(id, project, false)
}

func (s *Store) moveSubtree(id, project string, detach bool) (Task, int, error) {
	var t Task
	var moved int
	err := s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error {
		if err := tx.Get("tasks", id, &t); err != nil {
			return err
		}
		if t.ParentID != "" {
			var p Task
			err := tx.Get("tasks", t.ParentID, &p)
			if detach || err != nil || p.ProjectID != project {
				t.ParentID = ""
			}
		}
		t.ProjectID, t.Updated = project, Now()
		var err error
		moved, err = s.putTree(tx, t)
		return err
	})
	t.Status = NormalStatus(t.Status)
	return t, moved, err
}

// RepairSubtreeProjects gives every task its top task's project (the top
// task: the first one up the parents with no parent, or whose parent is
// missing), so subtasks a move left behind (a move before subtasks
// followed it, or a write by an older mtm) rejoin their parent. A top
// task without a project stays unassociated, and so do the tasks under
// it. A parent loop with no top task is left as it is. It is idempotent
// and writes nothing when every task already agrees, and it answers how
// many tasks it moved.
func (s *Store) RepairSubtreeProjects() (int, error) {
	rows, err := all[Task](s.db, "tasks")
	if err != nil {
		return 0, err
	}
	if len(strayTasks(newTaskForest(rows))) == 0 {
		return 0, nil // the usual case: no write, no lock
	}
	moved := 0
	err = s.db.Tx([]string{"tasks"}, func(tx *store.Tx) error {
		f, err := txTasks(tx)
		if err != nil {
			return err
		}
		now := Now()
		for _, d := range strayTasks(f) {
			d.Updated = now
			if err := tx.Put("tasks", d.ID, d); err != nil {
				return err
			}
			moved++
		}
		return nil
	})
	return moved, err
}

// strayTasks are the tasks whose project differs from their top task's,
// each with the top task's project set.
func strayTasks(f taskForest) []Task {
	var out []Task
	ids := make([]string, 0, len(f.tasks))
	for id := range f.tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		t := f.tasks[id]
		if _, ok := f.tasks[t.ParentID]; ok && t.ParentID != t.ID {
			continue // not a top task
		}
		for _, d := range f.below(id) {
			if task := f.tasks[d]; task.ProjectID != t.ProjectID {
				task.ProjectID = t.ProjectID
				out = append(out, task)
			}
		}
	}
	return out
}
