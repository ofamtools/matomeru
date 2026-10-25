package main

import (
	"bytes"
	"context"
	"io"
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
	if oden.ExitCode(err) != 1 || !strings.Contains(errOut, `"pa" is ambiguous`) || !strings.Contains(errOut, "panes") || !strings.Contains(errOut, "pane ") {
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

// mtm proj runs the family tool o-proj (WithPrefixes("o")) with
// ODEN_PARENT=mtm; an mtm-proj on the same PATH wins over it.
func TestFamilyToolOProjRunsAndMtmProjWins(t *testing.T) {
	app, _ := cliApp(t)
	dir := bins(t, map[string]string{"o-proj": `echo "o-proj $* parent=$ODEN_PARENT"`})
	out, errOut, err := mtm(app, dir, "proj", "check")
	if err != nil || out != "o-proj check parent=mtm\n" {
		t.Fatalf("mtm proj check: %v %q %q", err, out, errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, "mtm-proj"), []byte("#!/bin/sh\necho \"mtm-proj $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = mtm(app, dir, "proj", "check")
	if err != nil || out != "mtm-proj check\n" {
		t.Fatalf("mtm proj check with mtm-proj: %v %q %q", err, out, errOut)
	}
	out, _, err = mtm(app, dir, "help")
	if err != nil || !strings.Contains(out, "shadows external "+filepath.Join(dir, "o-proj")) {
		t.Fatalf("help: %v\n%s", err, out)
	}
}

// An unknown first word is reported by oden, not handed to the shell:
// tasks is not task, and suggestions never run.
func TestUnknownWordIsNotFoundWithSuggestions(t *testing.T) {
	app, _ := cliApp(t)
	out, errOut, err := mtm(app, t.TempDir(), "tasks")
	if oden.ExitCode(err) != 1 || out != "" ||
		!strings.HasPrefix(errOut, "mtm: command \"tasks\" not found\ndid you mean: task\n\nusage: mtm COMMAND") ||
		!strings.Contains(errOut, "\n  task ") || strings.Contains(errOut, "operations list") {
		t.Fatalf("mtm tasks: %v %q\n%s", err, out, errOut)
	}
	out, errOut, err = mtm(app, t.TempDir(), "task", "frob")
	if oden.ExitCode(err) != 1 || out != "" ||
		!strings.HasPrefix(errOut, "mtm task: subcommand \"frob\" not found\n") ||
		!strings.Contains(errOut, "\n  launch ") || !strings.Contains(errOut, "\n  list ") {
		t.Fatalf("mtm task frob: %v %q\n%s", err, out, errOut)
	}
}

func TestNoWordsRunsTheTUIAndNounVerbRuns(t *testing.T) {
	app, _ := cliApp(t)
	ran := false
	tui := func() error { ran = true; return nil }
	if err := app.commands(nil, strings.NewReader(""), io.Discard, io.Discard, tui, oden.WithPath(t.TempDir())).Run(context.Background()); err != nil || !ran {
		t.Fatalf("mtm: %v, tui ran %v", err, ran)
	}
	out, errOut, err := mtm(app, t.TempDir(), "task", "list")
	if err != nil || !strings.Contains(out, `"t1"`) {
		t.Fatalf("mtm task list: %v\n%s%s", err, out, errOut)
	}
}
