package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestTheRailShowsOnlyApplicationJobsNewestFirst(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{}
	press(t, app, "j", "enter")
	key(t, app, "l")
	cmd := key(t, app, "y")
	if r := app.rail(); r != "jobs: #1 agent.start running" {
		t.Fatalf("rail while launching = %q", r)
	}
	runOnce(app, cmd) // lands attached: the terminal's read job is running
	if app.r.Terminal(paneTermID) == nil {
		t.Fatal("the launch did not attach")
	}
	var reads int
	for _, j := range app.r.Jobs() {
		if j.Verb == "read" {
			reads++
		}
	}
	if reads == 0 {
		t.Fatal("no read job to hide")
	}
	if r := app.rail(); r != "jobs: #1 agent.start done" || strings.Contains(frame(app), " read ") {
		t.Fatalf("rail while attached = %q\n%s", r, frame(app))
	}
	// Newest first, at most three.
	press(t, app, "ctrl+a", "d", "}")
	for range 3 {
		settle(app, key(t, app, "p"))
	}
	r := app.rail()
	if strings.Count(r, "#") != 3 || !strings.HasPrefix(r, "jobs: #") || strings.Contains(r, "agent.start") {
		t.Fatalf("rail = %q", r)
	}
	jobs := app.r.Jobs()
	if want := "#" + strconv.FormatUint(jobs[len(jobs)-1].Job, 10) + " pane.preview done"; !strings.HasPrefix(r, "jobs: "+want) {
		t.Fatalf("rail = %q, want it to start with %q", r, want)
	}
}
