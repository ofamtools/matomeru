package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/schedules"
)

func inboxNotifyApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, now := reminderApp(t)
	a.inbox.after = nil
	a.inbox.items.SetClock(func() int64 { return now.UnixMilli() })
	if err := a.r.Settings().Set(settingNotifyChannel, "os"); err != nil {
		t.Fatal(err)
	}
	a.notify.exec = func(context.Context, []string) (string, error) { return "", nil }
	return a, now
}

func inboxNotifyTick(t *testing.T, a *App) inboxNotified {
	t.Helper()
	cmd := a.inboxNotificationsUpdate(inboxDue{})
	if cmd == nil {
		t.Fatal("missing inbox worker")
	}
	result := cmd().(inboxNotified)
	a.inboxNotificationsUpdate(result)
	return result
}

func TestInboxNotifyBaselineSourcesForceAndOptOut(t *testing.T) {
	t.Parallel()
	a, now := inboxNotifyApp(t)
	old, _, err := a.inbox.items.Add(actionitems.Spec{Source: "slack", Title: "old unread"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.notify.exec = func(context.Context, []string) (string, error) { calls++; return "", nil }
	result := inboxNotifyTick(t, a)
	if result.err != nil || result.sent != 0 || calls != 0 || result.since != now.UnixMilli() {
		t.Fatalf("baseline %+v calls=%d", result, calls)
	}
	// A second connection gets the first connection's stamp, not a new baseline.
	b := openWith(t, Options{Data: a.dataPath})
	since, err := inboxNotifySince(b.db, now.Add(time.Hour).UnixMilli())
	if err != nil || since != result.since {
		t.Fatalf("shared baseline %d %v", since, err)
	}
	*now = now.Add(time.Millisecond)
	for _, source := range []string{"slack", "gh", "mtm"} {
		mustCLI(t, a, "action-item", "add", "--source", source, "--title", source)
	}
	mustCLI(t, a, "action-item", "add", "--source", "manual", "--title", "forced", "--notify")
	result = inboxNotifyTick(t, a)
	if result.err != nil || result.sent != 3 || calls != 3 {
		t.Fatalf("source/force %+v calls=%d", result, calls)
	}
	old, err = a.inbox.items.Get(old.ID)
	if err != nil || old.NotifiedAt != 0 {
		t.Fatalf("upgrade storm %+v %v", old, err)
	}
	*now = now.Add(time.Millisecond)
	if err := a.r.Settings().Set(settingInboxNotify, "off"); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, a, "action-item", "add", "--source", "manual", "--title", "forced but off", "--notify")
	result = inboxNotifyTick(t, a)
	if result.err != nil || result.sent != 0 || calls != 3 {
		t.Fatalf("off %+v calls=%d", result, calls)
	}
	op, ok := a.r.Operation("action-item.notify-due")
	if !ok || a.r.AgentRight(op, false).Right != ochazuke.AgentAllow {
		t.Fatalf("notify-due right %+v", op)
	}
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestInboxNotifyHeadlessAndReminderOverrides(t *testing.T) {
	t.Parallel()
	a, now := inboxNotifyApp(t)
	for k, v := range map[string]any{settingNotifyChannel: "both", settingNotifyCommands: `[["shared","{{title}}","{{body}}"]]`} {
		if err := a.r.Settings().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	// Defaults follow the shared channel and commands. Existing explicit reminder
	// settings, including [], override them without changing inbox delivery.
	sender, err := a.notificationSender(settingReminderNotify, settingReminderCommands)
	if err != nil || sender.Channel != "both" || !reflect.DeepEqual(sender.Commands[1], []string{"shared", "{{title}}", "{{body}}"}) {
		t.Fatalf("follow %+v %v", sender, err)
	}
	if err := a.r.Settings().Set(settingReminderNotify, "terminal"); err != nil {
		t.Fatal(err)
	}
	if err := a.r.Settings().Set(settingReminderCommands, "[]"); err != nil {
		t.Fatal(err)
	}
	sender, err = a.notificationSender(settingReminderNotify, settingReminderCommands)
	if err != nil || sender.Channel != "terminal" || len(sender.Commands) != 0 {
		t.Fatalf("override %+v %v", sender, err)
	}
	if err := a.r.Settings().Set(settingNotifyChannel, "os"); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, a, "action-item", "notify-due")
	*now = now.Add(time.Millisecond)
	mustCLI(t, a, "action-item", "add", "--source", "slack", "--title", "a ping", "--why", "mentioned")
	var calls [][]string
	a.notify.exec = func(_ context.Context, argv []string) (string, error) { calls = append(calls, argv); return "", nil }
	var answer struct {
		Sent int `json:"sent"`
	}
	cliJSON(t, a, &answer, "action-item", "notify-due")
	if answer.Sent != 1 || !reflect.DeepEqual(calls[1], []string{"shared", "mtm: slack · a ping", "mentioned"}) {
		t.Fatalf("headless sent=%d argv=%#v", answer.Sent, calls)
	}
	cliJSON(t, a, &answer, "action-item", "notify-due")
	if answer.Sent != 0 || len(calls) != 2 {
		t.Fatalf("headless repeat sent=%d argv=%#v", answer.Sent, calls)
	}
}

func TestInboxNotifyOffLoopHeldNoticeWithoutPageOpen(t *testing.T) {
	t.Parallel()
	a, now := inboxNotifyApp(t)
	a.notify.live = true
	a.m.Width, a.m.Height = 140, 36
	if err := a.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if result := inboxNotifyTick(t, a); result.err != nil {
		t.Fatal(result.err)
	}
	*now = now.Add(time.Millisecond)
	it, _, err := a.inbox.items.Add(actionitems.Spec{Source: "slack", Title: "Review the incident"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.notify.exec = func(context.Context, []string) (string, error) { calls++; return "", nil }
	focus, page := a.r.FocusID(), a.r.Page
	cmd := a.inboxUpdate(inboxDue{})
	if calls != 0 {
		t.Fatal("sender ran on the UI loop")
	}
	result := cmd().(inboxNotified)
	a.Update(result)
	if result.err != nil || result.sent != 1 || calls != 1 {
		t.Fatalf("tick %+v calls=%d", result, calls)
	}
	if a.r.FocusID() != focus || a.r.Page != page || len(a.r.Modals()) != 0 {
		t.Fatal("inbox notification navigated or opened a modal")
	}
	if got := ansi.Strip(a.View().Content); !strings.Contains(got, "Inbox: slack · Review the incident") {
		t.Fatalf("notice not rendered:\n%s", got)
	}
	notice := a.inbox.notices[it.ID].id
	press(t, a, "j")
	a.Update(noticeExpired{gen: a.notices.gen})
	if !slicesContainsNotice(a.shownNotices(), notice) {
		t.Fatal("inbox notice expired")
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "ochazuke.notice", Verb: "clear"}); err != nil {
		t.Fatal(err)
	}
	a.syncInboxNotices()
	if strings.Contains(ansi.Strip(a.View().Content), "Inbox: slack · Review the incident") {
		t.Fatal("dismissed inbox notice returned")
	}
	if err := a.r.Settings().Set(settingInboxInApp, false); err != nil {
		t.Fatal(err)
	}
	if err := a.r.Settings().Set(settingInboxInApp, true); err != nil {
		t.Fatal(err)
	}
	if len(a.shownNotices()) != 0 {
		t.Fatal("opt-in restored a dismissed notice")
	}
	if err := a.r.Settings().Set(settingInboxNotify, "off"); err != nil {
		t.Fatal(err)
	}
	if len(a.shownNotices()) != 0 {
		t.Fatal("inbox.notify off kept the notice")
	}
	if err := a.r.Settings().Set(settingInboxNotify, "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.inbox.items.SetRead(it.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.inboxLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	if len(a.shownNotices()) != 0 {
		t.Fatal("read item's notice survived")
	}
}

func TestInboxDismissalSurvivesReadsThresholdAndRestart(t *testing.T) {
	t.Parallel()
	a, now := inboxNotifyApp(t)
	a.notify.live = true
	a.m.Width, a.m.Height = 140, 36
	inboxNotifyTick(t, a)
	*now = now.Add(time.Millisecond)
	var ids []string
	for i := range 11 {
		it, _, err := a.inbox.items.Add(actionitems.Spec{Source: "slack", Title: fmt.Sprintf("old %d", i), Key: fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, it.ID)
	}
	sent := 0
	a.notify.exec = func(context.Context, []string) (string, error) { sent++; return "", nil }
	if result := inboxNotifyTick(t, a); result.err != nil || result.sent != 11 || sent != 1 {
		t.Fatalf("initial desktop summary: %+v sends=%d", result, sent)
	}
	if got := ansi.Strip(a.View().Content); !strings.Contains(got, "Inbox: 11 new action items") {
		t.Fatalf("initial chrome summary missing:\n%s", got)
	}
	executeA(t, a, "ochazuke.notice", "clear", "", nil)
	assertDismissed := func(app *App) {
		t.Helper()
		if got := ansi.Strip(app.View().Content); strings.Contains(got, "Notice: Inbox:") {
			t.Fatalf("dismissed items raised another chrome notice:\n%s", got)
		}
	}
	assertDismissed(a)
	other := openWith(t, Options{Data: a.dataPath})
	other.inbox.items.SetClock(func() int64 { return now.UnixMilli() })
	// A second connection first leaves ten unread, then crosses the summary
	// threshold to five; neither change acknowledges anything new.
	for i := range 6 {
		*now = now.Add(time.Millisecond)
		if _, err := other.inbox.items.SetRead(ids[i], true); err != nil {
			t.Fatal(err)
		}
		if i == 0 || i == 5 {
			if err := a.reloadInbox(); err != nil {
				t.Fatal(err)
			}
			assertDismissed(a)
		}
	}
	if counts, err := other.inbox.items.Counts(); err != nil || counts.Open != 11 || counts.Unread != 5 {
		t.Fatalf("notice dismissal changed inbox state: %+v %v", counts, err)
	}
	// A fresh runtime and database connection have no in-memory notice history.
	b := openWith(t, Options{Data: a.dataPath})
	b.clock = func() time.Time { return *now }
	b.inbox.items.SetClock(func() int64 { return now.UnixMilli() })
	b.inbox.after = nil
	b.notify.live = true
	b.m.Width, b.m.Height = 140, 36
	if err := b.r.Settings().Set(settingNotifyChannel, "os"); err != nil {
		t.Fatal(err)
	}
	var delivered []string
	b.notify.exec = func(_ context.Context, argv []string) (string, error) {
		delivered = append(delivered, strings.Join(argv, " "))
		return "", nil
	}
	if result := inboxNotifyTick(t, b); result.err != nil || result.sent != 0 || len(delivered) != 0 {
		t.Fatalf("restart repeated desktop notifications: %+v sends=%v", result, delivered)
	}
	assertDismissed(b)
	*now = now.Add(time.Millisecond)
	if _, _, err := other.inbox.items.Add(actionitems.Spec{Source: "slack", Title: "Only genuinely new item", Key: "fresh"}); err != nil {
		t.Fatal(err)
	}
	if result := inboxNotifyTick(t, b); result.err != nil || result.sent != 1 || len(delivered) != 1 ||
		!strings.Contains(delivered[0], "Only genuinely new item") {
		t.Fatalf("new-item desktop delivery: %+v sends=%v", result, delivered)
	}
	if got := ansi.Strip(b.View().Content); !strings.Contains(got, "Inbox: slack · Only genuinely new item") ||
		strings.Contains(got, "new action items") {
		t.Fatalf("chrome counted previously dismissed items:\n%s", got)
	}
}

func TestInboxNotifyFailingWatcherRaisesOnceAndArchivesOnRecovery(t *testing.T) {
	t.Parallel()
	a, now := inboxNotifyApp(t)
	inboxNotifyTick(t, a)
	*now = now.Add(time.Second)
	r := a.sched.runner
	r.Now = func() time.Time { return *now }
	r.LookPath = func(s string) (string, error) { return s, nil }
	failing := true
	r.Exec = func(context.Context, []string, []string) ([]byte, error) {
		if failing {
			return nil, errors.New("sign-in expired")
		}
		return nil, nil
	}
	def := schedules.Def{Name: "watcher", Command: []string{"fake-watcher"}, Interval: schedules.Duration(time.Minute)}
	run := func() inboxNotified {
		t.Helper()
		if _, err := r.RunNow(context.Background(), def); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Second)
		result := inboxNotifyTick(t, a)
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result
	}
	for range 2 {
		if result := run(); result.sent != 0 {
			t.Fatalf("early failure notified %+v", result)
		}
	}
	if result := run(); result.sent != 1 {
		t.Fatalf("threshold did not notify %+v", result)
	}
	items, err := a.inbox.items.All()
	if err != nil || len(items) != 1 || items[0].Key != "schedule-failing:watcher" || !items[0].Notify || items[0].Source != "mtm" {
		t.Fatalf("failure item %+v %v", items, err)
	}
	id := items[0].ID
	if _, err := a.inbox.items.Archive(id); err != nil {
		t.Fatal(err)
	}
	if result := run(); result.sent != 0 {
		t.Fatalf("ongoing failure notified again %+v", result)
	}
	item, err := a.inbox.items.Get(id)
	if err != nil || item.State(now.UnixMilli()) != actionitems.StateArchived {
		t.Fatalf("manual archive lost %+v %v", item, err)
	}
	failing = false
	run()
	item, err = a.inbox.items.Get(id)
	if err != nil || item.State(now.UnixMilli()) != actionitems.StateArchived || item.Extra["failing"] != false {
		t.Fatalf("recovery %+v %v", item, err)
	}
	failing = true
	for range 2 {
		run()
	}
	if result := run(); result.sent != 1 {
		t.Fatalf("new failure cycle not rearmed %+v", result)
	}
	items, err = a.inbox.items.All()
	if err != nil || len(items) != 1 || items[0].ID != id || items[0].State(now.UnixMilli()) != actionitems.StateOpen {
		t.Fatalf("failure cycle duplicated row %+v %v", items, err)
	}
	failing = false
	run()
	item, err = a.inbox.items.Get(id)
	if err != nil || item.State(now.UnixMilli()) != actionitems.StateArchived {
		t.Fatalf("success did not archive %+v %v", item, err)
	}
}

func TestInboxNotifyWatcherProjectionWithoutAnotherTick(t *testing.T) {
	t.Parallel()
	a, now := inboxNotifyApp(t)
	r := a.sched.runner
	r.Now = func() time.Time { return *now }
	r.LookPath = func(s string) (string, error) { return s, nil }
	failing := true
	r.Exec = func(context.Context, []string, []string) ([]byte, error) {
		if failing {
			return nil, errors.New("offline")
		}
		return nil, nil
	}
	def := schedules.Def{Name: "watcher", Command: []string{"fake-watcher"}, Interval: schedules.Duration(time.Minute)}
	if err := a.r.Settings().Set(settingNotifyFailures, 0); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := r.RunNow(context.Background(), def); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Second)
	}
	items, err := a.inbox.items.All()
	if err != nil || len(items) != 0 {
		t.Fatalf("disabled projection %+v %v", items, err)
	}
	if err := a.r.Settings().Set(settingNotifyFailures, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunNow(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	items, err = a.inbox.items.All()
	if err != nil || len(items) != 1 || items[0].State(now.UnixMilli()) != actionitems.StateOpen {
		t.Fatalf("record did not project failure %+v %v", items, err)
	}
	failing = false
	*now = now.Add(time.Second)
	if _, err := r.RunNow(context.Background(), def); err != nil {
		t.Fatal(err)
	}
	recovered, err := a.inbox.items.Get(items[0].ID)
	if err != nil || recovered.State(now.UnixMilli()) != actionitems.StateArchived {
		t.Fatalf("record did not project recovery %+v %v", recovered, err)
	}
}
