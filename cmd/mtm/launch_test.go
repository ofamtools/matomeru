package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// fakeWindow is one pane the fake opened: the new-window request, the id
// it got, the readiness probes waited on it and the text typed into it.
type fakeWindow struct {
	harness.Window
	ID     string
	Waits  []harness.Ready
	Prompt string
}

// fakeDaemon is the harness.Driver of the tests. It records every window
// it opens (agents, with an argv, in launches; shells in shells) and the
// send-text calls; err makes every open fail, notReady every wait. With
// panes set, every new pane joins that fake's listing (agents as a1, a2,
// ..., shells as s1, s2, ...); without it an agent gets window w1.
type fakeDaemon struct {
	err      error
	notReady error
	launches []*fakeWindow
	shells   []*fakeWindow
	sends    []string // "session/window: text"
	panes    *fakePanes
}

func (f *fakeDaemon) OpenWindow(_ context.Context, w harness.Window) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	rec := &fakeWindow{Window: w}
	if len(w.Command) == 0 {
		f.shells = append(f.shells, rec)
		rec.ID = "s" + strconv.Itoa(len(f.shells))
	} else {
		f.launches = append(f.launches, rec)
		rec.ID = "w1"
		if f.panes != nil {
			rec.ID = "a" + strconv.Itoa(len(f.launches))
		}
	}
	if f.panes != nil {
		f.panes.panes = append(f.panes.panes, pane{Session: w.Session, Pane: daemon.Pane{ID: rec.ID, Name: w.Name, Cwd: w.Cwd}})
	}
	return rec.ID, nil
}

func (f *fakeDaemon) window(id string) *fakeWindow {
	for _, w := range append(append([]*fakeWindow{}, f.launches...), f.shells...) {
		if w.ID == id {
			return w
		}
	}
	return nil
}

func (f *fakeDaemon) WaitReady(_ context.Context, _, window string, r harness.Ready) error {
	if w := f.window(window); w != nil {
		w.Waits = append(w.Waits, r)
	}
	return f.notReady
}

func (f *fakeDaemon) SendText(_ context.Context, session, window, text string) error {
	f.sends = append(f.sends, session+"/"+window+": "+text)
	if w := f.window(window); w != nil {
		w.Prompt = text
	}
	return nil
}

// settle runs the commands an update returned, as the Bubble Tea loop
// would, feeding every message back into the model.
func settle(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := now(cmd).(type) {
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

func frame(a *App) string { return ansi.Strip(rawFrame(a)) }

// rawFrame is the frame with its styles: the focused list's selected row
// is reverse video, with no marker in the text.
func rawFrame(a *App) string {
	a.m.Width, a.m.Height = 120, 24
	return a.m.View().Content
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

// withTrees gives the seeded projects trees and opens matomeru's tasks:
// ochazuke at /src/ochazuke, matomeru at /src/matomeru plus a nested tree
// inside ochazuke's.
func withTrees(t *testing.T, a *App) {
	t.Helper()
	for _, p := range []work.Project{
		{ID: "ochazuke", Name: "ochazuke", Created: 1, Trees: []work.Tree{{Kind: "dir", Path: "/src/ochazuke"}}},
		{ID: "matomeru", Name: "matomeru", Created: 2, DefaultTree: "/src/matomeru", Trees: []work.Tree{{Kind: "dir", Path: "/src/ochazuke/vendor/matomeru"}}},
	} {
		if err := a.work.PutProject(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "project", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
}

// confirmPrompt is the open confirm modal's prompt and stored request.
func confirmPrompt(t *testing.T, a *App) (string, ochazuke.Request) {
	t.Helper()
	inst := a.r.Instances[a.r.TopModal()]
	if inst == nil || inst.Noun != "confirm" || inst.Payload.Request == nil {
		t.Fatalf("no confirm modal is open (top %q)", a.r.TopModal())
	}
	return inst.Payload.Prompt, *inst.Payload.Request
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
	if !strings.Contains(frame(app), "Launch into matomeru?") {
		t.Fatalf("frame lacks the confirm prompt:\n%s", frame(app))
	}
	cmd := key(t, app, "y")
	jobs := app.r.Jobs()
	if len(jobs) != 1 || jobs[0].Verb != "agent.start" || jobs[0].State != ochazuke.JobRunning || jobs[0].Owner != "work" {
		t.Fatalf("jobs after y = %+v", jobs)
	}
	if !strings.Contains(frame(app), "agent.start running") {
		t.Fatalf("ticker lacks the running job:\n%s", frame(app))
	}
	runOnce(app, cmd) // the launch lands attached; its terminal's read waits for a frame
	if len(f.launches) != 1 {
		t.Fatalf("launches = %+v", f.launches)
	}
	got := f.launches[0]
	if got.Session != "matomeru" || got.Prompt != "write the brief" || strings.Join(got.Command, " ") != "claude --model opus" || len(got.Waits) != 1 || got.Waits[0].State != "idle,done" {
		t.Fatalf("launch = %+v", got)
	}
	if len(app.panes) != 2 || app.panes[0].ID != "w1" || app.r.Instances["pane:matomeru/w1"] == nil {
		t.Fatalf("panes = %+v", app.panes)
	}
	if !strings.Contains(frame(app), "agent.start done") {
		t.Fatalf("ticker lacks the done job:\n%s", frame(app))
	}
	out, err := app.r.Execute(ochazuke.Request{Verb: "list", Noun: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	// The other listed job is the attached terminal's frame read.
	var starts []ochazuke.JobInfo
	for _, j := range out.([]ochazuke.JobInfo) {
		if j.Verb == "agent.start" {
			starts = append(starts, j)
		}
	}
	if len(starts) != 1 || starts[0].State != ochazuke.JobDone {
		t.Fatalf("jobs.list = %+v", out)
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
	if !strings.Contains(frame(app), "agent.start failed: launch claude into ochazuke: dial: no daemon") {
		t.Fatalf("rail lacks the failure:\n%s", frame(app))
	}
	// The app keeps working: the next key runs.
	app.m.Status = ""
	press(t, app, "j")
	if app.r.Focus != "matomeru" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
}

func TestAgentLaunchParameters(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{}
	app.launcher = f
	launch := app.r.Operations["agent.launch"]
	if p := launch.Parameters["project"]; p.Required || p.Source != "projects.list" || launch.Parameters["model"].Source != "harness.models" || len(launch.Parameters["harness"].Enum) != 3 {
		t.Fatalf("agent.launch parameters = %+v", launch.Parameters)
	}
	for _, key := range []string{"project.launch", "task.launch", "agent.launch"} {
		if c := app.r.Operations[key].Confirm; c != "" {
			t.Fatalf("%s confirms itself (%q); agent.start holds the confirm", key, c)
		}
	}
	if got := app.r.Operations["agent.start"].Confirm; got != launchConfirm {
		t.Fatalf("agent.start confirm = %q", got)
	}
	choices, err := app.r.Choices(app.r.Context(), launch.Parameters["project"])
	if err != nil || len(choices) != 2 {
		t.Fatalf("choices = %+v, %v", choices, err)
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "launch", Noun: "agent", Target: "work", Args: ochazuke.Arguments{"project": "matomeru", "model": "bogus"}}); err == nil {
		t.Fatal("a model outside the enum was accepted")
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "launch", Noun: "agent", Target: "work", Args: ochazuke.Arguments{"project": "matomeru", "model": "sonnet"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "accept", Noun: "confirm"}); err != nil {
		t.Fatal(err)
	}
	runOnce(app, app.r.PendingCommands())
	if len(f.launches) != 1 || strings.Join(f.launches[0].Command, " ") != "claude --model sonnet" || f.launches[0].Session != "matomeru" {
		t.Fatalf("launches = %+v", f.launches)
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func noProjectLaunch(t *testing.T, a *App) (string, ochazuke.Request) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Verb: "launch", Noun: "agent", Target: "work"}); err != nil {
		t.Fatal(err)
	}
	if a.m.Palette.IsOpen() {
		t.Fatalf("a launch with nothing inferred opened the palette on %q", a.m.Palette.Text())
	}
	return confirmPrompt(t, a)
}

func TestNothingInferredLaunchesIntoNoProject(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{}
	app.launcher = f
	prompt, q := noProjectLaunch(t, app)
	if _, named := q.Args["project"]; prompt != "Launch into no-project?" || q.Key() != "agent.start" || q.Args["session"] != noProjectSession || named {
		t.Fatalf("confirm = %q, request = %+v", prompt, q)
	}
	if fr := frame(app); !strings.Contains(fr, "Launch into no-project?") {
		t.Fatalf("frame:\n%s", fr)
	}
	runOnce(app, key(t, app, "y"))
	if len(f.launches) != 1 {
		t.Fatalf("launches = %+v", f.launches)
	}
	if got := f.launches[0]; got.Session != "no-project" || got.Cwd != "/home/test" || got.Name != defaultHarness || got.Prompt != "" || len(f.sends) != 0 {
		t.Fatalf("launch = %+v", got)
	}
	if stored, err := app.work.Projects(); err != nil || len(stored) != 2 {
		t.Fatalf("a no-project launch created a project: %+v, %v", stored, err)
	}
}

func TestANoProjectReviewListsAndOpens(t *testing.T) {
	app := open(t)
	f := app.panesd.(*fakePanes)
	app.launcher = &fakeDaemon{}
	noProjectLaunch(t, app)
	runOnce(app, key(t, app, "y"))
	reviews, err := app.work.Reviews()
	if err != nil || len(reviews) != 1 {
		t.Fatalf("reviews = %+v, %v", reviews, err)
	}
	rv := reviews[0]
	if rv.ProjectID != "" || rv.TaskID != "" || rv.Session != "no-project" || rv.Pane != "no-project/claude" {
		t.Fatalf("review = %+v", rv)
	}
	press(t, app, "ctrl+a", "d", "ctrl+j")
	if app.r.Page != reviewsPage || app.r.Focus != rv.ID {
		t.Fatalf("page %s focus %s", app.r.Page, app.r.Focus)
	}
	if fr := frame(app); !strings.Contains(fr, "[pending] (no project) · (no task) · claude · no-project/claude") {
		t.Fatalf("frame:\n%s", fr)
	}
	settle(app, key(t, app, "enter"))
	if len(f.captured) != 1 || f.captured[0] != "w1" {
		t.Fatalf("captured = %v", f.captured)
	}
	if fr := frame(app); !strings.Contains(fr, "output: no-project/claude [pending]") {
		t.Fatalf("frame after open:\n%s", fr)
	}
}

// now runs cmd, dropping one still waiting after 100ms (a timer such as the
// jobs ticker's JobLinger tick) instead of sleeping through it.
func now(cmd tea.Cmd) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// projects state is the catalog's state source: both row-source lists in
// one call, keyed by the nouns a host would otherwise run list on.
func TestProjectsStateAnswersBothLists(t *testing.T) {
	app := open(t)
	out, err := app.r.Execute(ochazuke.Request{Noun: "projects", Verb: "state"})
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, line := range out.(ocli.Lines) {
		count[line.(ocli.Row).Noun]++
	}
	if count["projects"] != len(app.projects) || count["projects"] == 0 || count["harness"] != len(app.harnesses.List()) || len(count) != 2 {
		t.Fatalf("state %+v", out)
	}
	data, err := ocli.Catalog(app.r, "mtm", "test")
	if err != nil || !strings.Contains(string(data), `"state": "projects.state"`) {
		t.Fatalf("catalog state: %v %.200s", err, data)
	}
}
