package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/harness"
	"ofam.dev/matomeru/ptyhost"
)

func shortTestSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtm-cli-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "mtm.sock")
}

func startInProcessDaemon(t *testing.T, withStore bool) (*daemon.Server, *daemon.Client) {
	t.Helper()
	cfg := daemon.Config{
		SocketPath:        shortTestSocket(t),
		SchedulerInterval: -1,
	}
	if withStore {
		store, err := data.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		cfg.Store = store
	}
	srv, err := daemon.New(cfg)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return srv, daemon.NewClient(cfg.SocketPath)
}

func TestAgentHookFastPathNoDBOpened(t *testing.T) {
	srv, client := startInProcessDaemon(t, false)
	dbDir := filepath.Join(t.TempDir(), "never-created")
	dbPath := filepath.Join(dbDir, "mtm.db")

	// 1. `mtm agent hook` forwards hook payload to daemon without opening/creating DB.
	var out, errs bytes.Buffer
	payload := `{"hook_event_name":"UserPromptSubmit","prompt":"ship wave 2"}`
	code := run(
		[]string{"--db", dbPath, "agent", "hook", "claude", "--socket", srv.SocketPath(), "--pane", "p-hook"},
		strings.NewReader(payload),
		&out,
		&errs,
	)
	if code != 0 {
		t.Fatalf("agent hook exit=%d stderr=%s", code, errs.String())
	}
	if _, err := os.Stat(dbDir); !os.IsNotExist(err) {
		t.Fatalf("agent hook touched DB dir %s (err=%v)", dbDir, err)
	}

	tel, err := client.Telemetry(context.Background(), "p-hook")
	if err != nil {
		t.Fatalf("Telemetry: %v", err)
	}
	pt := tel.Telemetry["p-hook"]
	if pt == nil || pt.State != daemon.StateRunning || pt.Harness != "claude" {
		t.Fatalf("telemetry = %+v", pt)
	}

	// 2. `mtm agent state` fast-path updates pane state without opening DB.
	out.Reset()
	errs.Reset()
	code = run(
		[]string{"--db", dbPath, "agent", "state", "needs_you", "--socket", srv.SocketPath(), "--pane", "p-hook", "--summary", "waiting on review"},
		strings.NewReader(""),
		&out,
		&errs,
	)
	if code != 0 {
		t.Fatalf("agent state exit=%d stderr=%s", code, errs.String())
	}
	if _, err := os.Stat(dbDir); !os.IsNotExist(err) {
		t.Fatalf("agent state touched DB dir %s (err=%v)", dbDir, err)
	}
	tel, err = client.Telemetry(context.Background(), "p-hook")
	if err != nil {
		t.Fatalf("Telemetry after state: %v", err)
	}
	pt = tel.Telemetry["p-hook"]
	if pt == nil || pt.State != daemon.StateNeedsYou || pt.Summary != "waiting on review" {
		t.Fatalf("telemetry after state = %+v", pt)
	}

	// 3. `mtm agent --help` works cleanly via oden without opening DB.
	out.Reset()
	errs.Reset()
	code = run([]string{"--db", dbPath, "agent", "--help"}, strings.NewReader(""), &out, &errs)
	if code != 0 || !strings.Contains(out.String(), "hook") || !strings.Contains(out.String(), "state") {
		t.Fatalf("agent --help exit=%d out=%q err=%q", code, out.String(), errs.String())
	}
	if _, err := os.Stat(dbDir); !os.IsNotExist(err) {
		t.Fatalf("agent --help touched DB dir %s (err=%v)", dbDir, err)
	}
}

func TestDaemonStatusResetStop(t *testing.T) {
	// Not-running status returns exit 0 and clean running=false output.
	missingSock := shortTestSocket(t)
	c := cli{t: t, db: ":memory:"}

	notRunning := decode[daemonStatusOutput](t, c.ok("daemon", "status", "--socket", missingSock, "--json"))
	if notRunning.Running || notRunning.Socket != missingSock {
		t.Fatalf("not-running json = %+v", notRunning)
	}
	if txt := c.ok("daemon", "status", "--socket", missingSock); !strings.Contains(txt, "running:") || !strings.Contains(txt, "false") {
		t.Fatalf("not-running text = %q", txt)
	}

	// Start in-process daemon with SQLite store + HTTP MCP.
	srv, client := startInProcessDaemon(t, true)
	if err := client.Open(context.Background(), daemon.OpenRequest{
		ID:   "p1",
		Cmd:  "cat",
		Cols: 80,
		Rows: 24,
	}); err != nil {
		t.Fatalf("Open pane: %v", err)
	}

	// Status reports running=true, pid, socket, mcpURL, paneCount=1, uptime.
	st := decode[daemonStatusOutput](t, c.ok("daemon", "status", "--socket", srv.SocketPath(), "--json"))
	if !st.Running || st.PID != os.Getpid() || st.Socket != srv.SocketPath() || st.MCPURL == "" || st.PaneCount != 1 || st.Uptime == "" {
		t.Fatalf("running status json = %+v", st)
	}
	txt := c.ok("daemon", "status", "--socket", srv.SocketPath())
	for _, want := range []string{"running:", "true", "pid:", "socket:", "mcpURL:", "paneCount:", "uptime:"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("running status text missing %q:\n%s", want, txt)
		}
	}

	// Reset drops all live panes cleanly.
	resetOut := decode[map[string]any](t, c.ok("daemon", "reset", "--socket", srv.SocketPath(), "--json"))
	if resetOut["ok"] != true || resetOut["reset"] != true {
		t.Fatalf("daemon reset json = %+v", resetOut)
	}
	stAfterReset := decode[daemonStatusOutput](t, c.ok("daemon", "status", "--socket", srv.SocketPath(), "--json"))
	if !stAfterReset.Running || stAfterReset.PaneCount != 0 {
		t.Fatalf("status after reset = %+v", stAfterReset)
	}

	// Stop shuts down the daemon cleanly.
	stopOut := decode[map[string]any](t, c.ok("daemon", "stop", "--socket", srv.SocketPath(), "--json"))
	if stopOut["ok"] != true || stopOut["stopped"] != true {
		t.Fatalf("daemon stop json = %+v", stopOut)
	}

	// Wait briefly for shutdown goroutine to close socket.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		after := decode[daemonStatusOutput](t, c.ok("daemon", "status", "--socket", srv.SocketPath(), "--json"))
		if !after.Running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon still running after stop")
}

func TestDaemonServeLifecycle(t *testing.T) {
	sock := shortTestSocket(t)
	dbPath := filepath.Join(t.TempDir(), "daemon.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runDaemonServe(ctx, globalOpts{db: dbPath}, sock, "127.0.0.1:0")
	}()

	client := daemon.NewClient(sock)
	var st *daemon.StatusResult
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		if st, err = client.Status(context.Background()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st == nil {
		t.Fatal("runDaemonServe did not become ready")
	}
	if st.MCPURL == "" {
		t.Fatalf("daemon serve did not start HTTP MCP server: %+v", st)
	}
	resp, err := http.Get(st.MCPURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDaemonServe returned error on cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runDaemonServe did not exit after context cancellation")
	}
}

func TestHarnessList(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("MTM_CONFIG_DIR", cfgDir)

	// 1. Without o-ai on PATH, harness list still succeeds with plain shell (zsh).
	emptyBin := t.TempDir()
	t.Setenv("PATH", emptyBin)
	c := cli{t: t, db: ":memory:"}

	entries := decode[[]harness.Entry](t, c.ok("harness", "list", "--json"))
	if len(entries) != 1 || entries[0].ID != "zsh" || !entries[0].Shell {
		t.Fatalf("harness list without o-ai = %+v", entries)
	}

	// 2. With fake o-ai + harnesses.json, merges installed, custom, and shell.
	fakeOAI := filepath.Join(emptyBin, "o-ai")
	script := `#!/bin/sh
echo '{"items":[{"kind":"ai.harness","id":"claude","fields":{"id":"claude","installed":true,"configured":true,"executable":"/usr/local/bin/claude"}}],"total":1}'
`
	if err := os.WriteFile(fakeOAI, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	harnessesJSON := `{
  "default": "claude",
  "harnesses": {
    "claude": {"command": ["claude", "--dangerously-skip-permissions"]},
    "my-agent": {"label": "My Agent", "command": ["my-agent", "--fast"]}
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "harnesses.json"), []byte(harnessesJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	entries = decode[[]harness.Entry](t, c.ok("harness", "list", "--json"))
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (claude, my-agent, zsh), got %+v", entries)
	}
	if entries[0].ID != "claude" || strings.Join(entries[0].Command, " ") != "claude --dangerously-skip-permissions" {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	tbl := c.ok("harness", "list")
	for _, want := range []string{"ID", "LABEL", "SOURCE", "COMMAND", "claude", "my-agent", "my-agent --fast", "zsh"} {
		if !strings.Contains(tbl, want) {
			t.Fatalf("harness list table missing %q:\n%s", want, tbl)
		}
	}
}

func TestBuildTUIOptionsDaemonAutoConnectAndFallback(t *testing.T) {
	t.Setenv("MTM_NO_DAEMON", "")
	cfgDir := t.TempDir()

	// 1. Connects automatically to a live daemon when enabled.
	srv, _ := startInProcessDaemon(t, true)
	t.Setenv("MTM_SOCKET", srv.SocketPath())

	var stderr bytes.Buffer
	opts, warnings := buildTUIOptions(context.Background(), ":memory:", cfgDir, &stderr, nil)
	if opts.Daemon == nil {
		t.Fatal("expected opts.Daemon to be set")
	}
	if _, ok := opts.Host.(*daemon.HostAdapter); !ok {
		t.Fatalf("opts.Host = %T, want *daemon.HostAdapter", opts.Host)
	}
	if len(warnings) != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected warnings=%v stderr=%q", warnings, stderr.String())
	}

	// 2. MTM_NO_DAEMON=1 disables daemon auto-connect.
	t.Setenv("MTM_NO_DAEMON", "1")
	opts, warnings = buildTUIOptions(context.Background(), ":memory:", cfgDir, &stderr, func(string) error {
		t.Fatal("launcher should not be called when MTM_NO_DAEMON=1")
		return nil
	})
	if opts.Daemon != nil || len(warnings) != 0 {
		t.Fatalf("with MTM_NO_DAEMON=1: Daemon=%v warnings=%v", opts.Daemon, warnings)
	}
	if _, ok := opts.Host.(*ptyhost.LocalHost); !ok {
		t.Fatalf("with MTM_NO_DAEMON=1: Host=%T, want *ptyhost.LocalHost", opts.Host)
	}

	// 3. settings.json "terminal.daemon": false disables daemon auto-connect.
	t.Setenv("MTM_NO_DAEMON", "")
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"terminal.daemon": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, warnings = buildTUIOptions(context.Background(), ":memory:", cfgDir, &stderr, func(string) error {
		t.Fatal("launcher should not be called when terminal.daemon is false")
		return nil
	})
	if opts.Daemon != nil || len(warnings) != 0 {
		t.Fatalf("with terminal.daemon=false: Daemon=%v warnings=%v", opts.Daemon, warnings)
	}

	// 4. Startup failure falls back to local PTY host and surfaces a warning.
	if err := os.Remove(filepath.Join(cfgDir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MTM_SOCKET", shortTestSocket(t))
	stderr.Reset()
	opts, warnings = buildTUIOptions(context.Background(), ":memory:", cfgDir, &stderr, func(string) error {
		return errors.New("boom")
	})
	if opts.Daemon != nil {
		t.Fatalf("expected nil Daemon on failure, got %v", opts.Daemon)
	}
	if _, ok := opts.Host.(*ptyhost.LocalHost); !ok {
		t.Fatalf("expected *ptyhost.LocalHost fallback, got %T", opts.Host)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "boom") || !strings.Contains(stderr.String(), "warning:") {
		t.Fatalf("expected warning on failure: warnings=%v stderr=%q", warnings, stderr.String())
	}
}
