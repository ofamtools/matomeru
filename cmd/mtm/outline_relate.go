package main

import (
	"errors"
	"time"

	"ofam.dev/ochazuke"
)

// The outline task row's relation verbs: task.associate (the reverse of
// action-item, context-item and pane associate --task) over the stored
// task a row stands for, and the chooser that picks the context item. The
// relations table (relations.go) lists outline-task beside task, so a new
// related noun reaches the outline row by its row there.

// taskContextChooser is the kind of the chooser that picks a context item
// for a task.
const taskContextChooser = "task-context-chooser"

// outlineAssociate is outline-task.attach: task.associate --<noun> ID on
// the row's task, one handler (associateTaskRelated).
func (a *App) outlineAssociate(ctx ochazuke.Ctx) ochazuke.Result {
	rel, id, ok := a.related(ctx, outlineTaskNoun)
	if !ok {
		return ochazuke.Failed(errors.New("name the thing to associate (" + a.relatedNames(outlineTaskNoun) + "); C chooses a context item"))
	}
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a task"))
	}
	row := *ctx.Instance
	row.Noun, row.ID = "task", taskOf(row.ID)
	row.Data = row.ID
	ctx.Instance = &row
	return a.associateTaskRelated(ctx, rel, id)
}

// outlineAttachContext is outline-task.attach-context (C): the context
// items of the task's project and the unassociated ones in a chooser (S
// widens it to every project's); the choice is the item's task, as
// outline-task.attach --context-item does.
func (a *App) outlineAttachContext(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a task to attach context to"))
	}
	t, err := a.work.Task(taskOf(ctx.Instance.ID))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.reloadContextItems(); err != nil {
		return ochazuke.Failed(err)
	}
	row := ctx.Instance.ID
	spec := pickSpec{
		Kind: taskContextChooser, Noun: contextNoun, Title: "Context for " + a.taskLabel(t),
		Scopes: []pickScope{
			{Name: "project", Choices: func() []ochazuke.Choice { return a.taskContextChoices(t.ProjectID, false) }},
			{Name: "all", Choices: func() []ochazuke.Choice { return a.taskContextChoices(t.ProjectID, true) }},
		},
		Choices: a.taskContextChoices(t.ProjectID, false),
		Match:   ochazuke.MatchSubstring,
		Empty:   "no context items yet: paths, URLs, PRs, branches and commits appear as panes print them",
	}
	data := dataByID(a.taskContextChoices(t.ProjectID, true))
	spec.Preview = func(id string) string { return pickerPreview(data[id]) }
	spec.Pick = func(id string) ochazuke.Result {
		return resultOf(a.r.Execute(ochazuke.Request{Noun: outlineTaskNoun, Verb: "attach", Target: row, Args: ochazuke.Arguments{contextNoun: id}}))
	}
	if _, err := a.choose(spec); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

// taskContextChoices are the context items a task can take: its project's
// and the unassociated ones, newest sighting first (every project's as
// well when everyProject); the picker's rows (contextChoices) without a pane.
func (a *App) taskContextChoices(project string, everyProject bool) []ochazuke.Choice {
	tasks := a.contextProjects()
	now := a.now()
	var rows []ochazuke.Choice
	for _, it := range a.cx.items {
		if !a.archiveVisible(it.Archived) {
			continue
		}
		owner := itemProject(it, tasks)
		where := a.ownerLabel(owner)
		switch {
		case owner != "" && owner == project:
		case owner == "":
			where = "no project"
		case !everyProject:
			continue
		}
		rows = append(rows, ochazuke.Choice{ID: it.ID, Label: it.Kind + " " + it.Text,
			Cells: []string{it.Kind, a.itemCell(it), where, age(now.Sub(time.UnixMilli(it.Seen())))}, RowFlags: ochazuke.RowFlags{Bare: true},
			Props: contextThing(it, tasks), Data: it})
	}
	return rows
}
