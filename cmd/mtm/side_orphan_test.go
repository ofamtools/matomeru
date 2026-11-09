package main

import (
	"context"
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

// A side opened before the daemon socket path changed (tuios-shaped path)
// is the same server's side under the mtm path: it is reused, not doubled.
func TestSideLinkSurvivesTheSocketPathRename(t *testing.T) {
	t.Parallel()
	a, driver := shellApp(t)
	a.cli = true
	a.daemon = Options{Socket: "/tmp/mtm-501/servers/mtm-daemon/mtm.sock"}.client()
	p := driver.panes.panes[0]
	driver.panes.panes = append(driver.panes.panes, pane{Pane: daemon.Pane{ID: "old-side", Workspace: p.Workspace}, Session: p.Session})
	// Written by the pre-rename code, then migrated (work.Open) to an id.
	if err := a.work.PutCompanion(work.Companion{Window: "old-side", Parent: p.ID, Kind: "term", Server: daemon.ServerID("/tmp/tuios-501/servers/mtm-daemon/tuios.sock")}); err != nil {
		t.Fatal(err)
	}
	openTermSide(t, a, p)
	if len(driver.launches) != 0 {
		t.Fatalf("a second side term was opened beside the existing one: %d launches", len(driver.launches))
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
