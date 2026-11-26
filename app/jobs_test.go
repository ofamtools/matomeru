package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/input"
)

func newJobsTestApp(t *testing.T, dbPath string, opts Options) (*App, *Driver) {
	t.Helper()
	store, err := data.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := New(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, NewDriver(a, 120, 36)
}

func jobsTestRecord(t *testing.T, a *App, kind string, fields map[string]any) core.Ref {
	t.Helper()
	rec, _, err := a.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, core.Actor{Kind: "system", ID: "seed"})
	if err != nil {
		t.Fatal(err)
	}
	return rec.Ref()
}

func TestJobsPageKeys(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "files")
	a, d := newJobsTestApp(t, filepath.Join(dir, "mtm.db"), Options{DataDir: dataDir})
	running := jobsTestRecord(t, a, data.KindRun, map[string]any{"title": "Alpha build", "runStatus": "running"})
	jobsTestRecord(t, a, data.KindRun, map[string]any{"title": "Beta checks", "runStatus": "done"})
	scheduled := jobsTestRecord(t, a, data.KindSchedule, map[string]any{"title": "Nightly checks", "cron": "0 0 * * *"})
	jobsTestRecord(t, a, data.KindOccurrence, map[string]any{"schedule": scheduled, "outcomeStatus": "done"})

	current := func(tab string) *ui.ViewNode {
		t.Helper()
		root := a.View(a.State())
		node := root.Find(views.JobsListID)
		if a.Space() != SpaceJobs || a.Focus() != views.JobsListID || node == nil || node.Facts["jobsTab"] != tab {
			t.Fatalf("space=%q focus=%q node=%+v, want Jobs %q", a.Space(), a.Focus(), node, tab)
		}
		return node
	}

	d.Keys("g", "j")
	all := current(views.JobsTabAll)
	if all.Filter != nil || all.Facts["activeCount"] != 2 || all.Facts["historyCount"] != 2 {
		t.Fatalf("all-tab projection has facts %+v and filter %+v", all.Facts, all.Filter)
	}
	if frame := d.Frame(); !strings.Contains(frame, "1. Actively Running") || !strings.Contains(frame, "2. History & Action Log") {
		t.Fatalf("missing stacked sections:\n%s", frame)
	}
	path := filepath.Join(dataDir, "action-log.jsonl")
	before := data.NewActionLogStore(path).List(1)
	if len(before) != 1 || before[0].Command != CmdJobsOpen {
		t.Fatalf("persisted actions = %+v, want Jobs open", before)
	}
	d.Keys("c")
	if current(views.JobsTabAll).Facts["actionCount"] != 1 {
		t.Fatal("c outside the Actions tab changed the log")
	}

	d.Keys("tab")
	active := current(views.JobsTabActive)
	if active.Facts["selectedID"] != data.RefText(running) || active.Filter == nil || active.Filter.Prefix != "jobs " {
		t.Fatalf("active selection/filter: %+v %+v", active.Facts, active.Filter)
	}
	d.Keys("j")
	if current(views.JobsTabActive).Facts["selectedID"] != data.RefText(scheduled) {
		t.Fatal("j did not select the scheduled job")
	}
	d.Keys("k")
	if current(views.JobsTabActive).Facts["selectedID"] != data.RefText(running) {
		t.Fatal("k did not return to the running job")
	}
	d.Keys("/")
	if a.Mode() != ModeFilter || !strings.Contains(d.Frame(), "jobs /") {
		t.Fatalf("Jobs filter did not attach to the filter bar. Mode %q:\n%s", a.Mode(), d.Frame())
	}
	d.Type("ALPHA")
	d.Keys("enter", "j", "y")
	if active := current(views.JobsTabActive); active.Facts["hasFilter"] != true || active.Facts["selectedID"] != data.RefText(running) {
		t.Fatalf("filtered selection = %+v", active.Facts)
	}
	if got := a.Register(core.RegClipboard); !strings.Contains(got, "Alpha build") || !strings.Contains(got, "running") {
		t.Fatalf("clipboard = %q, want selected job summary", got)
	}

	d.Keys("]")
	current(views.JobsTabHistory)
	jobsTestRecord(t, a, data.KindRun, map[string]any{"title": "Gamma release", "runStatus": "failed"})
	d.Keys("r")
	if hist := current(views.JobsTabHistory); hist.Facts["historyCount"] != 3 || a.Status() != "jobs refreshed" {
		t.Fatalf("Jobs refresh failed, facts %+v status %q", hist.Facts, a.Status())
	}
	d.Keys("[")
	if current(views.JobsTabActive).Facts["hasFilter"] != true {
		t.Fatal("returning to Active lost its filter")
	}
	d.Keys("]", "]")
	current(views.JobsTabActions)
	d.Keys("c", "y")
	if actions := current(views.JobsTabActions); actions.Facts["actionCount"] != 0 || actions.Facts["hasSelection"] != false || a.Modal() != "" {
		t.Fatalf("cleared action log facts = %+v, modal %q", actions.Facts, a.Modal())
	}
	if got := data.NewActionLogStore(path).List(1); len(got) != 0 {
		t.Fatalf("persistent action log wasn't cleared: %+v", got)
	}
	d.Keys("]")
	current(views.JobsTabAll)
	d.Keys("2")
	if a.Space() != SpaceWork {
		t.Fatalf("2 switched to %q, want Work", a.Space())
	}
	d.Keys("[")
	if a.Space() != SpacePanes {
		t.Fatalf("[ outside Jobs switched to %q, want Panes", a.Space())
	}
}

func TestActionLogDispatch(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	a, d := newJobsTestApp(t, ":memory:", Options{DataDir: dir, Now: func() time.Time { return now }})
	d.Keys("2")
	before := a.actionLog.List(0)
	for _, id := range []string{"motion.down", CmdFilterInsert, CmdFilterBack, CmdTermInput, core.CmdFocusNext, input.CmdKillWordBack} {
		d.Send(core.CommandEvent{Command: core.Command{ID: id, Source: "test"}})
	}
	if got := a.actionLog.List(0); len(got) != len(before) {
		t.Fatalf("noisy commands added entries: before %+v after %+v", before, got)
	}

	missing := core.Ref{Kind: data.KindTask, ID: "missing"}
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdTaskPriorityCycle, Target: &missing, Source: "mcp", Args: map[string]any{"text": "private input"}}})
	entry := a.actionLog.List(1)[0]
	if entry.ID == "" || !entry.At.Equal(now) || entry.Command != CmdTaskPriorityCycle || entry.Target != data.RefText(missing) ||
		entry.Space != SpaceWork || entry.Source != "mcp" || entry.Outcome != "error" || !strings.Contains(entry.Detail, "record not found") ||
		strings.Contains(entry.Detail, "private input") {
		t.Fatalf("failed action metadata = %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(dir, "action-log.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("memory-backed app created a log file: %v", err)
	}

	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"ui.actionLogEnabled": false}`), 0600); err != nil {
		t.Fatal(err)
	}
	disabled, keys := newJobsTestApp(t, ":memory:", Options{ConfigDir: cfg})
	keys.Keys("g", "j")
	if disabled.Space() != SpaceJobs || len(disabled.actionLog.List(0)) != 0 {
		t.Fatalf("disabled action log recorded Jobs open: space %q, entries %+v", disabled.Space(), disabled.actionLog.List(0))
	}
}

func TestTaskPriorityCycleUndo(t *testing.T) {
	a, d := newJobsTestApp(t, ":memory:", Options{})
	ref := jobsTestRecord(t, a, data.KindTask, map[string]any{"title": "Queued work", "priority": "P5", "agentStatus": data.AgentStatusQueued})
	d.Keys("2")
	priority := func() string {
		t.Helper()
		task, err := a.Store.Get(ref)
		if err != nil {
			t.Fatal(err)
		}
		return task.Text("priority")
	}
	for _, want := range []string{"P0", "P1", "P2", "P3", "", "P0"} {
		d.Keys("g", "P")
		if got := priority(); got != want {
			t.Fatalf("g P priority = %q, want %q (status %q)", got, want, a.Status())
		}
	}
	if entry := a.actionLog.List(1)[0]; entry.Command != CmdTaskPriorityCycle || entry.Target != data.RefText(ref) ||
		entry.Space != SpaceWork || !strings.HasPrefix(entry.Source, "key:") || entry.Outcome != "ok" {
		t.Fatalf("successful priority action metadata = %+v", entry)
	}
	d.Keys("u")
	if got := priority(); got != "" {
		t.Fatalf("u restored priority %q, want unset", got)
	}
	d.Keys("u")
	if got := priority(); got != "P3" {
		t.Fatalf("undo of priority clear restored %q, want P3", got)
	}
}
