package main

import (
	"cmp"
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

// renamePane is pane.rename (r): the standard set of the window's name,
// the name given or typed in the form, each write as you type a set (the
// daemon renames the window as a job, pane_store.go).
func (a *App) renamePane(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	key := p.rowID()
	set := func(name string) ochazuke.Result {
		return a.asVerb("pane.rename", func() ochazuke.Result {
			return resultOf(a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": key, "prop": "name", "value": name}}))
		})
	}
	if name, given := ctx.Args["name"].(string); given {
		return set(name)
	}
	return resultError(a.renameForm("Rename pane "+p.label(), p.Name, false, set))
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

// renameSession is session.rename (r): the standard set of the session's
// name, the name given or typed in the form, each write as you type a set.
func (a *App) renameSession(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.sessionAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	key := cmp.Or(p.SessionID, p.Session)
	set := func(name string) ochazuke.Result {
		return a.asVerb("session.rename", func() ochazuke.Result {
			return resultOf(a.r.Execute(ochazuke.Request{Noun: "session", Verb: "set", Args: ochazuke.Arguments{"id": key, "prop": "name", "value": name}}))
		})
	}
	if name, given := ctx.Args["name"].(string); given {
		return set(name)
	}
	return resultError(a.renameForm("Rename session "+p.Session, p.Session, true, set))
}

// renameFormID is the rename form's modal instance; its one field is
// renameFormID/name.
const renameFormID = "rename"

// renameForm opens the rename form: ochazuke's record editor as a modal
// with one name field in insert mode, written as you type (after the
// edit-debounce pause, and at once when it is left). enter and esc leave,
// as the prompt did, keeping what was typed; nothing changed writes
// nothing (UX review 2 F4). A required name left empty is not written.
// It is a rename (RecordSpec.Rename): with ochazuke's rename.select on (the
// default) the name opens selected, so typing replaces it and a motion
// keeps it (UX-RULES R11.6).
func (a *App) renameForm(title, current string, required bool, write func(name string) ochazuke.Result) error {
	if a.renaming != nil && a.r.Instances[renameFormID] != nil {
		return errors.New("a rename is already open")
	}
	form, err := a.r.OpenRecordEditor(renameFormID, ochazuke.RecordSpec{
		Title:    title + " · written as you type",
		Fields:   []ochazuke.RecordField{{Name: "name", Parameter: ochazuke.Parameter{Type: "string", Required: required}}},
		Debounce: a.editDebounce(),
		Insert:   true,
		Rename:   true,
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
