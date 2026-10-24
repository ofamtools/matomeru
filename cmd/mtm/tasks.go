package main

import (
	"errors"
	"fmt"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The task noun: rows of the tasks stop, the second stop of the work page.
// project.open fills it; every verb writes through the work store and then
// pushes the rows again with SetItems.

func (a *App) defineTask() {
	r := a.r
	r.Define("task").
		Verb("open", a.openTask, "Show the task in a prompt; enter saves the title, esc closes").
		Verb("edit", a.editTask, "Edit the task's title (omit title to be asked in a prompt)").
		Verb("done", a.doneTask, "Mark the task done").
		Verb("new", a.newTask, "Add a task to the open project (omit title to be asked in a prompt)").
		Verb("launch", a.launch, "Launch an agent on the task into its project's session (inferred; never asked)").
		Mode("normal").
		Bind("l", ochazuke.Do("task.launch")).
		Bind("enter", ochazuke.Do("task.open")).
		Bind("i", ochazuke.Do("task.edit")).
		Bind("x", ochazuke.Do("task.done"))
	title := map[string]ochazuke.Parameter{"title": {Type: "string", Description: "Omit to be asked in a prompt"}}
	for _, key := range []string{"task.edit", "task.new"} {
		op := r.Operations[key]
		op.Parameters = title
		r.Operations[key] = op
	}
}

// bindTaskStop puts the add row on the tasks list itself, so it answers on
// a task row and on an empty list alike.
func (a *App) bindTaskStop() error {
	row := ochazuke.Do("task.new")
	row.Instance, row.Mode, row.Keys = "tasks", "normal", "a"
	return a.r.Bind(row)
}

// refreshTasks reloads the open project's tasks and pushes them to the list.
func (a *App) refreshTasks() error {
	var tasks []work.Task
	if a.current != "" {
		var err error
		if tasks, err = a.work.Tasks(a.current); err != nil {
			return err
		}
	}
	a.tasks = tasks
	items := make([]ochazuke.ListItem, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, ochazuke.ListItem{ID: t.ID, Label: taskLabel(t), Noun: "task"})
	}
	return a.taskList.SetItems(items)
}

// taskLabel is the row text: a done box, the title, and any other status.
func taskLabel(t work.Task) string {
	box := "[ ] "
	if t.Status == work.StatusDone {
		box = "[x] "
	}
	label := box + t.Title
	if t.Status != work.StatusDone && t.Status != work.StatusTodo && t.Status != "" {
		label += "  (" + t.Status + ")"
	}
	return label
}

// task reads the addressed row's task fresh from the store.
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
	project := t.ProjectID
	if p, ok := a.project(t.ProjectID); ok {
		project = p.Name
	}
	status := t.Status
	if status == "" {
		status = work.StatusTodo
	}
	label := fmt.Sprintf("%s · %s · %s", project, status, t.ID)
	if err := a.prompt("Task", label, t.Title, a.retitle(t)); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(t)
}

func (a *App) editTask(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	commit := a.retitle(t)
	if title := ochazuke.StringArg(ctx.Args, "title", ""); title != "" {
		if err := commit(title); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(title)
	}
	if err := a.prompt("Edit task", "Title", t.Title, commit); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(t)
}

// retitle is the commit of the open and edit prompts: an unchanged title
// writes nothing.
func (a *App) retitle(t work.Task) func(string) error {
	return func(title string) error {
		if title == t.Title {
			return nil
		}
		if _, err := a.work.Retitle(t.ID, title); err != nil {
			return err
		}
		return a.refreshTasks()
	}
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
	if a.current == "" {
		return ochazuke.Failed(errors.New("open a project first"))
	}
	add := func(title string) error {
		t, err := a.work.AddTask(a.current, title)
		if err != nil {
			return err
		}
		if err := a.refreshTasks(); err != nil {
			return err
		}
		if err := a.taskList.Select(t.ID); err != nil {
			return err
		}
		return a.taskList.Focus()
	}
	if title := ochazuke.StringArg(ctx.Args, "title", ""); title != "" {
		if err := add(title); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(title)
	}
	name := a.current
	if p, ok := a.project(a.current); ok {
		name = p.Name
	}
	if err := a.prompt("New task", "Title of a new task in "+name, "", add); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}
