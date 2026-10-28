package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// A project becomes a task of the project above it, and a task directly
// under its project becomes a project (work.DemoteProject and
// work.PromoteTask). In the task outline tab on a project row and
// shift+tab on a top-level task do it, each one step on the outline's
// undo stack; project.make-task and task.make-project are the same from
// the palette and the CLI (--target ID), and from the outline's rows
// outline-project.demote and outline-task.promote.

// defineConversions adds the conversion verbs; the outline's keys are
// bound in defineOutline.
func (a *App) defineConversions() {
	a.r.Define("project").Verb("make-task", a.makeTask, "Turn the project into a task of the project above it (or --parent): its tasks become the task's subtasks, its notes, items, panes and sessions the task's, its directories the parent's, its pin the parent's")
	a.parameters("project.make-task", map[string]ochazuke.Parameter{"parent": {Type: "string", Source: "projects.list", Description: "The project it becomes a task of; omitted, the project listed above it"}})
	a.r.Define("task").Verb("make-project", a.makeProject, "Turn a task directly under its project into a project listed after it: its subtasks become the project's tasks, its notes, items, panes and sessions the project's, and every directory its panes, Vim sessions and context items name the project's directories")
	for _, noun := range []string{outlineProjectNoun, outlineRootNoun} {
		a.r.Define(noun).Verb("demote", a.outlineDemote, "Turn the project into a task of the project above it (tab); u undoes it")
	}
	a.r.Define(outlineTaskNoun).Verb("promote", a.outlinePromote, "Turn a task directly under its project into a project listed after it (shift+tab); u undoes it")
}

// makeTask is project.make-task.
func (a *App) makeTask(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a project"))
	}
	conv, err := a.projectToTask(projectOfRoot(ctx.Instance.ID), ochazuke.StringArg(ctx.Args, "parent", ""))
	return a.converted(ctx, conv, err)
}

// makeProject is task.make-project.
func (a *App) makeProject(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a task"))
	}
	conv, err := a.taskToProject(taskOf(ctx.Instance.ID))
	return a.converted(ctx, conv, err)
}

// outlineDemote is tab on an outline project row.
func (a *App) outlineDemote(ctx ochazuke.Ctx) ochazuke.Result {
	id, err := a.outlineProject(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	conv, err := a.projectToTask(id, "")
	return a.converted(ctx, conv, err)
}

// outlinePromote is shift+tab on a task directly under its project
// (outline-task.outdent, guarded) and outline-task.promote.
func (a *App) outlinePromote(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a task"))
	}
	conv, err := a.taskToProject(taskOf(ctx.Instance.ID))
	return a.converted(ctx, conv, err)
}

func (a *App) converted(ctx ochazuke.Ctx, conv work.Conversion, err error) ochazuke.Result {
	if err != nil {
		return ochazuke.Failed(err)
	}
	ctx.Notify(conversionNotice(a, conv))
	return ochazuke.Done(conv.Summary())
}

// conversionNotice says what a conversion did.
func conversionNotice(a *App, conv work.Conversion) string {
	name := func(id string) string {
		if p, ok := a.project(id); ok {
			return projectName(p)
		}
		return id
	}
	switch conv.Kind {
	case "demote":
		msg := "Now a task of " + name(conv.Project)
		if len(conv.Directories) > 0 {
			msg += "; its directories joined it: " + strings.Join(conv.Directories, ", ")
		}
		return msg + " · u undoes"
	}
	msg := "Now the project " + name(conv.Project)
	if len(conv.Directories) > 0 {
		msg += fmt.Sprintf(" with %d directories (default %s)", len(conv.Directories), conv.Directories[0])
	}
	return msg + " · u undoes"
}

func (a *App) projectToTask(id, parent string) (work.Conversion, error) {
	if err := a.leaveEditor(); err != nil {
		return work.Conversion{}, err
	}
	conv, err := a.work.DemoteProject(id, parent)
	if err != nil {
		return conv, err
	}
	return conv, a.afterConversion(conv, true)
}

func (a *App) taskToProject(id string) (work.Conversion, error) {
	if err := a.leaveEditor(); err != nil {
		return work.Conversion{}, err
	}
	conv, err := a.work.PromoteTask(id)
	if err != nil {
		return conv, err
	}
	return conv, a.afterConversion(conv, true)
}

// afterConversion reloads what a conversion changed and, with the outline
// open, puts it on the outline's undo stack (record) and refocuses it.
func (a *App) afterConversion(conv work.Conversion, record bool) error {
	o := a.outline
	open := o != nil && a.r.Tree(outlineTree) == o.tree
	var before, beforeBase outlineScope
	if open {
		before, beforeBase = o.scope, o.base
	}
	if err := a.conversionReload(conv, false); err != nil {
		return err
	}
	if !open || !record {
		return nil
	}
	after, afterBase := o.scope, o.base
	step := func(back bool) func() error {
		return func() error {
			apply, scope, base := a.work.Redo, after, afterBase
			if back {
				apply, scope, base = a.work.Undo, before, beforeBase
			}
			if err := apply(conv); err != nil {
				return err
			}
			o.scope, o.base = scope, base
			return a.conversionReload(conv, back)
		}
	}
	return o.tree.Record(ochazuke.TreeAction{Label: conv.Kind + " " + conv.From, Undo: step(true), Redo: step(false)})
}

// conversionReload reads the pin, projects, tasks and the open outline
// again after a conversion (back: after its undo) and focuses the row it
// made (or, undone, the row it came from). A project's outline follows a
// demoted project to the project it went into.
func (a *App) conversionReload(conv work.Conversion, back bool) error {
	var err error
	if a.pin, err = a.work.Pin(); err != nil {
		return err
	}
	a.pinVersion++
	if !back && conv.Kind == "demote" && a.current == conv.From {
		a.current = conv.Project
	}
	if err := a.refreshProjects(); err != nil {
		return err
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	if err := a.refreshScope(); err != nil {
		return err
	}
	o := a.outline
	if o == nil || a.r.Tree(outlineTree) != o.tree {
		return nil
	}
	if !back && conv.Kind == "demote" {
		if o.scope.project == conv.From && !o.scope.all {
			o.scope.project = conv.Project
		}
		if o.base.project == conv.From {
			o.base.project = conv.Project
		}
	}
	if err := a.reloadOutline(); err != nil {
		return err
	}
	focus := ""
	switch {
	case conv.Kind == "demote" && !back:
		focus = outlineNode(conv.Task)
	case conv.Kind == "demote":
		focus = projectNode(conv.From)
	case !back:
		focus = projectNode(conv.Project)
	default:
		focus = outlineNode(conv.Task)
	}
	if o.tree.Select(focus) != nil {
		return nil
	}
	if a.r.TopModal() == outlineModal {
		return a.r.SetFocus(focus)
	}
	return nil
}
