package main

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
)

// The daemon module is linked by one file (and its off twin): nothing else
// in the repository imports ofam.dev/mtmd, so the client stays wire-only and
// the app never reaches into the daemon.
func TestDaemonImportBoundary(t *testing.T) {
	t.Parallel()
	allowed := []string{"cmd/mtm/daemon_shell.go", "cmd/mtm/daemon_off.go"}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == ".worktrees" || name == "testdata" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if (p == "ofam.dev/mtmd" || strings.HasPrefix(p, "ofam.dev/mtmd/")) && !slices.Contains(allowed, filepath.ToSlash(rel)) {
				t.Errorf("%s imports %s; only %v may", rel, p, allowed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// daemon_shell.go carries the import under the default build.
	if _, err := os.Stat("daemon_shell.go"); err != nil {
		t.Fatal(err)
	}
}

// daemonJSON runs `mtm daemon status` in the isolated environment and
// returns its row.
func daemonStatusRow(t *testing.T, data string) daemonInfo {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := run([]string{"--data", data, "daemon", "status"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("daemon status: %v\n%s", err, errOut.String())
	}
	var info daemonInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("status output %q: %v", out.String(), err)
	}
	return info
}

// serial: exercises daemon process environment and the package serverStart hook.
func TestAbsentDaemonStatusStopAndLogsStartNothing(t *testing.T) {
	_, starts := isolate(t)
	data := filepath.Join(t.TempDir(), "mtm.db")
	t.Setenv("MTM_DAEMON_RUNNER", "")
	if info := daemonStatusRow(t, data); info.Running || info.Runner != runnerTuios {
		t.Fatalf("status = %+v", info)
	}
	for _, args := range [][]string{{"daemon", "show-log"}, {"daemon", "stop", "--yes"}} {
		var out, errOut bytes.Buffer
		_ = run(append([]string{"--data", data}, args...), strings.NewReader(""), &out, &errOut)
	}
	if *starts != 0 {
		t.Fatalf("status, show-log or stop started a daemon (%d starts)", *starts)
	}
	// Any other command still starts mtm's server when it is missing.
	var out, errOut bytes.Buffer
	_ = run([]string{"--data", data, "binding", "list"}, strings.NewReader(""), &out, &errOut)
	if *starts != 1 {
		t.Fatalf("a plain command made %d starts, want 1", *starts)
	}
}

// serial: exercises daemon process environment and the package serverStart hook.
func TestDaemonRunnerIsChosenByEnvironmentThenSetting(t *testing.T) {
	_, _ = isolate(t)
	data := filepath.Join(t.TempDir(), "mtm.db")
	t.Setenv("MTM_DAEMON_RUNNER", "")
	if got := daemonStatusRow(t, data).Runner; got != runnerTuios {
		t.Fatalf("default runner = %q", got)
	}
	cfg := filepath.Join(filepath.Dir(data), "settings.json")
	if err := os.WriteFile(cfg, []byte(`{"daemon.runner":"self"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := daemonStatusRow(t, data).Runner; got != runnerSelf {
		t.Fatalf("runner from settings.json = %q", got)
	}
	t.Setenv("MTM_DAEMON_RUNNER", runnerTuios)
	if got := daemonStatusRow(t, data).Runner; got != runnerTuios {
		t.Fatalf("runner from the environment = %q", got)
	}
}

// serial: exercises daemon process environment and the package serverStart hook.
func TestUnknownDaemonRunnerNamesTheChoices(t *testing.T) {
	_, _ = isolate(t)
	data := filepath.Join(t.TempDir(), "mtm.db")
	t.Setenv("MTM_DAEMON_RUNNER", "carrier-pigeon")
	var out, errOut bytes.Buffer
	_ = run([]string{"--data", data, "binding", "list"}, strings.NewReader(""), &out, &errOut)
	if !strings.Contains(errOut.String(), `unknown daemon runner "carrier-pigeon" (tuios`) {
		t.Fatalf("stderr: %s", errOut.String())
	}
}

// serial: exercises daemon process environment and the package serverStart hook.
func TestDaemonPathsFollowTheRunnerThenEnvironmentThenSetting(t *testing.T) {
	dir, _ := isolate(t)
	data := filepath.Join(t.TempDir(), "mtm.db")
	t.Setenv("MTM_DAEMON_RUNNER", "")
	sock := func(layout string) string {
		l, err := daemon.LayoutNamed(layout)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, l.SocketApp, "servers", MtmServer, l.SocketFile)
	}
	// The tuios runner's daemon is a tuios: always tuios's names.
	if info := daemonStatusRow(t, data); info.Paths != "tuios" || info.Socket != sock("tuios") {
		t.Fatalf("tuios runner: paths %q socket %s", info.Paths, info.Socket)
	}
	cfg := filepath.Join(filepath.Dir(data), "settings.json")
	write := func(body string) {
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"daemon.runner":"self"}`)
	info := daemonStatusRow(t, data)
	if info.Paths != "mtm" || info.Socket != sock("mtm") || !strings.HasSuffix(info.StateDir, filepath.Join("mtm", "servers", MtmServer)) {
		t.Fatalf("self runner: paths %q socket %s state %s", info.Paths, info.Socket, info.StateDir)
	}
	write(`{"daemon.runner":"self","daemon.paths":"tuios"}`)
	if info := daemonStatusRow(t, data); info.Paths != "tuios" || info.Socket != sock("tuios") || !strings.HasSuffix(info.StateDir, filepath.Join("tuios-servers", MtmServer, "tuios")) {
		t.Fatalf("daemon.paths=tuios: paths %q socket %s state %s", info.Paths, info.Socket, info.StateDir)
	}
	t.Setenv("MTM_DAEMON_PATHS", "mtm")
	if info := daemonStatusRow(t, data); info.Paths != "mtm" || info.Socket != sock("mtm") {
		t.Fatalf("MTM_DAEMON_PATHS=mtm: paths %q socket %s", info.Paths, info.Socket)
	}
}
