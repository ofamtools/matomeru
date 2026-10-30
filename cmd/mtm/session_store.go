package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The session noun's record side (standard verbs phase 1, lane M3b): its
// props and its store, so session get, list (--all), set (alias patch),
// watch and move (the project) are ochazuke's standard verbs over the
// daemon's sessions. The name is the daemon's (set renames the session
// there, as a job); the project is mtm's session owner record; the pane
// count is read-only. No create or delete: kill is a real action.
// history.undo reverts a rename and a move (Undo): the name it writes back
// is the one the daemon held, which only a rename changes.

// The deprecated forms of session.associate (Keep actions: one release).
const (
	sessionAssociateSince       = "mtm std-verbs-m3b (2026-10-29)"
	sessionAssociateRemoveAfter = "the next mtm release"
)

// sessionProps are a session's props: the key (the daemon's session id,
// else its name), the name (the label), the owning project, and how many
// panes it holds.
func sessionProps() []ochazuke.PropSpec {
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: ochazuke.Parameter{Type: "string", Description: "The session's id (or its row id, session:ID)"}},
		{Name: "name", Role: ochazuke.RoleLabel, Parameter: ochazuke.Parameter{Type: "string", Description: "The session's name in the daemon (never empty)"}},
		{Name: "project", Role: ochazuke.RoleOwner, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.list", Free: true,
			Description: "The project the session belongs to (empty: unassociated); panes with their own owner keep it"}},
		{Name: "panes", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "How many panes the session holds"}},
	}
}

// sessionStore is the session noun's store over the listed panes'
// sessions.
func (a *App) sessionStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true,
		ListParams: map[string]ochazuke.Parameter{
			"all": {Type: "boolean", Description: "Every project, whatever the pin"},
		},
		Get: func(c ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			things, err := a.sessionThings(c)
			if err != nil {
				return nil, err
			}
			want := sessionKeyOf(id)
			for _, t := range things {
				if t["id"] == want {
					return t, nil
				}
			}
			for _, t := range things {
				if t["name"] == want {
					return t, nil
				}
			}
			return nil, fmt.Errorf("unknown session %q", id)
		},
		List: func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			things, err := a.sessionThings(c)
			if err != nil {
				return nil, err
			}
			all, _ := q.Args["all"].(bool)
			out := []ochazuke.Thing{}
			for _, t := range things {
				if project, _ := t["project"].(string); all || a.inScope(project) {
					out = append(out, t)
				}
			}
			return out, nil
		},
		Patch: a.patchSession,
	}
}

// sessionKeyOf is a session key without its row prefix.
func sessionKeyOf(key string) string { return strings.TrimPrefix(key, "session:") }

// sessionThings are the listed sessions, in listing order.
func (a *App) sessionThings(c ochazuke.Ctx) ([]ochazuke.Thing, error) {
	panes, err := a.livePanes(c)
	if err != nil {
		return nil, err
	}
	var out []ochazuke.Thing
	at := map[string]int{}
	for _, p := range panes {
		id := cmp.Or(p.SessionID, p.Session)
		if id == "" {
			continue
		}
		i, seen := at[id]
		if !seen {
			project := ""
			if p.SessionID != "" {
				o, err := a.work.SessionOwner(p.SessionID)
				if err != nil {
					return nil, err
				}
				project = o.ProjectID
			}
			i = len(out)
			at[id] = i
			out = append(out, ochazuke.Thing{"id": id, "name": p.Session, "project": project, "panes": 0})
		}
		if p.ID != "" {
			out[i]["panes"] = out[i]["panes"].(int) + 1
		}
	}
	return out, nil
}

// sessionPaneOf is the first listed pane of the session a key names (its
// id, row id or name).
func (a *App) sessionPaneOf(c ochazuke.Ctx, key string) (pane, error) {
	panes, err := a.livePanes(c)
	if err != nil {
		return pane{}, err
	}
	want := sessionKeyOf(key)
	for _, p := range panes {
		if want != "" && (p.SessionID == want || p.Session == want) {
			return p, nil
		}
	}
	return pane{}, fmt.Errorf("unknown session %q", key)
}

// patchSession is the store's Patch: the name in the daemon (a job), the
// project in mtm's session owners.
func (a *App) patchSession(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	p, err := a.sessionPaneOf(c, id)
	if err != nil {
		return nil, err
	}
	t, err := a.sessionStore().Get(c, cmp.Or(p.SessionID, p.Session))
	if err != nil {
		return nil, err
	}
	if to, ok := thingString(changes, "name"); ok {
		to = strings.TrimSpace(to)
		if to == "" {
			return nil, errors.New("a session needs a name")
		}
		if err := a.launchSessionRename(c, "session:"+cmp.Or(p.SessionID, p.Session), p.Session, to); err != nil {
			return nil, err
		}
		t["name"] = to
	}
	if project, ok := thingString(changes, "project"); ok {
		if p.SessionID == "" {
			return nil, fmt.Errorf("session %s has no id in the daemon yet: list the panes again, then move it", p.Session)
		}
		project, _ = a.noteOwner(c, project, true, "", false)
		if err := a.work.SetSessionOwner(work.SessionOwner{ID: p.SessionID, Name: p.Session, ProjectID: project}); err != nil {
			return nil, err
		}
		if err := a.refreshScope(); err != nil {
			return nil, err
		}
		t["project"] = project
	}
	return t, nil
}

// launchSessionRename renames a session in the daemon as a job of the
// write; the list reloads after it. While mtm closes no job runs, so the
// write runs here.
func (a *App) launchSessionRename(c ochazuke.Ctx, row, session, name string) error {
	r := a.renamer
	if a.quitting {
		return r.RenameSession(context.Background(), session, name)
	}
	res := a.afterRename(ochazuke.Pending(func(ctx context.Context) (any, error) {
		return name, r.RenameSession(ctx, session, name)
	}), nil)
	_, err := c.Runtime.Launch(a.jobOwner(row), a.jobVerb(c, "session.set"), res, c)
	return err
}

// sessionKey is the key the session's standard verbs take for the session
// row at inst.
func (a *App) sessionKey(inst *ochazuke.Instance) (string, error) {
	p, err := a.sessionAt(inst)
	if err != nil {
		return "", err
	}
	return cmp.Or(p.SessionID, p.Session), nil
}

// associateSession is session.associate: from a key or the palette with
// no owner named, the project chooser (as m does), its choice the standard
// move; --project and --scratch are the deprecated form of move (--to).
func (a *App) associateSession(ctx ochazuke.Ctx) ochazuke.Result {
	if a.moving(ctx) {
		return a.moveTo(ctx, "session")
	}
	ctx.Notify(fmt.Sprintf("session.associate --project is an old form of session.move --to (since %s; removed after %s)", sessionAssociateSince, sessionAssociateRemoveAfter))
	return ctx.Each(a.associateSessionOne)
}

// associateSessionOne is associate's old form on one session: the
// standard move to the project named (--scratch, or none: unassociated;
// a task names its project).
func (a *App) associateSessionOne(ctx ochazuke.Ctx) ochazuke.Result {
	key, err := a.sessionKey(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	to := ""
	if scratch, _ := ctx.Args["scratch"].(bool); !scratch {
		to = ochazuke.StringArg(ctx.Args, "project", "")
		if task := ochazuke.StringArg(ctx.Args, "task", ""); task != "" {
			to = a.taskProject(task)
		}
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "session", Verb: "move", Args: ochazuke.Arguments{"id": key, "to": to}}))
}
