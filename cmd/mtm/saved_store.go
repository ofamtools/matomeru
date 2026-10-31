package main

import (
	"fmt"
	"slices"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// The saved noun's record side (standard verbs phase 1, lane M3b): its
// props and its store, so saved get, list (--all), create, set (alias
// patch), delete, watch, move (the project) and duplicate are ochazuke's
// standard verbs over the saved pane records. forget is a lasting alias of
// delete (the record only; d d asks "Forget ...? Its pane keeps
// running."); undo puts a forgotten record back as it was. rename keeps
// its form written as you type (a wrap of set name); save (a live pane's
// recipe), reopen and associate's chooser stay mtm's own; m is
// saved.choose-move.
//
// A record's owner is the pane's as well: a write of project or task
// moves the live pane it names with it, as saved.associate always did.

// The deprecated forms of saved.associate (Keep actions: one release).
const (
	savedAssociateSince       = "mtm std-verbs-m3b (2026-10-29)"
	savedAssociateRemoveAfter = "the next mtm release"
)

// savedProps are a saved record's props: the key; its name (the label);
// its project (move writes it) and task, both filled from the focus by
// create; the recipe (command, env, cwd, harness, model, flags), written by
// create only; and the read-only pane it was saved from and its times.
func savedProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The record's key")},
		{Name: "name", Role: ochazuke.RoleLabel, Parameter: text("The record's name (reopen names the window after it)")},
		{Name: "project", Role: ochazuke.RoleOwner, FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.list", Free: true,
			Description: "The project the record (and its live pane) belongs to (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "task", Description: "The task the record belongs to; its project wins over project"}},
		{Name: "command", CreateOnly: true, Parameter: ochazuke.Parameter{Type: "list", Description: "The argv reopen runs (empty: a daemon shell)"}},
		{Name: "env", CreateOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.PropObject, Description: "Environment variables reopen sets, an object of name to value"}},
		{Name: "cwd", CreateOnly: true, Parameter: text("The directory reopen starts in")},
		{Name: "harness", CreateOnly: true, Parameter: text("The agent's harness, for an agent's record")},
		{Name: "model", CreateOnly: true, Parameter: text("The agent's model, for an agent's record")},
		{Name: "flags", CreateOnly: true, Parameter: ochazuke.Parameter{Type: "list", Description: "The harness flags the agent was launched with"}},
		{Name: "window", ReadOnly: true, Parameter: text("The pane (window id) the record reopens: attached while it is live")},
		{Name: "session", ReadOnly: true, Parameter: text("The session the pane was in")},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the record was saved (Unix ms)"}},
		{Name: "updated", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the record last changed (Unix ms)"}},
	}
}

// savedStore is the saved noun's store over the saved pane records.
// history.undo may revert its writes (Undo); an undone delete puts the
// record back as it was (AcceptsKey, savedForgotten).
func (a *App) savedStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true, AcceptsKey: true,
		Confirm: map[string]string{"delete": "Forget {{label}}? Its pane keeps running."},
		ListParams: map[string]ochazuke.Parameter{
			"all": {Type: "boolean", Description: "Every project, whatever the pin"},
		},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			row, err := a.work.SavedPane(id)
			if err != nil {
				return nil, err
			}
			return savedThing(row), nil
		},
		List: func(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			rows, err := a.work.SavedPanes()
			if err != nil {
				return nil, err
			}
			all, _ := q.Args["all"].(bool)
			out := []ochazuke.Thing{}
			for _, row := range rows {
				if all || a.inScope(row.ProjectID) {
					out = append(out, savedThing(row))
				}
			}
			return out, nil
		},
		Create: a.createSaved,
		Patch:  a.patchSaved,
		Delete: a.removeSaved,
	}
}

// savedThing is a record as its props.
func savedThing(row work.SavedPane) ochazuke.Thing {
	t := ochazuke.Thing{
		"id": row.ID, "name": row.Name, "project": row.ProjectID, "task": row.TaskID,
		"command": slices.Clone(row.Command), "cwd": row.Cwd, "harness": row.Harness, "model": row.Model,
		"flags": slices.Clone(row.Flags), "window": row.Window, "session": row.Session,
		"created": row.Created, "updated": row.Updated,
	}
	if t["command"] == nil {
		t["command"] = []string{}
	}
	if t["flags"] == nil {
		t["flags"] = []string{}
	}
	env := map[string]any{}
	for k, v := range row.Env {
		env[k] = v
	}
	t["env"] = env
	return t
}

// thingList reads a list prop's value (typed by the prop, or from an
// undo's record) as words.
func thingList(v any) []string {
	switch l := v.(type) {
	case []string:
		return slices.Clone(l)
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

// thingEnv reads an object prop's value as an environment.
func thingEnv(v any) map[string]string {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, x := range m {
		out[k] = fmt.Sprint(x)
	}
	return out
}

// createSaved is the store's Create: a record from the props given (a
// recipe written by hand; one with no command reopens a daemon shell),
// under the key given when it is free. The key of a record this session
// forgot puts that record back as it was (history.undo of a delete), with
// the props given over it.
func (a *App) createSaved(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	id, _ := thingString(values, "id")
	if id != "" {
		if _, err := a.work.SavedPane(id); err == nil {
			id = ""
		}
	}
	row, restored := a.savedForgotten[id]
	if !restored {
		row = work.SavedPane{ID: id}
	}
	delete(a.savedForgotten, id)
	if v, ok := thingString(values, "name"); ok {
		row.Name = v
	}
	for name, field := range map[string]*string{"cwd": &row.Cwd, "harness": &row.Harness, "model": &row.Model} {
		if v, ok := thingString(values, name); ok {
			*field = v
		}
	}
	if v, ok := values["command"]; ok {
		row.Command = thingList(v)
	}
	if v, ok := values["flags"]; ok {
		row.Flags = thingList(v)
	}
	if v, ok := values["env"]; ok {
		row.Env = thingEnv(v)
	}
	if project, task, ok := a.ownerWrite(c, row.ProjectID, row.TaskID, values); ok {
		row.ProjectID, row.TaskID = project, task
	}
	saved, err := a.work.PutSavedPane(row)
	if err != nil {
		return nil, err
	}
	if err := a.refreshSaved(); err != nil {
		return nil, err
	}
	return savedThing(saved), nil
}

// patchSaved is the store's Patch: the name, and the owner, which the
// live pane the record names follows.
func (a *App) patchSaved(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	row, err := a.work.SavedPane(id)
	if err != nil {
		return nil, err
	}
	if v, ok := thingString(changes, "name"); ok {
		row.Name = v
	}
	project, task, owned := a.ownerWrite(c, row.ProjectID, row.TaskID, changes)
	if owned {
		row.ProjectID, row.TaskID = project, task
	}
	if row, err = a.work.PutSavedPane(row); err != nil {
		return nil, err
	}
	if owned && row.Window != "" {
		if err := a.work.SetPaneOwner(work.Assignment{Window: row.Window, Session: row.Session, ProjectID: row.ProjectID, TaskID: row.TaskID, AssignedAt: work.Now()}); err != nil {
			return nil, err
		}
		if err := a.refreshScope(); err != nil {
			return nil, err
		}
	} else if err := a.refreshSaved(); err != nil {
		return nil, err
	}
	return savedThing(row), nil
}

// removeSaved is the store's Delete (saved.delete, its alias forget, d d;
// the standard delete asks first): the record only, never its pane. The
// record is kept for this session, so history.undo puts it back as it was.
func (a *App) removeSaved(c ochazuke.Ctx, id string) error {
	row, err := a.work.SavedPane(id)
	if err != nil {
		return err
	}
	if err := a.work.ForgetSavedPane(row.ID); err != nil {
		return err
	}
	if a.savedForgotten == nil {
		a.savedForgotten = map[string]work.SavedPane{}
	}
	a.savedForgotten[row.ID] = row
	if err := a.refreshSaved(); err != nil {
		return err
	}
	name := row.Name
	if name == "" {
		name = row.ID
	}
	c.Notify("Forgot " + name + "; its pane keeps running")
	return nil
}

// savedKey is the key the saved noun's standard verbs take for the
// request: its id argument, else the selected record.
func (a *App) savedKey(ctx ochazuke.Ctx) (string, error) {
	row, err := a.savedAt(ctx)
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

// associateSavedOne is saved.associate's old form on one record, its owner
// given: the standard set of its project and task (--scratch: none).
func (a *App) associateSavedOne(ctx ochazuke.Ctx) ochazuke.Result {
	key, err := a.savedKey(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	changes := map[string]any{"project": "", "task": ""}
	if scratch, _ := ctx.Args["scratch"].(bool); !scratch {
		if v, ok := ctx.Args["project"]; ok {
			changes["project"] = fmt.Sprint(v)
		}
		if v, ok := ctx.Args["task"]; ok {
			changes = map[string]any{"task": fmt.Sprint(v)}
		}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "saved", Verb: "set", Args: ochazuke.Arguments{"id": key, "changes": changes}}))
}
