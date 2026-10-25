package main

import (
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

const unassociatedID = "no-project"

func (a *App) defineProjectCreation() {
	a.r.Define("project").Verb("new", a.newProject, "Register a project; omit name to open a draft editor").Positional("name")
	a.parameters("project.new", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "An empty name saves an untitled project"}})
	a.r.Define("unassociated").Verb("open", a.openUnassociated, "Show tasks with no project").
		Mode("normal").Bind("enter", ochazuke.Do("unassociated.open"))
}

func (a *App) newProject(ctx ochazuke.Ctx) ochazuke.Result {
	if _, present := ctx.Args["name"]; !present {
		return resultError(a.beginEdit(editRecord{Noun: "project", ID: work.NewID(), New: true}))
	}
	p, err := a.work.AddProject(ochazuke.StringArg(ctx.Args, "name", ""))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p)
}

func (a *App) openUnassociated(ochazuke.Ctx) ochazuke.Result {
	if err := a.leaveEditor(); err != nil {
		return ochazuke.Failed(err)
	}
	a.current = ""
	a.tasksOpen = true
	if err := a.refreshTasks(); err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.taskList.Focus())
}
