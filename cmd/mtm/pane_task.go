package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// A pane belongs to a task (and so to the task's project), to a project
// with no task, or to nothing: unassociated, which never blocks a send.
// The relation is the assignments row keyed by the pane's window
// (work.Assignment), the one pane.associate has always written.
//
// The two-noun verbs share one handler, relatePane:
//
//   - pane.associate --task T | --project P | --scratch (the pane row's own)
//   - task.associate --pane ID and project.associate --pane ID (the
//     reverses, the pane a noun-typed parameter)
//   - pane.unassociate: pane.associate --scratch
//
// pane.pick-task (t on a pane row) is the task picker over a pane row: the pane's current
// task (or project) first, so enter keeps it, then the open tasks, the
// pane's project's first and the most recently updated first, then the
// projects with no task, then "unassociated"; typing filters, and a query
// that matches nothing offers "new task in <project>". esc changes
// nothing. The prompt box opens it over the page right after a send
// (prompt.after-submit), the send already on its way.

// settingAfterSubmit is what the prompt box does once it has sent.
const settingAfterSubmit = "prompt.after-submit"

// The prompt.after-submit values.
const (
	afterSubmitPickTask             = "pick-task"
	afterSubmitNone                 = "none"
	afterSubmitPickTaskUnassociated = "pick-task-if-unassociated"
)

var afterSubmitSetting = ochazuke.Setting{Name: settingAfterSubmit, Type: ochazuke.SettingString, Default: afterSubmitPickTask,
	Enum:        []string{afterSubmitPickTask, afterSubmitNone, afterSubmitPickTaskUnassociated},
	Description: "After the prompt box sends: pick-task opens the pane's task picker (esc leaves it as it is), pick-task-if-unassociated only for a pane with no task or project, none does nothing"}

// taskPicker is the picker's modal kind.
const taskPicker = "task-picker"

// paneRelateParam is the pane task.associate and project.associate relate.
var paneRelateParam = ochazuke.Parameter{Type: "string", Noun: "pane", Source: "pane.list", Free: true, Description: "A pane (its row id or window id) to associate with this one"}

func (a *App) definePaneTask() {
	a.r.Define("pane").
		Verb("unassociate", a.unassociatePane, "Leave the pane with no task or project (pane.associate --scratch)").
		Verb("pick-task", a.pickPaneTask, "Choose the pane's task or project: its current one first (enter keeps it), open tasks, new task in its project; esc changes nothing").
		// t (task) was free on pane rows; m stays the project chooser. Off
		// the footer, which is full; ? help and bindings list show it.
		Mode("normal").Bind("t", ochazuke.Do("pane.pick-task").Hide())
}

// paneRef is a pane named by its row id (pane:session/window) or its
// window id: the loaded pane when there is one, else the window (and the
// session the row id spells), so a pane mtm has not listed yet is still
// recorded.
func (a *App) paneRef(ref string) pane {
	for _, p := range a.panes {
		if p.rowID() == ref || p.ID == ref {
			return p
		}
	}
	var p pane
	if rest, ok := strings.CutPrefix(ref, "pane:"); ok {
		if i := strings.LastIndex(rest, "/"); i >= 0 {
			p.Session, p.ID = rest[:i], rest[i+1:]
			return p
		}
		ref = rest
	}
	p.ID = ref
	return p
}

// relatePane is the one handler behind pane.associate, its reverses
// (task.associate and project.associate --pane, the pane row of
// relations()) and pane.unassociate: the pane's owner is what the last
// call named.
func (a *App) relatePane(p pane, o Owner) ochazuke.Result {
	if p.ID == "" {
		return ochazuke.Failed(errors.New("name the pane (its row id or window id)"))
	}
	row := work.Assignment{Window: p.ID, Session: p.Session, ProjectID: o.ProjectID, TaskID: o.TaskID, AssignedAt: work.Now()}
	if err := a.work.SetPaneOwner(row); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(row)
}

// relatePaneTo is the pane's relation: task and project associate
// --pane ID.
func (a *App) relatePaneTo(ref string, o relOwner) ochazuke.Result {
	return a.relatePane(a.paneRef(ref), Owner{ProjectID: o.ProjectID, TaskID: o.TaskID})
}

// unassociatePane is pane.unassociate.
func (a *App) unassociatePane(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.relatePane(p, Owner{})
}

// pickPaneTask is pane.pick-task: the task picker over the pane row.
func (a *App) pickPaneTask(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.openTaskPicker(p); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

// afterSubmit runs prompt.after-submit for a send to p: it opens the task
// picker over the page, or nothing. A picker that cannot open is a notice;
// the send goes either way.
func (a *App) afterSubmit(ctx ochazuke.Ctx, p pane) {
	if a.cli {
		return
	}
	switch a.r.Settings().String(settingAfterSubmit) {
	case afterSubmitNone:
		return
	case afterSubmitPickTaskUnassociated:
		if o := a.paneOwner(p); o.ProjectID != "" || o.TaskID != "" {
			return
		}
	}
	if err := a.openTaskPicker(p); err != nil {
		ctx.Error("task picker: " + err.Error())
	}
}

// pickedOwner is a picker row's owner: a task, a project with no task, or
// none (unassociated).
type pickedOwner struct {
	project, task string
	scratch       bool
}

// openTaskPicker opens the task picker for p. Choosing a row associates
// the pane through pane.associate; the chooser gives the focus back to
// where it was (the prompt box after a send), so the next thing is one
// key away.
func (a *App) openTaskPicker(p pane) error {
	owner := a.paneOwner(p)
	rows := a.taskPickerRows(owner)
	project := cmp.Or(owner.ProjectID, a.pin)
	where := "no project"
	if project != "" {
		where = a.ownerLabel(project)
	}
	row := p.rowID()
	associate := func(args ochazuke.Arguments) ochazuke.Result {
		out, err := a.r.Execute(ochazuke.Request{Verb: "associate", Noun: "pane", Target: row, Args: args})
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(out)
	}
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  taskPicker,
		Title: "Task for " + p.label(),
		Rows:  rows,
		Empty: "no open tasks or projects; type a title for a new task",
		Filter: func(query string, row ochazuke.ChooserRow) bool {
			if row.Trailing {
				return strings.Contains(row.Label, strings.ToLower(query))
			}
			return fuzzy(query, row.Label)
		},
		Choose: func(c ochazuke.ChooserRow) ochazuke.Result {
			o, _ := c.Data.(pickedOwner)
			switch {
			case o.scratch:
				return associate(ochazuke.Arguments{"scratch": true})
			case o.task != "":
				return associate(ochazuke.Arguments{"task": o.task})
			}
			return associate(ochazuke.Arguments{"project": o.project})
		},
		CreateRow:   true,
		CreateLabel: func(query string) string { return fmt.Sprintf("new task %q in %s", strings.TrimSpace(query), where) },
		Create: func(query string) ochazuke.Result {
			if strings.TrimSpace(query) == "" {
				return ochazuke.Failed(errors.New("type the new task's title"))
			}
			t, err := a.work.AddTask(project, query)
			if err != nil {
				return ochazuke.Failed(err)
			}
			if err := a.refreshTasks(); err != nil {
				return ochazuke.Failed(err)
			}
			return associate(ochazuke.Arguments{"task": t.ID})
		},
	})
	return err
}

// taskPickerRows are the picker's rows for a pane owned by owner: the
// current task or project first (enter keeps it), the open tasks (the
// owner's project's first, then the most recently updated), the projects
// by recency, and "unassociated" last while the pane has an owner.
func (a *App) taskPickerRows(owner work.Assignment) []ochazuke.ChooserRow {
	var rows []ochazuke.ChooserRow
	taskRow := func(t work.Task, current bool) ochazuke.ChooserRow {
		label := taskLabel(t)
		if t.ProjectID != "" {
			label += " · " + a.ownerLabel(t.ProjectID)
		}
		if current {
			label += " (current)"
		}
		return ochazuke.ChooserRow{ID: "task:" + t.ID, Label: label, Data: pickedOwner{project: t.ProjectID, task: t.ID}}
	}
	projectRow := func(p work.Project, current bool) ochazuke.ChooserRow {
		label := projectName(p) + " · no task"
		if current {
			label += " (current)"
		}
		return ochazuke.ChooserRow{ID: "project:" + p.ID, Label: label, Data: pickedOwner{project: p.ID}}
	}
	if owner.TaskID != "" {
		if t, err := a.work.Task(owner.TaskID); err == nil {
			rows = append(rows, taskRow(t, true))
		}
	} else if p, ok := a.project(owner.ProjectID); ok {
		rows = append(rows, projectRow(p, true))
	}
	tasks, err := a.work.Tasks("")
	if err != nil {
		a.r.UI().Error("tasks: " + err.Error())
	}
	tasks = slices.DeleteFunc(tasks, func(t work.Task) bool { return t.Status == work.StatusDone || t.ID == owner.TaskID })
	home := cmp.Or(owner.ProjectID, a.pin)
	slices.SortStableFunc(tasks, func(x, y work.Task) int {
		return cmp.Or(cmpBool(y.ProjectID == home && home != "", x.ProjectID == home && home != ""), cmp.Compare(y.Updated, x.Updated))
	})
	for _, t := range tasks {
		rows = append(rows, taskRow(t, false))
	}
	for _, p := range a.projectsByRecency() {
		if owner.TaskID == "" && p.ID == owner.ProjectID {
			continue
		}
		rows = append(rows, projectRow(p, false))
	}
	if owner.ProjectID != "" || owner.TaskID != "" {
		rows = append(rows, ochazuke.ChooserRow{ID: "scratch", Label: "unassociated", Trailing: true, Data: pickedOwner{scratch: true}})
	}
	return rows
}

// paneTaskFacts are the header's words for the pane's task: "task
// <title>" when it has one, "unassociated" for an agent with no task or
// project (the marker gutter's slots are all taken: star, side, state),
// unless the header already says it (outside: "owner unassociated").
func (a *App) paneTaskFacts(p pane, outside bool) []string {
	if p.ID == "" {
		return nil
	}
	o := a.paneOwner(p)
	if owner, ok := a.pv.view.owners[p.ID]; ok {
		o.ProjectID = owner // the project the list shows it under
	}
	switch {
	case o.TaskID != "":
		title := o.TaskID
		if t, err := a.work.Task(o.TaskID); err == nil {
			title = cmp.Or(t.Title, "untitled")
		}
		return []string{"task " + title}
	case o.ProjectID == "" && p.isAgent() && !outside:
		return []string{"unassociated"}
	}
	return nil
}
