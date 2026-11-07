package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

const savedPage = "saved"
const savedListID = "saved/list"

var savedColumns = []ochazuke.Column{{Title: "name", Min: 14}, {Title: "project", Priority: 3}, {Title: "command", Priority: 2}, {Title: "cwd", Priority: 1}}

func (a *App) defineSaved() {
	// Props and the store first: the standard verbs (get, list, create,
	// set, delete, watch, move, duplicate) come from them (saved_store.go);
	// forget is delete's lasting alias, r is edit of @label, m edit of @owner, associate's old
	// forms run set; save, reopen and the chooser are mtm's own.
	a.r.Define("saved").Props(a.savedProps()...).Store(a.savedStore()).Columns(savedColumns...).
		Verb("save", a.savePane, "Save a live pane's confirmed launch recipe").Positional("pane", "name").
		Verb("reopen", a.reopenSaved, "Attach the live pane, otherwise relaunch its recorded argv").Positional("id").
		Verb("associate", a.associateSaved, "Choose the project to move the record and its live pane to (omit the owner); --project, --task and --scratch are the deprecated forms of move and set").Positional("id").Many().
		Verb("choose-move", a.chooseMove, "Choose the project to move the record and its live pane to (most recent first, unassociated last); the choice is the standard set of @owner").Many().
		Mode("normal").Bind("w", ochazuke.Do("saved.save")).
		Bind("enter", ochazuke.Do("saved.reopen")).Bind("r", ochazuke.Do("saved.edit", ochazuke.Arguments{"field": "@label"}).Hinted("rename")).
		Bind("m", ochazuke.Do("saved.edit", ochazuke.Arguments{"field": "@owner"}).Hinted("move")).Bind("d d", ochazuke.Do("saved.delete"))
	// forget is the noun's own word for delete (the record only), kept as
	// a lasting alias: saved forget ID works as before.
	if err := a.r.AddActionAlias(ochazuke.ActionAlias{Alias: "saved.forget", Action: "saved.delete",
		Note: "the record only; its pane keeps running"}); err != nil {
		panic(err)
	}
	a.r.Define("pane").Mode("normal").Bind("w", ochazuke.Do("saved.save"))
	a.parameters("saved.save", map[string]ochazuke.Parameter{"pane": {Type: "string"}, "name": {Type: "string"}, "session": {Type: "string"}})
	for _, verb := range []string{"reopen", "associate"} {
		params := map[string]ochazuke.Parameter{"id": {Type: "string", Source: "saved.list"}}
		if verb == "associate" {
			params["project"] = ochazuke.Parameter{Type: "string", Noun: "project", Source: "projects.all", Free: true, Description: "Deprecated: saved set ID @owner=PROJECT (id or name)"}
			params["task"] = ochazuke.Parameter{Type: "string", Description: "Deprecated: saved set ID task=TASK (its project follows)"}
			params["scratch"] = ochazuke.Parameter{Type: "boolean", Description: "Deprecated: saved set ID @owner= (unassociate it)"}
		}
		a.parameters("saved."+verb, params)
	}
	a.ownedBy("saved", savedListID)
}

func (a *App) addSavedPage() error {
	a.r.Instance(savedPage, ochazuke.Kind("page"))
	var err error
	a.savedList, err = a.r.AddList(savedListID, nil, ochazuke.Parent(savedPage), ochazuke.From("saved"))
	if err != nil {
		return err
	}
	if a.detail.saved, err = a.addLinkedDetail(savedDetailID, savedPage, func() *ochazuke.List { return a.savedList }, a.savedSubject); err != nil {
		return err
	}
	if _, err = a.r.AddPage(savedPage, "Saved", a.renderSaved, ochazuke.Focus(savedListID)); err != nil {
		return err
	}
	// The list is the saved store's view and fills itself after any change
	// of the noun; opening reads again (another mtm may have changed them).
	a.onOpen(savedPage, a.savedList.Refill)
	return nil
}

// savedRowKey is the record key a saved row stands for: its thing's id.
func savedRowKey(row *ochazuke.Instance) string {
	if t, ok := row.Data.(ochazuke.Thing); ok {
		id, _ := t["id"].(string)
		return id
	}
	return ""
}

func (a *App) savedAt(ctx ochazuke.Ctx) (work.SavedPane, error) {
	id := ochazuke.StringArg(ctx.Args, "id", "")
	if id == "" && ctx.Instance != nil && ctx.Instance.Noun == "saved" {
		id = savedRowKey(ctx.Instance)
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
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err == nil {
			a.r.Changed("saved")
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
		old := row.Window
		row.Window, row.Session, row.SessionID = id, session, ""
		if _, err := db.PutSavedPane(row); err != nil {
			d.note(fmt.Errorf("pane %s is open but saved reference failed: %w", id, err))
		}
		// A star follows its record into the new window (pane.star).
		if err := db.MovePaneStar(old, id); err != nil {
			d.note(fmt.Errorf("pane %s: star not moved: %w", id, err))
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
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		a.r.Changed("saved")
		a.finishLaunch(v.(launched), owner, version)
	}).Label(row.Harness) // "" for a shell: no label
}

// associateSaved is saved.associate. From a key or the palette with no
// owner named, the project chooser asks (as m does), its choice the
// standard move; --project, --task and --scratch are the deprecated forms
// of move and set.
func (a *App) associateSaved(ctx ochazuke.Ctx) ochazuke.Result {
	_, hasProject := ctx.Args["project"]
	_, hasTask := ctx.Args["task"]
	_, hasScratch := ctx.Args["scratch"]
	if !a.cli && !hasProject && !hasTask && !hasScratch && ctx.Instance != nil && ctx.Instance.Noun == "saved" {
		return a.moveTo(ctx, "saved") // the project chooser, as m on every row
	}
	ctx.Notify(fmt.Sprintf("saved.associate --project is an old form of saved set ID @owner=PROJECT, --task of saved set ID task=TASK (since %s; removed after %s)", savedAssociateSince, savedAssociateRemoveAfter))
	return ctx.Each(a.associateSavedOne)
}
