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
	a.r.Define("pane").Verb("associate", a.associatePane, "Move a pane to a project (omit it to choose); scratch explicitly removes ownership").Verb("list", a.listPanes, "List panes in scope; --all widens the list").Fetch()
	a.r.Define("session").Verb("associate", a.associateSession, "Move a session to a project (omit it to choose); panes with their own row stay unchanged").Verb("list", a.listSessions, "List sessions in scope; --all widens the list").Fetch()
	a.r.Define("task").Verb("associate", a.associateTask, "Move a task to a project (omit it to choose), or scratch").Mode("normal").Bind("m", ochazuke.Do("task.associate"))
	for _, noun := range []string{"pane", "session", "task"} {
		a.parameters(noun+".associate", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, a chooser asks"}, "task": {Type: "string"}, "scratch": {Type: "boolean", Description: "Unassociate it"}})
	}
	for _, noun := range []string{"pane", "session", "review", "task"} {
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
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	o, err := a.resolveOwner(ochazuke.Ctx{}, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	row := work.Assignment{Window: p.ID, Session: p.Session, ProjectID: o.ProjectID, TaskID: o.TaskID, AssignedAt: work.Now()}
	if err := a.work.SetPaneOwner(row); err != nil {
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

func (a *App) associateSession(ctx ochazuke.Ctx) ochazuke.Result {
	if a.moving(ctx) {
		return a.moveTo(ctx, "session")
	}
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
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	o, err := a.resolveOwner(ochazuke.Ctx{}, ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	t, err = a.work.AssociateTask(t.ID, o.ProjectID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if o.Notice != "" {
		ctx.Notify(o.Notice)
	}
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(t)
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
