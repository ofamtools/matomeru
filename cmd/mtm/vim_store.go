package main

import (
	"context"
	"fmt"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// The vim noun's record side (standard verbs phase 1, lane M3c): its props
// and its store, so vim get, list (--all), set (alias patch), delete,
// watch, rename and move (the project) are ochazuke's standard verbs over
// mtm's Neovim session records. forget is a lasting alias of delete (the
// record and mtm's own session file; d d asks "Forget ...? Edited files
// and the live pane stay."); m is vim.choose-move.
//
// No standard create: a session is made by opening Neovim (vim.open, a
// launch), and save and reopen are real actions too. Without a create a
// delete cannot be undone: the session file it removes is gone as well.
// A move of a running session moves its live pane with it, as a saved
// record's does.

// vimProps are a Neovim session's props: the key; its name (the label);
// its project (move writes it) and task; and the read-only rest.
func (a *App) vimProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	ro := func(name, desc string) ochazuke.PropSpec {
		return ochazuke.PropSpec{Name: name, ReadOnly: true, Parameter: text(desc)}
	}
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The session's key")},
		{Name: "name", Role: ochazuke.RoleLabel, Parameter: ochazuke.Parameter{Type: "string", Required: true, Description: "The session's name (mtm's record; the pane keeps its window name)"}},
		{Name: "project", Role: ochazuke.RoleOwner, FromFocus: true, Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string { return a.ownerLabel(fmt.Sprint(t["project"])) }, Parameter: ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.all", Free: true,
			Description: "The project the session (and its running pane) belongs to (empty: unassociated); one not found is saved unassociated with a notice"}},
		{Name: "task", FromFocus: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "task", Description: "The task the session belongs to; its project wins over project"}},
		{Name: "path", Aliases: []string{"session-file"}, CreateOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.PropPath, Path: "file", Description: "The existing session file reopen restores; metadata copies never own or remove it"},
			Cell: func(_ ochazuke.Ctx, t ochazuke.Thing) string {
				home, _ := a.home()
				return tildePath(fmt.Sprint(t["path"]), home)
			}},
		{Name: "cwd", CreateOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.PropPath, Path: "dir", Description: "The directory Neovim starts in"}},
		ro("session", "The daemon session the pane opens in"),
		ro("window", "The pane (window id) Neovim runs in, while it runs"),
		{Name: "state", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string", Enum: []string{"running", "closed"}, Description: "running while its pane is listed and Neovim has not exited, else closed"}},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the session was opened first (Unix ms)"}},
		{Name: "updated", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the record last changed (Unix ms)"}},
		{Name: "archived", Role: ochazuke.RoleArchive, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Hide the record without changing its session file or live pane"}},
	}
}

// vimStore is the vim noun's store over the session records. history.undo
// may revert its writes (Undo): a rename, a move.
func (a *App) vimStore() ochazuke.Store {
	return ochazuke.Store{
		Kind:    ochazuke.KindCollection,
		Undo:    true,
		Confirm: map[string]string{"delete": "Forget {{label}}? Edited files and the live pane stay."},
		ListParams: map[string]ochazuke.Parameter{
			"all": {Type: "boolean", Description: "Every project, whatever the pin"},
		},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			row, err := a.work.VimSession(id)
			if err != nil {
				return nil, err
			}
			return a.vimThing(row, a.vimState), nil
		},
		List:   a.listVimThings,
		Create: a.createVim,
		Patch:  a.patchVim,
		Delete: a.removeVim,
	}
}

// vimThing is a session as its props, its state from state.
func (a *App) vimThing(row work.VimSession, state func(work.VimSession) string) ochazuke.Thing {
	project, err := a.work.VimProject(row)
	if err != nil {
		project = row.ProjectID
	}
	return ochazuke.Thing{
		"id": row.ID, "name": row.Name, "project": project, "task": row.TaskID, "path": row.Path, "cwd": row.Cwd,
		"session": row.Session, "window": row.WindowID, "state": state(row), "created": row.Created, "updated": row.Updated,
		"archived": row.Archived,
	}
}

// listVimThings is the store's List: the sessions in scope (all: every
// project), each state from the last pane listing; the CLI, which has
// none, asks the daemon now (one that does not answer leaves the stored
// states).
func (a *App) listVimThings(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
	state := a.vimState
	if a.cli {
		state = func(s work.VimSession) string { return s.State }
		ctx, cancel := context.WithTimeout(context.Background(), daemonTimeout)
		defer cancel()
		if panes, err := a.panesd.ListPanes(ctx); err == nil {
			exited, _ := probeExits(ctx, a.panesd, panes)
			running := map[string]bool{}
			for _, p := range panes {
				running[p.ID] = !exited[p.ID]
			}
			state = func(s work.VimSession) string {
				if running[s.WindowID] {
					return "running"
				}
				return "closed"
			}
		}
	}
	sessions, err := a.work.VimSessions()
	if err != nil {
		return nil, err
	}
	a.vimAll = sessions
	all, _ := q.Args["all"].(bool)
	out := make([]ochazuke.Thing, 0, len(sessions))
	for _, row := range sessions {
		project, err := a.work.VimProject(row)
		if err != nil {
			return nil, err
		}
		if q.Every || all || a.inScope(project) {
			out = append(out, a.vimThing(row, state))
		}
	}
	return out, nil
}

// createVim records a recipe for reopen; only open launches Neovim.
// A copied path is a reference, never an owned file or a running pane.
func (a *App) createVim(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	row := work.VimSession{ID: work.NewID(), State: "closed", Created: work.Now(), Updated: work.Now()}
	row.Name, _ = thingString(values, "name")
	row.Path, _ = thingString(values, "path")
	row.Cwd, _ = thingString(values, "cwd")
	row.Archived, _ = values["archived"].(bool)
	row.ProjectID, row.TaskID, _ = a.ownerWrite(c, "", "", values)
	if err := a.work.PutVimSession(row); err != nil {
		return nil, err
	}
	return a.vimThing(row, a.vimState), nil
}

// patchVim is the store's Patch: the name, and the owner, which a running
// session's pane follows.
func (a *App) patchVim(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	row, err := a.work.VimSession(id)
	if err != nil {
		return nil, err
	}
	props, err := thingOver(namePatch{Name: row.Name}, changes)
	if err != nil {
		return nil, err
	}
	row.Name = props.Name
	if archived, ok := changes["archived"].(bool); ok {
		row.Archived = archived
	}
	current, err := a.work.VimProject(row)
	if err != nil {
		return nil, err
	}
	project, task, owned := a.ownerWrite(c, current, row.TaskID, changes)
	if owned {
		row.ProjectID, row.TaskID = project, task
	}
	row.Updated = work.Now()
	if err := a.work.PutVimSession(row); err != nil {
		return nil, err
	}
	if owned && row.WindowID != "" && a.vimState(row) == "running" {
		if err := a.work.SetPaneOwner(work.Assignment{Window: row.WindowID, Session: row.Session, ProjectID: row.ProjectID, TaskID: row.TaskID, AssignedAt: work.Now()}); err != nil {
			return nil, err
		}
		if err := a.refreshScope(); err != nil {
			return nil, err
		}
	} else if err := a.vimList.Refill(); err != nil {
		return nil, err
	}
	return a.vimThing(row, a.vimState), nil
}

// removeVim is the store's Delete (vim.delete, its alias forget, d d; the
// standard delete asks first): the record and the session file mtm wrote
// for it, never the files edited in it or its live pane.
func (a *App) removeVim(c ochazuke.Ctx, id string) error {
	row, err := a.work.VimSession(id)
	if err != nil {
		return err
	}
	if err := forgetVimFile(a.vimDir, row); err != nil {
		return err
	}
	if err := a.work.ForgetVimSession(row.ID); err != nil {
		return err
	}
	if err := a.vimList.Refill(); err != nil {
		return err
	}
	name := row.Name
	if name == "" {
		name = row.ID
	}
	c.Notify(fmt.Sprintf("Forgot %s; edited files and the live pane stay", name))
	return nil
}
