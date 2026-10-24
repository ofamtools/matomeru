package main

import (
	"strings"
	"testing"
)

func TestTheRailShowsOnlyApplicationJobsNewestFirst(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{}
	press(t, app, "j", "enter")
	key(t, app, "l")
	cmd := key(t, app, "y")
	if r := app.rail(); r != "jobs: agent.start running" {
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
	if r := app.rail(); r != "jobs: agent.start done" || strings.Contains(frame(app), " read ") {
		t.Fatalf("rail while attached = %q\n%s", r, frame(app))
	}
	// Newest first, at most three.
	press(t, app, "ctrl+a", "d", "}")
	for range 3 {
		settle(app, key(t, app, "p"))
	}
	r := app.rail()
	if want := "jobs: pane.preview done · "; !strings.HasPrefix(r, want) || strings.Count(r, "pane.preview") != 3 || strings.Contains(r, "agent.start") || strings.Contains(r, "#") {
		t.Fatalf("rail = %q, want three pane.preview jobs, newest (done) first, no job numbers", r)
	}
}

// List reloads (panes.refresh runs at startup and after every launch and
// kill) are bookkeeping, not work the user asked for: the rail skips them.
func TestTheRailSkipsQuietVerbs(t *testing.T) {
	app, _ := panesApp(t)
	if len(jobsOf(app, "panes.refresh")) == 0 {
		t.Fatal("no panes.refresh job to hide")
	}
	if !app.quiet["panes.refresh"] {
		t.Fatal("panes.refresh is not marked quiet")
	}
	if r := app.rail(); r != "jobs: none" {
		t.Fatalf("rail = %q", r)
	}
}
