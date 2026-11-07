package main

import (
	"context"
	"errors"
	"strings"

	"ofam.dev/ochazuke"
)

// renamer renames sessions and windows on the daemon; tests replace it.
// A rename is the standard set of the label (r is edit {field: "@label"}
// on pane, session and saved rows): pane_store.go and session_store.go
// write the name through it.
type renamer interface {
	RenameSession(ctx context.Context, session, name string) error
	RenameWindow(ctx context.Context, session, window, name string) error
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
	a.r.Changed("saved")
	return nil
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
