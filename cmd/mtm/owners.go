package main

import (
	"fmt"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func (a *App) defineOwners() {
	// pane and session: the standard move (their owner a prop:
	// pane_store.go, session_store.go) with m on every row through the
	// project chooser (choose-move); associate keeps the chooser, the
	// relation forms and the deprecated owner forms.
	// Their stores are copies of the daemon's (copies.go): the name is the
	// copy's own, sent on to the daemon (Through, as are the owner and the
	// star, which are mtm's records). The nouns, their props, stores and
	// verbs are declared in nouns/pane.json and nouns/session.json.
	// Keep the standard editor (i, r is edit of @label, m of @owner),
	// resolving its record subject from the row (a session row, a side
	// pane's) or, through the association, the live pane (ctrl+a r).
	a.editSubject("pane", func(inst *ochazuke.Instance) (string, string, any, error) {
		p, err := a.paneAt(inst)
		return p.rowID(), p.label(), p.keyProps(), err
	})
	if err := a.r.AddActionAlias(starAlias("pane")); err != nil { // * on a pane row (pane_stars.go)
		panic(err)
	}
	a.readCopiesNow("pane", nil)
	paneNoun := a.r.Noun("pane")
	*paneNoun.Store = a.docStore("pane", *paneNoun.Store)
	a.readCopiesNow("session", checkSessionName)
	session := a.r.Noun("session")
	*session.Store = a.docStore("session", *session.Store)
	a.editSubject("session", func(inst *ochazuke.Instance) (string, string, any, error) {
		p, err := a.sessionAt(inst)
		return p.sessionRef().String(), p.Session, ochazuke.Thing{"id": p.SessionID}, err
	})
	// task.associate is the reverse of the relations (--pane,
	// --action-item, --context-item: relations.go); its move form
	// (--project, --task, --scratch) runs the standard set of @owner and is
	// deprecated (m is task.choose-move).
	a.r.Define("task").Verb("associate", a.associateTask, "Associate a thing with this task (--pane, --action-item or --context-item ID); --project, --task or --scratch is the deprecated form of task set ID @owner=PROJECT").Many()
	// pane.associate's and session.associate's are in their noun files.
	a.parameters("task.associate", map[string]ochazuke.Parameter{"project": {Type: "string", Noun: "project", Source: "projects.all", Free: true, Description: "The project id or name; omitted, a chooser asks"}, "task": {Type: "string"}, "scratch": {Type: "boolean", Description: "Unassociate it"}})
	for _, noun := range []string{"review"} {
		op := a.r.Operations.At(ochazuke.Key(a.r.CanonicalKey(noun + ".list")))
		if op.Parameters == nil {
			op.Parameters = map[string]ochazuke.Parameter{}
		}
		op.Parameters["all"] = ochazuke.Parameter{Type: "boolean"}
		a.r.Operations.Set(op)
	}
	a.ownedBy("session", paneListID)
}

// editSubject wraps the noun's standard edit so its subject is the thing
// resolve finds from the focused row (row id, label, data), and lets it
// act through an association (a live pane's leader keys). A row resolve
// cannot read is left to the standard edit, so an explicit id still wins.
func (a *App) editSubject(noun string, resolve func(*ochazuke.Instance) (id, label string, data any, err error)) {
	op, ok := a.r.Operations.Find(ochazuke.Key(noun + ".edit"))
	if !ok {
		return
	}
	edit := op.AppHandler
	subject := op.Parameters["id"]
	subject.From = "row:" + noun
	op.Parameters["id"] = subject
	op.ByAssociation = true
	op.AppHandler = func(ctx ochazuke.Ctx) ochazuke.Result {
		id, label, data, err := resolve(ctx.Instance)
		if err != nil {
			return edit(ctx)
		}
		row := *ctx.Instance
		row.Noun, row.ID, row.Label, row.Data = noun, id, label, data
		ctx.Instance = &row
		return edit(ctx)
	}
	a.r.Operations.Set(op)
}

// paneOwner is the pane's owner as it reads (paneOwnerIn), from the UI's
// database.
func (a *App) paneOwner(p pane) work.Assignment {
	o, err := a.paneOwnerIn(a.work, p.ID, p.SessionID)
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
	ctx.Notify(fmt.Sprintf("task.associate --project is an old form of task set ID @owner=PROJECT (since %s; removed after %s)", taskAssociateSince, taskAssociateRemoveAfter))
	return ctx.Each(a.associateTaskOne)
}

// associateTaskOne is task.associate's move form on one task, its owner
// given: the standard set of @owner to the owner's project (a task names its
// project; a task or project not found is unassociated with a notice).
// The form is deprecated: associateTask's notice names set @owner.
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
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "task", Verb: "set", Args: ochazuke.Arguments{"id": t.ID, "props": map[string]any{"@owner": o.ProjectID}}}))
}
