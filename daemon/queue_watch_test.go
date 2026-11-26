package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

type watchDispatch struct {
	paneID, taskID, prompt, schedID string
}

type queueWatchFixture struct {
	t       *testing.T
	store   *data.Store
	watcher *queueWatcher
	sent    []watchDispatch
	busy    map[string]bool
	now     time.Time
	owner   core.Actor
	project *data.Record
}

func newQueueWatchFixture(t *testing.T) *queueWatchFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &queueWatchFixture{
		t: t, store: store, busy: map[string]bool{},
		now:   time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),
		owner: core.Actor{Kind: "user", ID: "owner"},
	}
	// The dispatcher stands in for ServerQueueDispatcher: it records the
	// delivery and stamps the item dispatched, as the real one does.
	disp := QueueDispatcherFunc(func(_ context.Context, sched data.Record, items []data.AgentQueueItem, prompt string) (string, error) {
		ref, _ := sched.RefField("pane")
		for _, item := range items {
			f.sent = append(f.sent, watchDispatch{paneID: ref.ID, taskID: item.Task.ID, prompt: prompt, schedID: sched.ID})
			if _, err := store.MarkTasksDispatched([]string{item.Task.ID}, ref.ID, f.now, daemonActor); err != nil {
				t.Fatal(err)
			}
		}
		return "dispatched", nil
	})
	f.watcher = newQueueWatcher(store, disp, func() time.Time { return f.now }, func(id string) bool { return !f.busy[id] })
	f.project = f.create(data.KindTask, map[string]any{"title": "Alpha"})
	return f
}

func (f *queueWatchFixture) create(kind string, fields map[string]any) *data.Record {
	f.t.Helper()
	rec, _, err := f.store.Create(data.CreateInput{Kind: kind, Fields: fields}, f.owner)
	if err != nil {
		f.t.Fatal(err)
	}
	return rec
}

func (f *queueWatchFixture) queue(title, queueName string, parent *data.Record) *data.Record {
	f.t.Helper()
	task := f.create(data.KindTask, map[string]any{"title": title, "parent": parent.Ref()})
	if _, err := f.store.AddTaskToQueue(task.ID, queueName, nil, f.owner); err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f *queueWatchFixture) done(paneID string) string {
	f.t.Helper()
	detail, err := f.watcher.handleAgentDone(context.Background(), AgentDoneEvent{Event: EventAgentDone, PaneID: paneID, State: StateDone})
	if err != nil {
		f.t.Fatal(err)
	}
	return detail
}

func TestQueueWatchOnDoneDispatchesNextItemWithIDHandoff(t *testing.T) {
	f := newQueueWatchFixture(t)
	other := f.create(data.KindTask, map[string]any{"title": "Beta"})
	bugs1 := f.queue("bug one", "bugs", f.project)
	_ = f.queue("review one", "review", f.project)
	bugs2 := f.queue("bug two", "bugs", f.project)
	_ = f.queue("foreign bug", "bugs", other)
	lane := f.create(data.KindLane, map[string]any{"title": "bug lane", "task": bugs1.Ref(), "laneStatus": "running", "branch": "fix/bug-one"})

	watcher := f.create(data.KindPane, map[string]any{"title": "claude", "harness": "claude", "task": bugs1.Ref(),
		data.FieldWatchMode: data.WatchModeOnDone, data.FieldWatchQueues: "bugs"})
	manual := f.create(data.KindPane, map[string]any{"title": "manual", "harness": "claude", data.FieldWatchMode: data.WatchModeManual})
	unset := f.create(data.KindPane, map[string]any{"title": "unset", "harness": "claude"})

	for _, pane := range []*data.Record{manual, unset} {
		if detail := f.done(pane.ID); detail != "" || len(f.sent) != 0 {
			t.Fatalf("pane %q (not watching on_done) was handed work: %q %v", pane.Text("title"), detail, f.sent)
		}
	}
	if detail := f.done("missing-pane"); detail != "" {
		t.Fatalf("an unknown pane was handed work: %q", detail)
	}

	if detail := f.done(watcher.ID); detail != "dispatched" || len(f.sent) != 1 {
		t.Fatalf("first done: detail %q, sent %v", detail, f.sent)
	}
	first := f.sent[0]
	if first.paneID != watcher.ID || first.taskID != bugs1.ID || first.schedID != queueWatchScheduleID+watcher.ID {
		t.Fatalf("first dispatch = %+v, want bug one to the watching pane", first)
	}
	for _, want := range []string{"`bugs`", "`task:" + bugs1.ID + "`", "`" + f.project.ID + "`", "`" + lane.ID + "`", "`fix/bug-one`"} {
		if !strings.Contains(first.prompt, want) {
			t.Errorf("handoff prompt is missing %q:\n%s", want, first.prompt)
		}
	}

	// The next done edge takes the next item of the watched queue, in the
	// pane's own project, never the same item twice.
	f.done(watcher.ID)
	if len(f.sent) != 2 || f.sent[1].taskID != bugs2.ID {
		t.Fatalf("second dispatch = %v, want bug two", f.sent)
	}
	if detail := f.done(watcher.ID); detail != "" || len(f.sent) != 2 {
		t.Fatalf("an empty watched queue still dispatched: %q %v", detail, f.sent)
	}
}

func TestQueueWatchSchedulePollsOnTheSchedulerCadence(t *testing.T) {
	f := newQueueWatchFixture(t)
	first := f.queue("one", "", f.project)
	second := f.queue("two", "", f.project)
	pane := f.create(data.KindPane, map[string]any{"title": "codex", "harness": "codex",
		data.FieldWatchMode: data.WatchModeSchedule, data.FieldWatchQueues: "any", data.FieldWatchIntervalSec: int64(60)})
	_ = f.create(data.KindPane, map[string]any{"title": "on done", "harness": "claude", data.FieldWatchMode: data.WatchModeOnDone})

	sched := NewScheduler(SchedulerConfig{
		Store: f.store, Clock: func() time.Time { return f.now },
		AfterTick: f.watcher.pollScheduled,
	})
	tick := func() {
		t.Helper()
		if err := sched.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	f.busy[pane.ID] = true
	tick()
	if len(f.sent) != 0 {
		t.Fatalf("a busy pane was handed work: %v", f.sent)
	}
	f.busy[pane.ID] = false
	tick() // busy panes are retried on the next tick, not after a full interval
	if len(f.sent) != 1 || f.sent[0].taskID != first.ID || f.sent[0].paneID != pane.ID {
		t.Fatalf("first poll = %v, want item one to the schedule pane only", f.sent)
	}
	f.now = f.now.Add(30 * time.Second)
	tick()
	if len(f.sent) != 1 {
		t.Fatalf("polled again before the interval elapsed: %v", f.sent)
	}
	f.now = f.now.Add(31 * time.Second)
	tick()
	if len(f.sent) != 2 || f.sent[1].taskID != second.ID {
		t.Fatalf("second poll = %v, want item two", f.sent)
	}
}

func TestQueueWatchWiredToTelemetryDoneEdge(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv, err := New(Config{Store: store, DisableMCP: true, SchedulerInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	if srv.queueWatch == nil {
		t.Fatal("a daemon with a store has no queue watcher")
	}
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	srv.telemetry.recordEvent(HookEvent{PaneID: "p1", Kind: HookKindState, State: StateRunning}, now)
	srv.telemetry.recordEvent(HookEvent{PaneID: "p1", Kind: HookKindState, State: StateDone}, now)
	select {
	case ev := <-srv.queueWatch.events:
		if ev.PaneID != "p1" || ev.Event != EventAgentDone {
			t.Fatalf("watcher received %+v", ev)
		}
	default:
		t.Fatal("the agent.done edge did not reach the queue watcher")
	}
}
