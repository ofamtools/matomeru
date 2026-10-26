package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// isolate points the runtime dir at a temp dir, clears the daemon env and
// replaces serverStart with a failing fake that counts its calls.
func isolate(t *testing.T) (dir string, starts *int) {
	t.Helper()
	// Short: unix socket paths are capped near 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "mtmc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("TUIOS_SOCKET", "")
	t.Setenv("TUIOS_SERVER", "")
	t.Setenv("MTM_SERVER", "")
	os.Unsetenv("MTM_SERVER")
	starts = new(int)
	old := serverStart
	serverStart = func(context.Context, string) (<-chan error, error) { *starts++; return nil, errors.New("boom") }
	t.Cleanup(func() { serverStart = old })
	return dir, starts
}

func TestServerPrecedence(t *testing.T) {
	dir, _ := isolate(t)
	if got := defaultServer(); got != MtmServer {
		t.Fatalf("default server = %q", got)
	}
	t.Setenv("MTM_SERVER", DefaultDaemon)
	if got := defaultServer(); got != DefaultDaemon {
		t.Fatalf("MTM_SERVER=default: %q", got)
	}
	// TUIOS_SOCKET (the user's own daemon, set inside tuios) does not win
	// over the named server; an explicit -socket does.
	t.Setenv("TUIOS_SOCKET", "/main.sock")
	named := Options{Server: MtmServer}.client()
	if want := daemon.SocketFor(MtmServer); named.Server != MtmServer || named.Socket != want || !strings.HasPrefix(want, dir) {
		t.Fatalf("-server mtm-daemon = %+v", named)
	}
	if c := (Options{Server: MtmServer, Socket: "/x.sock"}).client(); c.Socket != "/x.sock" || c.Server != "" {
		t.Fatalf("-socket = %+v", c)
	}
	for _, server := range []string{DefaultDaemon, ""} {
		if c := (Options{Server: server}).client(); c.Socket != "/main.sock" || c.Server != "" {
			t.Fatalf("-server %q = %+v", server, c)
		}
	}
}

// --smoke never ensures; a CLI command ensures before dispatch and keeps
// working when the server cannot start.
func TestRunEnsuresOnlyOutsideSmoke(t *testing.T) {
	_, starts := isolate(t)
	data := filepath.Join(t.TempDir(), "mtm.db")
	var out, errOut bytes.Buffer
	if err := run([]string{"--data", data, "--smoke"}, strings.NewReader(""), &out, &errOut); err != nil || *starts != 0 {
		t.Fatalf("smoke: %v, starts %d", err, *starts)
	}
	if err := run([]string{"--data", data, "bindings", "list"}, strings.NewReader(""), &out, &errOut); err != nil || *starts != 1 {
		t.Fatalf("cli: %v, starts %d", err, *starts)
	}
	if !strings.Contains(errOut.String(), "mtm: daemon mtm-daemon not running: boom") {
		t.Fatalf("stderr: %s", errOut.String())
	}
}

func TestDefaultDaemonIsNeverStarted(t *testing.T) {
	dir, starts := isolate(t)
	data := filepath.Join(t.TempDir(), "mtm.db")
	t.Setenv("TUIOS_SOCKET", filepath.Join(dir, "none.sock"))
	var out, errOut bytes.Buffer
	if err := run([]string{"--data", data, "--server", DefaultDaemon, "bindings", "list"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MTM_SERVER", DefaultDaemon)
	if err := run([]string{"--data", data, "bindings", "list"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if *starts != 0 || strings.Count(errOut.String(), "not running") != 2 {
		t.Fatalf("starts %d, stderr %s", *starts, errOut.String())
	}
}

// The TUI ensures off the loop: a failure is the footer error and the
// panes page hint; success loads the pane list.
func TestEnsureJobFailureShowsInFooterAndPanesHint(t *testing.T) {
	app := open(t)
	app.m.Width, app.m.Height = 80, 24
	app.ensure = func(context.Context) error { return errors.New("daemon mtm-daemon not running: boom") }
	if err := app.ensureDaemon(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if !strings.Contains(app.m.Status, "daemon mtm-daemon not running: boom") {
		t.Fatalf("footer: %q", app.m.Status)
	}
	// The whole error is a notice until the next key (notices are
	// transient); the page keeps saying the daemon is down.
	if !slices.ContainsFunc(app.m.Notices, func(n string) bool { return strings.Contains(n, "boom") }) {
		t.Fatalf("notices: %q", app.m.Notices)
	}
	press(t, app, "ctrl+k")
	if f := ansi.Strip(app.m.View().Content); !strings.Contains(f, "is not running") || !strings.Contains(f, "e starts it (daemon.ensure)") {
		t.Fatalf("panes page:\n%s", f)
	}
	app.ensure = func(context.Context) error { return nil }
	if err := app.ensureDaemon(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if app.paneErr != "" || len(app.panes) == 0 {
		t.Fatalf("after ensure: err %q panes %d", app.paneErr, len(app.panes))
	}
}
