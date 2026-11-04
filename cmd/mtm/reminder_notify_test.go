package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
)

func TestReminderAgentNeedsOwnerOrExplicitAllow(t *testing.T) {
	t.Parallel()
	a, now := reminderApp(t)
	id := createReminder(t, a, "water the plants")
	for _, verb := range []string{"check", "uncheck", "toggle"} {
		var out, stderr bytes.Buffer
		sh := a.shell(strings.NewReader(""), &out, &stderr)
		sh.Actor = &ochazuke.Actor{Kind: "agent", Harness: "test", Pane: "test-pane", Session: "test-session"}
		err := sh.Run(context.Background(), []string{"reminder", verb, id, "--yes"})
		if err == nil || !strings.Contains(err.Error(), "owner's confirmation") {
			t.Fatalf("agent %s: %v, %s", verb, err, stderr.String())
		}
	}
	d, err := a.reminders.store.Day(id, now.Format(time.DateOnly))
	if err != nil || d.Done {
		t.Fatalf("refused check changed day %+v %v", d, err)
	}
	if err := a.r.Settings().Set(ochazuke.AgentRightsSetting, `[{"match":"reminder.toggle","right":"allow"}]`); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	sh := a.shell(strings.NewReader(""), &out, &stderr)
	sh.Actor = &ochazuke.Actor{Kind: "agent", Harness: "test", Pane: "test-pane", Session: "test-session"}
	if err := sh.Run(context.Background(), []string{"reminder", "toggle", id}); err != nil {
		t.Fatalf("allowed check: %v, %s", err, stderr.String())
	}
	d, err = a.reminders.store.Day(id, now.Format(time.DateOnly))
	if err != nil || !d.Done {
		t.Fatalf("allowed check %+v %v", d, err)
	}
	raw, err := json.Marshal(d.Actor)
	if err != nil {
		t.Fatal(err)
	}
	var actor ochazuke.Actor
	if err := json.Unmarshal(raw, &actor); err != nil {
		t.Fatal(err)
	}
	if actor.Kind != "agent" || actor.Pane != "test-pane" || actor.Harness != "test" {
		t.Fatalf("check actor %+v", actor)
	}
}

func TestReminderBadSenderConfigFailsVisiblyAndRetries(t *testing.T) {
	t.Parallel()
	a, now := reminderApp(t)
	id := createReminder(t, a, "water the plants")
	if err := a.r.Settings().Set(settingReminderCommands, "[[]]"); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	err := a.shell(strings.NewReader(""), &out, &stderr).Run(context.Background(), []string{"reminder", "notify-due"})
	if err == nil || !strings.Contains(err.Error(), "each sender needs an executable") {
		t.Fatalf("bad config %v, %s", err, stderr.String())
	}
	status, err := a.reminders.store.Status()
	if err != nil || !strings.Contains(status.LastError, "each sender needs an executable") || status.LastFailure != now.Format(time.RFC3339Nano) {
		t.Fatalf("failure status %+v %v", status, err)
	}
	d, err := a.reminders.store.Day(id, now.Format(time.DateOnly))
	if err != nil || d.NotifiedOn != "" {
		t.Fatalf("bad config notified %+v %v", d, err)
	}
	if err := a.r.Settings().Set(settingReminderCommands, "[]"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if err := a.shell(strings.NewReader(""), &out, &stderr).Run(context.Background(), []string{"reminder", "notify-due"}); err != nil {
		t.Fatalf("retry: %v, %s", err, stderr.String())
	}
	d, err = a.reminders.store.Day(id, now.Format(time.DateOnly))
	if err != nil || d.NotifiedOn != "2026-11-02" {
		t.Fatalf("retry day %+v %v", d, err)
	}
}

func TestReminderWorkUsesWakeDateNotQueuedDate(t *testing.T) {
	t.Parallel()
	a, now := reminderApp(t)
	id := createReminder(t, a, "water the plants")
	*now = time.Date(2026, 11, 2, 23, 59, 0, 0, time.UTC)
	work, err := a.notificationWork()
	if err != nil {
		t.Fatal(err)
	}
	*now = time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	n, err := work(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("wake %d %v", n, err)
	}
	yesterday, err := a.reminders.store.Day(id, "2026-11-02")
	if err != nil || yesterday.NotifiedOn != "" {
		t.Fatalf("stale date notified %+v %v", yesterday, err)
	}
	today, err := a.reminders.store.Day(id, "2026-11-03")
	if err != nil || today.NotifiedOn != "2026-11-03" {
		t.Fatalf("wake date %+v %v", today, err)
	}
}
