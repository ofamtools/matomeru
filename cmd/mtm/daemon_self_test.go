//go:build !nodaemon

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
)

// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestSelfRunnerRacingStartersConvergeOnOneDaemon(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "mtmself")
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
	server := "selfrace"
	for _, k := range []string{"TUIOS_SOCKET", "TUIOS_SERVER"} {
		// Both names: the daemon exports MTM_ beside TUIOS_, and mtm reads MTM_ first.
		for _, k := range []string{k, "MTM_" + strings.TrimPrefix(k, "TUIOS_")} {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	t.Setenv("MTM_SERVER", server)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pinDir := filepath.Join(root, "state", "daemon-bin")
	// One copy first: the racers should find it (Pin's own race is covered in
	// internal/daemon); a copy of this binary is large.
	pinned, err := daemon.Pin(exe, pinDir, "test", func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	self := daemon.SelfStart{Word: daemonWord, PinDir: pinDir, Label: "test", InUse: func(string) bool { return true }}
	var clients []*daemon.Client
	for i := 0; i < 6; i++ {
		c := daemon.ForServer(server)
		c.Start, c.Wait = self.Start, 60*time.Second
		c.Ready = c.HelloReady
		clients = append(clients, c)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(clients))
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			errs[i] = c.Ensure(ctx)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("starter %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h, err := clients[0].Hello(ctx)
	if err != nil || h.PID <= 1 {
		t.Fatalf("hello: %+v, %v", h, err)
	}
	pid := h.PID
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGTERM) })
	for i, c := range clients {
		if h2, err := c.Hello(ctx); err != nil || h2.PID != pid {
			t.Fatalf("starter %d sees pid %d (%v), want one daemon %d", i, h2.PID, err, pid)
		}
	}
	if got := processBinary(pid); got != pinned {
		t.Fatalf("the daemon runs %q, want the pinned copy %q", got, pinned)
	}
	if entries, _ := os.ReadDir(pinDir); len(entries) != 1 {
		t.Fatalf("pin dir holds %d entries", len(entries))
	}
	// The same daemon answers mtm's own status, and stop ends it.
	data := filepath.Join(root, "mtm.db")
	if info := daemonStatusRow(t, data); !info.Running || info.PID != pid || info.Runner != runnerSelf {
		t.Fatalf("status = %+v", info)
	}
	var out, errOut bytes.Buffer
	if err := run([]string{"--data", data, "daemon", "stop", "--yes"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("stop: %v %s", err, errOut.String())
	}
	if info := daemonStatusRow(t, data); info.Running {
		t.Fatalf("still running after stop: %+v", info)
	}
}

// The mirror of TestRunEnsuresOnlyOutsideSmoke for the default (self)
// runner: a CLI command on a fresh start ensures mtm's server by starting
// the linked daemon, and --smoke starts nothing.
// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestSelfRunnerCLIEnsuresTheDaemonOnAFreshStart(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "mtmens")
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
	t.Setenv("MTM_DAEMON_RUNNER", "") // the default: self
	for _, k := range []string{"TUIOS_SOCKET", "TUIOS_SERVER", "MTM_SOCKET"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("MTM_SERVER", "selfensure")
	data := filepath.Join(root, "mtm.db")
	var out, errOut bytes.Buffer
	if err := run([]string{"--data", data, "--smoke"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("smoke: %v", err)
	}
	if info := daemonStatusRow(t, data); info.Running {
		t.Fatalf("--smoke started a daemon: %+v", info)
	}
	if err := run([]string{"--data", data, "binding", "list"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("cli: %v %s", err, errOut.String())
	}
	info := daemonStatusRow(t, data)
	if !info.Running || info.Runner != runnerSelf {
		t.Fatalf("a CLI command did not start the self daemon: %+v", info)
	}
	t.Cleanup(func() { syscall.Kill(info.PID, syscall.SIGTERM) })
}
