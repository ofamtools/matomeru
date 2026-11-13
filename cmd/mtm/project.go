package main

import (
	"charm.land/lipgloss/v2"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
	"strings"
)

const unassociatedID = "no-project"

func (a *App) defineProject() {
	a.registerProjectHue()
	// Props and the store first: the standard verbs (get, list, create,
	// set, watch, rename, duplicate) come from them (project_store.go);
	// new and edit wrap create and set; the rest are mtm's own.
	a.r.Define("project").Props(projectProps()...).Store(a.docStore("project", a.projectStore())).Columns(projectColumns...).
		Verb("open", a.openProject, "View project and tasks").
		Verb("view", a.openProject, "View project and tasks").
		Verb("edit", a.editProject, "Edit name, theme and launch settings (standard set of those given); omit arguments for the editor").Positional("name").ShadowsStandard().
		Verb("launch", a.launchRecord, "Launch an agent into this project's session").
		Verb("directory", a.associateDirectory, "Attach directory; omit path to choose").Positional("path").
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
	a.editorNouns["project"] = editorNoun{
		fields: a.projectFields,
		text:   "name",
		load: func(record editRecord) (ochazuke.Record, error) {
			if record.New {
				return ochazuke.Record{}, nil
			}
			p, err := a.work.Project(record.ID)
			return ochazuke.Record{"name": p.Name, "color": p.Theme.Color, "glyph": p.Theme.Glyph, "harness": p.Settings.Harness, "preset": p.Settings.Preset}, err
		},
		save: a.saveProjectRecord,
		finish: func(record editRecord) error {
			if a.r.Instances[record.ID] != nil {
				if err := a.projectList.Select(record.ID); err != nil {
					return err
				}
			}
			return a.projectList.Focus()
		},
	}
	directory := dirParam("The directory to attach (a missing one is attached and flagged); omitted, a chooser asks", true)
	directory.Noun, directory.Free = work.DirectoryNoun, true
	a.parameters("project.directory", map[string]ochazuke.Parameter{"path": directory,
		"text": {Type: "string", Description: "Free text kept with the directory's relation (relation set changes it later)"},
		"data": {Type: ochazuke.PropObject, Description: "A JSON object kept with the directory's relation; a key set to null is removed"}})
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
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "set", Args: ochazuke.Arguments{"id": p.ID, "props": changes}}); err != nil {
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
	return ochazuke.Done(viewOf(p))
}

// associateDirectory is shared by both nouns; their declarations supply which
// side is focused. A directory path can still be typed or picked without limits.
func (a *App) associateDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	projectID := ochazuke.StringArg(ctx.Args, "project", "")
	if projectID == "" && ctx.Instance != nil {
		if p, ok := a.project(ctx.Instance.ID); ok {
			projectID = p.ID
		}
	}
	path := a.directoryPathAt(ctx)
	if path == "" {
		if _, ok := a.project(projectID); !ok {
			return ochazuke.Failed(errors.New("focus a project or supply a directory path"))
		}
		return resultError(a.openDirectoryChooser(projectID))
	}
	if projectID == "" {
		return a.attachOrAsk(path)
	}
	p, err := a.work.Project(projectID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	tree := inspectDirectory(path)
	tree.Text = ochazuke.StringArg(ctx.Args, "text", "")
	if raw, ok := ctx.Args["data"]; ok && raw != nil {
		data, ok := raw.(map[string]any)
		if !ok {
			return ochazuke.Failed(&ochazuke.ArgumentError{Err: errors.New("data is a JSON object")})
		}
		tree.Data = data
	}
	p, err = a.relateDirectory(ctx, p.ID, tree)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.Ready = false
	a.directories.ProjectID = p.ID
	a.rememberDirectory(tree)
	a.refreshDirectoryRows()
	return ochazuke.Done(viewOf(p))
}

func (a *App) defaultProjectDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.defaultDirectory(ctx, ctx.Instance.ID, a.expandPath(ochazuke.StringArg(ctx.Args, "path", "")))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(viewOf(p))
}

// removeProjectDirectory is project.remove-directory: the directory leaves
// this project only (other projects sharing it keep it, the disk is never
// touched); removing the default promotes the next and says so.
func (a *App) removeProjectDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	return a.detachDirectory(ctx.Instance.ID, a.expandPath(ochazuke.StringArg(ctx.Args, "path", "")))
}

func (a *App) detachDirectory(projectID, path string) ochazuke.Result {
	p, promoted, err := a.unrelateDirectory(ochazuke.Ctx{}, projectID, path)
	if err != nil {
		return ochazuke.Failed(err)
	}
	home, _ := a.home()
	msg := "Removed " + tildePath(path, home) + " from " + projectName(p) + "; nothing on disk changed"
	// Promoting the next directory to default is announced by the library
	// (kind directory is Exclusive on the default flag).
	switch {
	case promoted != "":
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
		projectView
		Promoted string `json:"promoted,omitempty"`
	}{viewOf(p), promoted})
}

// editProjectDirectory is project.edit-directory: path is re-pointed at to.
func (a *App) editProjectDirectory(ctx ochazuke.Ctx) ochazuke.Result {
	return a.replaceDirectory(ctx.Instance.ID, a.expandPath(ochazuke.StringArg(ctx.Args, "path", "")), ochazuke.StringArg(ctx.Args, "to", ""))
}

func (a *App) replaceDirectory(projectID, path, to string) ochazuke.Result {
	if strings.TrimSpace(to) == "" {
		return ochazuke.Failed(errors.New("to is required"))
	}
	p, err := a.repointDirectory(ochazuke.Ctx{}, projectID, path, inspectDirectory(a.expandPath(to)))
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.directories.Ready = false
	if err := a.refreshProjects(); err != nil {
		return ochazuke.Failed(err)
	}
	a.refreshDirectoryRows()
	return ochazuke.Done(viewOf(p))
}

// workLists draws the projects table over the tasks table. Each takes the
// rows it needs (its header plus its rows); when both cannot fit, the
// projects get at least half and the tasks the rest.
func (a *App) workLists(width, height int) string {
	avail := max(2, height-2)
	want := func(l *ochazuke.Collection) int { return l.Len() + 1 }
	top := min(want(a.projectList), max(avail/2, avail-want(a.taskList)))
	top = max(1, min(top, avail-1))
	return a.projectsHeading() + a.view(a.projectList, width, top) + "\n" + ansi.Truncate(a.tasksHeading(), width, "…") + "\n" + a.view(a.taskTree, width, max(1, avail-top))
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
	if a.editor != nil {
		return
	}
	selected := a.r.Instances[a.r.BaseFocus()]
	if selected == nil {
		return
	}
	// Passing through the record/related stops does not change the list the
	// user came from; a spill back from tasks must keep their selected scope.
	if a.isTaskList(selected.ID) || a.isTaskList(selected.Parent) {
		a.workFocus = selected.ID
		return
	}
	if selected.Parent != "projects" {
		return
	}
	a.workFocus = selected.ID
	if from := a.r.Instances[prev]; a.isTaskList(prev) || from != nil && a.isTaskList(from.Parent) {
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
	want := func(l *ochazuke.Collection) int { return l.Len() + 1 }
	top := min(want(a.projectList), max(avail/2, avail-want(a.taskList)))
	top = max(1, min(top, avail-1))
	if a.detail.work.showingForm() {
		// A short projects list must not reduce the form/related viewport to
		// a few lines; keep room for sections while retaining the task table.
		top = min(avail-1, max(top, height*3/5))
	}
	left := a.projectsHeading() + a.view(a.projectList, leftWidth, top)
	body := a.detail.work.recordBlock(detail, rightWidth, top+1)
	upper := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftWidth+2).Render(left), lipgloss.NewStyle().Width(rightWidth).Render(body.Text))
	return body.Offset(leftWidth+2, 0).WithText(upper + "\n" + ansi.Truncate(a.tasksHeading(), width, "…") + "\n" + a.view(a.taskTree, width, max(1, avail-top)))
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
	for id, l := range map[string]*ochazuke.Collection{"projects": a.projectList, taskTreeID: a.taskList, taskListID: a.taskList} {
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

// workTarget is the thing the Work detail shows, as noun and key: the
// highlighted task, else the highlighted project ("" for none). Its related
// sections (the notes about it) are drawn under the detail's text.
func (a *App) workTarget() (string, string) {
	taskID, projectID := a.workSelected()
	switch {
	case taskID != "":
		return "task", taskID
	case projectID != "":
		return "project", projectID
	}
	return "", ""
}

// workItem names what the Work detail shows, for its scroll.
func (a *App) workItem() string {
	taskID, projectID := a.workSelected()
	if taskID != "" {
		return "task:" + taskID
	}
	return "project:" + projectID
}

func (a *App) workData() string {
	tasks, _ := a.db.Version("tasks")
	projects, _ := a.db.Version("projects")
	relations, _ := a.db.Version("relations")
	return tasks + "/" + projects + "/" + relations
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
		if !a.detail.work.showingForm() {
			detail = a.recordText("task", a.taskThingOf(*task))
		}
	} else if p, ok := a.project(projectID); ok {
		detail = a.projectDirectoryText(p)
		if !a.detail.work.showingForm() {
			detail = a.recordText("project", projectThing(p)) + "\n\n" + detail
		}
	}
	return detail, task != nil
}

// Directory annotations and checkout marks are page-owned content, not a
// second layout of the project's catalog fields.
func (a *App) projectDirectoryText(p work.Project) string {
	if len(p.Trees) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("directories (%d)", len(p.Trees)))
	home, _ := a.home()
	for _, tree := range p.Trees {
		line := tildePath(tree.Path, home)
		if marks := a.treeMarks(p, tree); len(marks) > 0 {
			line += "  [" + strings.Join(marks, "] [") + "]"
		}
		if first, _, _ := strings.Cut(strings.TrimSpace(tree.Text), "\n"); first != "" {
			line += "  " + first
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// The opt-out still shows the record, from the same catalog prop order as the
// form, without maintaining a second task/project field inventory.
func (a *App) recordText(noun string, thing ochazuke.Thing) string {
	var lines []string
	for _, prop := range a.r.Noun(noun).Props {
		if value, ok := thing[prop.Name]; ok && !prop.Secret {
			lines = append(lines, prop.Name+": "+fmt.Sprint(value))
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) defineProjectCreation() {
	a.r.Define("project").Verb("new", a.newProject, "Register a project (standard create of name and dir); omit name to open the editor").Positional("name")
	a.parameters("project.new", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "An empty name saves an untitled project"}, "dir": dirParam("Attach this directory and make it the default (a missing path is attached and flagged)", true)})
	a.r.Define("unassociated").Kind(ochazuke.NounKindPart).DefaultKeys(false).Verb("open", a.openUnassociated, "Show tasks with no project").
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
