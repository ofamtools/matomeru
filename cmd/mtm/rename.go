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
	if name, given := ctx.Args["name"].(string); given {
		return a.renameWindow(p, name, nil)
	}
	// The form writes as you type: each write renames the window, and a
	// saved record follows while it carries the name the last write left.
	prev := p
	return resultError(a.renameForm("Rename pane "+p.label(), p.Name, false, func(name string) ochazuke.Result {
		return a.renameWindow(prev, name, func(name string) {
			if name != "" {
				prev.Name = name
			}
		})
	}))
}

// renameWindow renames a pane's window on the daemon; its saved records
// that still carry its old name follow, and the pane list reloads. done,
// when given, sees the name once the daemon has it.
func (a *App) renameWindow(p pane, name string, done func(string)) ochazuke.Result {
	name = strings.TrimSpace(name)
	r := a.renamer
	renamed := func(name string) error {
		err := a.renameSavedOf(p, name)
		if done != nil {
			done(name)
		}
		return err
	}
	if a.quitting {
		// mtm is closing: no job would run, so the last write runs here.
		if err := r.RenameWindow(context.Background(), p.Session, p.ID, name); err != nil {
			return ochazuke.Failed(err)
		}
		return resultError(renamed(name))
	}
	return a.afterRename(ochazuke.Pending(func(c context.Context) (any, error) {
		return name, r.RenameWindow(c, p.Session, p.ID, name)
	}), renamed)
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
	if name, given := ctx.Args["name"].(string); given {
		return a.renameSessionTo(p.Session, name, nil)
	}
	// Each write renames the session as the last write left it.
	session := p.Session
	return resultError(a.renameForm("Rename session "+p.Session, p.Session, true, func(name string) ochazuke.Result {
		return a.renameSessionTo(session, name, func(name string) { session = name })
	}))
}

// renameSessionTo renames a session on the daemon and reloads the panes;
// done, when given, sees the name once the daemon has it.
func (a *App) renameSessionTo(session, name string, done func(string)) ochazuke.Result {
	name = strings.TrimSpace(name)
	if name == "" {
		return ochazuke.Failed(errors.New("a session needs a name"))
	}
	r := a.renamer
	if a.quitting {
		err := r.RenameSession(context.Background(), session, name)
		if err == nil && done != nil {
			done(name)
		}
		return resultError(err)
	}
	var renamed func(string) error
	if done != nil {
		renamed = func(name string) error { done(name); return nil }
	}
	return a.afterRename(ochazuke.Pending(func(c context.Context) (any, error) {
		return name, r.RenameSession(c, session, name)
	}), renamed)
}

// renameFormID is the rename form's modal instance; its one field is
// renameFormID/name.
const renameFormID = "rename"

// renameForm opens the rename form: ochazuke's record editor as a modal
// with one name field in insert mode, written as you type (after the
// edit-debounce pause, and at once when it is left). enter and esc leave,
// as the prompt did, keeping what was typed; nothing changed writes
// nothing (UX review 2 F4). A required name left empty is not written.
func (a *App) renameForm(title, current string, required bool, write func(name string) ochazuke.Result) error {
	if a.renaming != nil && a.r.Instances[renameFormID] != nil {
		return errors.New("a rename is already open")
	}
	form, err := a.r.OpenRecordEditor(renameFormID, ochazuke.RecordSpec{
		Title:    title + " · written as you type",
		Fields:   []ochazuke.RecordField{{Name: "name", Parameter: ochazuke.Parameter{Type: "string", Required: required}}},
		Debounce: a.editDebounce(),
		Insert:   true,
		Load:     func() (ochazuke.Record, error) { return ochazuke.Record{"name": current}, nil },
		Save:     func(w ochazuke.RecordWrite) ochazuke.Result { return write(w.Values["name"]) },
		OnLeave:  func() { a.renaming = nil },
	})
	if err != nil {
		return err
	}
	a.renaming = form
	for _, key := range []string{"enter", "esc"} {
		row := ochazuke.Do(ochazuke.FormNoun + ".leave")
		row.Instance, row.Mode, row.Keys = renameFormID+"/name", "insert", key
		if err := a.r.Bind(row); err != nil {
			return err
		}
	}
	return nil
}

// renameFailure is a write the rename form made between messages that
// failed (its line also says "not saved").
func (a *App) renameFailure() error {
	if a.renaming == nil {
		return nil
	}
	return a.renaming.Failure()
}

// flushRename writes the open rename's pending change (on quit).
func (a *App) flushRename() error {
	if a.renaming == nil {
		return nil
	}
	return a.renaming.Flush()
}

// afterRename reloads the pane list once the daemon has the new name,
// after done (when given) records it.
func (a *App) afterRename(work ochazuke.Result, done func(name string) error) ochazuke.Result {
	return work.Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if done != nil {
			name, _ := v.(string)
			if err := done(name); err != nil {
				ctx.Error(err.Error())
			}
		}
		if _, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "refresh", Target: paneListID}); err != nil {
			ctx.Error(err.Error())
		}
	})
}
