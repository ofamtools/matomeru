package main

import (
	"reflect"
	"strings"
	"testing"
)

// ticker is the framework's jobs ticker as the 120-column frame draws it.
func ticker(a *App) []string { return a.m.JobsTicker(120) }

func tickerIs(t *testing.T, a *App, where string, want ...string) {
	t.Helper()
	if got := ticker(a); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Fatalf("%s: ticker = %q, want %q", where, got, want)
	}
}

// The ticker shows application jobs only (a terminal's read is the
// framework's), a running job on top, at most two rows: a newer job pushes
// the oldest finished one off.
func TestTheTickerShowsOnlyApplicationJobsNewestFirst(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{}
	press(t, app, "j", "enter")
	key(t, app, "l")
	cmd := key(t, app, "y")
	tickerIs(t, app, "launching", "agent.start running")
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
	tickerIs(t, app, "attached", "agent.start done")
	if strings.Contains(frame(app), " read ") || strings.Contains(frame(app), "jobs:") {
		t.Fatalf("attached frame:\n%s", frame(app))
	}
	press(t, app, "ctrl+a", "d", "}")
	for range 3 {
		settle(app, key(t, app, "p"))
	}
	tickerIs(t, app, "three previews", "pane.preview done", "pane.preview done")
}

// List reloads (panes.refresh runs at startup and after every launch and
// kill) are bookkeeping, not work the user asked for: Quiet keeps them off
// the ticker, which then takes no rows.
func TestTheTickerSkipsQuietVerbs(t *testing.T) {
	app, _ := panesApp(t)
	if len(jobsOf(app, "panes.refresh")) == 0 {
		t.Fatal("no panes.refresh job to hide")
	}
	for _, verb := range []string{"panes.refresh", "daemon.ensure"} {
		if !app.r.Operations[verb].Quiet {
			t.Fatalf("%s is not marked quiet", verb)
		}
	}
	tickerIs(t, app, "startup")
}
