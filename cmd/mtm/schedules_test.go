package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/panesignals"
	"github.com/unixsuperhero/matomeru/internal/schedules"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// fakeRuns stands in for every command a schedule runs.
type fakeRuns struct {
	mu   sync.Mutex
	argv [][]string
	env  [][]string
	err  error
	out  string
	// checks counts the auto checks (gh auth status --active), which always pass.
	checks int
}

func (f *fakeRuns) exec(_ context.Context, argv, env []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if argv[0] == "gh" { // the auto check passes; only commands are kept
		f.checks++
		return nil, nil
	}
	f.argv, f.env = append(f.argv, argv), append(f.env, env)
	return []byte(f.out), f.err
}

func (f *fakeRuns) runs() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.argv) }

// scheduleApp is the seeded app with a fake clock, fake commands and every
// command "installed" but o-slack.
func scheduleApp(t *testing.T) (*App, *fakeRuns, *time.Time) {
	t.Helper()
	a := open(t)
	a.loc = time.UTC
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	f := &fakeRuns{out: "0 signals"}
	r := a.sched.runner
	r.Now = func() time.Time { return now }
	r.Exec = f.exec
	r.LookPath = func(name string) (string, error) {
		if name == "o-slack" {
			return "", errors.New("not found")
		}
		return "/bin/" + name, nil
	}
	return a, f, &now
}

type scheduleAnswerJSON struct {
	Name        string             `json:"name"`
	Enabled     string             `json:"enabled"`
	Active      bool               `json:"active"`
	Interval    string             `json:"interval"`
	LastOutcome string             `json:"last_outcome"`
	LastError   string             `json:"last_error"`
	Failures    int                `json:"failures"`
	Runs        []schedules.Record `json:"runs"`
}

// The defaults ship off but gh-signals, which is auto; each is two
// settings; the verbs write them and the rows follow.
func TestScheduleSettingsAndVerbs(t *testing.T) {
	a, _, _ := scheduleApp(t)
	s := a.r.Settings()
	if got := s.String("schedule.gh-signals.enabled"); got != schedules.Auto {
		t.Fatalf("gh-signals enabled %q", got)
	}
	if got := s.String("schedule.slack-signals.enabled"); got != schedules.Off {
		t.Fatalf("slack-signals enabled %q", got)
	}
	if got := s.Duration("schedule.gh-signals.interval"); got != 5*time.Minute {
		t.Fatalf("interval %s", got)
	}
	var v scheduleAnswerJSON
	cliJSON(t, a, &v, "schedule", "enable", "slack-signals")
	if v.Enabled != schedules.On || !v.Active || s.String("schedule.slack-signals.enabled") != schedules.On {
		t.Fatalf("enable: %+v", v)
	}
	cliJSON(t, a, &v, "schedule", "disable", "slack-signals")
	if v.Enabled != schedules.Off || v.Active {
		t.Fatalf("disable: %+v", v)
	}
	cliJSON(t, a, &v, "schedule", "set-interval", "gh-signals", "--interval", "10m")
	if v.Interval != "10m" || s.Duration("schedule.gh-signals.interval") != 10*time.Minute {
		t.Fatalf("set-interval: %+v", v)
	}
	if _, _, err := cli(a, "", "schedule", "set-interval", "gh-signals", "--interval", "10s"); err == nil {
		t.Fatal("an interval under a minute was taken")
	}
	// One setting flips a schedule, from the settings verbs too.
	mustCLI(t, a, "settings", "set", "schedule.gh-signals.enabled", "off")
	if d, _ := a.scheduleDef("gh-signals"); d.Enabled != schedules.Off {
		t.Fatalf("settings set: %+v", d)
	}
	cliJSON(t, a, &v, "schedule", "reset", "gh-signals")
	if v.Enabled != schedules.Auto || v.Interval != "5m" {
		t.Fatalf("reset: %+v", v)
	}
	if _, _, err := cli(a, "", "schedule", "show", "nope"); err == nil || !strings.Contains(err.Error(), "known: gh-signals, slack-signals") {
		t.Fatalf("unknown schedule: %v", err)
	}
	// The pane signal kinds are settings, on by default.
	for _, kind := range panesignals.Kinds {
		if !s.Bool("inbox.signal." + kind) {
			t.Fatalf("inbox.signal.%s is off", kind)
		}
	}
}

// run-now runs the command (with {mtm} and {mtm-args} filled), records
// it, and history and show answer the runs; a command that is not
// installed is recorded as missing, not as an error.
func TestScheduleRunNowAndHistory(t *testing.T) {
	a, f, now := scheduleApp(t)
	var rec schedules.Record
	cliJSON(t, a, &rec, "schedule", "run-now", "gh-signals")
	if rec.Outcome != schedules.OutcomeOK || !rec.Manual || rec.Output != "0 signals" {
		t.Fatalf("run-now: %+v", rec)
	}
	if got := strings.Join(f.argv[0], " "); got != "o-gh signals" {
		t.Fatalf("argv %q", got)
	}
	env := strings.Join(f.env[0], " ")
	if !strings.Contains(env, "O_GH_MTM_ARGS=--data "+a.dataPath) || !strings.Contains(env, "O_GH_MTM=/") {
		t.Fatalf("env %q", env)
	}
	*now = now.Add(time.Minute)
	cliJSON(t, a, &rec, "schedule", "run-now", "slack-signals")
	if rec.Outcome != schedules.OutcomeMissing || rec.Error != "o-slack not on PATH" || f.runs() != 1 {
		t.Fatalf("missing: %+v", rec)
	}
	var runs []schedules.Record
	cliJSON(t, a, &runs, "schedule", "history", "gh-signals")
	if len(runs) != 1 || runs[0].Outcome != schedules.OutcomeOK {
		t.Fatalf("history %+v", runs)
	}
	var v scheduleAnswerJSON
	cliJSON(t, a, &v, "schedule", "show", "slack-signals")
	if v.LastOutcome != schedules.OutcomeMissing || v.Failures != 1 || len(v.Runs) != 1 {
		t.Fatalf("show %+v", v)
	}
	var rows []struct {
		ID    string   `json:"id"`
		Cells []string `json:"cells"`
	}
	cliJSON(t, a, &rows, "schedule", "list")
	if len(rows) != 2 || rows[0].ID != "schedule:gh-signals" {
		t.Fatalf("list %+v", rows)
	}
}

// A schedules.json beside the config adds a schedule (and its settings).
func TestSchedulesFileAddsRows(t *testing.T) {
	path := seeded(t)
	file := `[{"name": "todo-sync", "command": ["o-todo", "sync"], "interval": "1h"}, {"name": "gh-signals", "interval": "15m"}]`
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), scheduleFile), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	a := openAt(t, path)
	if d, ok := a.scheduleDef("todo-sync"); !ok || d.Enabled != schedules.On || d.Every() != time.Hour {
		t.Fatalf("todo-sync %+v", d)
	}
	if d, _ := a.scheduleDef("gh-signals"); d.Every() != 15*time.Minute || a.r.Settings().Duration("schedule.gh-signals.interval") != 15*time.Minute {
		t.Fatalf("gh-signals %+v", d)
	}
	// A broken file runs the built-ins and says why on the page.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), scheduleFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	if len(b.sched.defs) != 2 || b.sched.loadErr == "" {
		t.Fatalf("broken file: %d defs, %q", len(b.sched.defs), b.sched.loadErr)
	}
}

// The TUI runs them: a timer tick starts what is due, off the loop; the
// finished run rebuilds the row; a schedule that starts failing says so
// once as a notice (never a modal), and a missing command says nothing.
func TestTUIRunsDueSchedules(t *testing.T) {
	a, f, now := scheduleApp(t)
	mustCLI(t, a, "schedule", "enable", "slack-signals")
	a.startSchedules()
	tick := func() []string {
		t.Helper()
		cmd := a.schedulesUpdate(scheduleDue{})
		if cmd == nil {
			t.Fatal("tick did not check")
		}
		ticked := cmd().(scheduleTicked)
		a.Update(ticked)
		a.sched.runner.Wait()
		for range ticked.started {
			a.Update(scheduleRan{<-a.sched.runner.Done})
		}
		return ticked.started
	}
	// gh-signals is auto: its check runs first, then the command.
	if got := tick(); len(got) != 2 || f.runs() != 1 || f.checks != 1 || strings.Join(f.argv[0], " ") != "o-gh signals" {
		t.Fatalf("first tick started %v, ran %v after %d checks", got, f.argv, f.checks)
	}
	if a.r.TopModal() != "" || len(a.noticeSink.shown) != 0 {
		t.Fatalf("missing command spoke: modal %q notices %d", a.r.TopModal(), len(a.noticeSink.shown))
	}
	press(t, a, "I", "S")
	if a.r.Page != schedulesPage {
		t.Fatalf("S opened %s", a.r.Page)
	}
	frame := frameAt(a, 140)
	for _, want := range []string{"gh-signals", "auto (on)", "✓ ok now", "slack-signals", "✗ missing now", "o-gh signals"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("schedules page lacks %q:\n%s", want, frame)
		}
	}
	t.Logf("schedules, 140 columns:\n%s", frame)
	if got := tick(); len(got) != 0 {
		t.Fatalf("early tick started %v", got)
	}
	f.mu.Lock()
	f.err = errors.New("exit status 1")
	f.mu.Unlock()
	*now = now.Add(5 * time.Minute)
	tick()
	if len(a.noticeSink.shown) != 1 || a.r.TopModal() != "" {
		t.Fatalf("first failure: notices %d modal %q", len(a.noticeSink.shown), a.r.TopModal())
	}
	*now = now.Add(10 * time.Minute)
	if got := tick(); !slices.Contains(got, "gh-signals") || len(a.noticeSink.shown) != 1 {
		t.Fatalf("second failure: started %v, notices %d", got, len(a.noticeSink.shown))
	}
	if !strings.Contains(frameAt(a, 140), "✗ failed now ×2") {
		t.Fatalf("row lacks the failure count:\n%s", frameAt(a, 140))
	}
}

// On the page: r runs the selected schedule now, e turns it off and on,
// i asks for an interval.
func TestSchedulePageKeys(t *testing.T) {
	a, f, _ := scheduleApp(t)
	press(t, a, "I", "S")
	if sel, _ := a.sched.list.Selected(); sel.ID != "schedule:gh-signals" {
		t.Fatalf("first row %s", sel.ID)
	}
	settle(a, key(t, a, "r"))
	if f.runs() != 1 {
		t.Fatalf("r ran %d", f.runs())
	}
	if !strings.Contains(frameAt(a, 140), "✓ ok now") {
		t.Fatalf("r's run not on the row:\n%s", frameAt(a, 140))
	}
	press(t, a, "j", "e")
	if got := a.r.Settings().String("schedule.slack-signals.enabled"); got != schedules.On {
		t.Fatalf("e on an off schedule: %q", got)
	}
	press(t, a, "e")
	if got := a.r.Settings().String("schedule.slack-signals.enabled"); got != schedules.Off {
		t.Fatalf("e on an on schedule: %q", got)
	}
	press(t, a, "i")
	if a.r.TopModal() == "" {
		t.Fatal("i did not ask")
	}
	press(t, a, "enter")
	if got := a.r.Settings().Duration("schedule.slack-signals.interval"); got != time.Minute {
		t.Fatalf("i enter: %s", got)
	}
}

// agentPane is an agent's pane in state at stateAt.
func agentPane(window, state string, stateAt int64) pane {
	return pane{Session: "work", Harness: "claude-code", StateAt: stateAt, Pane: daemon.Pane{ID: window, Name: window, AgentState: state}}
}

func mtmItems(t *testing.T, a *App) []actionitems.Item {
	t.Helper()
	items, err := a.inbox.items.All()
	if err != nil {
		t.Fatal(err)
	}
	var out []actionitems.Item
	for _, it := range items {
		if it.Source == panesignals.Source {
			out = append(out, it)
		}
	}
	return out
}

// Pane state reaches the inbox while the TUI runs: each kind once per
// episode, owned by the pane's task; a needs-input item is archived when
// the pane stops waiting, a done item is read once the pane is attached;
// a kind turned off raises nothing. A CLI listing raises nothing.
func TestPaneSignalsReachTheInbox(t *testing.T) {
	a, _, _ := scheduleApp(t)
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "work", ProjectID: "matomeru", TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	panes := []pane{agentPane("w1", "needs_input", 100), agentPane("w2", "done", 200), agentPane("w3", "errored", 300), agentPane("w4", "working", 400)}
	a.setPanes(panes, nil)
	if got := mtmItems(t, a); len(got) != 0 {
		t.Fatalf("raised before the TUI runs: %+v", got)
	}
	a.startSchedules()
	a.setPanes(panes, nil)
	items := mtmItems(t, a)
	if len(items) != 3 {
		t.Fatalf("items %+v", items)
	}
	byKind := map[string]actionitems.Item{}
	for _, it := range items {
		byKind[it.Kind] = it
	}
	ni := byKind[panesignals.NeedsInput]
	if ni.Key != "needs-input:w1:100" || ni.Why != "waits for your input" || ni.TaskID != "t1" || ni.ProjectID != "matomeru" || ni.Window != "w1" || ni.Read() {
		t.Fatalf("needs-input %+v", ni)
	}
	if byKind[panesignals.Done].ProjectID != "" || byKind[panesignals.Errored].Why != "agent errored" {
		t.Fatalf("done/errored %+v", byKind)
	}
	// The same listing again: nothing new.
	a.signals.sig = "" // force a reconcile
	a.setPanes(panes, nil)
	if got := mtmItems(t, a); len(got) != 3 {
		t.Fatalf("repeat listing: %d items", len(got))
	}
	// w1 answered: its item is archived. w2 is attached: its done item is read.
	panes[0] = agentPane("w1", "working", 500)
	a.attached = panes[1]
	a.setPanes(panes, nil)
	if it, _ := a.inbox.items.Get(ni.ID); it.State(a.inbox.items.Now()) != actionitems.StateArchived {
		t.Fatalf("needs-input not archived: %+v", it)
	}
	if it, _ := a.inbox.items.Get(byKind[panesignals.Done].ID); !it.Read() || it.State(a.inbox.items.Now()) != actionitems.StateOpen {
		t.Fatalf("done item: %+v", it)
	}
	a.attached = pane{}
	// Turned off, a new needs-input raises nothing.
	mustCLI(t, a, "settings", "set", "inbox.signal.needs-input", "false")
	panes[0] = agentPane("w1", "needs_input", 600)
	a.setPanes(panes, nil)
	if got := mtmItems(t, a); len(got) != 3 {
		t.Fatalf("off kind raised: %d items", len(got))
	}
	mustCLI(t, a, "settings", "set", "inbox.signal.needs-input", "true")
	a.setPanes(panes, nil)
	if got := mtmItems(t, a); len(got) != 4 {
		t.Fatalf("on again: %d items", len(got))
	}
}

// Two TUIs open on one database run each occurrence once: the claim is
// in the store, not in either process.
func TestTwoTUIsRunEachScheduleOnce(t *testing.T) {
	path := seeded(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	f := &fakeRuns{}
	var apps []*App
	for range 2 {
		a := openAt(t, path)
		r := a.sched.runner
		r.Now, r.Exec = func() time.Time { return now }, f.exec
		r.LookPath = func(name string) (string, error) { return "/bin/" + name, nil }
		a.startSchedules()
		apps = append(apps, a)
	}
	mustCLI(t, apps[0], "schedule", "enable", "gh-signals")
	apps[1].r.Settings().Reload()
	var wg sync.WaitGroup
	started := make([][]string, 2)
	for i, a := range apps {
		cmd := a.schedulesUpdate(scheduleDue{})
		wg.Add(1)
		go func() { defer wg.Done(); started[i] = cmd().(scheduleTicked).started }()
	}
	wg.Wait()
	for _, a := range apps {
		a.sched.runner.Wait()
	}
	if n := len(started[0]) + len(started[1]); n != 1 || f.runs() != 1 {
		t.Fatalf("started %v, ran %d", started, f.runs())
	}
}
