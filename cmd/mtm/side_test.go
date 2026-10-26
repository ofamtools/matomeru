package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func TestSideTerminalUsesCurrentContextAndReusesWindow(t *testing.T) {
	a, driver := shellApp(t)
	a.cli = true
	p := driver.panes.panes[0]
	driver.panes.panes[0].Cwd = t.TempDir()
	if err := a.work.PutPaneRecipe(work.PaneRecipe{Window: p.ID, Cwd: "/old", Env: map[string]string{"SIDE_TEST": "a b"}}); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "side", Target: p.rowID(), Args: ochazuke.Arguments{"kind": "term"}}); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
		for _, job := range jobsOf(a, "pane.side") {
			if job.Error != "" {
				t.Fatal(job.Error)
			}
		}
	}
	run()
	if len(driver.launches) != 1 {
		t.Fatalf("created %d shells", len(driver.launches))
	}
	got := driver.launches[0].Window
	if got.Cwd != driver.panes.panes[0].Cwd || got.Session != p.Session || len(got.Command) < 3 || !reflect.DeepEqual(got.Command[:2], []string{"env", "SIDE_TEST=a b"}) {
		t.Fatalf("side launch: %+v", got)
	}
	links, err := a.work.Companions()
	if err != nil || len(links) != 1 {
		t.Fatalf("links: %+v %v", links, err)
	}
	owner, err := a.work.PaneOwner(links[0].Window, "")
	if err != nil || owner.ProjectID != "matomeru" {
		t.Fatalf("owner: %+v %v", owner, err)
	}
	for _, row := range a.panes {
		if row.ID == links[0].Window {
			t.Fatal("side window appears in parent list")
		}
	}
	run()
	if len(driver.launches) != 1 {
		t.Fatal("reopening created another process")
	}
}

func TestSidesBlockRestartedQuitAndKillThenOrphan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	a := openAt(t, path)
	p := pane{Session: "work", Pane: daemon.Pane{ID: "parent", Name: "agent"}}
	side := pane{Session: "work", Pane: daemon.Pane{ID: "side", Name: "side-term"}}
	if err := a.work.PutCompanion(work.Companion{Window: side.ID, Parent: p.ID, Kind: "term"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SetPaneOwner(work.Assignment{Window: side.ID, ProjectID: "owner"}); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	f := b.panesd.(*fakePanes)
	f.panes = []pane{p, side}
	b.setPanes(f.panes, nil)
	if len(b.panes) != 1 || b.panes[0].SideTerm != side.ID || b.panes[0].ShowTerm {
		t.Fatalf("restart state: %+v", b.panes)
	}
	for range 2 {
		if _, err := b.r.Execute(ochazuke.Request{Noun: "app", Verb: "quit"}); err != nil {
			t.Fatal(err)
		}
		settle(b, b.r.PendingCommands())
		if b.r.Quit {
			t.Fatal("quit bypassed live companion")
		}
	}
	if _, err := b.r.Execute(ochazuke.Request{Noun: "pane", Verb: "kill", Target: p.rowID(), Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	settle(b, b.r.PendingCommands())
	if len(f.killed) != 0 {
		t.Fatalf("killed parent: %v", f.killed)
	}
	if err := b.checkCompanions(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "ctrl+a t") {
		t.Fatalf("missing jump hint: %v", err)
	}
	f.panes = []pane{side}
	b.setPanes(f.panes, nil)
	if len(b.panes) != 1 || b.panes[0].ID != side.ID {
		t.Fatalf("orphan not visible: %+v", b.panes)
	}
	owner, err := b.work.PaneOwner(side.ID, "")
	if err != nil || owner.ProjectID != "owner" {
		t.Fatalf("lost orphan owner: %+v %v", owner, err)
	}
	if _, err := b.r.Execute(ochazuke.Request{Noun: "app", Verb: "quit"}); err != nil {
		t.Fatal(err)
	}
	runOnce(b, b.r.PendingCommands())
	if !b.r.Quit {
		t.Fatal("orphan still blocked quit")
	}
}

func TestRecordedEnvironmentFromConfirmedCommand(t *testing.T) {
	got := recordedEnvironment(work.PaneRecipe{Env: map[string]string{"BASE": "yes"}, Command: []string{"env", "KEY=a b", "SHELL=/bin/sh", "agent", "not=env"}})
	want := map[string]string{"BASE": "yes", "KEY": "a b", "SHELL": "/bin/sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env: %v", got)
	}
}
