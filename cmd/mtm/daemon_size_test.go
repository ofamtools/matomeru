//go:build !nodaemon

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The size a pane reports after pane.resize is the size it was resized to
// (mtmd reports the PTY size since v0.1.1).
//
// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestPaneShowReportsResizedSize(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "mtmsz")
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
	t.Setenv("MTM_DAEMON_RUNNER", runnerSelf)
	t.Setenv("MTM_SERVER", "sizechk")
	for _, k := range []string{"TUIOS_SOCKET", "TUIOS_SERVER", "TUIOS_PANE_ID", "TUIOS_PANE_TOKEN", "TUIOS_SESSION", "TUIOS_WINDOW_ID"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	data := filepath.Join(root, "mtm.db")
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"daemon.pin-binary":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	do := func(args ...string) []byte {
		t.Helper()
		var out, errOut bytes.Buffer
		if err := run(append([]string{"--data", data}, args...), strings.NewReader(""), &out, &errOut); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, errOut.String())
		}
		return out.Bytes()
	}
	do("daemon", "start")
	t.Cleanup(func() {
		if info := daemonStatusRow(t, data); info.PID > 1 {
			syscall.Kill(info.PID, syscall.SIGTERM)
		}
	})
	do("session", "new", "--name", "sz", "--yes")
	do("pane", "resize", "--session", "sz", "--width", "100", "--height", "30", "--yes")
	var row struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal(do("pane", "show", "--session", "sz"), &row); err != nil {
		t.Fatal(err)
	}
	if row.Width != 100 || row.Height != 30 {
		t.Fatalf("pane show reports %dx%d after resize to 100x30", row.Width, row.Height)
	}
}
