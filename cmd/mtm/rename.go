package main

import (
	"context"
	"errors"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// renamer renames sessions and windows on the daemon; tests replace it.
type renamer interface {
	RenameSession(ctx context.Context, session, name string) error
	RenameWindow(ctx context.Context, session, window, name string) error
}

// defineRenames adds pane.rename and session.rename (r on their rows).
// mtm keys ownership by session and window ids, so a rename keeps every
// association.
func (a *App) defineRenames() {
	a.r.Define("pane").Verb("rename", a.renamePane, "Rename the pane's window; omit name to type it (empty falls back to the shell's title)").Positional("name").
		Mode("normal").Bind("r", ochazuke.Do("pane.rename"))
	a.r.Define("session").Verb("rename", a.renameSession, "Rename the session; omit name to type it").Positional("name").
		Mode("normal").Bind("r", ochazuke.Do("session.rename"))
	a.parameters("pane.rename", map[string]ochazuke.Parameter{"name": {Type: "string"}})
	a.parameters("session.rename", map[string]ochazuke.Parameter{"name": {Type: "string"}})
}

// sessionAt is the pane a session row carries (its session's first).
func (a *App) sessionAt(inst *ochazuke.Instance) (pane, error) {
	if inst != nil {
		if p, ok := inst.Data.(pane); ok {
			return p, nil
		}
		id := strings.TrimPrefix(inst.ID, "session:")
		for _, p := range a.panes {
			if p.SessionID == id {
				return p, nil
			}
		}
	}
	return pane{}, errors.New("no session selected")
}

func (a *App) renamePane(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	name, given := ctx.Args["name"].(string)
	if !given {
		return a.askRename("Rename pane", p.label(), p.Name, "pane", ctx.Instance.ID)
	}
	r := a.renamer
	return a.afterRename(ochazuke.Pending(func(c context.Context) (any, error) {
		return strings.TrimSpace(name), r.RenameWindow(c, p.Session, p.ID, strings.TrimSpace(name))
	}), func(name string) error { return a.renameSavedOf(p, name) })
}

// renameSavedOf follows a pane rename in the pane's saved records that
// still carry its old name (a record renamed on its own keeps its name).
func (a *App) renameSavedOf(p pane, name string) error {
	if name == "" {
		return nil
	}
	rows, err := a.work.SavedPanes()
	if err != nil {
		return err
	}
	changed := false
	for _, row := range rows {
		if row.Window != p.ID || row.Name != p.Name {
			continue
		}
		row.Name = name
		if _, err := a.work.PutSavedPane(row); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return a.refreshSaved()
}

func (a *App) renameSession(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.sessionAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	name, given := ctx.Args["name"].(string)
	if !given {
		return a.askRename("Rename session", "session "+p.Session, p.Session, "session", ctx.Instance.ID)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ochazuke.Failed(errors.New("a session needs a name"))
	}
	r := a.renamer
	return a.afterRename(ochazuke.Pending(func(c context.Context) (any, error) {
		return name, r.RenameSession(c, p.Session, name)
	}), nil)
}

// askRename opens the app's one-line prompt with the current name; enter
// runs the rename with the typed one.
func (a *App) askRename(title, label, current, noun, target string) ochazuke.Result {
	err := a.prompt(title, label, current, func(text string) error {
		_, err := a.r.Execute(ochazuke.Request{Noun: noun, Verb: "rename", Target: target, Args: ochazuke.Arguments{"name": text}})
		return err
	})
	return resultError(err)
}

// afterRename reloads the pane list once the daemon has the new name,
// after done (when given) records it.
func (a *App) afterRename(work ochazuke.Result, done func(name string) error) ochazuke.Result {
	return work.Then(func(_ ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if done != nil {
			name, _ := v.(string)
			if err := done(name); err != nil {
				a.m.Status = err.Error()
			}
		}
		if _, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "refresh", Target: paneListID}); err != nil {
			a.m.Status = err.Error()
		}
	})
}
