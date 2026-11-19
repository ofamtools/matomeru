package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// An agent's restart-request.post, run in its own mtm process, reaches the
// TUI open on the same database: the request is stored in mtm.db, the TUI
// reads it (as the agent posted it) and draws the badge; a dismiss from
// the second process clears it in the TUI.
func TestRestartRequestReachesTheRunningTUI(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	tui := openAt(t, path)
	// The test app starts with every segment off; the badge is the one
	// under test.
	if _, err := tui.r.Settings().SetSession(ochazuke.SegmentSetting(ochazuke.RestartSegment), true); err != nil {
		t.Fatal(err)
	}
	cli := openAt(t, path)
	agent := ochazuke.Actor{Kind: "agent", Harness: "claude", Pane: "w9"}
	post := ochazuke.Request{Noun: "ochazuke.restart-request", Verb: "post", Actor: &agent, Args: ochazuke.Arguments{"reason": "installed v9", "version": "mtm v9"}}
	if o := cli.r.ExecuteOutcome(post); o.Kind != ochazuke.OutcomeDone || o.Err != nil {
		t.Fatalf("post: %+v", o)
	}
	req := tui.r.RestartRequested()
	if !req.Pending || req.Reason != "installed v9" || req.Version != "mtm v9" || !req.Actor.IsAgent() || req.Actor.Pane != "w9" {
		t.Fatalf("the TUI's request: %+v", req)
	}
	if cli.cliWords = []string{"restart-request", "post"}; !cli.startsNoDaemon() {
		t.Fatal("restart-request post starts a daemon")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(ansi.Strip(frameView(tui).Content), ochazuke.RestartBadgeText) {
		if time.Now().After(deadline) {
			t.Fatalf("the TUI's frame has no RESTART REQUESTED badge:\n%s", ansi.Strip(frameView(tui).Content))
		}
		settle(tui, tui.m.Init())
	}
	if o := cli.r.ExecuteOutcome(ochazuke.Request{Noun: "ochazuke.restart-request", Verb: "dismiss", Actor: &agent}); o.Err != nil || tui.r.RestartRequested().Pending {
		t.Fatalf("dismiss: %+v pending %v", o, tui.r.RestartRequested().Pending)
	}
}

// The TUI's restart hook: app.restart asks with the running build (the
// --version line) and the installed one; the cleanup before the exec
// closes mtm's side (extension processes, the database) once, however
// often it runs, and never reaches the daemon.
func TestRestartHookAsksThenCleansUpOnce(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	dir, err := os.MkdirTemp("/tmp", "mtm-rs") // a short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	var dialed atomic.Int32
	ln, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			dialed.Add(1)
			conn.Close()
		}
	}()
	a := openAt(t, path)
	a.daemon.Socket = ln.Addr().String()
	bin := filepath.Join(dir, "mtm")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'mtm v9 (commit abc, ochazuke v0.200.0)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.executable = func() (string, error) { return bin, nil }
	a.wireRestart()

	if _, err := a.r.Execute(ochazuke.Request{Noun: "app", Verb: "restart"}); err != nil {
		t.Fatal(err)
	}
	if a.r.TopModal() != "confirm" {
		t.Fatalf("app.restart did not ask: modal %q", a.r.TopModal())
	}
	running := versionLine(buildStamp())
	deadline := time.Now().Add(5 * time.Second)
	for {
		settle(a, a.r.PendingCommands())
		frame := ansi.Strip(frameView(a).Content)
		if strings.Contains(frame, "Restart mtm in this terminal?") && strings.Contains(frame, "Running:   "+running[:20]) && strings.Contains(frame, "Installed: mtm v9") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the restart question lacks a version:\n%s", frame)
		}
		time.Sleep(10 * time.Millisecond)
	}
	press(t, a, "esc")
	if a.r.Quit {
		t.Fatal("esc restarted")
	}

	before := dialed.Load()
	for range 2 { // main's deferred Close after a failed exec runs it again
		if err := a.r.CloseQuitHooks(); err != nil {
			t.Fatal(err)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.procs.life.Err(); err == nil {
		t.Fatal("extension processes not stopped")
	}
	if _, err := a.db.All(restartTable.Name); err == nil {
		t.Fatal("database still open")
	}
	if n := dialed.Load() - before; n != 0 {
		t.Fatalf("the cleanup reached the daemon %d times", n)
	}
}

// An agent's pane reads by its name where a person sees who asked (the
// restart dialog): the pane noun declares actorPane, so ochazuke reads
// the pane's label from its store.
func TestActorLabelNamesTheListedPane(t *testing.T) {
	t.Parallel()
	a := open(t)
	p := pane{Session: "hub", UniqueName: "orchestrator"}
	p.ID = "6ef88c2f-b85f-4b63-998a-d81c812c30ef"
	a.panes = []pane{p}
	a.pv = &paneUI{listed: true}
	agent := ochazuke.Actor{Kind: ochazuke.ActorAgent, Name: "claude", Pane: p.ID}
	if got := a.r.ActorLabel(agent); !strings.Contains(got, "orchestrator") || strings.Contains(got, p.ID) {
		t.Fatalf("label %q", got)
	}
}
