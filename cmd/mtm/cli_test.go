package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// cli runs one CLI command against the app with stdin, returning stdout,
// stderr and the error the process would exit with.
func cli(a *App, stdin string, args ...string) (string, string, error) {
	var out, errOut bytes.Buffer
	err := a.shell(strings.NewReader(stdin), &out, &errOut).Run(context.Background(), args)
	return out.String(), errOut.String(), err
}

// mustCLI is cli that fails the test on an error.
func mustCLI(t *testing.T, a *App, args ...string) string {
	t.Helper()
	out, errOut, err := cli(a, "", args...)
	if err != nil {
		t.Fatalf("mtm %s: %v\n%s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// cliApp is the seeded app with a fake daemon behind launches.
func cliApp(t *testing.T) (*App, *fakeDaemon) {
	t.Helper()
	app := open(t)
	f := &fakeDaemon{}
	app.launcher = f
	return app, f
}

func TestCLITaskLaunchByTargetStartsTheJobAndRecordsAReview(t *testing.T) {
	app, f := cliApp(t)
	if app.r.Instances["t2"] != nil {
		t.Fatal("t2 is a row before the command; the test would not prove the reveal")
	}
	out, errOut, err := cli(app, "", "task", "launch", "--target", "t2", "--model", "sonnet", "--yes")
	if err != nil {
		t.Fatalf("launch: %v\n%s", err, errOut)
	}
	if len(f.launches) != 1 {
		t.Fatalf("launches = %+v", f.launches)
	}
	got := f.launches[0]
	if got.Session != "matomeru" || got.Prompt != "lift the store" || strings.Join(got.Command, " ") != "claude --model sonnet" {
		t.Fatalf("launch = %+v", got)
	}
	if !strings.Contains(errOut, "job #1 agent.start claude done") {
		t.Fatalf("stderr lacks the finished job: %q", errOut)
	}
	// The shell prints the settled job's value: the pane it landed.
	var landed struct {
		Pane struct {
			Window string `json:"window"`
		} `json:"Pane"`
	}
	if err := json.Unmarshal([]byte(out), &landed); err != nil || landed.Pane.Window == "" {
		t.Fatalf("stdout = %q (%v)", out, err)
	}
	reviews, err := app.work.Reviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 || reviews[0].TaskID != "t2" || reviews[0].ProjectID != "matomeru" || reviews[0].Status != work.ReviewPending {
		t.Fatalf("reviews = %+v", reviews)
	}
}

func TestCLIConfirmReadsTheAnswerFromStdin(t *testing.T) {
	app, _ := cliApp(t)
	note, err := app.work.AddNote("matomeru", "", "scratch")
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, err := cli(app, "n\n", "note", "delete", "--target", note.ID)
	if err == nil || !strings.Contains(err.Error(), "declined: Delete note scratch") {
		t.Fatalf("declined delete: %v", err)
	}
	if !strings.Contains(errOut, "Delete note scratch? [y/N]") {
		t.Fatalf("stderr lacks the prompt: %q", errOut)
	}
	if _, err := app.work.Note(note.ID); err != nil || app.r.TopModal() != "" {
		t.Fatalf("note %v, modal = %q", err, app.r.TopModal())
	}
	// End of input declines too, so a script never hangs or deletes.
	if _, _, err := cli(app, "", "note", "delete", "--target", note.ID); err == nil {
		t.Fatal("delete on empty stdin")
	}
	if _, err := app.work.Note(note.ID); err != nil {
		t.Fatalf("empty stdin deleted: %v", err)
	}
	if _, errOut, err := cli(app, "y\n", "note", "delete", "--target", note.ID); err != nil {
		t.Fatalf("accepted delete: %v\n%s", err, errOut)
	}
	if _, err := app.work.Note(note.ID); err == nil {
		t.Fatal("y kept the note")
	}
}

func TestCLIHistoryListShowsTheOperationsThisProcessRan(t *testing.T) {
	app, _ := cliApp(t)
	mustCLI(t, app, "task", "edit", "--target", "t1", "--title", "rewrite the brief")
	if got := stored(t, app, "t1").Title; got != "rewrite the brief" {
		t.Fatalf("title = %q", got)
	}
	var entries []ochazuke.HistoryEntry
	if err := json.Unmarshal([]byte(mustCLI(t, app, "history", "list")), &entries); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.Request.Key() == "task.edit" && e.Request.Target == "t1" && e.Context.Source == "cli" && e.Result.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("history lacks the CLI task.edit: %+v", entries)
	}
}

func TestCLIBindingsListShowsEveryAppKeyOncePerStop(t *testing.T) {
	app, _ := cliApp(t)
	mustCLI(t, app, "task", "launch", "--target", "t1", "--yes")
	reviews, err := app.work.Reviews()
	if err != nil || len(reviews) != 1 {
		t.Fatalf("reviews = %+v, %v", reviews, err)
	}
	stops := map[string][]string{
		"matomeru":         {"enter project.open", "l project.open", "L project.launch"},
		"t1":               {"enter task.open", "L task.launch", "i task.edit", "x task.toggle", "s task.status", "n task.new"},
		"pane:matomeru/w1": {"enter pane.attach", "d d pane.kill", "r pane.rename", "m pane.associate", "n pane.new", "] panes.tab"},
		paneListID:         {"n pane.new", "e daemon.ensure"},
		reviews[0].ID:      {"enter review.open", "x review.accept", "s review.status", "i review.saveAnswer"},
	}
	fkey := regexp.MustCompile(`(^|[ +])f\d+$`)
	for target, want := range stops {
		var rows []ochazuke.Binding
		if err := json.Unmarshal([]byte(mustCLI(t, app, "bindings", "list", "--target", target)), &rows); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		listed := map[string]bool{}
		for _, b := range rows {
			id := b.Mode + "\x00" + b.Keys + "\x00" + b.Pattern
			if seen[id] {
				t.Errorf("%s: %q listed twice", target, b.Keys)
			}
			seen[id] = true
			for _, k := range strings.Fields(b.Keys) {
				if fkey.MatchString(k) {
					t.Errorf("%s: function key %q", target, b.Keys)
				}
			}
			listed[b.Keys+" "+b.Request.Key()] = true
		}
		for _, w := range want {
			if !listed[w] {
				t.Errorf("%s: bindings list lacks %q", target, w)
			}
		}
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestCLIErrorsExitNonZero(t *testing.T) {
	app, _ := cliApp(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"nope", "list"}, `unknown operation "nope.list"`},
		{[]string{"task", "fly"}, `unknown operation "task.fly"`},
		{[]string{"task", "launch", "--target", "t99"}, `unknown target "t99"`},
		{[]string{"task", "launch", "--target", "t1", "--colour", "red"}, `task.launch has no parameter "colour"`},
		{[]string{"task", "launch", "--model", "gpt"}, `model "gpt" is not one of claude's models: opus, sonnet, haiku`},
		{[]string{"task", "launch", "--harness", "nope"}, "harness must be one of claude, omp, codex"},
		{[]string{"task"}, "usage: mtm"},
	} {
		if _, _, err := cli(app, "", c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("mtm %s: err = %v, want %q", strings.Join(c.args, " "), err, c.want)
		}
	}
}

// TestCLIThroughRun is the --smoke of the CLI: the process entry with
// global flags, a database file and an unreachable daemon.
func TestCLIThroughRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	var out, errOut bytes.Buffer
	args := []string{"--data", path, "--socket", filepath.Join(t.TempDir(), "none.sock")}
	if err := run(append(args, "bindings", "list"), strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("bindings list: %v\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), `"keys":"ctrl+j"`) {
		t.Fatalf("bindings list:\n%s", out.String())
	}
	out.Reset()
	if err := run(append(args, "history", "list"), strings.NewReader(""), &out, &errOut); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("history list in a fresh process: %q, %v", out.String(), err)
	}
	if err := run(append(args, "nope", "list"), strings.NewReader(""), &out, &errOut); err == nil {
		t.Fatal("an unknown operation exited zero")
	}
	out.Reset()
	if err := run(append(args, "--smoke"), strings.NewReader(""), &out, &errOut); err != nil || !strings.Contains(out.String(), "› Panes  Work") || !strings.Contains(out.String(), "panes (0)") {
		t.Fatalf("smoke: %v\n%s", err, out.String())
	}
}

func TestCLIPanesRefreshTargetsTheOnlyPaneList(t *testing.T) {
	app, _ := cliApp(t)
	f := app.panesd.(*fakePanes)
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	f.panes = append(f.panes, pane{Session: "matomeru", Pane: daemon.Pane{ID: "w3", Name: "late"}})
	_, errOut, err := cli(app, "", "panes", "refresh")
	if err != nil {
		t.Fatalf("panes refresh: %v\n%s", err, errOut)
	}
	// panes.refresh is quiet: its job prints no "job #N" line unless it
	// fails (ochazuke v0.2.0).
	if strings.Contains(errOut, "job #") || len(app.panes) != 3 || app.r.Instances["pane:matomeru/w3"] == nil {
		t.Fatalf("stderr %q, panes %+v", errOut, app.panes)
	}
}

func TestCLIAVerbOfManyRowsNamesThemWithoutATarget(t *testing.T) {
	app, _ := cliApp(t)
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	_, _, err := cli(app, "", "pane", "preview")
	if err == nil || !strings.Contains(err.Error(), "pass --target, one of: pane:matomeru/w1, pane:ochazuke/w2") {
		t.Fatalf("pane preview without a target: %v", err)
	}
	// With the target it runs.
	mustCLI(t, app, "pane", "preview", "--target", "pane:ochazuke/w2")
}

// Test pass R12: a bad status names the statuses and no --target rows.
func TestCLITaskStatusBadValueNamesTheStatuses(t *testing.T) {
	app, _ := cliApp(t)
	executeA(t, app, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	mustCLI(t, app, "project", "open", "--target", "matomeru")
	_, _, err := cli(app, "", "task", "status", "t1", "bogus")
	if err == nil || err.Error() != "status must be one of todo, active, blocked, done" {
		t.Fatalf("bad status: %v", err)
	}
}
