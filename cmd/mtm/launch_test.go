package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// fakeDaemon records launches; err makes every launch fail.
type fakeDaemon struct {
	err      error
	launches []daemon.StartAgentParams
}

func (f *fakeDaemon) Launch(_ context.Context, p daemon.StartAgentParams) (daemon.StartAgentResult, error) {
	if f.err != nil {
		return daemon.StartAgentResult{}, f.err
	}
	f.launches = append(f.launches, p)
	return daemon.StartAgentResult{Session: p.Session, WindowID: "w1", Name: p.Name}, nil
}

func (f *fakeDaemon) Panes(_ context.Context, session string) ([]daemon.Pane, error) {
	return []daemon.Pane{{ID: "w1", Name: session}}, nil
}

// settle runs the commands an update returned, as the Bubble Tea loop
// would, feeding every message back into the model.
func settle(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			settle(a, c)
		}
	case nil:
	default:
		_, next := a.m.Update(msg)
		settle(a, next)
	}
}

// key sends one key and returns the commands the update scheduled.
func key(t *testing.T, a *App, k string) tea.Cmd {
	t.Helper()
	_, cmd := a.m.Update(keyMsg(k))
	if a.m.Status != "" {
		t.Fatalf("key %q: %s", k, a.m.Status)
	}
	return cmd
}

func frame(a *App) string {
	a.m.Width, a.m.Height = 120, 24
	return ansi.Strip(a.m.View().Content)
}

func TestSessionForSlugsTheProjectName(t *testing.T) {
	for _, c := range []struct {
		p    work.Project
		want string
	}{
		{work.Project{ID: "p1", Name: "first project"}, "first-project"},
		{work.Project{ID: "p2", Name: "Tuios Deck!"}, "tuios-deck"},
		{work.Project{ID: "p3", Name: "  --  "}, "p3"},
		{work.Project{}, "mtm"},
	} {
		if got := sessionFor(c.p); got != c.want {
			t.Errorf("sessionFor(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestLaunchConfirmNamesTheSession(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{}
	if got := app.r.Operations["project.launch"].Confirm; got != launchConfirm {
		t.Fatalf("confirm = %q", got)
	}
	press(t, app, "j", "k")
	key(t, app, "l")
	if !strings.Contains(frame(app), "Launch into session ochazuke?") {
		t.Fatalf("frame lacks the confirm prompt:\n%s", frame(app))
	}
}

func TestDecliningALaunchLeavesNoJob(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{}
	app.launcher = f
	press(t, app, "j")
	key(t, app, "l")
	settle(app, key(t, app, "n"))
	if jobs := app.r.Jobs(); len(jobs) != 0 || len(f.launches) != 0 {
		t.Fatalf("jobs = %+v, launches = %+v", jobs, f.launches)
	}
	if app.r.Focus != "matomeru" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
}

func TestAcceptingATaskLaunchRunsAJob(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{}
	app.launcher = f
	press(t, app, "j", "enter")
	key(t, app, "l")
	if !strings.Contains(frame(app), "Launch into session matomeru?") {
		t.Fatalf("frame lacks the confirm prompt:\n%s", frame(app))
	}
	cmd := key(t, app, "y")
	jobs := app.r.Jobs()
	if len(jobs) != 1 || jobs[0].Verb != "agent.launch" || jobs[0].State != ochazuke.JobRunning || jobs[0].Owner != "work" {
		t.Fatalf("jobs after y = %+v", jobs)
	}
	if !strings.Contains(frame(app), "jobs: #1 agent.launch running") {
		t.Fatalf("rail lacks the running job:\n%s", frame(app))
	}
	settle(app, cmd)
	if len(f.launches) != 1 {
		t.Fatalf("launches = %+v", f.launches)
	}
	got := f.launches[0]
	if got.Session != "matomeru" || got.Agent != agentHarness || got.Prompt != "write the brief" || strings.Join(got.Args, " ") != "--model opus" {
		t.Fatalf("launch = %+v", got)
	}
	if len(app.panes) != 1 || app.panes[0].ID != "w1" {
		t.Fatalf("panes = %+v", app.panes)
	}
	if !strings.Contains(frame(app), "jobs: #1 agent.launch done") {
		t.Fatalf("rail lacks the done job:\n%s", frame(app))
	}
	out, err := app.r.Execute(ochazuke.Request{Verb: "list", Noun: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	if listed := out.([]ochazuke.JobInfo); len(listed) != 1 || listed[0].State != ochazuke.JobDone {
		t.Fatalf("jobs.list = %+v", listed)
	}
}

func TestAFailingLaunchShowsInTheRail(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{err: errors.New("dial: no daemon")}
	press(t, app, "j", "k")
	key(t, app, "l")
	settle(app, key(t, app, "y"))
	jobs := app.r.Jobs()
	if len(jobs) != 1 || jobs[0].State != ochazuke.JobFailed || !strings.Contains(jobs[0].Error, "no daemon") {
		t.Fatalf("jobs = %+v", jobs)
	}
	if !strings.Contains(frame(app), "agent.launch failed: launch into ochazuke: dial: no daemon") {
		t.Fatalf("rail lacks the failure:\n%s", frame(app))
	}
	// The app keeps working: the next key runs.
	app.m.Status = ""
	press(t, app, "j")
	if app.r.Focus != "matomeru" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
}

func TestAgentLaunchTakesModelAndProject(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{}
	app.launcher = f
	op := app.r.Operations["agent.launch"]
	if op.Parameters["project"].Source != "projects.list" || len(op.Parameters["model"].Enum) == 0 {
		t.Fatalf("parameters = %+v", op.Parameters)
	}
	choices, err := app.r.Choices(app.r.Context(), op.Parameters["project"])
	if err != nil || len(choices) != 2 {
		t.Fatalf("choices = %+v, %v", choices, err)
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "launch", Noun: "agent", Target: "work", Args: ochazuke.Arguments{"project": "matomeru", "model": "bogus"}}); err == nil {
		t.Fatal("a model outside the enum was accepted")
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "launch", Noun: "agent", Target: "work", Args: ochazuke.Arguments{"project": "matomeru", "model": "sonnet"}}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if len(f.launches) != 1 || strings.Join(f.launches[0].Args, " ") != "--model sonnet" || f.launches[0].Session != "matomeru" {
		t.Fatalf("launches = %+v", f.launches)
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}
