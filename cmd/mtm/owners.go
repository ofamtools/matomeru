package main

import (
	"context"
	"fmt"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) defineOwners() {
	// One move verb per noun, m on every row: associate. From a key with
	// no owner named, a chooser of projects asks (unassociated last).
	a.r.Define("pane").Verb("associate", a.associatePane, "Move a pane to a task (--task) or a project (omit them to choose a project); scratch explicitly removes ownership").Many().Verb("list", a.listPanes, "List panes in scope; --all widens the list").Fetch()
	a.r.Define("session").Verb("associate", a.associateSession, "Move a session to a project (omit it to choose); panes with their own row stay unchanged").Many().Verb("list", a.listSessions, "List sessions in scope; --all widens the list").Fetch()
	// task.associate is the reverse of the relations (--pane,
	// --action-item, --context-item: relations.go); its move form
	// (--project, --task, --scratch) runs the standard task.move and is
	// deprecated (m is task.choose-move).
	a.r.Define("task").Verb("associate", a.associateTask, "Associate a thing with this task (--pane, --action-item or --context-item ID); --project, --task or --scratch is the deprecated form of task.move").Many()
	for _, noun := range []string{"pane", "session", "task"} {
		a.parameters(noun+".associate", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, a chooser asks"}, "task": {Type: "string"}, "scratch": {Type: "boolean", Description: "Unassociate it"}})
	}
	for _, noun := range []string{"pane", "session", "review"} {
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

func (a *App) associatePane(ctx ochazuke.Ctx) ochazuke.Result {
	if rel, id, ok := a.related(ctx, "pane"); ok {
		return a.associatePaneRelated(ctx, rel, id)
	}
	if a.moving(ctx) {
		return a.moveTo(ctx, "pane")
	}
	return ctx.Each(a.associatePaneOne)
}

// associatePaneOne is pane.associate on one pane, its owner given.
func (a *App) associatePaneOne(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	o, err := a.resolveOwner(ochazuke.Ctx{}, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if o.Notice != "" {
		ctx.Info(o.Notice)
	}
	return a.relatePane(p, o)
}

func (a *App) associateSession(ctx ochazuke.Ctx) ochazuke.Result {
	if a.moving(ctx) {
		return a.moveTo(ctx, "session")
	}
	return ctx.Each(a.associateSessionOne)
}

// associateSessionOne is session.associate on one session, its owner given.
func (a *App) associateSessionOne(ctx ochazuke.Ctx) ochazuke.Result {
	p, ok := ctx.Instance.Data.(pane)
	if !ok || p.SessionID == "" {
		return ochazuke.Failed(fmt.Errorf("select a session"))
	}
	o, err := a.resolveOwner(ochazuke.Ctx{}, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	row := work.SessionOwner{ID: p.SessionID, Name: p.Session, ProjectID: o.ProjectID}
	if err := a.work.SetSessionOwner(row); err != nil {
		return ochazuke.Failed(err)
	}
	if o.Notice != "" {
		ctx.Notify(o.Notice)
	}
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(row)
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

type ownedPane struct {
	pane
	Owner work.Assignment `json:"owner"`
	Label string          `json:"label"`
}

type ownedSession struct {
	work.SessionOwner
	Label string `json:"label"`
}

func (a *App) listPanes(ctx ochazuke.Ctx) ochazuke.Result    { return a.listLive(ctx, false) }
func (a *App) listSessions(ctx ochazuke.Ctx) ochazuke.Result { return a.listLive(ctx, true) }

func (a *App) listLive(ctx ochazuke.Ctx, sessions bool) ochazuke.Result {
	d := a.panesd
	// Capture the scope before the job starts; filtering still uses inScope.
	scope := &App{pin: a.pin, all: a.all, projects: append([]work.Project(nil), a.projects...)}
	if all, _ := ctx.Args["all"].(bool); all {
		scope.all = true
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		rows, err := d.ListPanes(c)
		if err != nil {
			return nil, err
		}
		if sessions {
			out := []ownedSession{}
			seen := map[string]bool{}
			for _, p := range rows {
				if seen[p.SessionID] {
					continue
				}
				seen[p.SessionID] = true
				o, err := a.work.SessionOwner(p.SessionID)
				if err != nil {
					return nil, err
				}
				o.ID, o.Name = p.SessionID, p.Session
				if scope.inScope(o.ProjectID) {
					out = append(out, ownedSession{SessionOwner: o, Label: p.Session + " · " + scope.ownerLabel(o.ProjectID)})
				}
			}
			return out, nil
		}
		out := []ownedPane{}
		for _, p := range rows {
			if p.ID == "" {
				continue
			}
			o, err := a.work.PaneOwner(p.ID, p.SessionID)
			if err != nil {
				return nil, err
			}
			if scope.inScope(o.ProjectID) {
				out = append(out, ownedPane{pane: p, Owner: o, Label: p.label() + " · " + scope.ownerLabel(o.ProjectID)})
			}
		}
		return out, nil
	})
}
