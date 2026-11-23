package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestCronParseMatchAndNext(t *testing.T) {
	expr, err := ParseCron("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	// Monday 2026-11-23 09:15 UTC
	mon := time.Date(2026, 11, 23, 9, 15, 0, 0, time.UTC)
	if !expr.Matches(mon) {
		t.Fatalf("expected %v to match", mon)
	}
	if expr.Matches(mon.Add(time.Minute)) {
		t.Fatalf("did not expect %v to match", mon.Add(time.Minute))
	}
	// Sunday 2026-11-22 09:15 UTC
	sun := time.Date(2026, 11, 22, 9, 15, 0, 0, time.UTC)
	if expr.Matches(sun) {
		t.Fatalf("did not expect Sunday %v to match 1-5", sun)
	}
	next := expr.Next(mon)
	want := time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", mon, next, want)
	}

	// Sunday as 7 in DOW
	sunExpr, err := ParseCron("0 10 * * 7")
	if err != nil {
		t.Fatalf("ParseCron sunday=7: %v", err)
	}
	if !sunExpr.Matches(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("Sunday=7 did not match Sunday")
	}

	// Macros
	daily, err := ParseCron("@daily")
	if err != nil {
		t.Fatal(err)
	}
	if got := daily.Next(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("@daily next = %v", got)
	}

	every, err := ParseCron("@every 10m")
	if err != nil {
		t.Fatal(err)
	}
	if got := every.Next(mon); !got.Equal(mon.Add(10 * time.Minute)) {
		t.Fatalf("@every 10m next = %v", got)
	}

	// Invalid expressions
	for _, bad := range []string{"", "* * *", "60 * * * *", "* 25 * * *", "* * * * */0", "@every -5m"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("ParseCron(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestSchedulerRemindRunCheckAndDisable(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	task, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Ops"}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	var (
		mu       sync.Mutex
		notified []string
		ran      []string
	)
	sched := NewScheduler(SchedulerConfig{
		Store: store,
		Clock: clock,
		Notifier: NotifierFunc(func(_ context.Context, title, body string) error {
			mu.Lock()
			defer mu.Unlock()
			notified = append(notified, title)
			return nil
		}),
		Runner: RunnerFunc(func(_ context.Context, command string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			ran = append(ran, command)
			if strings.Contains(command, "fail") {
				return "disk full", errors.New("exit 1")
			}
			return "all green", nil
		}),
	})

	dueStr := now.Add(-time.Minute).Format(data.TimeFormat)

	// 1. Active remind schedule (due now)
	remRec, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Standup",
			"scheduleKind": "remind",
			"cron":         "0 9 * * *",
			"nextDueAt":    dueStr,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Active run schedule (due now)
	runRec, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Nightly backup",
			"scheduleKind": "run",
			"cron":         "0 9 * * *",
			"command":      "backup.sh",
			"nextDueAt":    dueStr,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Active check schedule (due now, fails -> creates failure signal linked to task)
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Disk check",
			"scheduleKind": "check",
			"cron":         "*/30 * * * *",
			"command":      "check-fail",
			"nextDueAt":    dueStr,
			"task":         task.Ref(),
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// 4. Paused schedule (must NOT run)
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Paused reminder",
			"scheduleKind": "remind",
			"cron":         "* * * * *",
			"nextDueAt":    dueStr,
			"paused":       true,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// 5. Disabled schedule (must NOT run)
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Disabled job",
			"scheduleKind": "run",
			"cron":         "* * * * *",
			"command":      "should-not-run",
			"nextDueAt":    dueStr,
			"enabled":      false,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// 6. Archived schedule (must NOT run)
	archRec, _, err := store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":        "Archived reminder",
			"scheduleKind": "remind",
			"cron":         "* * * * *",
			"nextDueAt":    dueStr,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Archive(archRec.Ref(), owner); err != nil {
		t.Fatal(err)
	}

	// First tick fires remind, run, and check (3 occurrences, 1 signal).
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	mu.Lock()
	if len(notified) != 1 || notified[0] != "Standup" {
		t.Fatalf("notified = %v, want [Standup]", notified)
	}
	if len(ran) != 2 || ran[0] != "backup.sh" || ran[1] != "check-fail" {
		t.Fatalf("ran = %v, want [backup.sh check-fail]", ran)
	}
	mu.Unlock()

	// Check occurrences written
	occs, err := store.Query(data.Query{Kind: data.KindOccurrence})
	if err != nil {
		t.Fatal(err)
	}
	if len(occs.Items) != 3 {
		t.Fatalf("occurrences = %d, want 3", len(occs.Items))
	}

	// Check signal created by "check"
	sigs, err := store.Query(data.Query{Kind: data.KindSignal})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.Items) != 1 {
		t.Fatalf("signals = %d, want 1", len(sigs.Items))
	}
	sig := sigs.Items[0]
	if sig.Text("title") != "Disk check" || sig.Text("eventKind") != "failure" || !strings.Contains(sig.Text("body"), "disk full") {
		t.Fatalf("signal = %+v", sig.Fields)
	}
	if tref, ok := sig.RefField("task"); !ok || tref != task.Ref() {
		t.Fatalf("signal task ref = %v (%v), want %v", tref, ok, task.Ref())
	}

	// Verify nextDueAt advanced for Standup to tomorrow 09:00 UTC.
	updatedRem, err := store.Get(remRec.Ref())
	if err != nil {
		t.Fatal(err)
	}
	wantNext := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	if got := updatedRem.Time("nextDueAt"); !got.Equal(wantNext) {
		t.Fatalf("Standup nextDueAt = %v, want %v", got, wantNext)
	}

	// Opening mtm and pausing/archiving a schedule stops future runs IMMEDIATELY.
	if _, _, err := store.Patch(data.PatchInput{Ref: remRec.Ref(), Fields: map[string]any{"paused": true}}, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Archive(runRec.Ref(), owner); err != nil {
		t.Fatal(err)
	}

	// Advance clock to tomorrow 09:01 UTC -> neither Standup nor Nightly backup may run!
	now = wantNext.Add(time.Minute)
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	mu.Lock()
	if len(notified) != 1 {
		t.Fatalf("paused schedule ran again: notified = %v", notified)
	}
	for _, cmd := range ran[2:] {
		if cmd == "backup.sh" {
			t.Fatalf("archived schedule ran again: ran = %v", ran)
		}
	}
	mu.Unlock()
}

func TestEscapeAppleScript(t *testing.T) {
	got := EscapeAppleScript(`Say "hello" \ world` + "\nline2")
	want := `Say \"hello\" \\ world line2`
	if got != want {
		t.Fatalf("EscapeAppleScript = %q, want %q", got, want)
	}
}
