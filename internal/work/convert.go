package work

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/unixsuperhero/matomeru/store"
)

// A project becomes a task of the project above it (DemoteProject), and a
// task directly under its project becomes a project (PromoteTask). Each is
// one store transaction that moves every reference before it removes the
// old record (record first: nothing is orphaned), and each returns the
// rows it wrote, before and after, so Undo and Redo put them back exactly:
// the same ids, the same owners. The shape follows matomeru-old's
// work.apply-tree-draft (load the rows, check, write only what changed, all
// in one transaction; a failure applies nothing), here over existing
// records and their owners instead of a draft of new rows.

// RowChange is one row a conversion wrote: its stored JSON before and
// after (nil when the row did not exist, or no longer does).
type RowChange struct {
	Table  string          `json:"table"`
	ID     string          `json:"id"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

// Conversion is one project-to-task or task-to-project change.
type Conversion struct {
	// Kind is "demote" (a project became a task) or "promote".
	Kind string `json:"kind"`
	// Project is the project the change is about: the parent project the
	// demoted project's task now lives in, or the promoted task's new
	// project.
	Project string `json:"project"`
	// Task is the demoted project's new task, or the promoted task.
	Task string `json:"task"`
	// From is the removed record: the demoted project, or the project the
	// promoted task came from ("" for an unassociated task).
	From string `json:"from"`
	// Directories are the directories the receiving project has after the
	// change that it did not have before.
	Directories []string    `json:"directories,omitempty"`
	Rows        []RowChange `json:"rows"`
}

// ErrNoProjectAbove refuses demoting the first project: there is no
// project above it to take it as a task.
var ErrNoProjectAbove = errors.New("the first project has no project above it to become a task of")

// ErrNotTopTask refuses promoting a task that is not directly under its
// project: a deeper task outdents one level instead.
var ErrNotTopTask = errors.New("only a task directly under its project becomes a project; a deeper task outdents one level")

// ownerTables are the tables whose rows name a project or task owner, in
// the order a conversion rewrites them. ext_rows rows name a project only.
var ownerTables = []string{"notes", "assignments", "session_owners", "reviews", "saved_panes", "pane_recipes", "vim_sessions", "ext_rows", "context_items", "action_items", "launch_state"}

// convertFault, when set (tests), fails a write so the rollback is seen.
var convertFault func(table, id string) error

type rowMap = map[string]json.RawMessage

type rowEntry struct {
	id     string
	before json.RawMessage
	row    rowMap
}

// convertTx is one conversion's transaction: the rows it read and the
// changes it wrote.
type convertTx struct {
	tx      *store.Tx
	changes []RowChange
}

func (c *convertTx) entries(table string) ([]rowEntry, error) {
	raws, err := c.tx.Entries(table)
	if err != nil {
		return nil, err
	}
	out := make([]rowEntry, 0, len(raws))
	for _, e := range raws {
		row := rowMap{}
		if err := json.Unmarshal(e.Row, &row); err != nil {
			return nil, fmt.Errorf("%s/%s: %w", table, e.ID, err)
		}
		out = append(out, rowEntry{id: e.ID, before: e.Row, row: row})
	}
	return out, nil
}

func (c *convertTx) put(table string, e rowEntry) error {
	after, err := json.Marshal(e.row)
	if err != nil {
		return err
	}
	if e.before != nil && sameJSON(e.before, after) {
		return nil
	}
	if convertFault != nil {
		if err := convertFault(table, e.id); err != nil {
			return err
		}
	}
	if err := c.tx.Put(table, e.id, json.RawMessage(after)); err != nil {
		return err
	}
	c.changes = append(c.changes, RowChange{Table: table, ID: e.id, Before: e.before, After: after})
	return nil
}

func (c *convertTx) delete(table string, e rowEntry) error {
	if convertFault != nil {
		if err := convertFault(table, e.id); err != nil {
			return err
		}
	}
	if err := c.tx.Delete(table, e.id); err != nil {
		return err
	}
	c.changes = append(c.changes, RowChange{Table: table, ID: e.id, Before: e.before})
	return nil
}

func str(row rowMap, key string) string {
	var v string
	if raw, ok := row[key]; ok {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

func setStr(row rowMap, key, value string) {
	raw, _ := json.Marshal(value)
	row[key] = raw
}

func sameJSON(a, b []byte) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// convertTables are the declared tables a conversion reads and writes
// (a unit that never opened, such as action items in a narrow test, has
// nothing to move).
func (s *Store) convertTables() []string {
	tables := []string{"projects", "tasks", "active"}
	for _, t := range append(slices.Clone(ownerTables), "history") {
		if s.db.Declared(t) {
			tables = append(tables, t)
		}
	}
	return tables
}

func (s *Store) convert(fn func(c *convertTx, declared map[string]bool) error) ([]RowChange, error) {
	tables := s.convertTables()
	declared := map[string]bool{}
	for _, t := range tables {
		declared[t] = true
	}
	var changes []RowChange
	err := s.db.Tx(tables, func(tx *store.Tx) error {
		c := &convertTx{tx: tx}
		if err := fn(c, declared); err != nil {
			return err
		}
		changes = c.changes
		return nil
	})
	return changes, err
}

// taskIndex is the tasks table read for a conversion.
type taskIndex struct {
	entries []rowEntry
	tasks   map[string]Task
}

func (c *convertTx) taskIndex() (taskIndex, error) {
	entries, err := c.entries("tasks")
	if err != nil {
		return taskIndex{}, err
	}
	idx := taskIndex{entries: entries, tasks: map[string]Task{}}
	for _, e := range entries {
		var t Task
		raw, _ := json.Marshal(e.row)
		if err := json.Unmarshal(raw, &t); err != nil {
			return taskIndex{}, err
		}
		idx.tasks[e.id] = t
	}
	return idx, nil
}

// projectOf follows the parent chain to the task's project (old subtasks
// stored none of their own).
func (x taskIndex) projectOf(id string) string {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		t, ok := x.tasks[id]
		if !ok {
			return ""
		}
		if t.ProjectID != "" {
			return t.ProjectID
		}
		id = t.ParentID
	}
	return ""
}

// topLevel reports whether a task is directly under its project: no
// parent, or a parent that is not a stored task.
func (x taskIndex) topLevel(t Task) bool {
	_, ok := x.tasks[t.ParentID]
	return t.ParentID == "" || !ok
}

func sortedProjects(entries []rowEntry) ([]Project, map[string]rowEntry, error) {
	byID := map[string]rowEntry{}
	out := make([]Project, 0, len(entries))
	for _, e := range entries {
		var p Project
		raw, _ := json.Marshal(e.row)
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, nil, err
		}
		p.ID = e.id
		out = append(out, p)
		byID[e.id] = e
	}
	slices.SortFunc(out, func(a, b Project) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	return out, byID, nil
}

// DemoteProject turns a project into a task of parent ("" is the project
// listed above it): the task, named as the project was, goes last at the
// parent's top level; the project's tasks become its subtasks in their
// order and nesting; notes, context items, inbox items, reviews, saved
// panes, Vim sessions and pane and session owners that named the project
// alone name {parent, the new task}, and those that named one of its tasks
// name the parent with that task; its directories join the parent's
// (identical paths once; the parent's default stays, or with none the
// project's becomes it); a pin on it moves to the parent; then the project
// row is deleted. One transaction: a failure writes nothing.
func (s *Store) DemoteProject(id, parent string) (Conversion, error) {
	conv := Conversion{Kind: "demote", From: id}
	changes, err := s.convert(func(c *convertTx, declared map[string]bool) error {
		projectRows, err := c.entries("projects")
		if err != nil {
			return err
		}
		projects, byID, err := sortedProjects(projectRows)
		if err != nil {
			return err
		}
		at := slices.IndexFunc(projects, func(p Project) bool { return p.ID == id })
		if at < 0 {
			return fmt.Errorf("unknown project %s", id)
		}
		if parent == "" {
			if at == 0 {
				return ErrNoProjectAbove
			}
			parent = projects[at-1].ID
		}
		if parent == id {
			return errors.New("a project cannot become a task of itself")
		}
		if _, ok := byID[parent]; !ok {
			return fmt.Errorf("unknown project %s", parent)
		}
		conv.Project = parent
		source, target := projects[at], byID[parent]
		idx, err := c.taskIndex()
		if err != nil {
			return err
		}
		inSource := map[string]bool{}
		var position int64
		for tid, t := range idx.tasks {
			switch idx.projectOf(tid) {
			case id:
				inSource[tid] = true
			case parent:
				if idx.topLevel(t) {
					position = max(position, t.Position)
				}
			}
		}
		now := Now()
		task := Task{ID: NewID(), ProjectID: parent, Title: source.Name, Theme: source.Theme, Position: position + 1, Status: StatusTodo, Created: now, Updated: now}
		conv.Task = task.ID
		raw, _ := json.Marshal(task)
		newRow := rowMap{}
		_ = json.Unmarshal(raw, &newRow)
		if err := c.put("tasks", rowEntry{id: task.ID, row: newRow}); err != nil {
			return err
		}
		for _, e := range idx.entries {
			if !inSource[e.id] {
				continue
			}
			t := idx.tasks[e.id]
			if str(e.row, "project_id") != "" {
				setStr(e.row, "project_id", parent)
			}
			if idx.topLevel(t) {
				setStr(e.row, "project_id", parent)
				setStr(e.row, "parent_id", task.ID)
			}
			if err := c.put("tasks", e); err != nil {
				return err
			}
		}
		for _, table := range ownerTables {
			if !declared[table] {
				continue
			}
			rows, err := c.entries(table)
			if err != nil {
				return err
			}
			for _, e := range rows {
				project, owner := str(e.row, "project_id"), str(e.row, "task_id")
				switch {
				case owner != "" && inSource[owner]:
					if project == id {
						setStr(e.row, "project_id", parent)
					}
				case project == id:
					setStr(e.row, "project_id", parent)
					if table != "ext_rows" {
						setStr(e.row, "task_id", task.ID)
					}
				default:
					continue
				}
				if err := c.put(table, e); err != nil {
					return err
				}
			}
		}
		if err := movePin(c, id, parent); err != nil {
			return err
		}
		merged, added := mergeTrees(target, source)
		conv.Directories = added
		if err := c.put("projects", merged); err != nil {
			return err
		}
		return c.delete("projects", byID[id])
	})
	conv.Rows = changes
	return conv, err
}

// movePin moves a pin on from to to, its pinned time with it.
func movePin(c *convertTx, from, to string) error {
	raw, err := c.tx.Raw("active", "singleton")
	if err != nil || raw == nil {
		return err
	}
	row := rowMap{}
	if err := json.Unmarshal(raw, &row); err != nil {
		return err
	}
	changed := false
	if str(row, "project_id") == from {
		setStr(row, "project_id", to)
		changed = true
	}
	pinned := map[string]int64{}
	if p, ok := row["pinned_at"]; ok {
		_ = json.Unmarshal(p, &pinned)
	}
	if at, ok := pinned[from]; ok {
		pinned[to] = max(pinned[to], at)
		delete(pinned, from)
		row["pinned_at"], _ = json.Marshal(pinned)
		changed = true
	}
	if !changed {
		return nil
	}
	return c.put("active", rowEntry{id: "singleton", before: raw, row: row})
}

// mergeTrees adds source's directories to target's, identical paths once;
// target's default stays (with none, source's default becomes it). It
// returns target's row and the paths it gained.
func mergeTrees(target rowEntry, source Project) (rowEntry, []string) {
	var trees []Tree
	if raw, ok := target.row["trees"]; ok {
		_ = json.Unmarshal(raw, &trees)
	}
	have := map[string]bool{}
	for _, t := range trees {
		have[filepath.Clean(t.Path)] = true
	}
	var added []string
	for _, t := range source.Trees {
		path := filepath.Clean(t.Path)
		if t.Path == "" || have[path] {
			continue
		}
		have[path] = true
		t.Path = path
		trees = append(trees, t)
		added = append(added, path)
	}
	if len(added) == 0 {
		return target, nil
	}
	target.row["trees"], _ = json.Marshal(trees)
	if str(target.row, "default_tree") == "" && source.DefaultTree != "" {
		setStr(target.row, "default_tree", filepath.Clean(source.DefaultTree))
	}
	if str(target.row, "repo") == "" && source.Repo != "" {
		setStr(target.row, "repo", source.Repo)
	}
	return target, added
}

// PromoteTask turns a task directly under its project (or an unassociated
// top-level task) into a project named as the task was, listed right
// after the project it came from (last for an unassociated task). Its
// subtasks become the project's tasks, in their order and nesting; rows
// owned by the task (notes, context items, inbox items, reviews, saved
// panes, Vim sessions, pane and session owners) name the new project
// alone, rows owned by a subtask name the new project with that subtask.
// Every directory the store associates with the task or its subtasks
// becomes a directory of the new project (TaskDirectories), the first
// one its default. The project it came from keeps its own directories.
// Then the task row is deleted. One transaction: a failure writes nothing.
func (s *Store) PromoteTask(id string) (Conversion, error) {
	conv := Conversion{Kind: "promote", Task: id}
	changes, err := s.convert(func(c *convertTx, declared map[string]bool) error {
		idx, err := c.taskIndex()
		if err != nil {
			return err
		}
		task, ok := idx.tasks[id]
		if !ok {
			return fmt.Errorf("unknown task %s", id)
		}
		if !idx.topLevel(task) {
			return ErrNotTopTask
		}
		from := idx.projectOf(id)
		conv.From = from
		// The task's subtree, the task first.
		subtree := map[string]bool{id: true}
		for grew := true; grew; {
			grew = false
			for tid, t := range idx.tasks {
				if !subtree[tid] && subtree[t.ParentID] {
					subtree[tid] = true
					grew = true
				}
			}
		}
		projectRows, err := c.entries("projects")
		if err != nil {
			return err
		}
		projects, _, err := sortedProjects(projectRows)
		if err != nil {
			return err
		}
		dirs, err := taskDirectories(c, declared, idx, id, subtree)
		if err != nil {
			return err
		}
		p := Project{ID: NewID(), Name: task.Title, Theme: task.Theme, Status: "open", Trees: []Tree{}}
		p.Created = placeAfter(&p, projects, from)
		for _, d := range dirs {
			p.Trees = append(p.Trees, Tree{Kind: "directory", Path: d})
		}
		if len(dirs) > 0 {
			p.DefaultTree = dirs[0]
		}
		conv.Project, conv.Directories = p.ID, dirs
		raw, _ := json.Marshal(p)
		newRow := rowMap{}
		_ = json.Unmarshal(raw, &newRow)
		if err := c.put("projects", rowEntry{id: p.ID, row: newRow}); err != nil {
			return err
		}
		var self rowEntry
		for _, e := range idx.entries {
			if e.id == id {
				self = e
				continue
			}
			if !subtree[e.id] {
				continue
			}
			setStr(e.row, "project_id", p.ID)
			if str(e.row, "parent_id") == id {
				setStr(e.row, "parent_id", "")
			}
			if err := c.put("tasks", e); err != nil {
				return err
			}
		}
		for _, table := range ownerTables {
			if !declared[table] {
				continue
			}
			rows, err := c.entries(table)
			if err != nil {
				return err
			}
			for _, e := range rows {
				owner := str(e.row, "task_id")
				switch {
				case owner == id:
					setStr(e.row, "project_id", p.ID)
					setStr(e.row, "task_id", "")
				case owner != "" && subtree[owner]:
					setStr(e.row, "project_id", p.ID)
				default:
					continue
				}
				if err := c.put(table, e); err != nil {
					return err
				}
			}
		}
		return c.delete("tasks", self)
	})
	conv.Rows = changes
	return conv, err
}

// placeAfter is the creation time that lists p right after the project
// from in the projects' order (creation time, then id): one millisecond
// after it when that is free, else the same time with an id that sorts
// after it; last for no project.
func placeAfter(p *Project, projects []Project, from string) int64 {
	at := slices.IndexFunc(projects, func(q Project) bool { return q.ID == from })
	now := Now()
	switch {
	case len(projects) == 0:
		return now
	case at < 0:
		return max(now, projects[len(projects)-1].Created+1)
	case at == len(projects)-1:
		return max(now, projects[at].Created+1)
	}
	prev, next := projects[at], projects[at+1]
	if next.Created > prev.Created+1 {
		return prev.Created + 1
	}
	for range 4096 {
		if p.ID > prev.ID && (next.Created > prev.Created || p.ID < next.ID) {
			break
		}
		p.ID = NewID()
	}
	return prev.Created
}

// taskDirectories are the directories the store associates with a task
// and its subtasks, most specific first, each once (cleaned): the launch
// directories of the panes they own (pane recipes, by the pane's owner or
// the recipe's own), saved panes', Vim sessions', their context items of
// kind dir, the working directories those context items were seen in,
// then the working directories of their journal entries. Within each
// source the task's own rows come before its subtasks', newest first.
// Tasks have no directory field of their own.
func taskDirectories(c *convertTx, declared map[string]bool, idx taskIndex, id string, subtree map[string]bool) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	rank := func(owner string) int {
		if owner == id {
			return 0
		}
		return 1
	}
	type found struct {
		rank int
		at   int64
		dirs []string
	}
	collect := func(table string, pick func(e rowEntry) (owner string, at int64, dirs []string)) error {
		if !declared[table] {
			return nil
		}
		rows, err := c.entries(table)
		if err != nil {
			return err
		}
		var hits []found
		for _, e := range rows {
			owner, at, dirs := pick(e)
			if owner == "" || !subtree[owner] {
				continue
			}
			hits = append(hits, found{rank(owner), at, dirs})
		}
		slices.SortStableFunc(hits, func(a, b found) int { return cmp.Or(cmp.Compare(a.rank, b.rank), cmp.Compare(b.at, a.at)) })
		for _, h := range hits {
			for _, d := range h.dirs {
				add(d)
			}
		}
		return nil
	}
	num := func(row rowMap, keys ...string) int64 {
		for _, k := range keys {
			var v int64
			if raw, ok := row[k]; ok && json.Unmarshal(raw, &v) == nil && v != 0 {
				return v
			}
		}
		return 0
	}
	// Panes a task owns: its assignments (the owner of record), and
	// recipes that name the task themselves.
	paneOwner := map[string]string{}
	if declared["assignments"] {
		rows, err := c.entries("assignments")
		if err != nil {
			return nil, err
		}
		for _, e := range rows {
			paneOwner[e.id] = str(e.row, "task_id")
		}
	}
	if err := collect("pane_recipes", func(e rowEntry) (string, int64, []string) {
		owner, ok := paneOwner[e.id]
		if !ok || !subtree[owner] {
			owner = str(e.row, "task_id")
		}
		return owner, num(e.row, "updated", "created"), []string{str(e.row, "cwd")}
	}); err != nil {
		return nil, err
	}
	for _, table := range []string{"saved_panes", "vim_sessions"} {
		if err := collect(table, func(e rowEntry) (string, int64, []string) {
			return str(e.row, "task_id"), num(e.row, "updated", "created"), []string{str(e.row, "cwd")}
		}); err != nil {
			return nil, err
		}
	}
	if err := collect("context_items", func(e rowEntry) (string, int64, []string) {
		if str(e.row, "kind") != "dir" {
			return "", 0, nil
		}
		return str(e.row, "task_id"), num(e.row, "last_seen", "created"), []string{str(e.row, "text")}
	}); err != nil {
		return nil, err
	}
	if err := collect("context_items", func(e rowEntry) (string, int64, []string) {
		var sources []struct {
			Cwd string `json:"cwd"`
		}
		if raw, ok := e.row["sources"]; ok {
			_ = json.Unmarshal(raw, &sources)
		}
		dirs := make([]string, 0, len(sources))
		for i := len(sources) - 1; i >= 0; i-- {
			dirs = append(dirs, sources[i].Cwd)
		}
		return str(e.row, "task_id"), num(e.row, "last_seen", "created"), dirs
	}); err != nil {
		return nil, err
	}
	if err := collect("history", func(e rowEntry) (string, int64, []string) {
		owner := str(e.row, "task_id")
		if owner == "" {
			var prov struct {
				TaskID string `json:"task_id"`
			}
			if raw, ok := e.row["provenance"]; ok {
				_ = json.Unmarshal(raw, &prov)
			}
			owner = prov.TaskID
		}
		return owner, num(e.row, "at", "observed"), []string{str(e.row, "cwd")}
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// Undo puts back every row a conversion wrote as it was before; Redo
// writes them again as the conversion left them. A row deleted or created
// is restored or removed whole (the same id); a row changed has only the
// fields the conversion changed put back, so a later edit to another
// field of it (a note's text, an item's last seen) is kept. One
// transaction each.
func (s *Store) Undo(c Conversion) error { return s.replay(c.Rows, true) }

// Redo writes a conversion's rows again (Undo's inverse).
func (s *Store) Redo(c Conversion) error { return s.replay(c.Rows, false) }

func (s *Store) replay(rows []RowChange, back bool) error {
	var tables []string
	for _, r := range rows {
		if !slices.Contains(tables, r.Table) {
			tables = append(tables, r.Table)
		}
	}
	return s.db.Tx(tables, func(tx *store.Tx) error {
		order := slices.Clone(rows)
		if back {
			slices.Reverse(order)
		}
		for _, r := range order {
			from, to := r.Before, r.After
			if back {
				from, to = r.After, r.Before
			}
			if to == nil {
				if err := tx.Delete(r.Table, r.ID); err != nil {
					return err
				}
				continue
			}
			current, err := tx.Raw(r.Table, r.ID)
			if err != nil {
				return err
			}
			if from == nil || current == nil {
				if err := tx.Put(r.Table, r.ID, to); err != nil {
					return err
				}
				continue
			}
			merged, err := mergeFields(current, from, to)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", r.Table, r.ID, err)
			}
			if err := tx.Put(r.Table, r.ID, merged); err != nil {
				return err
			}
		}
		return nil
	})
}

// mergeFields sets, on current, every top-level field that differs
// between from and to to its value in to (removing it when to lacks it).
func mergeFields(current, from, to json.RawMessage) (json.RawMessage, error) {
	var cur, f, t rowMap
	for _, x := range []struct {
		raw json.RawMessage
		dst *rowMap
	}{{current, &cur}, {from, &f}, {to, &t}} {
		if err := json.Unmarshal(x.raw, x.dst); err != nil {
			return nil, err
		}
	}
	keys := map[string]bool{}
	for k := range f {
		keys[k] = true
	}
	for k := range t {
		keys[k] = true
	}
	for k := range keys {
		fv, fok := f[k]
		tv, tok := t[k]
		if fok == tok && (!fok || sameJSON(fv, tv)) {
			continue
		}
		if tok {
			cur[k] = tv
		} else {
			delete(cur, k)
		}
	}
	return json.Marshal(cur)
}
