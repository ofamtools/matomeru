package main

import (
	"testing"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
)

// mtm on another TUIOS server shares the database: its listing names none
// of this server's windows, and the side links made here survive it, so a
// restart here still reattaches them.
func TestAnotherServersListingKeepsSideLinks(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	parent := pane{Session: "matomeru", Pane: daemon.Pane{ID: "w1", Name: "claude"}}
	side := pane{Session: "matomeru", Pane: daemon.Pane{ID: "s1", Name: "side-term"}}
	a := openWith(t, Options{Data: path})
	if err := a.work.PutCompanion(work.Companion{Window: side.ID, Parent: parent.ID, Kind: "term", Server: a.server()}); err != nil {
		t.Fatal(err)
	}
	here := a.server()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	other := openWith(t, Options{Data: path, Socket: "/tmp/mtm-test-other-server.sock"})
	if other.server() == here {
		t.Fatalf("both apps on %s", here)
	}
	of := other.panesd.(*fakePanes)
	of.panes = []pane{{Session: "matomeru", Pane: daemon.Pane{ID: "x9", Name: "side-term"}}, {Session: "nabe"}}
	reload(t, other)
	if links, err := other.work.Companions(); err != nil || len(links) != 1 {
		t.Fatalf("another server's listing dropped the side link: %+v %v", links, err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	back := openWith(t, Options{Data: path})
	f := back.panesd.(*fakePanes)
	f.panes = []pane{parent, side}
	reload(t, back)
	if len(back.panes) != 1 || back.panes[0].SideTerm != side.ID {
		t.Fatalf("restart lost the side: %+v", back.panes)
	}
}
