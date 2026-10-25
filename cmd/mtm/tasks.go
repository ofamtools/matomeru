package main

import (
	"errors"
	"fmt"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) defineTask() {
	r := a.r
	r.Define("task").
		Verb("list", a.listTasks, "List tasks, including unassociated tasks").
		Verb("open", a.openTask, "View task without editing").
		Verb("view", a.openTask, "View task without editing").
		Verb("edit", a.editTask, "Edit title and status; omit arguments for draft editor").Positional("title").
		Verb("done", a.doneTask, "Mark the task done").
		Verb("new", a.newTask, "Add a task; omit title to open the draft editor").Positional("title").
		Verb("launch", a.launchRecord, "Launch an agent on the task").
		Mode("normal").
		Bind("enter", ochazuke.Do("task.open")).Bind("l", ochazuke.Do("task.launch")).
		Bind("i", ochazuke.Do("task.edit")).Bind("x", ochazuke.Do("task.done"))
	a.parameters("task.edit", map[string]ochazuke.Parameter{"title": {Type: "string"}, "status": {Type: "string"}})
	a.parameters("task.new", map[string]ochazuke.Parameter{
		"title": {Type: "string"}, "project": {Type: "string"}, "task": {Type: "string"}, "scratch": {Type: "boolean"},
	})
}

func (a *App) bindTaskStop() error {
	row := ochazuke.Do("task.new")
	row.Instance, row.Mode, row.Keys = "tasks", "normal", "a"
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
	a.tasks = tasks
	drafts, err := a.draftRecords("task")
	if err != nil {
		return err
	}
	marked := map[string]bool{}
	for _, d := range drafts {
		marked[d.ID] = true
	}
	items := make([]ochazuke.ListItem, 0, len(tasks))
	for _, t := range tasks {
		label := taskLabel(t)
		if a.all || a.pin == "" {
			label += " · " + a.ownerLabel(t.ProjectID)
		}
		if marked[t.ID] {
			label += " [draft]"
		}
		items = append(items, ochazuke.ListItem{ID: t.ID, Label: label, Noun: "task"})
	}
	for _, d := range drafts {
		if d.New && a.tasksOpen && (a.all || d.ProjectID == a.current) && a.inScope(d.ProjectID) {
			items = append(items, ochazuke.ListItem{ID: d.ID, Label: "untitled [draft]", Noun: "draft", Data: d})
		}
	}
	return a.taskList.SetItems(items)
}

func taskLabel(t work.Task) string {
	box := "[ ] "
	if t.Status == work.StatusDone {
		box = "[x] "
	}
	title := t.Title
	if title == "" {
		title = "untitled"
	}
	label := box + title
	if t.ProjectID == "" {
		label += " (unassociated)"
	}
	if t.Status != work.StatusDone && t.Status != work.StatusTodo && t.Status != "" {
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
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if t, err = a.work.Done(t.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshTasks(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(t)
}

func (a *App) newTask(ctx ochazuke.Ctx) ochazuke.Result {
	owner, err := a.resolveOwner(ctx, ctx.Args)
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
	t, err := a.work.AddTask(owner.ProjectID, ochazuke.StringArg(ctx.Args, "title", ""))
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
