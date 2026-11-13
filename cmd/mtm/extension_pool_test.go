package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// serveToolDir holds the served fixture tool (testdata/servetool), built
// once per test binary; TestMain removes it.
var serveToolDir string

var serveTool = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "mtm-servetool")
	if err != nil {
		return "", err
	}
	serveToolDir = dir
	bin := filepath.Join(dir, "o-serve")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/servetool").CombinedOutput(); err != nil {
		return "", fmt.Errorf("build servetool: %v\n%s", err, out)
	}
	return bin, nil
})

// servedApp is an app with the served fixture registered as "serve", the
// settings set first (a change makes the pools again).
func servedApp(t *testing.T, settings map[string]any) (*App, *extension) {
	t.Helper()
	bin, err := serveTool()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mtm.db")
	mustCLI(t, openAt(t, path), "extension", "register", "serve", "--bin", bin)
	a := openAt(t, path)
	for name, v := range settings {
		if err := a.r.Settings().Set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	x := a.extension("serve")
	if x == nil || x.doc.Serve != ocli.ServeProtocol {
		t.Fatalf("servetool not mounted as a served tool: %+v", x)
	}
	return a, x
}

// starts are the fixture's process starts since mtm's data was made.
func starts(t *testing.T, a *App) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(a.dataDir(), "servetool-starts"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(raw)), " ", "_"))
}

// openServedPage opens the fixture's page n times (its lists and its three
// parameter sources load each time), from the Work page each time.
func openServedPage(t *testing.T, a *App, x *extension, n int) {
	t.Helper()
	for range n {
		toWork(t, a)
		openExtPage(t, a, x.views[0].Page())
	}
}

// With extensions.transport persistent, opening a served tool's page and
// opening it again starts at most two of its processes in the session: the
// first call's exec and one kept process the later reads share.
func TestServedPageOpensAtMostTwoProcesses(t *testing.T) {
	t.Parallel()
	// A long read wait keeps the count exact on a loaded machine: the
	// default (250ms) is about mtm's speed, not this check.
	a, x := servedApp(t, map[string]any{settingExtPoolWait: 10 * time.Second})
	openServedPage(t, a, x, 3)
	if got := starts(t, a); len(got) > 2 {
		t.Fatalf("%d processes for 3 page opens (18 reads), want at most 2: %v", len(got), got)
	}
	out, err := x.read(context.Background(), a.extEnv(x.name), "item", "whoami", "--json", "{}")
	if err != nil || !strings.Contains(string(out), `"served": true`) {
		t.Fatalf("whoami: %s %v", out, err)
	}
	if conns := a.connections("serve"); len(conns) != 1 || conns[0].Calls < 5 {
		t.Fatalf("connections %+v", conns)
	}
}

// With extensions.transport exec, every read is its own process, as before.
func TestExecTransportStartsAProcessPerCall(t *testing.T) {
	t.Parallel()
	a, x := servedApp(t, map[string]any{settingExtTransport: extTransportExec})
	if x.pool.Load() != nil {
		t.Fatal("exec transport kept a pool")
	}
	openServedPage(t, a, x, 3)
	// Six reads per open: the items, the feed (twice), three sources.
	if got := starts(t, a); len(got) != 18 {
		t.Fatalf("%d processes for 3 page opens, want 18 (one per read): %v", len(got), got)
	}
	if conns := a.connections(""); len(conns) != 0 {
		t.Fatalf("connections %+v", conns)
	}
}

// A noun's watch runs on a kept process (cli.PoolWatch) and holds it
// until stopped.
func TestServedWatchStreamsOnAKeptProcess(t *testing.T) {
	t.Parallel()
	a, x := servedApp(t, map[string]any{settingExtPersistentAfter: 1})
	events := make(chan ochazuke.WatchEvent, 4)
	stop, err := a.extWatch(x, "feed", ochazuke.WatchQuery{}, func(e ochazuke.WatchEvent) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Type != "added" || e.ID != "w1" {
			t.Fatalf("event %+v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no event streamed")
	}
	if conns := a.connections("serve"); len(conns) != 1 || conns[0].State != "busy" {
		t.Fatalf("watch holds no kept process: %+v", conns)
	}
	stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conns := a.connections("serve"); len(conns) == 1 && conns[0].State == "idle" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stop left %+v", a.connections("serve"))
}

// extensions.host-calls pipe: a kept tool's Host.Call is answered in this
// process, by the callback runtime, with no mtm started.
func TestServedHostCallAnsweredInProcess(t *testing.T) {
	t.Parallel()
	a, _ := servedApp(t, map[string]any{settingExtPersistentAfter: 1})
	out, errOut, err := mtm(a, t.TempDir(), "serve.item", "host")
	if err != nil || !strings.Contains(out, `"peers": 1`) {
		t.Fatalf("host: %v\n%s\n%s", err, out, errOut)
	}
	if !callbackBuilt(a) {
		t.Fatal("the callback did not run on the callback runtime")
	}
}

// extensions.host-calls exec: a callback runs mtm's command as a process,
// as before (this test binary, which refuses mtm's flags), and no
// callback runtime is built.
func TestHostCallsExecStartsTheHost(t *testing.T) {
	t.Parallel()
	a := openAt(t, filepath.Join(t.TempDir(), "mtm.db"))
	if err := a.r.Settings().Set(settingExtHostCalls, extHostCallsExec); err != nil {
		t.Fatal(err)
	}
	answer := a.answerHostRun(context.Background(), ocli.ServeCall{Argv: append(a.hostCommand(), "extension", "peers")})
	if answer.Exit == 0 || callbackBuilt(a) {
		t.Fatalf("exit %d, callback runtime %v: %s", answer.Exit, callbackBuilt(a), answer.Stderr)
	}
}

// callbackBuilt reads whether the callback runtime was built, in its turn.
func callbackBuilt(a *App) bool {
	a.callbacks.turn <- struct{}{}
	defer func() { <-a.callbacks.turn }()
	return a.callbacks.app != nil
}
