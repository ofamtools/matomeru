package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/schedules"
	"ofam.dev/matomeru/internal/work"
)

// Prompt schedules: the prompt kind of internal/schedules. A run starts an
// agent the way agent.start does (the same launch job: the pane's owner
// and recipe recorded, the harness profile's argv, readiness, the prompt
// typed), so it is an ordinary agent pane: it shows in the Working list
// while it works and in the inbox when it is done or waiting. With target
// pane a run sends the prompt to the pane of that name through tuios's
// delivery queue, which types it when the agent rests and never over a
// question it is asking; with no such pane open it starts one.

// promptEnv is what a prompt run reads that lives on the UI loop: the
// harness registry and the default harness. The loop refreshes it before
// every check and run-now (scheduleEnv); runs read it off the loop.
type promptEnv struct {
	harnesses      *harness.Registry
	defaultHarness string
}

// scheduleEnv snapshots the loop's state for the runs it starts.
func (a *App) scheduleEnv() {
	a.sched.env.Store(&promptEnv{harnesses: a.harnesses, defaultHarness: a.launchHarness()})
}

// scheduleSettingsOf are a prompt row's launch settings by harness setting name.
func scheduleSettingsOf(ag schedules.Agent) map[string]string {
	out := map[string]string{}
	for name, v := range map[string]string{harness.SettingEffort: ag.Effort, harness.SettingFast: ag.Fast, harness.SettingPermission: ag.Permission} {
		if v != "" {
			out[name] = v
		}
	}
	return out
}

// runPromptSchedule is the prompt kind's executor (off the loop).
func (a *App) runPromptSchedule(ctx context.Context, def schedules.Def, rec *schedules.Record) error {
	env := a.sched.env.Load()
	if env == nil {
		return errors.New("mtm has not read its harnesses yet")
	}
	ag := def.Agent
	out := &schedules.Launched{Target: schedules.TargetNew, Project: ag.Project, Task: ag.Task}
	rec.Launched = out
	var notes []string
	// Record first: a project or task that is gone runs the prompt
	// unassociated, and says so.
	var p work.Project
	if ag.Project != "" {
		got, err := a.work.Project(ag.Project)
		if err != nil {
			notes = append(notes, fmt.Sprintf("project %s: %v; ran in no project", ag.Project, err))
			out.Project = ""
		} else {
			p = got
		}
	}
	prompt := def.Prompt
	if ag.Task != "" {
		t, err := a.work.Task(ag.Task)
		switch {
		case err != nil:
			notes = append(notes, fmt.Sprintf("task %s: %v; not linked", ag.Task, err))
			out.Task = ""
		case prompt == "":
			prompt = t.Title
		}
	}
	h, err := env.harnesses.Lookup(cmp.Or(ag.Harness, p.Settings.Harness, env.defaultHarness))
	if err != nil {
		return err
	}
	model, err := h.Model(ag.Model)
	if err != nil {
		return err
	}
	settings := scheduleSettingsOf(ag)
	out.Harness, out.Model, out.Settings = h.Name, model, settings
	session := noProjectSession
	if p.ID != "" {
		session = sessionFor(p)
	}
	name := cmp.Or(ag.Pane, def.Name)
	defer func() { out.Note = strings.Join(append(splitNote(out.Note), notes...), "; ") }()

	if ag.Target == schedules.TargetPane {
		sent, err := a.sendToNamedPane(ctx, h, name, session, prompt, settings, out)
		if err != nil || sent {
			return err
		}
		notes = append(notes, "no open pane named "+name+"; started one")
	}

	cwd, note := a.scheduleCwd(ag.Dir, p)
	if note != "" {
		notes = append(notes, note)
	}
	spec := harness.Spec{Session: session, Name: name, Cwd: cwd, Model: model, Prompt: prompt, Settings: settings}
	owner := Owner{ProjectID: out.Project, TaskID: out.Task}
	v, err := a.launchJob(owner, h, spec)(ctx)
	if err != nil {
		return err
	}
	done, _ := v.(launched)
	r := done.Pane
	out.Session, out.Window, out.Name, out.Argv, out.Typed, out.Cwd = r.Session, r.Window, r.Name, r.Argv, r.Typed, cwd
	out.Pane, out.Prompt = "pane:"+r.Session+"/"+r.Window, r.Prompt
	notes = append(notes, splitNote(r.Note)...)
	notes = append(notes, splitNote(done.Note)...)
	rec.Window = r.Window
	if r.Prompt == harness.PromptNotSent {
		return fmt.Errorf("pane %s opened but the prompt was not sent: %s", out.Pane, r.Note)
	}
	return nil
}

func splitNote(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// sendToNamedPane queues the setting commands and the prompt in the open
// pane named name (in session when several are), answering whether there
// was one. tuios types each when the agent rests, one per rest, and never
// over a question.
func (a *App) sendToNamedPane(ctx context.Context, h harness.Harness, name, session, prompt string, settings map[string]string, out *schedules.Launched) (bool, error) {
	panes, err := a.panesd.ListPanes(ctx)
	if err != nil {
		return false, fmt.Errorf("listing panes to find %s: %w", name, err)
	}
	var found *pane
	for i := range panes {
		p := &panes[i]
		if p.Exited || p.ID == "" || (p.Name != name && p.ID != name && p.rowID() != name) {
			continue
		}
		if found == nil || p.Session == session && found.Session != session {
			found = p
		}
	}
	if found == nil {
		return false, nil
	}
	d, ok := a.panesd.(promptDaemon)
	if !ok {
		return true, errors.New("sending to a pane needs the TUIOS daemon")
	}
	flags, typed, notes, err := h.LaunchSettings(settings)
	if err != nil {
		return true, err
	}
	if len(flags) > 0 {
		notes = append(notes, "launch flags "+strings.Join(flags, " ")+" not applied to an open pane")
	}
	out.Target, out.Session, out.Window, out.Name, out.Pane = "queued", found.Session, found.ID, found.Name, found.rowID()
	out.Note = strings.Join(notes, "; ")
	for _, text := range append(typed, prompt) {
		if text == "" {
			continue
		}
		res, err := d.PromptQueue(ctx, *found, text)
		if err != nil {
			return true, fmt.Errorf("queueing in %s: %w", found.label(), err)
		}
		if text == prompt {
			out.QueueID, out.Prompt = res.ID, "queued"
		} else {
			out.Typed = append(out.Typed, text)
		}
	}
	return true, nil
}

// scheduleCwd is where a new pane works: the row's folder (~ expanded),
// else the project's directory, else home.
func (a *App) scheduleCwd(dir string, p work.Project) (string, string) {
	home, _ := a.home()
	if dir != "" {
		if rest, ok := strings.CutPrefix(dir, "~"); ok && home != "" {
			dir = filepath.Join(home, rest)
		}
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir, ""
		}
		return home, "folder " + dir + " is not there; ran in home"
	}
	if d := projectCwd(p); d != "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d, ""
		}
		return home, "default directory unavailable: " + d + "; ran in home"
	}
	return home, ""
}
