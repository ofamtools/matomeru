package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

const savedPage = "saved"
const savedListID = "saved/list"

var savedColumns = []ochazuke.Column{{Title: "name", Min: 14}, {Title: "project", Priority: 3}, {Title: "command", Priority: 2}, {Title: "cwd", Priority: 1}}

func (a *App) defineSaved() {
	a.r.Define("saved").Columns(savedColumns...).
		Verb("save", a.savePane, "Save a live pane's confirmed launch recipe").Positional("pane", "name").
		Verb("list", a.listSaved, "List saved shells and agents in scope").Rows("saved").
		Verb("reopen", a.reopenSaved, "Attach the live pane, otherwise relaunch its recorded argv").Positional("id").
		Verb("rename", a.renameSaved, "Rename the saved record").Positional("id", "name").
		Verb("associate", a.associateSaved, "Associate the saved record and its live pane").Positional("id").
		Verb("forget", a.forgetSaved, "Forget only the record; never kill its pane").Positional("id").Confirm("Forget {{label}}? Its pane keeps running.").
		Mode("normal").Bind("w", ochazuke.Do("saved.save")).
		Bind("enter", ochazuke.Do("saved.reopen")).Bind("r", ochazuke.Do("saved.rename")).
		Bind("m", ochazuke.Do("saved.associate")).Bind("d d", ochazuke.Do("saved.forget"))
	a.r.Define("pane").Mode("normal").Bind("w", ochazuke.Do("saved.save"))
	a.parameters("saved.save", map[string]ochazuke.Parameter{"pane": {Type: "string"}, "name": {Type: "string"}, "session": {Type: "string"}})
	a.parameters("saved.list", map[string]ochazuke.Parameter{"all": {Type: "boolean"}})
	for _, verb := range []string{"reopen", "rename", "associate", "forget"} {
		params := map[string]ochazuke.Parameter{"id": {Type: "string", Source: "saved.list"}}
		if verb == "rename" {
			params["name"] = ochazuke.Parameter{Type: "string"}
		}
		if verb == "associate" {
			params["project"] = ochazuke.Parameter{Type: "string", Source: "projects.list"}
			params["task"] = ochazuke.Parameter{Type: "string"}
			params["scratch"] = ochazuke.Parameter{Type: "boolean"}
		}
		a.parameters("saved."+verb, params)
	}
	a.ownedBy("saved", savedListID)
}

func (a *App) addSavedPage() error {
	a.r.Instance(savedPage, ochazuke.Kind("page"))
	var err error
	a.savedList, err = a.r.AddList(savedListID, nil, ochazuke.Parent(savedPage), ochazuke.Columns(savedColumns...))
	if err != nil {
		return err
	}
	if _, err = a.r.AddPage(savedPage, "Saved", func(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
		return ochazuke.Text(a.view(a.savedList, width, height))
	}, ochazuke.Focus(savedListID)); err != nil {
		return err
	}
	return a.refreshSaved()
}

func (a *App) savedRows() ([]ocli.Row, error) {
	rows, err := a.work.SavedPanes()
	if err != nil {
		return nil, err
	}
	out := make([]ocli.Row, 0, len(rows))
	for _, row := range rows {
		if !a.inScope(row.ProjectID) {
			continue
		}
		command := strings.Join(row.Command, " ")
		if command == "" {
			command = "daemon shell"
		}
		name := row.Name
		if name == "" {
			name = row.ID
		}
		out = append(out, ocli.Row{Noun: "saved", ID: row.ID, Label: name, Cells: []string{name, a.ownerLabel(row.ProjectID), command, row.Cwd}, Data: row})
	}
	return out, nil
}

func (a *App) refreshSaved() error {
	if a.savedList == nil {
		return nil
	}
	rows, err := a.savedRows()
	if err != nil {
		return err
	}
	items := make([]ochazuke.ListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ochazuke.ListItem{ID: row.ID, Noun: "saved", Label: row.Label, Cells: row.Cells, Data: row.Data})
	}
	return a.savedList.SetItems(items)
}

func (a *App) listSaved(ctx ochazuke.Ctx) ochazuke.Result {
	previous := a.all
	if all, _ := ctx.Args["all"].(bool); all {
		a.all = true
	}
	defer func() { a.all = previous }()
	rows, err := a.savedRows()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(rows)
}

func (a *App) savedAt(ctx ochazuke.Ctx) (work.SavedPane, error) {
	id := ochazuke.StringArg(ctx.Args, "id", "")
	if id == "" && ctx.Instance != nil && ctx.Instance.Noun == "saved" {
		id = ctx.Instance.ID
	}
	if id == "" {
		return work.SavedPane{}, errors.New("specify a saved id or select a saved row")
	}
	return a.work.SavedPane(id)
}

func (a *App) savePane(ctx ochazuke.Ctx) ochazuke.Result {
	id := ochazuke.StringArg(ctx.Args, "pane", "")
	session := ochazuke.StringArg(ctx.Args, "session", "")
	if id == "" && ctx.Instance != nil && ctx.Instance.Noun == "saved" {
		row, err := a.savedAt(ctx)
		if err != nil {
			return ochazuke.Failed(err)
		}
		id, session = row.Window, row.Session
	}
	if id == "" && ctx.Instance != nil && ctx.Instance.Noun == "pane" {
		p, err := a.paneAt(ctx.Instance)
		if err != nil {
			return ochazuke.Failed(err)
		}
		id, session = p.ID, p.Session
	}
	if id == "" {
		return ochazuke.Failed(errors.New("specify a live pane id or target a pane row"))
	}
	name := ochazuke.StringArg(ctx.Args, "name", "")
	db, panesd, driver := a.work, a.panesd, a.launcher
	return ochazuke.Pending(func(c context.Context) (any, error) {
		panes, err := panesd.ListPanes(c)
		if err != nil {
			return nil, err
		}
		for _, p := range panes {
			if p.ID != id || (session != "" && p.Session != session) {
				continue
			}
			recipe, err := db.PaneRecipe(p.ID)
			if errors.Is(err, store.ErrNotFound) {
				recipe, err = externalRecipe(c, driver, p)
			}
			if err != nil {
				return nil, err
			}
			owner, err := db.PaneOwner(p.ID, p.SessionID)
			if err != nil {
				return nil, err
			}
			recipe.ProjectID, recipe.TaskID = owner.ProjectID, owner.TaskID
			recipe.Session, recipe.SessionID = p.Session, p.SessionID
			if name != "" {
				recipe.Name = name
			}
			rows, err := db.SavedPanes()
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if row.Window == p.ID {
					recipe.Created = row.Created
					if name == "" {
						recipe.Name = row.Name
					}
					return db.PutSavedPane(work.SavedPane{ID: row.ID, PaneRecipe: recipe})
				}
			}
			return db.PutSavedPane(work.SavedPane{PaneRecipe: recipe})
		}
		return nil, fmt.Errorf("live pane %s not found", id)
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			if err := a.refreshSaved(); err != nil {
				a.m.Notify(err.Error())
			}
		}
	})
}

// Current TUIOS versions may omit launch argv entirely. Never turn a title,
// foreground command or capture-pane cmdline into a purported launch recipe.
func externalRecipe(ctx context.Context, driver harness.Driver, p pane) (work.PaneRecipe, error) {
	t, ok := driver.(tuios)
	if !ok {
		return work.PaneRecipe{}, errors.New("no confirmed launch recipe for this external pane")
	}
	var out struct {
		Command []string          `json:"command"`
		Env     map[string]string `json:"env"`
		Cwd     string            `json:"cwd"`
	}
	if err := t.Call(ctx, "get-window", map[string]any{"session": p.Session, "window": p.ID}, &out); err != nil {
		return work.PaneRecipe{}, err
	}
	if len(out.Command) == 0 || out.Cwd == "" {
		return work.PaneRecipe{}, errors.New("daemon does not expose this external pane's exact launch argv/cwd; save a pane launched by mtm instead")
	}
	return work.PaneRecipe{Window: p.ID, Session: p.Session, SessionID: p.SessionID, Name: p.Name, Command: out.Command, Env: out.Env, Cwd: out.Cwd}, nil
}

func savedCommand(recipe work.PaneRecipe) []string {
	if len(recipe.Env) == 0 {
		return slices.Clone(recipe.Command)
	}
	keys := make([]string, 0, len(recipe.Env))
	for key := range recipe.Env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	argv := []string{"env"}
	for _, key := range keys {
		argv = append(argv, key+"="+recipe.Env[key])
	}
	return append(argv, recipe.Command...)
}

func (a *App) reopenSaved(ctx ochazuke.Ctx) ochazuke.Result {
	row, err := a.savedAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	owner := Owner{ProjectID: row.ProjectID, TaskID: row.TaskID}
	version := a.pinVersion
	db, panesd, driver := a.work, a.panesd, a.launcher
	return ochazuke.Pending(func(c context.Context) (any, error) {
		panes, err := panesd.ListPanes(c)
		if err != nil {
			return nil, err
		} // Unknown liveness must not create a duplicate.
		for _, p := range panes {
			if p.ID == row.Window {
				return launched{Pane: harness.Result{Session: p.Session, Window: p.ID, Name: p.Name}, Panes: panes}, nil
			}
		}
		session := mtmOnlySession
		if row.Harness != "" {
			session = noProjectSession
		}
		if row.ProjectID != "" {
			if p, err := db.Project(row.ProjectID); err == nil {
				session = sessionFor(p)
			} else {
				return nil, err
			}
		}
		d := &recordingDriver{Driver: driver, work: db, owner: owner, harness: row.Harness, model: row.Model}
		id, err := d.OpenWindow(c, harness.Window{Session: session, Name: row.Name, Cwd: row.Cwd, Command: savedCommand(row.PaneRecipe)})
		if err != nil {
			return nil, err
		}
		row.Window, row.Session, row.SessionID = id, session, ""
		if _, err := db.PutSavedPane(row); err != nil {
			d.note(fmt.Errorf("pane %s is open but saved reference failed: %w", id, err))
		}
		panes, err = panesd.ListPanes(c)
		if err != nil {
			d.note(err)
		}
		found := false
		for _, p := range panes {
			if p.ID == id {
				found = true
			}
		}
		if !found {
			panes = append(panes, pane{Session: session, Pane: daemon.Pane{ID: id, Name: row.Name, Cwd: row.Cwd}})
		}
		return launched{Pane: harness.Result{Session: session, Window: id, Name: row.Name, Harness: row.Harness, Argv: row.Command}, Panes: panes, Note: strings.Join(d.notes, "; ")}, nil
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if err := a.refreshSaved(); err != nil {
			a.m.Notify(err.Error())
		}
		a.finishLaunch(v.(launched), owner, version)
	})
}

func (a *App) renameSaved(ctx ochazuke.Ctx) ochazuke.Result {
	row, err := a.savedAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	name, ok := ctx.Args["name"].(string)
	if !ok {
		err := a.prompt("Rename saved pane", "Name", row.Name, func(text string) error {
			latest, err := a.work.SavedPane(row.ID)
			if err != nil {
				return err
			}
			latest.Name = text
			if _, err := a.work.PutSavedPane(latest); err != nil {
				return err
			}
			return a.refreshSaved()
		})
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(nil)
	}
	row.Name = name
	return a.putSaved(row)
}

func (a *App) associateSaved(ctx ochazuke.Ctx) ochazuke.Result {
	row, err := a.savedAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	_, hasProject := ctx.Args["project"]
	_, hasTask := ctx.Args["task"]
	_, hasScratch := ctx.Args["scratch"]
	if !a.cli && !hasProject && !hasTask && !hasScratch && ctx.Instance != nil && ctx.Instance.Noun == "saved" {
		return a.moveTo(ctx, "saved") // the project chooser, as m on every row
	}
	if err := a.associateSavedRecord(row, ctx.Args); err != nil {
		return ochazuke.Failed(err)
	}
	updated, err := a.work.SavedPane(row.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(updated)
}

func (a *App) associateSavedRecord(row work.SavedPane, args ochazuke.Arguments) error {
	owner, err := a.resolveOwner(ochazuke.Ctx{}, args)
	if err != nil {
		return err
	}
	row.ProjectID, row.TaskID = owner.ProjectID, owner.TaskID
	if _, err := a.work.PutSavedPane(row); err != nil {
		return err
	}
	if err := a.work.SetPaneOwner(work.Assignment{Window: row.Window, Session: row.Session, ProjectID: row.ProjectID, TaskID: row.TaskID, AssignedAt: work.Now()}); err != nil {
		return err
	}
	if owner.Notice != "" {
		a.m.Notify(owner.Notice)
	}
	return a.refreshScope()
}

func (a *App) putSaved(row work.SavedPane) ochazuke.Result {
	saved, err := a.work.PutSavedPane(row)
	if err == nil {
		err = a.refreshSaved()
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(saved)
}

func (a *App) forgetSaved(ctx ochazuke.Ctx) ochazuke.Result {
	row, err := a.savedAt(ctx)
	if err == nil {
		err = a.work.ForgetSavedPane(row.ID)
	}
	if err == nil {
		err = a.refreshSaved()
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(row.ID)
}
