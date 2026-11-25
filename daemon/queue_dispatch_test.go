package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func shortQueueSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtm-q-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "mtm.sock")
}

func TestSchedulerAgentQueueEmptySkips(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	dispatched := 0
	notified := 0
	sched := NewScheduler(SchedulerConfig{
		Store: store,
		Clock: func() time.Time { return now },
		Notifier: NotifierFunc(func(_ context.Context, _, _ string) error {
			notified++
			return nil
		}),
		Dispatcher: QueueDispatcherFunc(func(_ context.Context, _ data.Record, _ []data.AgentQueueItem, _ string) (string, error) {
			dispatched++
			return "dispatched", nil
		}),
	})

	schedRec, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Hourly agent queue",
			"scheduleKind": "agent_queue",
			"cron":         "0 * * * *",
			"nextDueAt":    now.Add(-time.Minute).Format(data.TimeFormat),
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	if err := sched.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if dispatched != 0 {
		t.Fatalf("dispatched = %d, want 0 when queue is empty", dispatched)
	}
	if notified != 0 {
		t.Fatalf("notified = %d, want 0 when queue is empty", notified)
	}

	occs, err := store.Query(data.Query{Kind: data.KindOccurrence})
	if err != nil {
		t.Fatal(err)
	}
	if len(occs.Items) != 1 {
		t.Fatalf("occurrences = %d, want 1", len(occs.Items))
	}
	if got := occs.Items[0].Text("outcomeStatus"); got != "skipped" {
		t.Fatalf("outcomeStatus = %q, want skipped", got)
	}

	sigs, err := store.Query(data.Query{Kind: data.KindSignal})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.Items) != 0 {
		t.Fatalf("signals = %d, want 0 when queue is empty", len(sigs.Items))
	}

	updated, err := store.Get(schedRec.Ref())
	if err != nil {
		t.Fatal(err)
	}
	wantNext := time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC)
	if got := updated.Time("nextDueAt"); !got.Equal(wantNext) {
		t.Fatalf("nextDueAt = %v, want %v", got, wantNext)
	}
}

func TestSchedulerAgentQueueDispatchesHydratedItems(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	project, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Matomeru"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       "Wire periodic pickup",
			"description": "Deliver queued tasks to idle agent panes",
			"parent":      project.Ref(),
			"priority":    "P1",
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID: child.ID,
		Title:  "Spec note",
		Body:   "Use bracketed paste without moving promptedAt",
		Attachments: []data.Attachment{
			{Kind: "file", Path: "daemon/queue_dispatch.go", Label: "dispatcher"},
		},
	}, owner); err != nil {
		t.Fatal(err)
	}
	ctxRec, _, err := store.Create(data.CreateInput{
		Kind: data.KindContext,
		Fields: map[string]any{
			"title":       "Design PR",
			"value":       "https://github.com/ofam/matomeru/pull/42",
			"contextKind": "pr",
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Relate(data.RelateInput{From: child.Ref(), Kind: data.RelContext, To: ctxRec.Ref()}, owner); err != nil {
		t.Fatal(err)
	}

	// Unrelated queued task in another project must be filtered out.
	otherProj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Other"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       "Out of scope",
			"parent":      otherProj.Ref(),
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
		},
	}, owner); err != nil {
		t.Fatal(err)
	}

	var gotItems []data.AgentQueueItem
	var gotPrompt string
	sched := NewScheduler(SchedulerConfig{
		Store: store,
		Clock: func() time.Time { return now },
		Dispatcher: QueueDispatcherFunc(func(_ context.Context, _ data.Record, items []data.AgentQueueItem, prompt string) (string, error) {
			gotItems = items
			gotPrompt = prompt
			return "dispatched 1 queued task to pane p-1", nil
		}),
	})

	extraPrompt := "Focus on P1 items first."
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Matomeru agent queue",
			"scheduleKind": "agent_queue",
			"cron":         "*/15 * * * *",
			"nextDueAt":    now.Add(-time.Minute).Format(data.TimeFormat),
			"task":         project.Ref(),
			"prompt":       extraPrompt,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	if err := sched.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(gotItems) != 1 || gotItems[0].Task.ID != child.ID {
		t.Fatalf("dispatched items = %+v, want only %s", gotItems, child.ID)
	}
	if len(gotItems[0].Notes) != 1 {
		t.Fatalf("hydrated notes = %d, want 1", len(gotItems[0].Notes))
	}
	if len(gotItems[0].Attachments) != 1 || gotItems[0].Attachments[0].Path != "daemon/queue_dispatch.go" {
		t.Fatalf("hydrated attachments = %+v, want daemon/queue_dispatch.go", gotItems[0].Attachments)
	}
	if len(gotItems[0].Context) != 1 {
		t.Fatalf("hydrated context = %d, want 1", len(gotItems[0].Context))
	}

	wantPrompt := data.FormatAgentQueuePrompt(gotItems, extraPrompt)
	if gotPrompt != wantPrompt {
		t.Fatalf("prompt mismatch:\ngot:\n%s\nwant:\n%s", gotPrompt, wantPrompt)
	}
	for _, needle := range []string{extraPrompt, "Wire periodic pickup", "Use bracketed paste", "daemon/queue_dispatch.go", "pull/42"} {
		if !strings.Contains(gotPrompt, needle) {
			t.Errorf("prompt missing %q", needle)
		}
	}

	occs, err := store.Query(data.Query{Kind: data.KindOccurrence})
	if err != nil {
		t.Fatal(err)
	}
	if len(occs.Items) != 1 || occs.Items[0].Text("outcomeStatus") != "done" {
		t.Fatalf("occurrences = %+v, want 1 done", occs.Items)
	}
}

func TestSchedulerAgentQueueCommandAndNilDispatcherFallback(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	queuedTask, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       "Queued task A",
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	var ranCmd string
	var notifiedTitles []string
	sched := NewScheduler(SchedulerConfig{
		Store: store,
		Clock: func() time.Time { return now },
		Runner: RunnerFunc(func(_ context.Context, cmd string) (string, error) {
			ranCmd = cmd
			return "ok", nil
		}),
		Notifier: NotifierFunc(func(_ context.Context, title, _ string) error {
			notifiedTitles = append(notifiedTitles, title)
			return nil
		}),
	})

	due := now.Add(-time.Minute).Format(data.TimeFormat)
	// 1. Schedule with command runs through Runner.
	if _, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Command pickup",
			"scheduleKind": "agent_queue",
			"cron":         "0 * * * *",
			"command":      "custom-agent-pickup",
			"nextDueAt":    due,
		},
	}, owner); err != nil {
		t.Fatal(err)
	}
	// 2. Schedule without command and without Dispatcher creates signal + notifies.
	if _, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Fallback pickup",
			"scheduleKind": "agent_queue",
			"cron":         "0 * * * *",
			"nextDueAt":    due,
		},
	}, owner); err != nil {
		t.Fatal(err)
	}

	if err := sched.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if ranCmd != "custom-agent-pickup" {
		t.Fatalf("ranCmd = %q, want custom-agent-pickup", ranCmd)
	}
	if len(notifiedTitles) != 1 || !strings.Contains(notifiedTitles[0], "1 agent task queued") {
		t.Fatalf("notifiedTitles = %v, want [1 agent task queued]", notifiedTitles)
	}
	sigs, err := store.Query(data.Query{Kind: data.KindSignal})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.Items) != 1 || !strings.Contains(sigs.Items[0].Text("title"), "queued") {
		t.Fatalf("signals = %+v, want 1 queued signal", sigs.Items)
	}
	gotTask, err := store.Get(queuedTask.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if got := gotTask.Text("agentDispatchedAt"); got != now.UTC().Format(data.TimeFormat) {
		t.Fatalf("command schedule agentDispatchedAt = %q, want %q", got, now.UTC().Format(data.TimeFormat))
	}
	if _, ok := gotTask.RefField("agentDispatchedPane"); ok {
		t.Fatal("command schedule should not set agentDispatchedPane")
	}
}

func TestServerQueueDispatcherNoIdlePaneFallback(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	var (
		mu       sync.Mutex
		notified []string
	)
	srv, client := startTestServer(t, Config{
		SocketPath:        shortQueueSocketPath(t),
		Store:             store,
		DisableMCP:        true,
		SchedulerInterval: -1,
		Clock:             func() time.Time { return now },
		Notifier: NotifierFunc(func(_ context.Context, title, _ string) error {
			mu.Lock()
			defer mu.Unlock()
			notified = append(notified, title)
			return nil
		}),
	})

	// Create a project with a queued task.
	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Backend"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	waitingTask, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       "Fix queue retry",
			"parent":      proj.Ref(),
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// Open a live shell pane (must NOT be chosen) and a live running agent pane (busy, must NOT be interrupted).
	shellPane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "shell", "harness": "zsh", "task": proj.Ref()},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	busyPane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "busy-claude", "harness": "claude", "task": proj.Ref()},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Open(context.Background(), OpenRequest{ID: shellPane.ID, Cmd: "cat"}); err != nil {
		t.Fatalf("open shellPane: %v", err)
	}
	if err := client.Open(context.Background(), OpenRequest{ID: busyPane.ID, Cmd: "cat"}); err != nil {
		t.Fatalf("open busyPane: %v", err)
	}
	if _, err := client.HookEvent(context.Background(), HookEvent{
		PaneID:  busyPane.ID,
		Harness: "claude",
		Kind:    HookKindState,
		State:   StateRunning,
	}); err != nil {
		t.Fatalf("HookEvent running: %v", err)
	}

	if _, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Backend queue",
			"scheduleKind": "agent_queue",
			"cron":         "*/5 * * * *",
			"nextDueAt":    now.Add(-time.Minute).Format(data.TimeFormat),
			"task":         proj.Ref(),
		},
	}, owner); err != nil {
		t.Fatal(err)
	}

	if err := srv.Scheduler().Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	sigs, err := store.Query(data.Query{Kind: data.KindSignal})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.Items) != 1 {
		t.Fatalf("signals = %d, want 1 waiting signal", len(sigs.Items))
	}
	sig := sigs.Items[0]
	if !strings.Contains(sig.Text("title"), "1 agent task waiting in queue") {
		t.Fatalf("signal title = %q, want waiting in queue", sig.Text("title"))
	}
	if sig.Text("triageState") != data.TriageActionable {
		t.Fatalf("signal triageState = %q, want %q", sig.Text("triageState"), data.TriageActionable)
	}
	if tref, ok := sig.RefField("task"); !ok || tref != proj.Ref() {
		t.Fatalf("signal task ref = %v (%v), want %v", tref, ok, proj.Ref())
	}
	if !strings.Contains(sig.Text("body"), "Fix queue retry") {
		t.Fatalf("signal body = %q, want task title", sig.Text("body"))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notified) != 1 || !strings.Contains(notified[0], "waiting in queue") {
		t.Fatalf("notified = %v, want 1 waiting notification", notified)
	}
	gotWaiting, err := store.Get(waitingTask.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if got := gotWaiting.Text("agentDispatchedAt"); got != "" {
		t.Fatalf("waiting signal fallback must NOT stamp agentDispatchedAt, got %q", got)
	}
}

func TestServerQueueDispatcherDeliversToIdlePaneWithoutMovingPromptedAt(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	humanPrompted := now.Add(-10 * time.Minute).UTC().Format(data.TimeFormat)

	srv, client := startTestServer(t, Config{
		SocketPath:        shortQueueSocketPath(t),
		Store:             store,
		DisableMCP:        true,
		SchedulerInterval: -1,
		Clock:             func() time.Time { return now },
	})

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Daemon"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       "Drain queued items",
			"parent":      proj.Ref(),
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	idlePane, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "claude-worker",
			"harness":    "claude",
			"task":       proj.Ref(),
			"promptedAt": humanPrompted,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	readyFile := filepath.Join(tmpDir, "ready")
	captureFile := filepath.Join(tmpDir, "captured.bin")
	cmd := "stty raw -echo && touch '" + readyFile + "' && exec cat > '" + captureFile + "'"
	if err := client.Open(context.Background(), OpenRequest{ID: idlePane.ID, Cmd: cmd}); err != nil {
		t.Fatalf("open idlePane: %v", err)
	}
	readyDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(readyDeadline) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("pane did not become ready: %v", err)
	}

	if _, err := client.HookEvent(context.Background(), HookEvent{
		PaneID:  idlePane.ID,
		Harness: "claude",
		Kind:    HookKindState,
		State:   StateIdle,
	}); err != nil {
		t.Fatalf("HookEvent idle: %v", err)
	}

	if _, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Daemon queue",
			"scheduleKind": "agent_queue",
			"cron":         "*/5 * * * *",
			"nextDueAt":    now.Add(-time.Minute).Format(data.TimeFormat),
			"task":         proj.Ref(),
			"harness":      "claude",
		},
	}, owner); err != nil {
		t.Fatal(err)
	}

	if err := srv.Scheduler().Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var captured string
	for time.Now().Before(deadline) {
		raw, rerr := os.ReadFile(captureFile)
		if rerr == nil && strings.Contains(string(raw), "\x1b[201~\r") {
			captured = string(raw)
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.HasPrefix(captured, "\x1b[200~") || !strings.Contains(captured, "Drain queued items") || !strings.Contains(captured, task.ID) || !strings.HasSuffix(captured, "\x1b[201~\r") {
		t.Fatalf("captured PTY bytes = %q, want bracketed paste containing task %s", captured, task.ID)
	}

	// Verify human promptedAt was NOT clobbered while lastAgentInputAt/lastAgentBy were recorded.
	updatedPane, err := store.Get(idlePane.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if got := updatedPane.Text("promptedAt"); got != humanPrompted {
		t.Fatalf("promptedAt moved from %q to %q", humanPrompted, got)
	}
	if updatedPane.Text("lastAgentInputAt") == "" || updatedPane.Text("lastAgentBy") != daemonActor.ID {
		t.Fatalf("agent input audit missing: lastAgentInputAt=%q lastAgentBy=%q", updatedPane.Text("lastAgentInputAt"), updatedPane.Text("lastAgentBy"))
	}

	// No waiting signal should have been created.
	sigs, err := store.Query(data.Query{Kind: data.KindSignal})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.Items) != 0 {
		t.Fatalf("unexpected waiting signals = %+v", sigs.Items)
	}

	// Real dispatch must stamp agentDispatchedAt and agentDispatchedPane.
	updatedTask, err := store.Get(task.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if got := updatedTask.Text("agentDispatchedAt"); got != now.UTC().Format(data.TimeFormat) {
		t.Fatalf("agentDispatchedAt = %q, want %q", got, now.UTC().Format(data.TimeFormat))
	}
	if paneRef, ok := updatedTask.RefField("agentDispatchedPane"); !ok || paneRef != idlePane.Ref() {
		t.Fatalf("agentDispatchedPane = (%+v, %v), want %+v", paneRef, ok, idlePane.Ref())
	}
}

func TestServerQueueDispatcherExplicitPaneBusyDoesNotClobber(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	srv, client := startTestServer(t, Config{
		SocketPath:        shortQueueSocketPath(t),
		Store:             store,
		DisableMCP:        true,
		SchedulerInterval: -1,
		Clock:             func() time.Time { return now },
	})

	if _, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       "Wait for busy pane",
			"forAgent":    true,
			"agentStatus": data.AgentStatusQueued,
		},
	}, owner); err != nil {
		t.Fatal(err)
	}

	targetPane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "explicit-claude", "harness": "claude"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Open(context.Background(), OpenRequest{ID: targetPane.ID, Cmd: "cat"}); err != nil {
		t.Fatalf("open targetPane: %v", err)
	}
	if _, err := client.HookEvent(context.Background(), HookEvent{
		PaneID:  targetPane.ID,
		Harness: "claude",
		Kind:    HookKindState,
		State:   StateRunning,
	}); err != nil {
		t.Fatalf("HookEvent running: %v", err)
	}

	if _, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Explicit pane queue",
			"scheduleKind": "agent_queue",
			"cron":         "*/5 * * * *",
			"nextDueAt":    now.Add(-time.Minute).Format(data.TimeFormat),
			"pane":         targetPane.Ref(),
		},
	}, owner); err != nil {
		t.Fatal(err)
	}

	if err := srv.Scheduler().Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	sigs, err := store.Query(data.Query{Kind: data.KindSignal})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.Items) != 1 || !strings.Contains(sigs.Items[0].Text("body"), "busy") {
		t.Fatalf("signals = %+v, want 1 busy waiting signal", sigs.Items)
	}
}
