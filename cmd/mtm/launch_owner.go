package main

import (
	"context"
	"fmt"
	"strings"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/work"
)

// recordingDriver persists a pane before the harness starts waiting for readiness.
// A failed write must not hide a successfully created pane or start a second one.
type recordingDriver struct {
	harness.Driver
	work *work.Store
	// actor is who started the launch, taken on the loop when the job
	// starts: the driver writes off the loop, where the running request
	// (if any) is someone else's.
	actor   work.Actor
	owner   Owner
	harness string
	model   string
	// sessions are the ids of the sessions this launch created, by the
	// name it asked for: the pane's assignment names its session by id.
	sessions   map[string]string
	notes      []string
	unrecorded bool
}

func (d *recordingDriver) note(err error) {
	if err != nil {
		d.notes = append(d.notes, err.Error())
	}
}

func (d *recordingDriver) OpenWindow(ctx context.Context, w harness.Window) (string, error) {
	var id string
	var err error
	if real, ok := d.Driver.(tuios); ok {
		caller := creationRecorder{Client: real.Client, driver: d}
		var ws int
		ws, err = launchWorkspace(ctx, caller, w)
		if err == nil {
			var out daemon.NewWindowResult
			out, err = real.NewWindow(ctx, windowParams(w, ws))
			id = out.WindowID
		}
	} else {
		id, err = d.Driver.OpenWindow(ctx, w)
	}
	if err != nil {
		return id, err
	}
	recipe := work.PaneRecipe{Window: id, Session: w.Session, Name: w.Name, Cwd: w.Cwd, Command: w.Command, Harness: d.harness, Model: d.model, ProjectID: d.owner.ProjectID, TaskID: d.owner.TaskID}
	recipe.Env = recordedEnvironment(recipe)
	if len(w.Command) > 1 {
		recipe.Flags = w.Command[1:]
	}
	// The recipe first: it records the agent that launched the pane
	// (LaunchedBy), which lets that agent associate its child pane.
	d.note(d.work.PutPaneRecipeAs(d.actor, recipe))
	if err := d.work.SetPaneOwnerAs(d.actor, work.Assignment{Window: id, Session: d.sessions[w.Session], ProjectID: d.owner.ProjectID, TaskID: d.owner.TaskID, AssignedAt: work.Now()}); err != nil {
		d.unrecorded = true
		d.note(fmt.Errorf("pane %s is open but owner was not saved; associate it: %w", id, err))
	}
	if err := d.work.TouchDir(w.Cwd); err != nil {
		d.note(fmt.Errorf("directory recency: %w", err))
	}
	return id, nil
}

type creationRecorder struct {
	*daemon.Client
	driver *recordingDriver
}

func (c creationRecorder) Call(ctx context.Context, verb string, params, out any) error {
	if verb != "new-session" {
		return c.Client.Call(ctx, verb, params, out)
	}
	var created struct {
		ID   string `json:"session_id"`
		Name string `json:"session"`
	}
	if err := c.Client.Call(ctx, verb, params, &created); err != nil {
		return err
	}
	if c.driver.sessions == nil {
		c.driver.sessions = map[string]string{}
	}
	c.driver.sessions[created.Name] = created.ID
	if err := c.driver.work.SetSessionOwner(work.SessionOwner{ID: created.ID, Name: created.Name, ProjectID: c.driver.owner.ProjectID}); err != nil {
		c.driver.note(fmt.Errorf("session %s exists but owner was not saved; associate it: %w", created.Name, err))
	}
	return nil
}

// launchJob launches as me (the request's actor, or the owner for a
// schedule's run).
func (a *App) launchJob(me work.Actor, owner Owner, h harness.Harness, spec harness.Spec) func(context.Context) (any, error) {
	spec.Flags = append(spec.Flags, a.hookFlags(h)...)
	d := &recordingDriver{Driver: a.launcher, work: a.work, actor: me, owner: owner, harness: h.Name, model: spec.Model}
	l := &harness.Launcher{Driver: d, Delay: a.promptDelay, Sleep: a.sleep}
	panes := a.panesd
	return func(ctx context.Context) (any, error) {
		started, err := l.Launch(ctx, h, spec)
		if err != nil && started.Window == "" {
			if h.Name == "" {
				return nil, fmt.Errorf("new pane in %s: %w", spec.Session, err)
			}
			return nil, fmt.Errorf("launch %s into %s: %w", h.Name, spec.Session, err)
		}
		d.note(err)
		// The launch's own notes (a setting not applied) are told with the
		// pane; a prompt not sent tells its note as "prompt not sent".
		if started.Note != "" && started.Prompt != harness.PromptNotSent {
			d.notes = append(d.notes, started.Note)
		}
		rows, listErr := panes.ListPanes(ctx)
		if listErr != nil {
			d.note(fmt.Errorf("pane %s is open; listing panes failed: %w", started.Window, listErr))
		}
		found := false
		for _, p := range rows {
			if p.ID == started.Window {
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, pane{Session: started.Session, Pane: daemon.Pane{ID: started.Window, Name: started.Name, Cwd: spec.Cwd}})
		}
		if d.unrecorded {
			for i := range rows {
				if rows[i].ID == started.Window {
					rows[i].OwnerUnsaved = true
				}
			}
		}
		if owner.Notice != "" {
			d.notes = append(d.notes, owner.Notice)
		}
		return launched{Pane: started, Panes: rows, Note: strings.Join(d.notes, "; ")}, nil
	}
}

func (a *App) finishLaunch(done launched, owner Owner, version uint64) {
	if version != a.pinVersion {
		a.r.UI().Notify("Launch finished in " + done.Pane.Session + "; pin changed, view unchanged")
		if done.Note != "" {
			a.r.UI().Notify(done.Note)
		}
		if done.Pane.Prompt == harness.PromptNotSent {
			a.r.UI().Notify("prompt not sent: " + done.Pane.Note)
		}
		return
	} else {
		outside := a.pin != "" && owner.ProjectID != a.pin
		p := pane{Session: done.Pane.Session, Pane: daemon.Pane{ID: done.Pane.Window, Name: done.Pane.Name}}
		for _, row := range done.Panes {
			if row.ID == p.ID {
				p = row
				break
			}
		}
		if outside {
			a.setPanes(done.Panes, nil)
			a.landErr = "outside pin: " + a.ownerLabel(owner.ProjectID)
			if err := a.r.OpenPage(panesPage); err != nil {
				a.r.UI().Notify(err.Error())
				return
			}
			// The explicit launch opens its terminal, not an out-of-scope list row.
			if err := a.attachLive(p, paneListID); err != nil {
				a.r.UI().Notify("show the new pane: " + err.Error())
			}
		} else {
			a.landPane(p, done.Panes)
		}
	}
	if done.Note != "" {
		a.r.UI().Notify(done.Note)
		a.landErr = strings.TrimPrefix(a.landErr+"; "+done.Note, "; ")
	}
	if done.Pane.Prompt == harness.PromptNotSent {
		a.r.UI().Notify("prompt not sent: " + done.Pane.Note)
		a.landErr = strings.TrimPrefix(a.landErr+"; prompt not sent: "+done.Pane.Note, "; ")
	}
}
