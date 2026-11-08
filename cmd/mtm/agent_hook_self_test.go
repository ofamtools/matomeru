//go:build !nodaemon

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
)

// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
//
// mtm agent hook against the self daemon, with the recorded Claude Code
// payloads of internal/agenthook/testdata/hooks: each event is fed to a fresh
// pane and the pane's agent state must be the one the fixture says. A hook
// with no daemon exits 0, prints nothing and starts none.
func TestAgentHookReportsPaneStateToTheSelfDaemon(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "mtmhook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, d := range []string{"home", "run", "config", "state", "data"} {
		os.MkdirAll(filepath.Join(root, d), 0o700)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("SHELL", "/bin/sh")
	for _, k := range []string{"TUIOS_SOCKET", "TUIOS_SERVER", "TUIOS_PANE_ID", "TUIOS_SESSION", "TUIOS_AGENT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	server := "hookself"
	pinDir := filepath.Join(root, "state", "daemon-bin")
	self := daemon.SelfStart{Word: daemonWord, PinDir: pinDir, Label: "test", InUse: func(string) bool { return true }}
	c := daemon.ForServer(server)
	c.Start, c.Wait = self.Start, 60*time.Second
	c.Ready = c.HelloReady
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello(ctx)
	if err != nil || h.PID <= 1 {
		t.Fatalf("hello: %+v, %v", h, err)
	}
	t.Cleanup(func() { syscall.Kill(h.PID, syscall.SIGTERM) })
	if _, err := daemon.LaunchWorkspace(ctx, c, "s1", 100, 30); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "agenthook", "testdata", "hooks", "claude-code.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
		Env     map[string]string
		Event   string
		Want    map[string]any `json:"want"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for i, f := range fixtures {
		state, _ := f.Want["state"].(string)
		if f.Want == nil || state == "" || f.Env != nil || f.Event != "" || f.Want["if_state"] != nil || f.Want["if_subagent_id"] != nil {
			continue
		}
		w, err := c.NewWindow(ctx, daemon.NewWindowParams{Session: "s1", Name: "p" + string(rune('a'+i%26)), Command: []string{"/bin/sh"}})
		if err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		args := []string{"--socket", c.Socket, "agent", "hook", "claude-code", "--integration", "3", "--session", "s1", "--window", w.WindowID}
		if err := run(args, bytes.NewReader(f.Payload), &out, &errOut); err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if out.Len() != 0 {
			t.Fatalf("%s: the hook printed %q", f.Name, out.String())
		}
		var got struct {
			State string `json:"state"`
		}
		if err := c.Call(ctx, "get-agent-state", map[string]any{"session": "s1", "window": w.WindowID}, &got); err != nil {
			t.Fatal(err)
		}
		if got.State != state {
			t.Errorf("%s: pane state = %q, fixture says %q (%s)", f.Name, got.State, state, errOut.String())
		}
		checked++
	}
	if checked < 15 {
		t.Fatalf("only %d fixtures were fed to the daemon", checked)
	}

	// A hook never starts a daemon.
	gone := filepath.Join(root, "run", "tuios", "servers", "none", "tuios.sock")
	var out, errOut bytes.Buffer
	args := []string{"--socket", gone, "agent", "hook", "claude-code", "--session", "s1", "--window", "w1", "--explain"}
	if err := run(args, strings.NewReader(`{"hook_event_name":"Stop","session_id":"x"}`), &out, &errOut); err != nil {
		t.Fatalf("with no daemon the hook failed: %v", err)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "no daemon") {
		t.Fatalf("with no daemon: stdout %q explain %q", out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Dir(gone)); err == nil {
		t.Fatalf("the hook made %s", filepath.Dir(gone))
	}
}
