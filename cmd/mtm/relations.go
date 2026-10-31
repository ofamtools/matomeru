package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// Two-noun associate verbs. A thing that can belong to a task, a pane or
// a project (an action item, a context item) has its own associate verb
// (action-item.associate --task T) and the reverse on each noun it can
// belong to (task.associate --action-item ID), sharing one handler: the
// relation's relate. The reverse verbs read the relations table below;
// adding a related noun is a row, not a branch in task, pane or project.

// relOwner is the owner a reverse verb hands a relation: the target row
// as a task (its project and id), a pane (its window, session and the
// pane's own owner) or a project.
type relOwner struct {
	ProjectID, TaskID, Window, Session string
}

// relation is one noun a reverse associate verb relates: --<noun> ID on
// the associate verb of every noun in on.
type relation struct {
	noun   string
	on     []string
	param  ochazuke.Parameter
	relate func(id string, o relOwner) ochazuke.Result
}

// relations are the related nouns, in the order their parameters are
// read (one per call).
func (a *App) relations() []relation {
	return []relation{
		{noun: actionItemNoun, on: []string{"task", "pane", "project"}, param: inboxRelateParam, relate: a.relateActionItemTo},
		{noun: contextNoun, on: []string{"task", "project"}, param: contextRelateParam, relate: a.relateContextItemTo},
		{noun: "pane", on: []string{"task", "project"}, param: paneRelateParam, relate: a.relatePaneTo},
	}
}

// defineRelations gives every reverse verb its relations' parameters and
// declares project.associate, whose only job is the reverse. It runs after
// the nouns' own definitions (task.associate and pane.associate exist).
func (a *App) defineRelations() {
	var names []string
	for _, rel := range a.relations() {
		names = append(names, "--"+rel.noun)
	}
	a.r.Define("project").Verb("associate", a.associateProject, "Associate a thing with this project: "+strings.Join(names, " or ")+" ID")
	for _, rel := range a.relations() {
		for _, noun := range rel.on {
			key := a.r.CanonicalKey(noun + ".associate")
			op := a.r.Operations[key]
			if op.Parameters == nil {
				op.Parameters = map[string]ochazuke.Parameter{}
			}
			op.Parameters[rel.noun] = rel.param
			a.r.Operations[key] = op
		}
	}
}

// related is the relation a reverse verb on noun names and the id it
// names; ok is false when the call names none.
func (a *App) related(ctx ochazuke.Ctx, noun string) (relation, string, bool) {
	for _, rel := range a.relations() {
		if !slices.Contains(rel.on, noun) {
			continue
		}
		if id := ochazuke.StringArg(ctx.Args, rel.noun, ""); id != "" {
			return rel, id, true
		}
	}
	return relation{}, "", false
}

// relatedNames are the parameters a reverse verb on noun takes, for its
// error when none is given.
func (a *App) relatedNames(noun string) string {
	var names []string
	for _, rel := range a.relations() {
		if slices.Contains(rel.on, noun) {
			names = append(names, "--"+rel.noun)
		}
	}
	return strings.Join(names, " or ")
}

// associateProject is project.associate --<noun> ID.
func (a *App) associateProject(ctx ochazuke.Ctx) ochazuke.Result {
	rel, id, ok := a.related(ctx, "project")
	if !ok {
		return ochazuke.Failed(fmt.Errorf("name the thing to associate (%s)", a.relatedNames("project")))
	}
	if ctx.Instance == nil || ctx.Instance.Noun != "project" {
		return ochazuke.Failed(errors.New("target a project row"))
	}
	return rel.relate(id, relOwner{ProjectID: ctx.Instance.ID})
}

// associateTaskRelated is task.associate --<noun> ID.
func (a *App) associateTaskRelated(ctx ochazuke.Ctx, rel relation, id string) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return rel.relate(id, relOwner{ProjectID: t.ProjectID, TaskID: t.ID})
}

// associatePaneRelated is pane.associate --<noun> ID.
func (a *App) associatePaneRelated(ctx ochazuke.Ctx, rel relation, id string) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	o := relOwner{Window: p.ID, Session: p.Session}
	if owner, err := a.work.PaneOwner(p.ID, p.SessionID); err == nil {
		o.ProjectID, o.TaskID = owner.ProjectID, owner.TaskID
	}
	return rel.relate(id, o)
}
