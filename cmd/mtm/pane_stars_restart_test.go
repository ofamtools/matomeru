package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// Stars survive a restart and every listing that lacks their windows. The
// owner's stars were lost when an mtm on another TUIOS server (plain mtm
// runs on mtm-daemon, mtm --server default on the owner's daemon) opened the
// same database: its startup listing, complete and successful for its own
// server, named none of the starred windows, and the listing pruned them.
func TestPaneStarsSurviveARestartOnAnotherServer(t *testing.T) {
	path := seeded(t)
	app := openWith(t, Options{Data: path})
	reload(t, app)
	executeA(t, app, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	executeA(t, app, "scope", "all", "", nil)
	for _, row := range []string{"pane:matomeru/w1", "pane:ochazuke/w2"} { // inside and outside the pin
		if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "star", Target: row, Args: ochazuke.Arguments{"value": true}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(starred(t, app)) != 2 {
		t.Fatalf("stars before the restart: %+v", starred(t, app))
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	// mtm on another server, same database: its startup listing has only
	// that server's window, plus an empty session.
	other := openWith(t, Options{Data: path})
	of := other.panesd.(*fakePanes)
	of.panes = []pane{
		{Session: "matomeru", Pane: daemon.Pane{ID: "x9", Name: "side-term"}},
		{Session: "nabe"},
	}
	if err := other.ensureDaemon(); err != nil { // the TUI's startup path
		t.Fatal(err)
	}
	settle(other, other.r.PendingCommands())
	executeA(t, other, "scope", "all", "", nil) // S re-lists through setPanes
	if got := starred(t, other); len(got) != 2 {
		t.Fatalf("another server's listing dropped stars: %+v", got)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	// Back on the owner's server: both rows read ★.
	again := openWith(t, Options{Data: path})
	if err := again.ensureDaemon(); err != nil {
		t.Fatal(err)
	}
	settle(again, again.r.PendingCommands())
	executeA(t, again, "scope", "all", "", nil)
	for _, row := range []string{"pane:matomeru/w1", "pane:ochazuke/w2"} {
		if !strings.Contains(cells(again, row), "★") {
			t.Fatalf("%s lost its star after the restart: %q (stars %+v)", row, cells(again, row), starred(t, again))
		}
	}
}
