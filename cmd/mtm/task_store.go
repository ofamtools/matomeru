package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
)

// The task noun's record side (standard verbs phase 1, lane M2): its props
// and its store, so task get, list, create, set (alias patch), watch,
// rename (the title), move (the project) and duplicate are ochazuke's
// standard verbs over the work store. task new and task edit wrap create
// and set (tasks.go); task.done and task.status are aliases of set with
// the status preset; toggle, choose-status, choose-move, open, view,
// launch, outline and make-project stay named. delete is the standard
// delete: the store's ConfirmFor counts what it orphans (delete.go).

// taskAssociateSince and taskAssociateRemoveAfter date the deprecated move
// form of task.associate (Keep actions: one release).
const (
	taskAssociateSince       = "mtm std-verbs-m2 (2026-10-29)"
	taskAssociateRemoveAfter = "the next mtm release"
)

// taskEditorProps are the props the task editor shows, in order (the
// editor's schema before props: the title and the status chooser).
var taskEditorProps = []string{"title", "status"}

// taskProps are the task's props: the key; the title (the label); the
// status, one of work.Statuses (a legacy value is read as it is); the
// project that owns the task (move writes it; create fills it from the
// focus) and the parent task (its project wins); easy and the snooze
// time; and the read-only board position and times.
func taskProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	integer := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "integer", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The task's key")},
		{Name: "title", Role: ochazuke.RoleLabel, Parameter: text("The task's title")},
		{Name: "status", Default: work.StatusTodo, Parameter: ochazuke.Parameter{Type: "string", Enum: work.Statuses,
			Description: "The task's status: todo, active, blocked or done (a value from an older mtm reads as it is)"}},
		{Name: "project", Role: ochazuke.RoleOwner, FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.list", Free: true,
			Description: "The project the task belongs to (empty: unassociated); one not found is saved unassociated with a notice; a move to another project takes the task to its top level, its subtasks with it"}},
		{Name: "parent", Parameter: ochazuke.Parameter{Type: "string", Noun: "task", Free: true,
			Description: "The task this one is under (empty: the top level); its project wins over project; one not found is left empty with a notice"}},
		{Name: "easy", Parameter: ochazuke.Parameter{Type: "boolean", Description: "Whether the task is marked easy"}},
		{Name: "snoozed-until", Parameter: integer("When the task's snooze ends (Unix ms; 0 for none)")},
		{Name: "position", ReadOnly: true, Parameter: integer("The task's place on its board, among its siblings")},
		{Name: "created", ReadOnly: true, Parameter: integer("When the task was created (Unix ms)")},
		{Name: "updated", ReadOnly: true, Parameter: integer("When the task last changed (Unix ms)")},
		{Name: "private", Parameter: ochazuke.Parameter{Type: "boolean",
			Description: "Keep the task and every task under it in mtm, inside a family project too (make-private, make-family); an agent may set it, only the owner clears it"}},
		{Name: "exported", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean",
			Description: "Whether the task may leave mtm: its project is family and neither it nor a task above it is private"}},
		{Name: "created-by", Parameter: ochazuke.Parameter{Type: "string",
			Description: "Who made the task, stamped once: owner, agent (with its harness, pane, session, lane task and model) or unknown (made before mtm recorded it); the owner may set it (owner, agent, unknown or an actor as JSON) to claim tasks"}},
		{Name: "changed-by", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string",
			Description: "Who wrote the task last: owner or agent (as created-by); empty before mtm recorded it"}},
		{Name: "made-by", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string", Enum: []string{work.ActorOwner, work.ActorAgent, work.ActorUnknown},
			Description: "created-by's kind, flat for --where made-by=agent: owner, agent or unknown"}},
	}
}

// taskStore is the task noun's store over the work store. history.undo may
// revert its writes (Undo; a move's undo puts back the project and the
// parent the move cleared), and a create may keep a key it is given
// (AcceptsKey).
func (a *App) taskStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true, AcceptsKey: true,
		// The standard delete's prompt names what the delete orphans.
		ConfirmFor: a.deletionConfirm("task"),
		ListParams: map[string]ochazuke.Parameter{"all": {Type: "boolean", Description: "Every task, not only those in the current scope"}},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			t, err := a.taskByID(id)
			if err != nil {
				return nil, err
			}
			return a.taskThingOf(t), nil
		},
		List: func(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			scope := *a
			if all, _ := q.Args["all"].(bool); all {
				scope.all = true
			}
			tasks, err := a.work.Tasks("")
			if err != nil {
				return nil, err
			}
			exported, err := a.work.Exports()
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, 0, len(tasks))
			for _, t := range tasks {
				if scope.inScope(t.ProjectID) {
					out = append(out, taskThing(t, exported[t.ID]))
				}
			}
			return out, nil
		},
		Create: a.createTask,
		Patch:  a.patchTask,
		Delete: a.removeTask,
	}
}

// taskByID is the stored task with that id, "no task" when there is none.
func (a *App) taskByID(id string) (work.Task, error) {
	t, err := a.work.Task(id)
	if errors.Is(err, store.ErrNotFound) {
		return work.Task{}, fmt.Errorf("no task %q", id)
	}
	return t, err
}

// taskThing is a task as its props; exported is whether it may leave mtm
// (work.Exports).
func taskThing(t work.Task, exported bool) ochazuke.Thing {
	thing := ochazuke.Thing{
		"id": t.ID, "title": t.Title, "status": t.Status, "project": t.ProjectID, "parent": t.ParentID,
		"easy": t.Easy, "snoozed-until": t.SnoozedUntil, "position": t.Position, "created": t.Created, "updated": t.Updated,
		"private": t.Private, "exported": exported, "created-by": t.Maker(), "made-by": t.MadeBy(), "changed-by": "",
	}
	if t.ChangedBy.Kind != "" {
		thing["changed-by"] = t.ChangedBy
	}
	return thing
}

// taskThingOf is taskThing for one task, its export read from the store.
func (a *App) taskThingOf(t work.Task) ochazuke.Thing {
	exported, _ := a.work.TaskExported(t.ID)
	return taskThing(t, exported)
}

// thingActor reads an actor a write gives: text (owner, agent, unknown,
// JSON) from the CLI, or the actor an undo's record holds.
func thingActor(v any) (work.Actor, error) {
	var text string
	switch x := v.(type) {
	case work.Actor:
		return x, nil
	case string:
		text = x
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return work.Actor{}, err
		}
		text = string(raw)
	}
	act, err := work.ParseActor(text)
	if err != nil {
		return work.Actor{}, &ochazuke.ArgumentError{Err: err}
	}
	return act, nil
}

// thingString reads a prop value of a write as text; ok is whether the
// write names the prop.
func thingString(values ochazuke.Thing, name string) (string, bool) {
	v, ok := values[name]
	if !ok || v == nil {
		return "", ok
	}
	return fmt.Sprint(v), true
}

// thingInt reads an integer prop value of a write (typed by the prop, or
// a number from an undo's record).
func thingInt(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(math.Round(n))
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
	}
	return 0
}

// taskOwner resolves a write's owner as resolveOwner does, never refusing
// (record first): a parent wins (its project is the task's); a parent not
// found leaves the task without one, with a notice, and the project is
// read as if no parent were named; a project not found is saved
// unassociated with a notice. Without a project, the task keeps keep. It
// answers the project and the parent.
func (a *App) taskOwner(c ochazuke.Ctx, project string, hasProject bool, parent, keep string) (string, string) {
	if parent != "" {
		t, err := a.work.Task(parent)
		switch {
		case err == nil:
			if hasProject && project != t.ProjectID {
				c.Notify("the parent's project overrides the project given")
			}
			return t.ProjectID, t.ID
		case errors.Is(err, store.ErrNotFound):
			c.Notify(fmt.Sprintf("unknown task %s; saved without a parent", parent))
		default:
			c.Notify(err.Error())
		}
	}
	switch {
	case !hasProject:
		return keep, ""
	case project == "":
		return "", ""
	}
	if _, ok := a.project(project); ok {
		return project, ""
	}
	c.Notify(fmt.Sprintf("unknown project %s; saved unassociated", project))
	return "", ""
}

// underTask reports whether task id is parent or one of its ancestors: a
// parent there would make a loop.
func (a *App) underTask(id, parent string) bool {
	seen := map[string]bool{}
	for cur := parent; cur != "" && !seen[cur]; {
		if cur == id {
			return true
		}
		seen[cur] = true
		t, err := a.work.Task(cur)
		if err != nil {
			return false
		}
		cur = t.ParentID
	}
	return false
}

// createTask is the store's Create: a new task from the props given, last
// on its board (among its siblings under a parent), under the key given
// when it is free.
func (a *App) createTask(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	id, _ := thingString(values, "id")
	if id != "" {
		if _, err := a.work.Task(id); err == nil {
			id = ""
		}
	}
	if id == "" {
		id = work.NewID()
	}
	now := work.Now()
	t := work.Task{ID: id, Status: work.StatusTodo, Created: now, Updated: now}
	project, hasProject := thingString(values, "project")
	parent, _ := thingString(values, "parent")
	t.ProjectID, t.ParentID = a.taskOwner(c, project, hasProject, parent, "")
	title, _ := thingString(values, "title")
	t.Title = strings.TrimSpace(title)
	if status, ok := thingString(values, "status"); ok && status != "" {
		t.Status = status
	}
	if easy, ok := values["easy"].(bool); ok {
		t.Easy = easy
	}
	t.SnoozedUntil = thingInt(values["snoozed-until"])
	if v, ok := values["private"].(bool); ok {
		t.Private = v
	}
	if v, ok := values["created-by"]; ok && v != nil {
		// The store keeps the maker it stamps unless the owner names one
		// (an undo of a delete puts the task back as it was).
		by, err := thingActor(v)
		if err != nil {
			return nil, err
		}
		t.CreatedBy = by
	}
	siblings, err := a.work.Tasks(t.ProjectID)
	if err != nil {
		return nil, err
	}
	for _, s := range siblings {
		if t.ParentID == "" || s.ParentID == t.ParentID {
			t.Position = max(t.Position, s.Position)
		}
	}
	t.Position++
	if err := a.work.PutTask(t); err != nil {
		return nil, err
	}
	if err := a.refreshTasks(); err != nil {
		return nil, err
	}
	if t, err = a.taskByID(t.ID); err != nil {
		return nil, err
	}
	return a.taskThingOf(t), nil
}

// patchTask is the store's Patch: it writes the changed props. A parent
// brings its project; a move to another project takes the task to its top
// level (the parent belonged to the old project) and its subtasks with it
// (work.PutTaskSubtree, one write); a parent that would be
// the task itself or under it is refused (a loop, not a missing owner). A
// status is written as given (an undo puts back a legacy value as it was;
// set has checked a new one). An editor open on the task writes its last
// change and closes first, so it never writes its old values over the
// patch.
func (a *App) patchTask(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	t, err := a.taskByID(id)
	if err != nil {
		return nil, err
	}
	if a.editor != nil && a.editor.Record.Noun == "task" && a.editor.Record.ID == id {
		if err := a.leaveEditor(); err != nil {
			return nil, err
		}
		if t, err = a.taskByID(id); err != nil {
			return nil, err
		}
	}
	if v, ok := thingString(changes, "title"); ok {
		t.Title = strings.TrimSpace(v)
	}
	if v, ok := thingString(changes, "status"); ok {
		t.Status = work.NormalStatus(v)
	}
	if v, ok := changes["easy"]; ok {
		t.Easy, _ = v.(bool)
	}
	if v, ok := changes["snoozed-until"]; ok {
		t.SnoozedUntil = thingInt(v)
	}
	if v, ok := changes["private"]; ok {
		t.Private, _ = v.(bool)
	}
	t.CreatedBy = work.Actor{} // the store keeps the stored maker
	if v, ok := changes["created-by"]; ok && v != nil {
		by, err := thingActor(v)
		if err != nil {
			return nil, err
		}
		t.CreatedBy = by // the owner's claim; an agent's is ignored (work.putTask)
	}
	project, hasProject := thingString(changes, "project")
	parent, hasParent := thingString(changes, "parent")
	owned := hasProject || hasParent
	if owned {
		if parent != "" && a.underTask(id, parent) {
			return nil, &ochazuke.ArgumentError{Err: fmt.Errorf("task %s cannot be under %s: it is that task or above it", id, parent)}
		}
		if hasProject && !hasParent && t.ParentID != "" && a.taskProject(t.ParentID) == project {
			parent = t.ParentID // the parent stays when it is in the new project
		}
		t.ProjectID, t.ParentID = a.taskOwner(c, project, hasProject, parent, t.ProjectID)
	}
	t.Updated = work.Now()
	put := a.work.PutTask
	if owned {
		// Its subtasks follow it to the project, in the same write, so the
		// undo (a patch back) takes them back too.
		put = func(t work.Task) error { _, err := a.work.PutTaskSubtree(t); return err }
	}
	if err := put(t); err != nil {
		return nil, err
	}
	if owned {
		err = a.refreshScope()
	} else {
		err = a.refreshTasks()
	}
	if err != nil {
		return nil, err
	}
	if t, err = a.taskByID(id); err != nil {
		return nil, err
	}
	return a.taskThingOf(t), nil
}

// removeTask is the store's Delete (task.delete, and history.undo of a
// create): the task goes and what it held is orphaned.
func (a *App) removeTask(c ochazuke.Ctx, id string) error {
	if _, err := a.taskByID(id); err != nil {
		return err
	}
	_, err := a.deleteThing(c, "task", id)
	return err
}

// taskFields are the task editor's fields, read from the task's props
// (taskEditorProps): the title and the status, both required (the status
// is chosen from the same list s offers, never free text).
func (a *App) taskFields() []ochazuke.RecordField {
	var out []ochazuke.RecordField
	for _, name := range taskEditorProps {
		p, ok := a.r.PropOf("task", name)
		if !ok {
			continue
		}
		f := ochazuke.PropField(p)
		f.Parameter.Required = true
		f.Parameter.Description = ""
		out = append(out, f)
	}
	return out
}
