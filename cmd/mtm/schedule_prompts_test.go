package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/schedules"
)

// schedPromptApp is scheduleApp with the fake launcher over the fake panes.
func schedPromptApp(t *testing.T) (*App, *fakeDaemon, *fakePanes, *time.Time) {
	t.Helper()
	a, _, now := scheduleApp(t)
	panes := a.panesd.(*fakePanes)
	f := &fakeDaemon{panes: panes}
	a.launcher = f
	return a, f, panes, now
}

type promptViewJSON struct {
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Source  string          `json:"source"`
	Enabled string          `json:"enabled"`
	When    string          `json:"when"`
	Prompt  string          `json:"prompt"`
	Agent   schedules.Agent `json:"agent"`
}

// A prompt schedule's agent configuration reaches the launch: the
// harness and model in the argv, a setting with a launch flag as a flag,
// the others typed before the prompt; the project's session, the task
// linked; the run's record links the pane.
func TestPromptScheduleLaunchesWithItsAgentConfig(t *testing.T) {
	a, f, _, _ := schedPromptApp(t)
	var v promptViewJSON
	cliJSON(t, a, &v, "schedule", "add", "--name", "ask", "--prompt", "summarise the inbox", "--when", "mon,fri 9:00",
		"--harness", "claude", "--model", "sonnet", "--effort", "high", "--fast", "on", "--permission-mode", "plan",
		"--project", "matomeru", "--task", "t1")
	if v.Kind != schedules.KindPrompt || v.Source != schedules.SourceStored || v.Enabled != schedules.On || v.When != "mon,fri 09:00" ||
		v.Agent.Model != "sonnet" || v.Agent.Permission != "plan" {
		t.Fatalf("added %+v", v)
	}
	var rec schedules.Record
	cliJSON(t, a, &rec, "schedule", "run-now", "ask")
	if rec.Outcome != schedules.OutcomeOK || rec.Launched == nil {
		t.Fatalf("run %+v", rec)
	}
	if len(f.launches) != 1 {
		t.Fatalf("%d launches", len(f.launches))
	}
	w := f.launches[0]
	argv := strings.Join(w.Command, " ")
	if !strings.Contains(argv, "claude --model sonnet --permission-mode plan") || w.Session != "matomeru" || w.Name != "ask" {
		t.Fatalf("launched %q in %s as %s", argv, w.Session, w.Name)
	}
	want := []string{"matomeru/" + w.ID + ": /effort high", "matomeru/" + w.ID + ": /fast on", "matomeru/" + w.ID + ": summarise the inbox"}
	if !slices.Equal(f.sends, want) {
		t.Fatalf("typed %q, want %q", f.sends, want)
	}
	l := rec.Launched
	if l.Target != schedules.TargetNew || l.Pane != "pane:matomeru/"+w.ID || l.Task != "t1" || l.Project != "matomeru" || l.Prompt != "sent" || l.Model != "sonnet" {
		t.Fatalf("launched record %+v", l)
	}
	if o, err := a.work.PaneOwner(w.ID, ""); err != nil || o.TaskID != "t1" || o.ProjectID != "matomeru" {
		t.Fatalf("pane owner %+v %v", o, err)
	}
	var runs []schedules.Record
	cliJSON(t, a, &runs, "schedule", "history", "ask")
	if len(runs) != 1 || runs[0].Launched == nil || runs[0].Window != w.ID {
		t.Fatalf("history %+v", runs)
	}
}

// Target pane sends to the open pane of that name through the delivery
// queue, which waits for the agent to rest and never types over a
// question: nothing is submitted, even while the pane waits on its
// person. With no such pane, a run starts one by that name.
func TestPromptScheduleQueuesInANamedPane(t *testing.T) {
	a, f, panes, _ := schedPromptApp(t)
	panes.panes = append(panes.panes, pane{Session: "no-project", Pane: daemon.Pane{ID: "w7", Name: "reviewer", AgentState: "needs_input"}})
	mustCLI(t, a, "schedule", "add", "--name", "review", "--prompt", "check the PRs", "--into", "pane", "--pane", "reviewer", "--effort", "high")
	var rec schedules.Record
	cliJSON(t, a, &rec, "schedule", "run-now", "review")
	if want := []string{"queue w7 /effort high", "queue w7 check the PRs"}; !slices.Equal(panes.prompts, want) {
		t.Fatalf("prompts %q, want %q", panes.prompts, want)
	}
	if len(f.launches) != 0 || len(f.sends) != 0 {
		t.Fatalf("typed into a waiting pane: launches %d sends %q", len(f.launches), f.sends)
	}
	if rec.Outcome != schedules.OutcomeOK || rec.Launched.Target != "queued" || rec.Launched.Window != "w7" || rec.Launched.Prompt != "queued" {
		t.Fatalf("run %+v", rec.Launched)
	}
	// The pane is gone: the next run opens one named reviewer.
	panes.panes = nil
	panes.prompts = nil
	cliJSON(t, a, &rec, "schedule", "run-now", "review")
	if len(f.launches) != 1 || f.launches[0].Name != "reviewer" || len(panes.prompts) != 0 || !strings.Contains(rec.Launched.Note, "no open pane named reviewer") {
		t.Fatalf("fallback: launches %d prompts %q note %q", len(f.launches), panes.prompts, rec.Launched.Note)
	}
}

// add, edit, duplicate, enable, disable and delete write stored rows;
// a built-in edits into a stored copy and never deletes.
func TestScheduleRowVerbs(t *testing.T) {
	a, _, _, _ := schedPromptApp(t)
	var v promptViewJSON
	cliJSON(t, a, &v, "schedule", "add", "--prompt", "Tidy the notes, please")
	if v.Name != "tidy-the-notes-please" || v.Enabled != schedules.Off || v.When != "never" {
		t.Fatalf("add without a time %+v", v)
	}
	cliJSON(t, a, &v, "schedule", "edit", v.Name, "--when", "cron 0 9 * * 1-5", "--harness", "codex")
	if v.When != "cron 0 9 * * 1-5" || v.Agent.Harness != "codex" || v.Prompt != "Tidy the notes, please" {
		t.Fatalf("edit %+v", v)
	}
	if _, _, err := cli(a, "", "schedule", "edit", v.Name, "--when", "fooday"); err == nil {
		t.Fatal("a bad time was taken")
	}
	cliJSON(t, a, &v, "schedule", "enable", "tidy-the-notes-please")
	if v.Enabled != schedules.On {
		t.Fatalf("enable %+v", v)
	}
	cliJSON(t, a, &v, "schedule", "duplicate", "tidy-the-notes-please")
	if v.Name != "tidy-the-notes-please-copy" || v.Enabled != schedules.Off || v.When != "cron 0 9 * * 1-5" {
		t.Fatalf("duplicate %+v", v)
	}
	if _, _, err := cli(a, "", "schedule", "add", "--name", "tidy-the-notes-please", "--prompt", "x"); err == nil {
		t.Fatal("a taken name was added")
	}
	mustCLI(t, a, "schedule", "delete", "tidy-the-notes-please-copy", "--yes")
	if _, ok := a.scheduleDef("tidy-the-notes-please-copy"); ok {
		t.Fatal("delete left the row")
	}
	// A built-in: edit stores a copy (its source says so), delete refuses
	// and says to disable it; deleting the copy brings the built-in back.
	cliJSON(t, a, &v, "schedule", "edit", "gh-signals", "--when", "every 15m")
	if v.Source != schedules.SourceStored || v.When != "every 15m" {
		t.Fatalf("edit built-in %+v", v)
	}
	mustCLI(t, a, "schedule", "delete", "gh-signals", "--yes")
	if d, _ := a.scheduleDef("gh-signals"); d.Source != schedules.SourceBuiltin || d.Every() != 5*time.Minute {
		t.Fatalf("built-in not back: %+v", d)
	}
	if _, _, err := cli(a, "", "schedule", "delete", "gh-signals", "--yes"); err == nil || !strings.Contains(err.Error(), "schedule disable gh-signals") {
		t.Fatalf("deleted a built-in: %v", err)
	}
	out := mustCLI(t, a, "schedule", "list")
	if !strings.Contains(out, "tidy-the-notes-please") || !strings.Contains(out, "codex: Tidy the notes, please") {
		t.Fatalf("list:\n%s", out)
	}
}

// In the TUI a day-and-time prompt schedule fires at its time and opens
// nothing (prompt.after-submit is the prompt box's, not a schedule's); a
// disabled one never fires; add (n) with no fields records an off row and
// opens its form.
func TestTUIPromptSchedules(t *testing.T) {
	a, f, _, now := schedPromptApp(t)
	*now = time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC) // a Wednesday
	mustCLI(t, a, "schedule", "disable", "gh-signals")
	mustCLI(t, a, "schedule", "add", "--name", "standup", "--prompt", "what changed overnight?", "--when", "wed 09:00")
	mustCLI(t, a, "schedule", "add", "--name", "quiet", "--prompt", "never me", "--when", "every 1m", "--enabled", "off")
	a.startSchedules()
	tick := func() []string {
		t.Helper()
		ticked := a.schedulesUpdate(scheduleDue{})().(scheduleTicked)
		a.Update(ticked)
		a.sched.runner.Wait()
		for range ticked.started {
			_, cmd := a.Update(scheduleRan{<-a.sched.runner.Done})
			settle(a, cmd)
		}
		return ticked.started
	}
	if got := tick(); len(got) != 0 {
		t.Fatalf("08:00 started %v", got)
	}
	*now = now.Add(time.Hour + 5*time.Second)
	if got := tick(); !slices.Equal(got, []string{"standup"}) {
		t.Fatalf("09:00 started %v", got)
	}
	if len(f.launches) != 1 || f.launches[0].Name != "standup" || a.r.TopModal() != "" {
		t.Fatalf("launches %d modal %q", len(f.launches), a.r.TopModal())
	}
	for range 10 {
		*now = now.Add(time.Hour)
		tick()
	}
	if len(f.launches) != 1 {
		t.Fatalf("%d launches: the disabled row fired, or standup ran twice", len(f.launches))
	}
	a.cli = false // the CLI calls above set it
	press(t, a, "I", "S")
	press(t, a, "n")
	if a.r.Instances[scheduleFormID] == nil {
		t.Fatalf("add opened no form; modal %q", a.r.TopModal())
	}
	if d, ok := a.scheduleDef("prompt"); !ok || d.Enabled != schedules.Off || d.Source != schedules.SourceStored {
		t.Fatalf("add recorded %+v %v", d, ok)
	}
	// Typing in the form writes the row: the prompt field is first.
	press(t, a, "h", "i")
	_, cmd := a.Update(keyMsg("esc"))
	settle(a, cmd)
	if d, _ := a.scheduleDef("prompt"); d.Prompt != "hi" {
		t.Fatalf("the form wrote %+v", d)
	}
}
