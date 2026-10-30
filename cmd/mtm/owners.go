package main

import (
	"fmt"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) defineOwners() {
	// pane and session: the standard move (their owner a prop:
	// pane_store.go, session_store.go) with m on every row through the
	// project chooser (choose-move); associate keeps the chooser, the
	// relation forms and the deprecated owner forms.
	a.r.Define("pane").Props(paneProps()...).Store(a.paneStore()).
		Verb("associate", a.associatePane, "Choose the project to move the pane to (omit the owner); --action-item and --context-item relate those to it; --project, --task and --scratch are the deprecated forms of move and set").Many().
		Verb("choose-move", a.chooseMove, "Choose the project to move the pane to (most recent first, unassociated last); the choice runs pane.move").Many()
	a.r.Define("session").Props(sessionProps()...).Store(a.sessionStore()).
		Verb("associate", a.associateSession, "Choose the project to move the session to (omit it); --project and --scratch are the deprecated form of move; panes with their own owner keep it").Many().
		Verb("choose-move", a.chooseMove, "Choose the project to move the session to (most recent first, unassociated last); the choice runs session.move").Many()
	// task.associate is the reverse of the relations (--pane,
	// --action-item, --context-item: relations.go); its move form
	// (--project, --task, --scratch) runs the standard task.move and is
	// deprecated (m is task.choose-move).
	a.r.Define("task").Verb("associate", a.associateTask, "Associate a thing with this task (--pane, --action-item or --context-item ID); --project, --task or --scratch is the deprecated form of task.move").Many()
	for _, noun := range []string{"pane", "session", "task"} {
		a.parameters(noun+".associate", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, a chooser asks"}, "task": {Type: "string"}, "scratch": {Type: "boolean", Description: "Unassociate it"}})
	}
	for _, noun := range []string{"review"} {
		op := a.r.Operations[a.r.CanonicalKey(noun+".list")]
		if op.Parameters == nil {
			op.Parameters = map[string]ochazuke.Parameter{}
		}
		op.Parameters["all"] = ochazuke.Parameter{Type: "boolean"}
		a.r.Operations[a.r.CanonicalKey(noun+".list")] = op
	}
	a.ownedBy("session", paneListID)
}

func (a *App) paneOwner(p pane) work.Assignment {
	o, err := a.work.PaneOwner(p.ID, p.SessionID)
	if err != nil {
		a.paneErr = "read owner: " + err.Error()
	}
	return o
}

func (a *App) associateTask(ctx ochazuke.Ctx) ochazuke.Result {
	if rel, id, ok := a.related(ctx, "task"); ok {
		return a.associateTaskRelated(ctx, rel, id)
	}
	if a.moving(ctx) {
		return a.moveTo(ctx, "task")
	}
	ctx.Notify(fmt.Sprintf("task.associate --project is an old form of task.move --to (since %s; removed after %s)", taskAssociateSince, taskAssociateRemoveAfter))
	return ctx.Each(a.associateTaskOne)
}

// associateTaskOne is task.associate's move form on one task, its owner
// given: the standard task.move to the owner's project (a task names its
// project; a task or project not found is unassociated with a notice).
// The form is deprecated: associateTask's notice names task.move.
func (a *App) associateTaskOne(ctx ochazuke.Ctx) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	o, err := a.resolveOwner(ochazuke.Ctx{}, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if o.Notice != "" {
		ctx.Notify(o.Notice)
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "task", Verb: "move", Args: ochazuke.Arguments{"id": t.ID, "to": o.ProjectID}}))
}
