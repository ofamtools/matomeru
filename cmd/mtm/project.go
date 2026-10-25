package main

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	"os"
	"strings"
)

const unassociatedID = "no-project"

func (a *App) defineProject() {
	a.r.Define("project").
		Verb("open", a.openProject, "View project and tasks").
		Verb("view", a.openProject, "View project and tasks").
		Verb("edit", a.editProject, "Edit name, theme and launch settings").Positional("name").
		Verb("launch", a.launchRecord, "Launch an agent into this project's session").
		Verb("directory", a.addProjectDirectory, "Attach directory; omit path to choose").Positional("path").
		Verb("default", a.defaultProjectDirectory, "Set default directory").Positional("path").
		Mode("normal").Bind("enter", ochazuke.Do("project.open")).
		Bind("i", ochazuke.Do("project.edit")).Bind("l", ochazuke.Do("project.launch")).
		Bind("d", ochazuke.Do("project.directory"))
	a.parameters("project.edit", map[string]ochazuke.Parameter{"name": {Type: "string"}, "color": {Type: "string"}, "glyph": {Type: "string"}, "harness": {Type: "string"}, "preset": {Type: "string"}})
	for _, verb := range []string{"project.directory", "project.default"} {
		a.parameters(verb, map[string]ochazuke.Parameter{"path": {Type: "string"}})
	}
}

func (a *App) editProject(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.work.Project(ctx.Instance.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	fields := map[string]string{"color": p.Theme.Color, "glyph": p.Theme.Glyph, "harness": p.Settings.Harness, "preset": p.Settings.Preset}
	if len(ctx.Args) == 0 {
		return resultError(a.beginEdit(editRecord{Noun: "project", ID: p.ID, Text: p.Name, Fields: fields}))
	}
	if name, ok := ctx.Args["name"]; ok {
		p.Name = strings.TrimSpace(name.(string))
	}
	for key := range fields {
		if value, ok := ctx.Args[key]; ok {
			fields[key] = value.(string)
		}
	}
	p.Theme = work.Theme{Color: fields["color"], Glyph: fields["glyph"]}
	p.Settings = work.ProjectSettings{Harness: fields["harness"], Preset: fields["preset"]}
	if err := a.work.PutProject(p); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p)
}

func (a *App) addProjectDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.work.Project(ctx.Instance.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	path, present := ctx.Args["path"]
	if !present {
		a.directories.ProjectID = p.ID
		if err := a.r.OpenPage("directories"); err != nil {
			return ochazuke.Failed(err)
		}
		if err := a.directoryList.Focus(); err != nil {
			return ochazuke.Failed(err)
		}
		return a.refreshDirectories(ctx)
	}
	tree := inspectDirectory(expandPath(path.(string)))
	p, err = a.work.AddTree(p.ID, tree)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.Ready = false
	return ochazuke.Done(p)
}

func (a *App) defaultProjectDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.work.SetDefaultTree(ctx.Instance.ID, expandPath(ochazuke.StringArg(ctx.Args, "path", "")))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p)
}

func (a *App) workLists(width, height int) string {
	top := max(1, (height-3)/2)
	return fmt.Sprintf("projects (%d)\n", len(a.projects)) + a.view(a.projectList, width, top) + "\ntasks\n" + a.view(a.taskList, width, max(1, height-top-3))
}

func (a *App) renderWorkDetail(width, height int) ochazuke.Block {
	leftWidth := max(1, width/2-1)
	rightWidth := max(1, width-leftWidth-2)
	detail := ""
	taskID, projectID := a.detailTask, a.current
	if selected := a.r.Instances[a.r.BaseFocus()]; selected != nil {
		switch selected.Noun {
		case "task":
			taskID = selected.ID
		case "project":
			projectID, taskID = selected.ID, ""
		case "unassociated":
			projectID, taskID = "", ""
		}
	}
	var task *work.Task
	for i := range a.tasks {
		if a.tasks[i].ID == taskID && a.inScope(a.tasks[i].ProjectID) {
			task = &a.tasks[i]
			break
		}
	}
	if task != nil {
		detail = fmt.Sprintf("%s\n\nstatus: %s\nproject: %s", taskLabel(*task), task.Status, task.ProjectID)
		detail += a.detailNotes(task.ProjectID, task.ID)
	} else if p, ok := a.project(projectID); ok && a.inScope(p.ID) {
		detail = fmt.Sprintf("%s\n\ncolor: %s  glyph: %s\nharness: %s\npreset: %s\n\ndirectories", projectLabel(p), p.Theme.Color, p.Theme.Glyph, p.Settings.Harness, p.Settings.Preset)
		if a.inScope(p.ID) {
			for _, tree := range p.Trees {
				mark := ""
				if tree.Path == p.DefaultTree {
					mark = " [default]"
				}
				if st, err := os.Stat(tree.Path); err != nil || !st.IsDir() {
					mark += " [missing]"
				}
				var shared []string
				pathKey := directoryKey(tree.Path)
				for _, other := range a.projects {
					if other.ID != p.ID {
						for _, t := range other.Trees {
							if directoryKey(t.Path) == pathKey {
								shared = append(shared, projectName(other))
								break
							}
						}
					}
				}
				if len(shared) > 0 {
					mark += " [also: " + strings.Join(shared, ", ") + "]"
				}
				detail += "\n" + tree.Path + mark
			}
		}
		if a.inScope(p.ID) {
			detail += a.detailNotes(p.ID, "")
		}
	}
	return ochazuke.Text(lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth+2).Render(a.workLists(leftWidth, height)), lipgloss.NewStyle().Width(rightWidth).Render(detail)))
}

func (a *App) defineProjectCreation() {
	a.r.Define("project").Verb("new", a.newProject, "Register a project; omit name to open a draft editor").Positional("name")
	a.parameters("project.new", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "An empty name saves an untitled project"}, "dir": {Type: "string", Description: "Attach this directory and make it the default (a missing path is attached and flagged)"}})
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
	if dir := ochazuke.StringArg(ctx.Args, "dir", ""); dir != "" {
		tree := inspectDirectory(expandPath(dir))
		if _, err := a.work.AddTree(p.ID, tree); err != nil {
			return ochazuke.Failed(err)
		}
		if p, err = a.work.SetDefaultTree(p.ID, tree.Path); err != nil {
			return ochazuke.Failed(err)
		}
		a.directories.Ready = false
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
