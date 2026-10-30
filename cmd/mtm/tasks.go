package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) defineTask() {
	r := a.r
	// Props and the store first: the standard verbs (get, list, create,
	// set, watch, rename, move, duplicate) come from them (task_store.go);
	// new and edit wrap create and set; done and status are aliases of
	// set; the rest are mtm's own.
	r.Define("task").Props(taskProps()...).Store(a.taskStore()).Columns(taskColumns...).
		Verb("open", a.openTask, "View task without editing").
		Verb("view", a.openTask, "View task without editing").
		Verb("edit", a.editTask, "Edit title and status (standard set of those given); omit arguments for the editor").Positional("title").
		Verb("toggle", a.toggleTask, "Mark the task done, or a done task todo (standard set of status)").
		Verb("choose-status", a.chooseStatus, "Choose the task's status: todo, active, blocked or done; over a visual selection one choice sets every task (standard set of status)").Many().
		Verb("choose-move", a.chooseMove, "Choose the project to move the task to (most recent first, unassociated last); the choice runs task.move").Many().
		Verb("new", a.newTask, "Add a task (standard create: under --parent, else the pin or no project); omit title to open the draft editor").Positional("title").
		Verb("launch", a.launchRecord, "Launch an agent on the task").
		Mode("normal").
		Bind("enter", ochazuke.Do("task.edit")).Bind("l", ochazuke.Do("task.open")).Bind("L", ochazuke.Do("task.launch")).
		Bind("i", ochazuke.Do("task.edit")).Bind("x", ochazuke.Do("task.toggle")).Bind("s", ochazuke.Do("task.choose-status")).
		Bind("m", ochazuke.Do("task.choose-move"))
	// done and status are the standard set with the status preset (2.5:
	// task.status shares the prop's name as its set); the old arguments
	// are set's: task done ID, task status ID STATUS. The old --status
	// still sets it, with a one-time notice (ochazuke.ParamAlias; Keep
	// actions: seed scripts run task status --id T --status X).
	for _, alias := range []ochazuke.ActionAlias{
		{Alias: "task.done", Action: "task.set", Args: ochazuke.Arguments{"prop": "status", "value": work.StatusDone},
			Note: "the standard set of status with done"},
		{Alias: "task.status", Action: "task.set", Args: ochazuke.Arguments{"prop": "status"},
			Note:         "the standard set of status: task status ID STATUS; s (task.choose-status) chooses one (--status before " + taskStatusSince + ")",
			ParamAliases: []ochazuke.ParamAlias{{Alias: "status", Param: "value", Since: taskStatusSince, RemoveAfter: statusFlagRemoveAfter}}},
	} {
		if err := r.AddActionAlias(alias); err != nil {
			panic(err)
		}
	}
	a.parameters("task.edit", map[string]ochazuke.Parameter{"title": {Type: "string"}, "status": {Type: "string", Enum: work.Statuses}})
	a.parameters("task.new", map[string]ochazuke.Parameter{
		"title": {Type: "string"}, "project": {Type: "string"}, "task": {Type: "string"}, "scratch": {Type: "boolean"}, "parent": {Type: "string"},
	})
	a.editorFields["task"] = a.taskFields
}

func (a *App) bindTaskStop() error {
	row := ochazuke.Do("task.new")
	row.Instance, row.Mode, row.Keys = "tasks", "normal", "n"
	return a.r.Bind(row)
}

func (a *App) refreshTasks() error {
	var tasks []work.Task
	if a.tasksOpen {
		project := a.current
		if a.all {
			project = ""
		}
		rows, err := a.work.Tasks(project)
		if err != nil {
			return err
		}
		// The tasks follow the selected project row whatever the pin: the
		// projects list shows every project, and selecting one shows its
		// tasks without pinning it (the "no pin" rule, 2026-10-26).
		for _, t := range rows {
			if a.all || t.ProjectID == a.current {
				tasks = append(tasks, t)
			}
		}
	}
	// Active first, then todo, blocked, other values and done; board
	// order within a status.
	slices.SortStableFunc(tasks, func(x, y work.Task) int { return work.StatusRank(x.Status) - work.StatusRank(y.Status) })
	a.tasks = tasks
	drafts, err := a.draftRecords("task")
	if err != nil {
		return err
	}
	marked := map[string]bool{}
	for _, d := range drafts {
		marked[d.ID] = true
	}
	// The project column shows only when the rows can belong to more than
	// one project (all projects).
	cols := taskColumns
	if !a.all {
		cols = slices.DeleteFunc(slices.Clone(taskColumns), func(c ochazuke.Column) bool { return c.Title == "project" })
	}
	a.taskList.SetColumns(cols...)
	now := time.Now()
	items := make([]ochazuke.ListItem, 0, len(tasks))
	for _, t := range tasks {
		label := taskLabel(t)
		if a.all || a.pin == "" {
			label += " · " + a.ownerLabel(t.ProjectID)
		}
		if marked[t.ID] {
			label += " [draft]"
		}
		items = append(items, ochazuke.ListItem{ID: t.ID, Label: label, Noun: "task", Cells: a.taskCells(t, marked[t.ID], now)})
	}
	for _, d := range drafts {
		if d.New && a.tasksOpen && (a.all || d.ProjectID == a.current) && a.inScope(d.ProjectID) {
			items = append(items, ochazuke.ListItem{ID: d.ID, Label: "untitled [draft]", Noun: "draft", Data: d, Cells: []string{"[ ] untitled [draft]"}})
		}
	}
	return a.taskList.SetItems(items)
}

// taskColumns are the task noun's table: the checkbox and title (with a
// draft mark), the status, the project (all projects only) and the last
// change; narrow, the change drops first, then the project.
var taskColumns = []ochazuke.Column{
	{Title: "task", Min: 24},
	{Title: "status", Priority: 1},
	{Title: "project", Priority: 2},
	{Title: "updated", Priority: 3},
}

// taskCells are a task row's cells in taskColumns order, the project
// column left out unless the list shows all projects.
func (a *App) taskCells(t work.Task, draft bool, now time.Time) []string {
	box := work.StatusGlyph(t.Status) + " "
	title := t.Title
	if title == "" {
		title = "untitled"
	}
	if draft {
		title += " [draft]"
	}
	status := work.NormalStatus(t.Status)
	updated := ""
	if t.Updated != 0 {
		updated = sinceUse(t.Updated, now)
	}
	if !a.all {
		return []string{box + title, status, updated}
	}
	return []string{box + title, status, a.ownerLabel(t.ProjectID), updated}
}

func taskLabel(t work.Task) string {
	box := work.StatusGlyph(t.Status) + " "
	title := t.Title
	if title == "" {
		title = "untitled"
	}
	label := box + title
	if t.ProjectID == "" {
		label += " (unassociated)"
	}
	if !work.ValidStatus(work.NormalStatus(t.Status)) {
		label += "  (" + t.Status + ")"
	}
	return label
}

func (a *App) task(ctx ochazuke.Ctx) (work.Task, error) {
	if ctx.Instance == nil || ctx.Instance.Noun != "task" {
		return work.Task{}, errors.New("no task is selected")
	}
	t, err := a.work.Task(ctx.Instance.ID)
	if err != nil {
		return work.Task{}, fmt.Errorf("task %s: %w", ctx.Instance.ID, err)
	}
	return t, nil
}

func (a *App) openTask(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.leaveEditor(); err != nil {
		return ochazuke.Failed(err)
	}
	a.detailTask = t.ID
	return ochazuke.Done(t)
}

// editTask is task.edit: without arguments it opens the editor on the
// task; with title or status it is the standard set of those given.
func (a *App) editTask(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if len(ctx.Args) == 0 {
		return resultError(a.beginEdit(editRecord{Noun: "task", ID: t.ID, Text: t.Title, ProjectID: t.ProjectID, Fields: map[string]string{"status": t.Status}}))
	}
	changes := map[string]any{}
	for _, name := range []string{"title", "status"} {
		if _, present := ctx.Args[name]; present {
			changes[name] = ochazuke.StringArg(ctx.Args, name, "")
		}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "task", Verb: "set", Args: ochazuke.Arguments{"id": t.ID, "changes": changes}}))
}

// toggleTask is task.toggle (x): the standard set of status, done for a
// task that is not, todo for a done one.
func (a *App) toggleTask(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	status := work.StatusDone
	if t.Status == work.StatusDone {
		status = work.StatusTodo
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "task", Verb: "set", Args: ochazuke.Arguments{"id": t.ID, "prop": "status", "value": status}}))
}

// chooseStatus is task.choose-status (s): a chooser of the four statuses
// on the task, or one for every task of a visual selection; the choice is
// the standard set of status over them (one undo group).
func (a *App) chooseStatus(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	var subjects []string
	if len(ctx.Subjects) > 1 {
		subjects = ctx.SubjectIDs()
	}
	return resultError(a.openStatusChooserFor(t, max(len(subjects), 1), func(status string) ochazuke.Result {
		q := ochazuke.Request{Noun: "task", Verb: "set", Target: ctx.Instance.ID, Subjects: subjects, Args: ochazuke.Arguments{"prop": "status", "value": status}}
		if subjects == nil {
			q.Args["id"] = t.ID
		}
		return resultOf(a.r.Execute(q))
	}))
}

// statusChooser is the status chooser's modal kind.
const statusChooser = "status-chooser"

// openStatusChooser opens the four statuses for t; typing narrows by the
// first letters (t, a, b, d) and enter sets the top one, then refresh runs.
func (a *App) openStatusChooser(t work.Task, refresh func() error) error {
	return a.openStatusChooserFor(t, 1, func(status string) ochazuke.Result {
		got, err := a.work.SetStatus(t.ID, status)
		if err != nil {
			return ochazuke.Failed(err)
		}
		if err := refresh(); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(got)
	})
}

// resultOf is an Execute's answer as a Result.
func resultOf(v any, err error) ochazuke.Result {
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(v)
}

// openStatusChooserFor opens the four statuses for t (n tasks with it:
// the title counts them, no status is current) and hands the chosen one
// to set.
func (a *App) openStatusChooserFor(t work.Task, n int, set func(status string) ochazuke.Result) error {
	rows := make([]ochazuke.ChooserRow, 0, len(work.Statuses))
	for _, s := range work.Statuses {
		label := s
		if s == t.Status && n == 1 {
			label += " (current)"
		}
		rows = append(rows, ochazuke.ChooserRow{ID: "status-choice:" + s, Label: label, Data: s, Cells: []string{work.StatusGlyph(s), label}})
	}
	title := t.Title
	if title == "" {
		title = "untitled"
	}
	if n > 1 {
		title = fmt.Sprintf("%d tasks", n)
	}
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  statusChooser,
		Title: "Status · " + title,
		Rows:  rows,
		Filter: func(query string, row ochazuke.ChooserRow) bool {
			return strings.HasPrefix(row.Label, strings.ToLower(strings.TrimSpace(query)))
		},
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			status, _ := row.Data.(string)
			return set(status)
		},
	})
	return err
}

// newTask is task.new, a wrap of the standard create: the owner is
// inferred as it always was (resolveOwner: --parent, the selected row,
// --task or --project, scratch for none; a --parent not found is a
// notice and the task is saved at the top level)
// and passed to create. Without a title it opens the editor on a new
// task, written at once (record first). It answers the stored task as it
// always has (scripts read project_id and parent_id).
func (a *App) newTask(ctx ochazuke.Ctx) ochazuke.Result {
	args := ctx.Args
	parent := ochazuke.StringArg(args, "parent", "")
	if parent != "" {
		// A subtask belongs to its parent's project, whatever the pin. A
		// parent not found is never a refusal (record first): the task
		// is saved at the top level, its owner inferred as without one.
		_, err := a.work.Task(parent)
		switch {
		case err == nil:
			args = maps.Clone(args)
			args["task"] = parent
		case errors.Is(err, store.ErrNotFound):
			ctx.Notify(fmt.Sprintf("unknown task %s; saved at the top level", parent))
			parent = ""
		default:
			return ochazuke.Failed(fmt.Errorf("parent task %s: %w", parent, err))
		}
	}
	owner, err := a.resolveOwner(ctx, args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if owner.Notice != "" {
		ctx.Notify(owner.Notice)
	}
	a.current, a.tasksOpen = owner.ProjectID, true
	if _, present := ctx.Args["title"]; !present {
		record := editRecord{Noun: "task", ID: work.NewID(), ProjectID: owner.ProjectID, New: true}
		if ctx.Instance != nil && a.r.Terminal(ctx.Instance.ID) != nil {
			record.ReturnTo = ctx.Instance.ID // the leader's n: back to live when it closes
		}
		if err := a.beginEdit(record); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(owner)
	}
	// The standard create with the owner resolved here, so create's focus
	// fill never second-guesses it.
	args = ochazuke.Arguments{"title": ochazuke.StringArg(ctx.Args, "title", ""), "project": owner.ProjectID, "parent": parent}
	out, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "create", Args: args})
	if err != nil {
		return ochazuke.Failed(err)
	}
	thing, _ := out.(map[string]any)
	id, _ := thing["id"].(string)
	t, err := a.taskByID(id)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if a.inScope(t.ProjectID) {
		if err := a.r.OpenPage("work"); err != nil {
			return ochazuke.Failed(err)
		}
		if err := a.taskList.Select(t.ID); err != nil {
			return ochazuke.Failed(err)
		}
		if err := a.taskList.Focus(); err != nil {
			return ochazuke.Failed(err)
		}
	} else {
		ctx.Notify("Task saved outside pin: " + a.ownerLabel(t.ProjectID))
	}
	return ochazuke.Done(struct {
		work.Task
		Notice string `json:"notice,omitempty"`
	}{t, owner.Notice})
}
