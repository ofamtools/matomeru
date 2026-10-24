package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/oden"
)

// bins writes executable scripts named name into a fresh PATH directory.
func bins(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// mtm runs one invocation of the oden shell over app with PATH dir.
func mtm(app *App, dir string, args ...string) (string, string, error) {
	var out, errOut bytes.Buffer
	tui := func() error { return nil }
	err := app.commands(args, strings.NewReader(""), &out, &errOut, tui, oden.WithPath(dir)).Run(context.Background())
	return out.String(), errOut.String(), err
}

func TestExternalMtmBinRunsWithItsExitCode(t *testing.T) {
	dir := bins(t, map[string]string{"mtm-web": `echo "web $* parent=$ODEN_PARENT"; exit 7`})
	t.Setenv("PATH", dir)
	var out, errOut bytes.Buffer
	args := []string{"--data", filepath.Join(t.TempDir(), "mtm.db"), "--socket", filepath.Join(t.TempDir(), "none.sock"), "web", "serve", "--port", "1"}
	err := run(args, strings.NewReader(""), &out, &errOut)
	if oden.ExitCode(err) != 7 || out.String() != "web serve --port 1 parent=mtm\n" {
		t.Fatalf("mtm web: exit %d (%v), out %q, err %q", oden.ExitCode(err), err, out.String(), errOut.String())
	}
}

func TestHelpListsNounsAndExternalBins(t *testing.T) {
	dir := bins(t, map[string]string{"mtm-web": "true"})
	t.Setenv("PATH", dir)
	data := filepath.Join(t.TempDir(), "mtm.db")
	for _, flag := range []string{"help", "--help"} {
		var out, errOut bytes.Buffer
		if err := run([]string{"--data", data, flag}, strings.NewReader(""), &out, &errOut); err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, errOut.String())
		}
		for _, want := range []string{"\n  task ", "\n  project ", "\n  which ", "(external)", filepath.Join(dir, "mtm-web")} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("mtm %s lacks %q:\n%s", flag, want, out.String())
			}
		}
	}
}

func TestNounPrefixResolves(t *testing.T) {
	app, _ := cliApp(t)
	if _, errOut, err := mtm(app, t.TempDir(), "ta", "launch", "--target", "t1", "--yes"); err != nil {
		t.Fatalf("mtm ta launch: %v\n%s", err, errOut)
	}
	reviews, err := app.work.Reviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 || reviews[0].TaskID != "t1" {
		t.Fatalf("reviews = %+v", reviews)
	}
}

func TestVerbPrefixResolves(t *testing.T) {
	app, _ := cliApp(t)
	out, errOut, err := mtm(app, t.TempDir(), "bind", "li")
	if err != nil || !strings.Contains(out, `"keys":"ctrl+j"`) {
		t.Fatalf("mtm bind li: %v\n%s%s", err, out, errOut)
	}
}

func TestAmbiguousPrefixListsCandidates(t *testing.T) {
	app, _ := cliApp(t)
	_, errOut, err := mtm(app, t.TempDir(), "pa", "list")
	if oden.ExitCode(err) != 1 || !strings.Contains(errOut, `"pa" is ambiguous`) || !strings.Contains(errOut, "panes") || !strings.Contains(errOut, "palette") {
		t.Fatalf("mtm pa: %v\n%s", err, errOut)
	}
}

func TestNounWinsOverSameNamedBin(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	dir := bins(t, map[string]string{"mtm-task": "touch " + marker})
	app, _ := cliApp(t)
	if _, errOut, err := mtm(app, dir, "task", "launch", "--target", "t1", "--yes"); err != nil {
		t.Fatalf("mtm task launch: %v\n%s", err, errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the mtm-task bin ran instead of the noun")
	}
	out, _, err := mtm(app, dir, "help")
	if err != nil || !strings.Contains(out, "shadows external "+filepath.Join(dir, "mtm-task")) {
		t.Fatalf("help: %v\n%s", err, out)
	}
	out, _, err = mtm(app, dir, "which", "task")
	if err != nil || out != "task inline noun\n" {
		t.Fatalf("which task: %v %q", err, out)
	}
}
