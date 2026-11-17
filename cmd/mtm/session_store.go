package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
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

// sessionCopyRoles are what a session's copy row keeps of its props
// (copies.go), beside its key and when it ended: its name (held once mtm
// writes it). The project is mtm's session owner record (sessionRecords),
// the pane count the daemon's. The props are declared in
// nouns/session.json.
var sessionCopyRoles = propCopies{{"name", copyHeld}, {"archived", copyOwn}}

// sessionRecords are mtm's own records of a session its copy t stands for
// when the daemon does not list it: its project.
func (a *App) sessionRecords(t ochazuke.Thing) ochazuke.Thing {
	id, _ := t["id"].(string)
	o, err := a.work.SessionOwner(id)
	if err != nil {
		return ochazuke.Thing{"panes": 0}
	}
	return ochazuke.Thing{"owner": ownerRef(o.ProjectID, o.TaskID), "former_owner": ownerRef(o.FormerProjectID, ""), "project": o.ProjectID, "panes": 0}
}

// sessionStore is the session noun's store over the listed panes'
// sessions.
func (a *App) sessionStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.KindCollection,
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
				owner, _ := t["owner"].(string)
				if all || a.inScope(a.inherited(a.work, "session", ochazuke.Thing{"owner": owner}, "project")) {
					out = append(out, t)
				}
			}
			return out, nil
		},
		Patch:       a.patchSession,
		ChangeToken: func(ochazuke.Ctx) (string, error) { return a.sessionChangeToken() },
	}
}

// sessionChangeToken is the session store's change token (ochazuke
// Store.ChangeToken), read without reading a session: the listing's print
// and the scope (listingToken) and the records a read merges in (session
// owners, the tasks an owner reaches: work.SessionRecordsVersion). Unknown
// ("") before the listing is known.
func (a *App) sessionChangeToken() (string, error) {
	listing, ok := a.listingToken()
	if !ok {
		return "", nil
	}
	records, err := a.work.SessionRecordsVersion()
	if err != nil {
		return "", err
	}
	return listing + "|" + records, nil
}

// sessionKeyOf is the session key a key or a session row's id names.
func sessionKeyOf(key string) string { return ochazuke.RefOf("session", key).Key }

// resolveSession is the key (id) of the session text names: its id, or a
// name read through the session noun's store once (ochazuke.ResolveRef,
// the one reading of every session reference given at the edge).
func (a *App) resolveSession(text string) string {
	ref, _ := a.r.ResolveRef(ochazuke.Ctx{Runtime: a.r}, []string{"session"}, text)
	return ref.Key
}

// sessionThings are the listed sessions, in listing order.
func (a *App) sessionThings(c ochazuke.Ctx) ([]ochazuke.Thing, error) {
	panes, err := a.livePanes(c)
	if err != nil {
		return nil, err
	}
	var out []ochazuke.Thing
	at := map[string]int{}
	for _, p := range panes {
		id := p.SessionID
		if id == "" {
			continue
		}
		i, seen := at[id]
		if !seen {
			o, err := a.work.SessionOwner(p.SessionID)
			if err != nil {
				return nil, err
			}
			i = len(out)
			at[id] = i
			// The owner as stored; project (and task) read through it
			// (nouns/session.json: inherit owner).
			out = append(out, ochazuke.Thing{"id": id, "name": p.Session, "owner": ownerRef(o.ProjectID, o.TaskID),
				"former_owner": ownerRef(o.FormerProjectID, ""), "project": "", "panes": 0})
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
	t, err := a.sessionStore().Get(c, p.SessionID)
	if err != nil {
		return nil, err
	}
	if err := checkSessionName(changes); err != nil {
		return nil, err
	}
	if to, ok := thingString(changes, "name"); ok {
		to = strings.TrimSpace(to)
		if err := a.launchSessionRename(c, p.sessionRef().String(), p.Session, to); err != nil {
			return nil, err
		}
		t["name"] = to
	}
	changes = a.ownerChanges("session", changes)
	if task, ok := thingString(changes, "task"); ok && task != "" {
		changes["project"] = a.taskProject(task) // a session owned by a task is in the task's project
	}
	if project, ok := thingString(changes, "project"); ok {
		if p.SessionID == "" {
			return nil, fmt.Errorf("session %s has no id in the daemon yet: list the panes again, then move it", p.Session)
		}
		project, _ = a.noteOwner(c, project, true, "", false)
		task, _ := thingString(changes, "task")
		if err := a.work.SetSessionOwner(work.SessionOwner{ID: p.SessionID, Name: p.Session, ProjectID: project, TaskID: task}); err != nil {
			return nil, err
		}
		if err := a.refreshScope(); err != nil {
			return nil, err
		}
		t["owner"], t["project"] = ownerRef(project, task), ""
	}
	return t, nil
}

// checkSessionName refuses a session name that is empty: the daemon needs
// one, so neither it nor mtm's copy takes it.
func checkSessionName(changes ochazuke.Thing) error {
	if to, ok := thingString(changes, "name"); ok && strings.TrimSpace(to) == "" {
		return errors.New("a session needs a name")
	}
	return nil
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
	return p.SessionID, nil
}

// associateSession is session.associate: from a key or the palette with
// no owner named, the project chooser (as m does), its choice the standard
// move; --project and --scratch are the deprecated form of move (--to).
func (a *App) associateSession(ctx ochazuke.Ctx) ochazuke.Result {
	if a.moving(ctx) {
		return a.moveTo(ctx, "session")
	}
	ctx.Notify(fmt.Sprintf("session.associate --project is an old form of session set ID @owner=PROJECT (since %s; removed after %s)", sessionAssociateSince, sessionAssociateRemoveAfter))
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
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "session", Verb: "set", Args: ochazuke.Arguments{"id": key, "props": map[string]any{"@owner": to}}}))
}

// sessionCopyKey is the key a session's copy is kept under (copies.go) for
// any form of its key: its row id or id, or its name, read from the
// listing, else from the copies (a name mtm gave it included).
func (a *App) sessionCopyKey(key string) string {
	key = sessionKeyOf(key)
	for _, p := range a.panes {
		if p.SessionID != "" && (p.SessionID == key || p.Session == key) {
			return p.SessionID
		}
	}
	copies, err := a.work.Copies("session")
	if err != nil {
		return key
	}
	for _, c := range copies {
		if c.Key == key {
			return key
		}
	}
	for _, c := range copies {
		if name, _ := c.Props["name"].(string); name == key {
			return c.Key
		}
	}
	return key
}
