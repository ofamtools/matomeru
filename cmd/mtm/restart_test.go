package main

import (
	"encoding/json"
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
// TUI open on the same database: the TUI holds the request (as the agent)
// and draws the badge; show and dismiss from the second process answer the
// TUI's request. With no TUI running the verb answers nothing, saying why.
func TestRestartRequestRelaysToTheRunningTUI(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	cli := openAt(t, path)
	cli.relay.wait, cli.relay.poll = 5*time.Second, 5*time.Millisecond
	agent := ochazuke.Actor{Kind: "agent", Harness: "claude", Pane: "w9"}
	post := ochazuke.Request{Noun: "ochazuke.restart-request", Verb: "post", Actor: &agent, Args: ochazuke.Arguments{"reason": "installed v9", "version": "mtm v9"}}

	if o := cli.r.ExecuteOutcome(post); o.Kind != ochazuke.OutcomeNothing || !strings.Contains(o.Reason, uiRelayNone) {
		t.Fatalf("no TUI: outcome %+v, want nothing with the reason", o)
	}

	tui := openAt(t, path)
	tui.hostUIRelay()
	if tui.relay.host == nil {
		t.Fatal("the TUI is not a relay host")
	}
	// The caller waits for the TUI's answer while the TUI's loop looks
	// for requests, as its timer would.
	relay := func(q ochazuke.Request) ochazuke.Outcome {
		t.Helper()
		done := make(chan ochazuke.Outcome, 1)
		go func() { done <- cli.r.ExecuteOutcome(q) }()
		for {
			select {
			case o := <-done:
				return o
			case <-time.After(5 * time.Millisecond):
				if err := tui.serveUIRelay(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if o := relay(post); o.Kind != ochazuke.OutcomeDone || o.Err != nil {
		t.Fatalf("post: %+v", o)
	}
	req := tui.r.RestartRequested()
	if !req.Pending || req.Reason != "installed v9" || req.Version != "mtm v9" || !req.Actor.IsAgent() || req.Actor.Pane != "w9" {
		t.Fatalf("the TUI's request: %+v", req)
	}
	if lead := tui.r.LeadCells(); len(lead) != 1 || lead[0].Text != ochazuke.RestartBadgeText {
		t.Fatalf("badge: %+v", lead)
	}
	if !strings.Contains(ansi.Strip(frameView(tui).Content), ochazuke.RestartBadgeText) {
		t.Fatal("the TUI's frame has no RESTART REQUESTED badge")
	}
	if cli.r.RestartRequested().Pending {
		t.Fatal("the caller's own runtime holds the request")
	}

	show := relay(ochazuke.Request{Noun: "ochazuke.restart-request", Verb: "show", Actor: &agent})
	var shown ochazuke.RestartRequest
	if raw, ok := show.Value.(json.RawMessage); !ok || json.Unmarshal(raw, &shown) != nil || !shown.Pending || shown.Reason != "installed v9" {
		t.Fatalf("show: %+v", show)
	}
	if o := relay(ochazuke.Request{Noun: "ochazuke.restart-request", Verb: "dismiss", Actor: &agent}); o.Err != nil || tui.r.RestartRequested().Pending || len(tui.r.LeadCells()) != 0 {
		t.Fatalf("dismiss: %+v pending %v", o, tui.r.RestartRequested().Pending)
	}
	calls, _ := tui.db.All(uiCallsTable.Name)
	answers, _ := tui.db.All(uiAnswersTable.Name)
	if len(calls)+len(answers) != 0 {
		t.Fatalf("spent requests kept: %d calls %d answers", len(calls), len(answers))
	}

	// A TUI that has quit is no host: the caller answers nothing at once.
	if err := tui.Close(); err != nil {
		t.Fatal(err)
	}
	if o := cli.r.ExecuteOutcome(post); o.Kind != ochazuke.OutcomeNothing || !strings.Contains(o.Reason, uiRelayNone) {
		t.Fatalf("after the TUI quit: %+v", o)
	}
}

// The TUI's restart hook: app.restart asks with the running build (the
// --version line) and the installed one; the cleanup before the exec
// closes mtm's side (its relay host row, extension processes, the
// database) once, however often it runs, and never reaches the daemon.
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
	a.hostUIRelay()

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
	other := openAt(t, path)
	for range 2 { // main's deferred Close after a failed exec runs it again
		if err := a.beforeRestart(); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.procs.life.Err(); err == nil {
		t.Fatal("extension processes not stopped")
	}
	if _, err := a.db.All(uiHostsTable.Name); err == nil {
		t.Fatal("database still open")
	}
	if hosts, err := other.uiHosts(); err != nil || len(hosts) != 0 {
		t.Fatalf("the relay host row outlived the restart: %+v %v", hosts, err)
	}
	if n := dialed.Load() - before; n != 0 {
		t.Fatalf("the cleanup reached the daemon %d times", n)
	}
}
