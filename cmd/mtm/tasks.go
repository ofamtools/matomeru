package main

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) defineTask() {
	r := a.r
	r.Define("task").Columns(taskColumns...).
		Verb("list", a.listTasks, "List tasks, including unassociated tasks").
		Verb("open", a.openTask, "View task without editing").
		Verb("view", a.openTask, "View task without editing").
		Verb("edit", a.editTask, "Edit title and status; omit arguments for draft editor").Positional("title").
		Verb("done", a.doneTask, "Mark the task done").Positional("id").
		Verb("toggle", a.toggleTask, "Mark the task done, or a done task todo").
		Verb("status", a.statusTask, "Set the task's status (todo, active, blocked, done); omit status to choose one").Positional("id", "status").
		Verb("new", a.newTask, "Add a task (under --parent, else the pin or no project); omit title to open the draft editor").Positional("title").
		Verb("launch", a.launchRecord, "Launch an agent on the task").
		Mode("normal").
		Bind("enter", ochazuke.Do("task.open")).Bind("l", ochazuke.Do("task.open")).Bind("L", ochazuke.Do("task.launch")).
		Bind("i", ochazuke.Do("task.edit")).Bind("x", ochazuke.Do("task.toggle")).Bind("s", ochazuke.Do("task.status"))
	a.parameters("task.edit", map[string]ochazuke.Parameter{"title": {Type: "string"}, "status": {Type: "string"}})
	a.parameters("task.new", map[string]ochazuke.Parameter{
		"title": {Type: "string"}, "project": {Type: "string"}, "task": {Type: "string"}, "scratch": {Type: "boolean"}, "parent": {Type: "string"},
	})
	a.parameters("task.done", map[string]ochazuke.Parameter{"id": {Type: "string"}})
	a.parameters("task.status", map[string]ochazuke.Parameter{"id": {Type: "string"}, "status": {Type: "string", Enum: work.Statuses}})
}

func (a *App) bindTaskStop() error {
	row := ochazuke.Do("task.new")
	row.Instance, row.Mode, row.Keys = "tasks", "normal", "n"
	return a.r.Bind(row)
}

func (a *App) listTasks(ctx ochazuke.Ctx) ochazuke.Result {
	scope := *a
	if all, _ := ctx.Args["all"].(bool); all {
		scope.all = true
	}
	tasks, err := a.work.Tasks("")
	if err != nil {
		return ochazuke.Failed(err)
	}
	out := make([]work.Task, 0, len(tasks))
	for _, task := range tasks {
		if scope.inScope(task.ProjectID) {
			out = append(out, task)
		}
	}
	return ochazuke.Done(out)
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
		for _, t := range rows {
			if (a.all || t.ProjectID == a.current) && a.inScope(t.ProjectID) {
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

func (a *App) editTask(ctx ochazuke.Ctx) ochazuke.Result {
	if len(ctx.Args) == 0 {
		t, err := a.task(ctx)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return resultError(a.beginEdit(editRecord{Noun: "task", ID: t.ID, Text: t.Title, ProjectID: t.ProjectID, Fields: map[string]string{"status": t.Status}}))
	}
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if _, present := ctx.Args["title"]; present {
		t.Title = ochazuke.StringArg(ctx.Args, "title", "")
	}
	if _, present := ctx.Args["status"]; present {
		t.Status = ochazuke.StringArg(ctx.Args, "status", "")
	}
	t.Updated = work.Now()
	err = a.work.PutTask(t)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshTasks(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(t)
}

func (a *App) doneTask(ctx ochazuke.Ctx) ochazuke.Result {
	return a.changeTask(ctx, func(id string) (work.Task, error) { return a.work.Done(id) })
}

func (a *App) toggleTask(ctx ochazuke.Ctx) ochazuke.Result {
	return a.changeTask(ctx, a.work.ToggleDone)
}

// statusTask is task.status: the status argument sets it; without one a
// chooser of the four statuses opens on the task.
func (a *App) statusTask(ctx ochazuke.Ctx) ochazuke.Result {
	if status, ok := ctx.Args["status"].(string); ok {
		return a.changeTask(ctx, func(id string) (work.Task, error) { return a.work.SetStatus(id, status) })
	}
	t, err := a.taskArg(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.openStatusChooser(t, a.refreshTasks))
}

// taskArg is the task an id argument names, else the selected task.
func (a *App) taskArg(ctx ochazuke.Ctx) (work.Task, error) {
	if id, ok := ctx.Args["id"].(string); ok && id != "" {
		t, err := a.work.Task(id)
		if err != nil {
			return work.Task{}, fmt.Errorf("task %s: %w", id, err)
		}
		return t, nil
	}
	return a.task(ctx)
}

// changeTask applies change to the task taskArg names and reloads the
// task rows (and the open todo outline).
func (a *App) changeTask(ctx ochazuke.Ctx, change func(id string) (work.Task, error)) ochazuke.Result {
	t, err := a.taskArg(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if t, err = change(t.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshTasks(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(t)
}

// statusChooser is the status chooser's modal kind.
const statusChooser = "status-chooser"

// openStatusChooser opens the four statuses for t; typing narrows by the
// first letters (t, a, b, d) and enter sets the top one, then refresh runs.
func (a *App) openStatusChooser(t work.Task, refresh func() error) error {
	rows := make([]ChooserRow, 0, len(work.Statuses))
	for _, s := range work.Statuses {
		label := s
		if s == t.Status {
			label += " (current)"
		}
		rows = append(rows, ChooserRow{ID: "status-choice:" + s, Label: label, Data: s, Cells: []string{work.StatusGlyph(s), label}})
	}
	title := t.Title
	if title == "" {
		title = "untitled"
	}
	_, err := a.choosers.Open(ChooserSpec{
		Kind:  statusChooser,
		Title: "Status · " + title,
		Rows:  rows,
		Filter: func(query string, row ChooserRow) bool {
			return strings.HasPrefix(row.Label, strings.ToLower(strings.TrimSpace(query)))
		},
		Choose: func(row ChooserRow) ochazuke.Result {
			status, _ := row.Data.(string)
			got, err := a.work.SetStatus(t.ID, status)
			if err != nil {
				return ochazuke.Failed(err)
			}
			if err := refresh(); err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(got)
		},
	})
	return err
}

func (a *App) newTask(ctx ochazuke.Ctx) ochazuke.Result {
	args := ctx.Args
	if parent := ochazuke.StringArg(args, "parent", ""); parent != "" {
		// A subtask belongs to its parent's project, whatever the pin.
		if _, err := a.work.Task(parent); err != nil {
			return ochazuke.Failed(fmt.Errorf("parent task %s: %w", parent, err))
		}
		args = maps.Clone(args)
		args["task"] = parent
	}
	owner, err := a.resolveOwner(ctx, args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if owner.Notice != "" {
		a.m.Notify(owner.Notice)
	}
	a.current, a.tasksOpen = owner.ProjectID, true
	if _, present := ctx.Args["title"]; !present {
		if err := a.beginEdit(editRecord{Noun: "task", ID: work.NewID(), ProjectID: owner.ProjectID, New: true}); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(owner)
	}
	var t work.Task
	if parent := ochazuke.StringArg(ctx.Args, "parent", ""); parent != "" {
		t, err = a.work.AddSubtask(owner.ProjectID, parent, ochazuke.StringArg(ctx.Args, "title", ""))
	} else {
		t, err = a.work.AddTask(owner.ProjectID, ochazuke.StringArg(ctx.Args, "title", ""))
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshTasks(); err != nil {
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
		a.m.Notify("Task saved outside pin: " + a.ownerLabel(t.ProjectID))
	}
	return ochazuke.Done(struct {
		work.Task
		Notice string `json:"notice,omitempty"`
	}{t, owner.Notice})
}
