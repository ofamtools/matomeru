package main

import (
	"charm.land/lipgloss/v2"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	"strings"
)

const unassociatedID = "no-project"

func (a *App) defineProject() {
	// Props and the store first: the standard verbs (get, list, create,
	// set, watch, rename, duplicate) come from them (project_store.go);
	// new and edit wrap create and set; the rest are mtm's own.
	a.r.Define("project").Props(projectProps()...).Store(a.projectStore()).Columns(projectColumns...).
		Verb("open", a.openProject, "View project and tasks").
		Verb("view", a.openProject, "View project and tasks").
		Verb("edit", a.editProject, "Edit name, theme and launch settings (standard set of those given); omit arguments for the editor").Positional("name").
		Verb("launch", a.launchRecord, "Launch an agent into this project's session").
		Verb("directory", a.addProjectDirectory, "Attach directory; omit path to choose").Positional("path").
		Verb("default", a.defaultProjectDirectory, "Set default directory").Positional("path").
		Verb("remove-directory", a.removeProjectDirectory, "Detach a directory (never touches disk); removing the default promotes the next").Positional("path").
		Renamed("directory.remove", oldNamesSince, oldNamesRemoveAfter).
		Verb("edit-directory", a.editProjectDirectory, "Point an attached directory at another path, keeping its default mark").Positional("path").
		Renamed("directory.edit", oldNamesSince, oldNamesRemoveAfter).
		Mode("normal").Bind("enter", ochazuke.Do("project.open")).
		Bind("l", ochazuke.Do("project.open")).
		Bind("i", ochazuke.Do("project.edit")).Bind("L", ochazuke.Do("project.launch")).
		Bind("c", ochazuke.Do("project.directory"))
	a.parameters("project.edit", map[string]ochazuke.Parameter{"name": {Type: "string"}, "color": {Type: "string"}, "glyph": {Type: "string"}, "harness": {Type: "string", Source: "harness.names", Free: true}, "preset": {Type: "string"}})
	a.editorFields["project"] = a.projectFields
	a.editorText["project"] = "name"
	a.editorSave["project"] = a.saveProjectRecord
	a.editorFinish["project"] = func(record editRecord) error {
		if a.r.Instances[record.ID] != nil {
			if err := a.projectList.Select(record.ID); err != nil {
				return err
			}
		}
		return a.projectList.Focus()
	}
	a.parameters("project.directory", map[string]ochazuke.Parameter{"path": dirParam("The directory to attach (a missing one is attached and flagged); omitted, a chooser asks", true)})
	a.parameters("project.default", map[string]ochazuke.Parameter{"path": {Type: "string"}})
	a.parameters("project.remove-directory", map[string]ochazuke.Parameter{"path": {Type: "string", Required: true}})
	a.parameters("project.edit-directory", map[string]ochazuke.Parameter{"path": {Type: "string", Required: true}, "to": dirParam("The new path (a missing one is attached and flagged)", true)})
}

// editProject is project.edit: without arguments it opens the editor on
// the project; with name, color, glyph, harness or preset it is the
// standard set of those given.
func (a *App) editProject(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.projectByID(ctx.Instance.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if len(ctx.Args) == 0 {
		fields := map[string]string{"color": p.Theme.Color, "glyph": p.Theme.Glyph, "harness": p.Settings.Harness, "preset": p.Settings.Preset}
		return resultError(a.beginEdit(editRecord{Noun: "project", ID: p.ID, Text: p.Name, Fields: fields}))
	}
	changes := map[string]any{}
	for _, name := range projectEditorProps {
		if _, present := ctx.Args[name]; present {
			changes[name] = ochazuke.StringArg(ctx.Args, name, "")
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "set", Args: ochazuke.Arguments{"id": p.ID, "changes": changes}}); err != nil {
		return ochazuke.Failed(err)
	}
	return a.storedProject(p.ID)
}

// storedProject answers the stored project, as project new and edit
// always have (scripts read its trees and settings).
func (a *App) storedProject(id string) ochazuke.Result {
	p, err := a.projectByID(id)
	if err != nil {
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
		// The chooser opens over the current page; a stale cache loads
		// behind it.
		return resultError(a.openDirectoryChooser(p.ID))
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

// removeProjectDirectory is project.remove-directory: the directory leaves
// this project only (other projects sharing it keep it, the disk is never
// touched); removing the default promotes the next and says so.
func (a *App) removeProjectDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	return a.detachDirectory(ctx.Instance.ID, expandPath(ochazuke.StringArg(ctx.Args, "path", "")))
}

func (a *App) detachDirectory(projectID, path string) ochazuke.Result {
	p, promoted, err := a.work.RemoveTree(projectID, path)
	if err != nil {
		return ochazuke.Failed(err)
	}
	home, _ := a.home()
	msg := "Removed " + tildePath(path, home) + " from " + projectName(p) + "; nothing on disk changed"
	switch {
	case promoted != "":
		msg += "; default is now " + tildePath(promoted, home)
	case len(p.Trees) == 0:
		msg += "; it has no directories left"
	}
	a.r.UI().Notify(msg)
	a.directories.Ready = false
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.refreshDirectoryRows()
	return ochazuke.Done(struct {
		work.Project
		Promoted string `json:"promoted,omitempty"`
	}{p, promoted})
}

// editProjectDirectory is project.edit-directory: path is re-pointed at to.
func (a *App) editProjectDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	return a.replaceDirectory(ctx.Instance.ID, expandPath(ochazuke.StringArg(ctx.Args, "path", "")), ochazuke.StringArg(ctx.Args, "to", ""))
}

func (a *App) replaceDirectory(projectID, path, to string) ochazuke.Result {
	if strings.TrimSpace(to) == "" {
		return ochazuke.Failed(errors.New("to is required"))
	}
	p, err := a.work.ReplaceTree(projectID, path, inspectDirectory(expandPath(to)))
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.Ready = false
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.refreshDirectoryRows()
	return ochazuke.Done(p)
}

// workLists draws the projects table over the tasks table. Each takes the
// rows it needs (its header plus its rows); when both cannot fit, the
// projects get at least half and the tasks the rest.
func (a *App) workLists(width, height int) string {
	avail := max(2, height-2)
	want := func(l *ochazuke.List) int { return l.Len() + 1 }
	top := min(want(a.projectList), max(avail/2, avail-want(a.taskList)))
	top = max(1, min(top, avail-1))
	return a.projectsHeading() + a.view(a.projectList, width, top) + "\n" + ansi.Truncate(a.tasksHeading(), width, "…") + "\n" + a.view(a.taskList, width, max(1, avail-top))
}

// tasksHeading names whose tasks the tasks list holds.
func (a *App) tasksHeading() string {
	switch {
	case a.all:
		return "tasks · all projects"
	case !a.tasksOpen:
		return "tasks"
	}
	return "tasks · " + a.ownerLabel(a.current)
}

// followProject makes the tasks list follow the highlighted project row,
// so the tasks under the cursor's project show without enter. Arriving
// from the tasks (k spilling off the first task, {) is not a move in the
// projects: the tasks stay, so j back spills onto the task just left
// instead of into a list the spill rebuilt (UX review 2 F6).
func (a *App) followProject() {
	prev := a.workFocus
	a.workFocus = a.r.BaseFocus()
	if a.editor != nil {
		return
	}
	selected := a.r.Instances[a.r.BaseFocus()]
	if selected == nil || selected.Parent != "projects" {
		return
	}
	if from := a.r.Instances[prev]; prev == "tasks" || from != nil && from.Parent == "tasks" {
		return
	}
	current := ""
	switch selected.Noun {
	case "project":
		current = selected.ID
	case "unassociated":
	default:
		return
	}
	if a.tasksOpen && current == a.current {
		return
	}
	a.current, a.tasksOpen, a.detailTask = current, true, ""
	if err := a.refreshTasks(); err != nil {
		a.r.UI().Error(err.Error())
	}
}

// renderWorkDetail is the stacked Work page below sideBySide: the projects
// with the detail beside them, then the task table across the full width
// (test pass R5: it used to keep to the left half).
func (a *App) renderWorkDetail(width, height int) ochazuke.Block {
	leftWidth := max(1, width/2-1)
	rightWidth := max(1, width-leftWidth-2)
	detail, _ := a.workDetail()
	avail := max(2, height-2)
	want := func(l *ochazuke.List) int { return l.Len() + 1 }
	top := min(want(a.projectList), max(avail/2, avail-want(a.taskList)))
	top = max(1, min(top, avail-1))
	left := a.projectsHeading() + a.view(a.projectList, leftWidth, top)
	upper := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth+2).Render(left), lipgloss.NewStyle().Width(rightWidth).Render(a.detail.work.fit(detail, rightWidth, rightWidth, top+1)))
	return ochazuke.Text(upper + "\n" + ansi.Truncate(a.tasksHeading(), width, "…") + "\n" + a.view(a.taskList, width, max(1, avail-top)))
}

// workDetail is the detail of the highlighted task, else of the
// highlighted (or open) project; isTask reports a task's.
func (a *App) workDetail() (detail string, isTask bool) {
	return a.workDetailOf(a.workSelected())
}

// workSelected is the highlighted task (else "") and project.
func (a *App) workSelected() (taskID, projectID string) {
	taskID, projectID = a.detailTask, a.current
	selected := a.r.Instances[a.r.BaseFocus()]
	// A focus on a list itself (a jump that has not landed on a row) shows
	// that list's selected row.
	for id, l := range map[string]*ochazuke.List{"projects": a.projectList, "tasks": a.taskList} {
		if selected != nil && selected.ID == id {
			if item, ok := l.Selected(); ok {
				selected = a.r.Instances[item.ID]
			}
		}
	}
	if selected != nil {
		switch selected.Noun {
		case "task":
			taskID = selected.ID
		case "project":
			projectID, taskID = selected.ID, ""
		case "unassociated":
			projectID, taskID = "", ""
		}
	}
	return taskID, projectID
}

// workItem names what the Work detail shows, for its scroll.
func (a *App) workItem() string {
	taskID, projectID := a.workSelected()
	if taskID != "" {
		return "task:" + taskID
	}
	return "project:" + projectID
}

// workDetailOf is the detail text of task taskID, else of project
// projectID.
func (a *App) workDetailOf(taskID, projectID string) (detail string, isTask bool) {
	var task *work.Task
	for i := range a.tasks {
		if a.tasks[i].ID == taskID {
			task = &a.tasks[i]
			break
		}
	}
	if task != nil {
		detail = fmt.Sprintf("%s\n\nstatus: %s\nproject: %s", taskLabel(*task), task.Status, a.ownerLabel(task.ProjectID))
		detail += a.detailNotes(task.ProjectID, task.ID)
	} else if p, ok := a.project(projectID); ok {
		detail = projectLabel(p) + "\n"
		// Only the settings that are set; i edits them all.
		for _, f := range [][2]string{{"color", p.Theme.Color}, {"harness", p.Settings.Harness}, {"preset", p.Settings.Preset}, {"origin", p.Origin}} {
			if f[1] != "" {
				detail += "\n" + f[0] + ": " + f[1]
			}
		}
		detail += fmt.Sprintf("\n\ndirectories (%d)", len(p.Trees))
		home, _ := a.home()
		for _, tree := range p.Trees {
			line := tildePath(tree.Path, home)
			if marks := a.treeMarks(p, tree); len(marks) > 0 {
				line += "  [" + strings.Join(marks, "] [") + "]"
			}
			detail += "\n" + line
		}
		if a.inScope(p.ID) {
			detail += a.detailNotes(p.ID, "")
		}
	}
	return detail, task != nil
}

func (a *App) defineProjectCreation() {
	a.r.Define("project").Verb("new", a.newProject, "Register a project (standard create of name and dir); omit name to open the editor").Positional("name")
	a.parameters("project.new", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "An empty name saves an untitled project"}, "dir": dirParam("Attach this directory and make it the default (a missing path is attached and flagged)", true)})
	a.r.Define("unassociated").Verb("open", a.openUnassociated, "Show tasks with no project").
		Mode("normal").Bind("enter", ochazuke.Do("unassociated.open"))
}

// newProject is project.new, a wrap of the standard create (name and
// dir); without a name it opens the editor on a new project, written at
// once (record first). It answers the stored project as it always has.
func (a *App) newProject(ctx ochazuke.Ctx) ochazuke.Result {
	if _, present := ctx.Args["name"]; !present {
		return resultError(a.beginEdit(editRecord{Noun: "project", ID: work.NewID(), New: true}))
	}
	args := ochazuke.Arguments{"name": ochazuke.StringArg(ctx.Args, "name", "")}
	if dir := ochazuke.StringArg(ctx.Args, "dir", ""); dir != "" {
		args["dir"] = dir
	}
	out, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "create", Args: args})
	if err != nil {
		return ochazuke.Failed(err)
	}
	thing, _ := out.(map[string]any)
	id, _ := thing["id"].(string)
	return a.storedProject(id)
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
