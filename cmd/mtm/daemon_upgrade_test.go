package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

// helloDaemon is a socket that answers hello with version and pid 4242, and
// any other verb with an empty result. close stops it.
func helloDaemon(t *testing.T, version string) (socket string, close func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtm-up") // a short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket = filepath.Join(dir, "d.sock")
	return socket, serveHello(t, socket, version, nil)
}

// serveHello serves helloDaemon's answers at socket; answer, when given,
// answers the other verbs (nil: an empty result). The returned func stops it.
func serveHello(t *testing.T, socket, version string, answer func(verb string, params json.RawMessage) map[string]any) func() {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() { once.Do(func() { ln.Close() }) }
	t.Cleanup(stop)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					var req struct {
						ID     string          `json:"id"`
						Verb   string          `json:"verb"`
						Params json.RawMessage `json:"params"`
					}
					_ = json.Unmarshal(sc.Bytes(), &req)
					result := map[string]any{}
					switch {
					case req.Verb == "hello":
						result = map[string]any{"daemon_version": version, "pid": 4242, "protocol": 1}
					case answer != nil:
						result = answer(req.Verb, req.Params)
					}
					_ = json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result})
				}
			}()
		}
	}()
	return stop
}

// upgradeFake records the facade calls and answers outcome.
type upgradeFake struct {
	mu      sync.Mutex
	calls   [][]string
	outcome string
}

func (f *upgradeFake) run(_ context.Context, argv, _ []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, argv)
	return []byte(f.outcome), nil
}

func (f *upgradeFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// upgradeApp is an app on a hello daemon of version, whose facade runs are
// fake, with the pin off (no copy of a binary) and envRestarted set.
func upgradeApp(t *testing.T, version, outcome string) (*App, *upgradeFake, func()) {
	t.Helper()
	a := open(t)
	sock, stop := helloDaemon(t, version)
	a.daemon.Socket = sock
	fake := &upgradeFake{outcome: outcome}
	a.runUpgrade = fake.run
	a.executable = func() (string, error) { return "/opt/mtm/bin/mtm", nil }
	a.ensure = func(context.Context) error { return nil }
	env := a.getenv
	a.getenv = func(k string) string {
		if k == envRestarted {
			return "1"
		}
		if k == envDaemonRunner {
			return runnerSelf
		}
		return env(k)
	}
	if err := a.r.Settings().Set(settingDaemonPin, false); err != nil {
		t.Fatal(err)
	}
	return a, fake, stop
}

const upgradedOutcome = `{"action":"upgraded","pid":4242,"from":"v1","to":"v2","panes":2,"panes_kept":true}`

// The TUI that & started upgrades the daemon only when it runs another
// build, and not at all with restart.upgrade-daemon off.
func TestRestartUpgradesTheDaemonOnlyOnAnotherBuild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		version string
		setting bool
		want    int
	}{
		{"same build", daemonVersion(), true, 0},
		{"another build", "v0.1.0+0123456789ab", true, 1},
		{"setting off", "v0.1.0+0123456789ab", false, 0},
	} {
		a, fake, _ := upgradeApp(t, tc.version, upgradedOutcome)
		if err := a.r.Settings().Set(settingRestartUpgradeDaemon, tc.setting); err != nil {
			t.Fatal(err)
		}
		a.upgradeAfterRestart()
		settle(a, a.r.PendingCommands())
		if got := fake.count(); got != tc.want {
			t.Fatalf("%s: %d upgrade runs, want %d", tc.name, got, tc.want)
		}
		if tc.want == 1 {
			argv := fake.calls[0]
			if !slices.Contains(argv, daemonWord) || !strings.Contains(strings.Join(argv, " "), "daemon upgrade --binary /opt/mtm/bin/mtm --json") {
				t.Fatalf("%s: facade argv %q", tc.name, argv)
			}
		}
	}
}

// daemon.restart hands off live and never stops the daemon; a daemon too
// old to hand off is left running with a notice naming --fresh; fresh stops
// it.
// serial: replaces the package killPID hook.
func TestDaemonRestartIsLiveUnlessFresh(t *testing.T) {
	var killed []int
	old := killPID
	t.Cleanup(func() { killPID = old })
	a, fake, stop := upgradeApp(t, "v0.53.0+dbfefedb371d",
		`{"action":"refused","pid":4242,"from":"v0.53.0","not_live":true,"reason":"the daemon (v0.53.0, pid 4242) predates live upgrades"}`)
	killPID = func(pid int, _ syscall.Signal) error { killed = append(killed, pid); stop(); return nil }
	restart := func(args ochazuke.Arguments) {
		t.Helper()
		if _, err := a.r.Execute(ochazuke.Request{Noun: "daemon", Verb: "restart", Target: paneListID, Confirmed: true, Args: args}); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
	}

	restart(nil)
	if fake.count() != 1 || len(killed) != 0 {
		t.Fatalf("too-old daemon: %d upgrade runs, killed %v", fake.count(), killed)
	}
	if !slices.ContainsFunc(a.m.Notices, func(n string) bool {
		return strings.Contains(n, "predates live upgrades") && strings.Contains(n, "mtm daemon restart --fresh") && strings.Contains(n, "ends the programs")
	}) {
		t.Fatalf("no too-old notice: %q", a.m.Notices)
	}

	fake.outcome = upgradedOutcome
	restart(nil)
	if fake.count() != 2 || len(killed) != 0 {
		t.Fatalf("live restart: %d upgrade runs, killed %v", fake.count(), killed)
	}

	restart(ochazuke.Arguments{"fresh": true})
	if fake.count() != 2 || !slices.Equal(killed, []int{4242}) {
		t.Fatalf("fresh restart: %d upgrade runs, killed %v", fake.count(), killed)
	}
}

// A fresh restart with daemon.resume-agents on asks the daemon to resume each
// agent that ran before it with a recorded conversation, and skips one with
// none; off, it asks nothing.
// serial: replaces the package killPID hook.
func TestFreshRestartResumesKnownAgents(t *testing.T) {
	old := killPID
	t.Cleanup(func() { killPID = old })
	for _, on := range []bool{true, false} {
		a, _, stop := upgradeApp(t, daemonVersion(), upgradedOutcome)
		var mu sync.Mutex
		var resumed []string
		answer := func(verb string, params json.RawMessage) map[string]any {
			if verb != "resume-agent" {
				return map[string]any{}
			}
			var p struct{ Session, Window string }
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			resumed = append(resumed, p.Session+"/"+p.Window)
			mu.Unlock()
			return map[string]any{"window_id": p.Window, "harness": "claude-code", "agent_session_id": "s1",
				"command": "claude --resume s1", "typed": true}
		}
		killPID = func(int, syscall.Signal) error { stop(); return nil }
		a.ensure = func(context.Context) error {
			stop = serveHello(t, a.daemon.Socket, daemonVersion(), answer)
			return nil
		}
		a.panesd.(*fakePanes).panes = []pane{
			{Session: "work", Harness: "claude-code", AgentSession: "s1", Pane: daemon.Pane{ID: "w1", Name: "lead"}},
			{Session: "work", Harness: "claude-code", Pane: daemon.Pane{ID: "w2", Name: "fresh"}},
			{Session: "work", Pane: daemon.Pane{ID: "w3", Name: "shell"}},
		}
		if err := a.r.Settings().Set(settingResumeAgents, on); err != nil {
			t.Fatal(err)
		}
		if _, err := a.r.Execute(ochazuke.Request{Noun: "daemon", Verb: "restart", Target: paneListID, Confirmed: true,
			Args: ochazuke.Arguments{"fresh": true}}); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
		mu.Lock()
		got := slices.Clone(resumed)
		mu.Unlock()
		if !on {
			if len(got) != 0 {
				t.Fatalf("setting off: resumed %v", got)
			}
			continue
		}
		if !slices.Equal(got, []string{"work/w1"}) {
			t.Fatalf("resume-agent calls %v, want work/w1 only", got)
		}
		if !slices.ContainsFunc(a.m.Notices, func(n string) bool {
			return strings.Contains(n, "resumed 1 agent(s): work/lead") && strings.Contains(n, "skipped 1: work/fresh: no conversation")
		}) {
			t.Fatalf("no resume notice: %q", a.m.Notices)
		}
	}
}
