package main

import (
	"context"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func openTermSide(t *testing.T, a *App, p pane) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "side", Target: p.rowID(), Args: ochazuke.Arguments{"kind": "term"}}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
}

// A link left on another server (the tuios daemon mtm used before its own
// daemon) is not this server's side: it must not refuse a new side link
// after the window was created (the owner's orphan "side-term").
func TestSideIgnoresStaleLinkFromAnotherServer(t *testing.T) {
	t.Parallel()
	a, driver := shellApp(t)
	a.cli = true
	a.daemon = Options{Socket: filepath.Join(t.TempDir(), "mtmd.sock")}.client()
	p := driver.panes.panes[0]
	if err := a.work.PutCompanion(work.Companion{Window: "old-side", Parent: p.ID, Kind: "term", Server: "/old/tuios.sock"}); err != nil {
		t.Fatal(err)
	}
	openTermSide(t, a, p)
	for _, job := range jobsOf(a, "pane.side") {
		if job.Error != "" {
			t.Fatal(job.Error)
		}
	}
	links, _ := a.work.Companions()
	mine := 0
	for _, l := range links {
		if l.Server == a.server() && l.Parent == p.ID && l.Kind == "term" {
			mine++
		}
	}
	if mine != 1 || len(driver.launches) != 1 {
		t.Fatalf("links %+v, launches %d", links, len(driver.launches))
	}
}

// When the link cannot be saved the new window is closed, never left as an
// unattached pane.
func TestSideLinkFailureClosesTheNewWindow(t *testing.T) {
	t.Parallel()
	a, driver := shellApp(t)
	p := driver.panes.panes[0]
	// Same-server link to another window: a genuine conflict.
	if err := a.work.PutCompanion(work.Companion{Window: "existing", Parent: p.ID, Kind: "term", Server: "s"}); err != nil {
		t.Fatal(err)
	}
	before := len(driver.panes.panes)
	side := &sideDriver{Driver: driver, work: a.work, server: "s", parent: p, cwd: "/tmp", kind: "term", kill: func(ctx context.Context, id string) error {
		return driver.panes.KillPane(ctx, pane{Pane: daemon.Pane{ID: id}})
	}}
	if _, err := side.OpenWindow(context.Background(), harness.Window{Name: "side-term"}); err == nil {
		t.Fatal("conflicting link accepted")
	}
	if len(driver.panes.panes) != before {
		t.Fatalf("orphan pane left: %d panes, was %d", len(driver.panes.panes), before)
	}
	if len(driver.launches) != 0 {
		t.Fatalf("window was created before the link was validated: %d", len(driver.launches))
	}
}
