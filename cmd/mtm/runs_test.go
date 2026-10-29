package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/schedules"
)

// command.run with drive exec (the default) runs the line as a plain
// process in a job: its lines stream into the run while it runs, the exit
// code is the outcome, and the Runs page draws the run with its output.
func TestCommandRunExecStreamsOutputAndRecordsTheExitCode(t *testing.T) {
	app := open(t)
	res, err := app.r.Execute(ochazuke.Request{Noun: commandNoun, Verb: "run", Target: runsListID,
		Args: ochazuke.Arguments{"command": "echo first; echo to-stderr >&2; sleep 0.4; echo second; exit 4", "dir": t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	run := app.runs.find("run:1")
	if run == nil {
		t.Fatal("no run recorded")
	}
	jobs := jobsOf(app, "command.run")
	if len(jobs) != 1 || jobs[0].Label != "echo first; echo to-stderr >&2; sleep 0.4; echo second; exit 4" || jobs[0].State != ochazuke.JobRunning {
		t.Fatalf("jobs %+v", jobs)
	}
	// The job's work runs off the loop: start it, and read the run while
	// it streams.
	done := make(chan struct{})
	go func() { settle(app, app.r.PendingCommands()); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := app.runs.snapshot(run)
		if len(s.Output) >= 2 && s.State == ochazuke.JobRunning {
			if strings.Join(s.Output, ",") != "first,to-stderr" && strings.Join(s.Output, ",") != "to-stderr,first" {
				t.Fatalf("streamed %q", s.Output)
			}
			break
		}
		if time.Now().After(deadline) || s.State != ochazuke.JobRunning {
			t.Fatalf("no streamed lines while running: %+v", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
	<-done
	s := app.runs.snapshot(run)
	if s.State != ochazuke.JobFailed || s.ExitCode != 4 || s.Output[len(s.Output)-1] != "second" || s.Error != "" || s.Drive != "exec" {
		t.Fatalf("finished run %+v", s)
	}
	if err := app.r.OpenPage(runsPage); err != nil {
		t.Fatal(err)
	}
	if err := app.refreshRuns(); err != nil {
		t.Fatal(err)
	}
	f := frame(app)
	for _, want := range []string{"Runs", "failed", "exit 4", "drive exec", "$ sh -c echo first", "second"} {
		if !strings.Contains(f, want) {
			t.Fatalf("Runs page lacks %q:\n%s", want, f)
		}
	}

	// A clean exit is done, exit 0; the CLI waits for the job and prints
	// the run.
	out := mustCLI(t, app, "command", "run", "printf 'one\\ntwo'")
	var got commandRun
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got.State != ochazuke.JobDone || got.ExitCode != 0 || strings.Join(got.Output, ",") != "one,two" {
		t.Fatalf("cli run %+v", got)
	}
}

// drive api is accepted as data but refused when used, everywhere a
// drive is chosen: command.run, a harness profile (and agent.start's
// override), and a schedule's command row.
func TestDriveAPIIsRefusedAsNotAvailableYet(t *testing.T) {
	app, f := harnessApp(t, `[{"name": "omp", "drive": "api"}]`)
	if _, err := app.r.Execute(ochazuke.Request{Noun: commandNoun, Verb: "run", Target: runsListID,
		Args: ochazuke.Arguments{"command": "echo x", "drive": "api"}}); err == nil || !strings.Contains(err.Error(), "drive api is not available yet") {
		t.Fatalf("command.run --drive api: %v", err)
	}
	for _, args := range []ochazuke.Arguments{
		{"session": "s", "harness": "omp"},
		{"session": "s", "harness": "claude", "drive": "api"},
	} {
		_, err := app.r.Execute(ochazuke.Request{Noun: "agent", Verb: "start", Target: "work", Args: args})
		if err == nil || !strings.Contains(err.Error(), "drive api is not available yet") {
			t.Fatalf("agent.start %v: %v", args, err)
		}
	}
	if len(f.launches) != 0 || len(app.runs.all()) != 0 {
		t.Fatalf("something ran: launches %+v runs %+v", f.launches, app.runs.all())
	}
	rec, err := app.sched.runner.RunNow(app.sched.ctx, schedules.Def{Name: "sig", Command: []string{"tool"}, Drive: "api", Interval: schedules.Duration(time.Minute), Enabled: schedules.On})
	if err != nil || rec.Outcome != schedules.OutcomeFailed || !strings.Contains(rec.Error, "drive api is not available yet") {
		t.Fatalf("schedule: %+v %v", rec, err)
	}
}

// drive terminal opens a pane running the command (the fake daemon's
// window gets the argv), for command.run and for a schedule's command row
// run now with the drive overridden for that run only.
func TestDriveTerminalOpensAPane(t *testing.T) {
	app, f := harnessApp(t, "")
	mustCLI(t, app, "command", "run", "htop", "--drive", "terminal", "--dir", "/tmp")
	if len(f.launches) != 1 || strings.Join(f.launches[0].Command, " ") != "sh -c htop" || f.launches[0].Cwd != "/tmp" || f.launches[0].Session != mtmOnlySession {
		t.Fatalf("launches %+v", f.launches)
	}
	if len(app.runs.all()) != 0 {
		t.Fatal("a terminal run is a pane, not an exec run")
	}
	name := schedules.Defaults()[0].Name
	out := mustCLI(t, app, "schedule", "run-now", name, "--drive", "terminal")
	var rec schedules.Record
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if rec.Outcome != schedules.OutcomeOK || rec.Drive != "terminal" || rec.Launched == nil || len(f.launches) != 2 || f.launches[1].Name != name {
		t.Fatalf("schedule run %+v launches %+v", rec, f.launches)
	}
	if d, _ := app.scheduleDef(name); d.Drive != "" {
		t.Fatalf("the override stuck on the row: %q", d.Drive)
	}
}

// A harness whose profile says drive exec (or a launch with --drive exec)
// runs its argv as an exec run: no pane, the prompt (the task's title) on
// its stdin, the output on the Runs page.
func TestHarnessDriveExecRunsWithoutAPane(t *testing.T) {
	app, f := harnessApp(t, `[{"name": "echoer", "command": ["cat"], "drive": "exec"}]`)
	home := t.TempDir()
	app.home = func() (string, error) { return home, nil }
	tasks, err := app.work.Tasks("")
	if err != nil || len(tasks) == 0 {
		t.Fatalf("tasks %v %v", tasks, err)
	}
	out := mustCLI(t, app, "agent", "start", "--session", "s", "--harness", "echoer", "--task", tasks[0].ID)
	var got commandRun
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(f.launches) != 0 || got.State != ochazuke.JobDone || strings.Join(got.Output, "\n") != tasks[0].Title || got.Title != "echoer (exec)" {
		t.Fatalf("run %+v launches %+v", got, f.launches)
	}
	// --drive terminal overrides the profile for one launch: a pane.
	mustCLI(t, app, "agent", "start", "--session", "s", "--harness", "echoer", "--drive", "terminal")
	if len(f.launches) != 1 || f.launches[0].Command[0] != "cat" {
		t.Fatalf("launches %+v", f.launches)
	}
}
