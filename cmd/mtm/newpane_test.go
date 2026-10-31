package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// shellApp has a pinned project and two explicitly owned panes.
func shellApp(t *testing.T) (*App, *fakeDaemon) {
	t.Helper()
	app := open(t)
	withTrees(t, app)
	app.pin = "matomeru"
	for _, p := range app.panesd.(*fakePanes).panes {
		if err := app.work.SetPaneOwner(work.Assignment{Window: p.ID, Session: p.Session, ProjectID: "matomeru"}); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	press(t, app, "ctrl+k") // Panes is the tab before Work
	if app.r.Page != panesPage || app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("page %s focus %s", app.r.Page, app.r.Focus)
	}
	return app, f
}

// n opens a shell at once, in the inferred session: no confirm (owner,
// 2026-10-25; x kills a shell).
func TestNewPaneNeverConfirms(t *testing.T) {
	t.Parallel()
	app, _ := shellApp(t)
	key(t, app, "n")
	if app.r.TopModal() != "" {
		t.Fatalf("n opened %q", app.r.TopModal())
	}
	jobs := jobsOf(app, "pane.start")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v", app.r.Jobs())
	}
}

func TestNewPaneOpensAShellAndReloadsTheList(t *testing.T) {
	t.Parallel()
	app, f := shellApp(t)
	cmd := key(t, app, "n")
	jobs := jobsOf(app, "pane.start")
	if len(jobs) != 1 || jobs[0].State != ochazuke.JobRunning || jobs[0].Owner != panesPage {
		t.Fatalf("jobs after n = %+v", jobs)
	}
	if fr := frame(app); !strings.Contains(fr, "pane.start running") {
		t.Fatalf("rail lacks the running job:\n%s", fr)
	}
	runOnce(app, cmd)
	if len(f.shells) != 1 {
		t.Fatalf("shells = %+v", f.shells)
	}
	if got := f.shells[0]; got.Session != "matomeru" || got.Cwd != "/home/test" || got.Name != shellName || len(got.Command) != 0 || got.Prompt != "" || len(got.Waits) != 0 || len(f.sends) != 0 {
		t.Fatalf("shell request = %+v, sends %v", got, f.sends)
	}
	// The wire request says focus false (the daemon defaults to true), so a
	// new shell never steals the user's focus in TUIOS.
	if wire, _ := json.Marshal(windowParams(f.shells[0].Window, 1)); !strings.Contains(string(wire), `"focus":false`) || strings.Contains(string(wire), `"command"`) {
		t.Fatalf("new-window params = %s", wire)
	}
	if len(f.launches) != 0 {
		t.Fatalf("pane.new started an agent: %+v", f.launches)
	}
	if len(app.panes) != 3 || app.r.Instances["pane:matomeru/s1"] == nil {
		t.Fatalf("panes = %+v", app.panes)
	}
	if jobs := jobsOf(app, "pane.start"); len(jobs) != 1 || jobs[0].State != ochazuke.JobDone {
		t.Fatalf("jobs after completion = %+v", jobs)
	}
	if fr := frame(app); !strings.Contains(fr, "panes (3)") || !strings.Contains(fr, "matomeru/shell") {
		t.Fatalf("frame after the new pane:\n%s", fr)
	}
}

func TestNewPaneAnswersOnAnEmptyList(t *testing.T) {
	t.Parallel()
	app, f := shellApp(t)
	f.panes.panes = nil
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if app.r.Focus != paneListID {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	runOnce(app, key(t, app, "n"))
	if len(f.shells) != 1 || f.shells[0].Session != "matomeru" {
		t.Fatalf("shells = %+v", f.shells)
	}
}

func TestAFailingNewPaneShowsInTheRail(t *testing.T) {
	t.Parallel()
	app, f := shellApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	f.err = errors.New("dial: no daemon")
	settle(app, key(t, app, "n"))
	jobs := jobsOf(app, "pane.start")
	if len(jobs) != 1 || jobs[0].State != ochazuke.JobFailed || !strings.Contains(jobs[0].Error, "new pane in matomeru: dial: no daemon") {
		t.Fatalf("jobs = %+v", jobs)
	}
	if len(app.panes) != 2 || app.r.Page != panesPage {
		t.Fatalf("panes = %+v, page %s", app.panes, app.r.Page)
	}
	app.m.Status = ""
	if fr := frame(app); !strings.Contains(fr, "pane.start failed: new pane in matomeru: dial: no daemon") {
		t.Fatalf("rail lacks the failure:\n%s", fr)
	}
	// The app keeps working: j walks the tree, the next session's row first.
	press(t, app, "j", "j")
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
}

func TestNewPaneWithNothingInferredGoesToMtmOnly(t *testing.T) {
	t.Parallel()
	app := open(t)
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	press(t, app, "ctrl+k")
	cmd := key(t, app, "n")
	if app.m.Palette.IsOpen() || app.r.TopModal() != "" {
		t.Fatalf("pane.new with nothing inferred opened %q", app.r.TopModal())
	}
	runOnce(app, cmd)
	if len(f.shells) != 1 || f.shells[0].Session != "mtm-only" || f.shells[0].Cwd != "/home/test" {
		t.Fatalf("shells = %+v", f.shells)
	}
	if app.r.Instances["pane:mtm-only/s1"] == nil {
		t.Fatalf("the list lacks the new pane: %+v", app.panes)
	}
}

func TestCLIPaneNew(t *testing.T) {
	t.Parallel()
	app, f := cliApp(t)
	f.panes = app.panesd.(*fakePanes)
	app.pin = "ochazuke"
	out, errOut, err := cli(app, "", "pane", "new")
	if err != nil {
		t.Fatalf("pane new: %v\n%s", err, errOut)
	}
	if len(f.shells) != 1 || f.shells[0].Session != "ochazuke" {
		t.Fatalf("shells = %+v", f.shells)
	}
	if !strings.Contains(errOut, "pane.start done") {
		t.Fatalf("stderr lacks the finished job: %q", errOut)
	}
	var landed struct {
		Pane struct {
			Window string `json:"window"`
		} `json:"Pane"`
	}
	if err := json.Unmarshal([]byte(out), &landed); err != nil || landed.Pane.Window != "s1" {
		t.Fatalf("stdout = %q (%v)", out, err)
	}
	if app.r.Instances["pane:ochazuke/s1"] == nil {
		t.Fatalf("the list lacks the new pane: %+v", app.panes)
	}
}
