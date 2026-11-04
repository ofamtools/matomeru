package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

func reminderApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a := openWith(t, Options{Data: filepath.Join(t.TempDir(), "mtm.db")})
	now := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	a.clock = func() time.Time { return now }
	a.loc = time.UTC
	a.reminders.notify.after = nil
	a.reminders.notify.output = io.Discard
	a.reminders.notify.exec = func(context.Context, []string) (string, error) {
		return "", errors.New("unexpected notification command")
	}
	return a, &now
}
func createReminder(t *testing.T, a *App, title string) string {
	t.Helper()
	out, err := a.r.Execute(ochazuke.Request{Noun: reminderNoun, Verb: "create", Args: ochazuke.Arguments{"title": title, "time": "10:30", "days": "daily"}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := ochazuke.ThingOf(out)
	if err != nil {
		t.Fatal(err)
	}
	return v["id"].(string)
}

func TestReminderKeyMidnightHistoryAndCLI(t *testing.T) {
	t.Parallel()
	a, now := reminderApp(t)
	id := createReminder(t, a, "water the plants")
	a.m.Width, a.m.Height = 120, 36
	if err := a.r.OpenPage(schedulesPage); err != nil {
		t.Fatal(err)
	}
	a.View()
	if err := a.reminders.list.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "x")
	yesterday := now.Format(time.DateOnly)
	*now = now.AddDate(0, 0, 1)
	a.Update(nil)
	screen := ansi.Strip(a.View().Content)
	if !strings.Contains(screen, "☐ water the plants") || !strings.Contains(screen, yesterday) {
		t.Fatalf("rollover frame:\n%s", screen)
	}
	hs, err := a.reminders.store.History(id, *now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hs[0].State != "missed" || hs[0].Done || hs[1].State != "done" || hs[1].Date != yesterday {
		t.Fatalf("rollover %+v", hs)
	}
	var out, stderr bytes.Buffer
	if err := a.shell(strings.NewReader(""), &out, &stderr).Run(context.Background(), []string{"reminder", "toggle", "water the plants"}); err != nil {
		t.Fatalf("CLI %v, %s", err, stderr.String())
	}
	d, err := a.reminders.store.Day(id, now.Format(time.DateOnly))
	if err != nil || !d.Done {
		t.Fatalf("CLI check %+v %v", d, err)
	}
	if !strings.Contains(out.String(), `"done": true`) {
		t.Fatalf("CLI output %s", out.String())
	}
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestReminderArchivePreservesHistory(t *testing.T) {
	t.Parallel()
	a, now := reminderApp(t)
	id := createReminder(t, a, "water the plants")
	for _, verb := range []string{"check", "archive", "restore"} {
		args := ochazuke.Arguments{"id": id}
		if verb == "check" {
			args = ochazuke.Arguments{"name": id}
		}
		_, err := a.r.Execute(ochazuke.Request{Noun: reminderNoun, Verb: verb, Args: args})
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		r, err := a.reminders.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if verb == "archive" && !r.Current().Archived {
			t.Fatal("archive did not set archived")
		}
		if verb == "restore" && r.Current().Archived {
			t.Fatal("restore did not clear archived")
		}
	}
	hs, err := a.reminders.store.History(id, *now, 0)
	if err != nil || hs[0].State != "done" {
		t.Fatalf("archived history %+v %v", hs, err)
	}
}

func TestReminderSenderArgvAndPartialFailure(t *testing.T) {
	t.Parallel()
	a, _ := reminderApp(t)
	title := "water the \"plants\"\nthen rest"
	id := createReminder(t, a, title)
	for key, value := range map[string]any{settingReminderNotify: "both", settingReminderCommands: `[["extra","{{title}}","{{body}}"]]`} {
		if err := a.r.Settings().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	var terminal bytes.Buffer
	a.reminders.notify.output = &terminal
	var calls [][]string
	a.reminders.notify.exec = func(_ context.Context, argv []string) (string, error) {
		calls = append(calls, argv)
		if argv[0] == "extra" {
			return "offline", errors.New("fake failure")
		}
		return "", nil
	}
	work, err := a.notificationWork()
	if err != nil {
		t.Fatal(err)
	}
	n, err := work(context.Background())
	if n != 1 || err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("partial %d %v", n, err)
	}
	body := "Due 10:30 · check off in mtm"
	want := [][]string{{"/usr/bin/osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", "--", title, body}, {"extra", title, body}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("argv = %#v, want %#v", calls, want)
	}
	if got := terminal.String(); got != "\x1b]9;mtm: water the \"plants\"then rest · "+body+"\x1b\\" {
		t.Fatalf("terminal %q", got)
	}
	d, err := a.reminders.store.Day(id, "2026-11-02")
	if err != nil || d.NotifiedOn != "2026-11-02" {
		t.Fatalf("notified day %+v %v", d, err)
	}
	work, err = a.notificationWork()
	if err != nil {
		t.Fatal(err)
	}
	n, err = work(context.Background())
	if err != nil || n != 0 || len(calls) != 2 {
		t.Fatalf("repeat %d %v, calls %#v", n, err, calls)
	}
}

func TestReminderTerminalSanitizesControls(t *testing.T) {
	t.Parallel()
	got := terminalReminder("plants\x1b\a\n\r\t\x9d\u009b", "body\x1b\a\n")
	// Invalid UTF-8 is a replacement rune, not a C1 byte in UTF-8 output.
	want := "\x1b]9;mtm: plants� · body\x1b\\"
	if got != want {
		t.Fatalf("OSC 9 = %q, want %q", got, want)
	}
	if got := terminalReminder("4;danger", "body"); got != "\x1b]9;mtm: 4;danger · body\x1b\\" {
		t.Fatalf("OSC 9 numeric title became a subcommand: %q", got)
	}
}

func TestReminderNoticePersistsDismissesAndOptOut(t *testing.T) {
	t.Parallel()
	a, _ := reminderApp(t)
	id := createReminder(t, a, "water the plants")
	a.reminders.notify.live = true
	a.syncReminderNotices()
	if len(a.reminders.notify.notices) != 1 {
		t.Fatalf("notices %v", a.reminders.notify.notices)
	}
	var notice ochazuke.NoticeID
	for _, n := range a.reminders.notify.notices {
		notice = n
	}
	focus := a.r.Focus
	press(t, a, "j")
	a.Update(noticeExpired{gen: a.notices.gen})
	if !slicesContainsNotice(a.shownNotices(), notice) {
		t.Fatal("due notice expired on key or timer")
	}
	if a.r.Focus == "reminder:"+id || focus == "reminder:"+id {
		t.Fatal("notice stole focus")
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "ochazuke.notice", Verb: "clear"}); err != nil {
		t.Fatal(err)
	}
	a.syncReminderNotices()
	if len(a.m.Notices) != 0 {
		t.Fatal("dismissed notice came back")
	}
	if err := a.r.Settings().Set(settingReminderInApp, false); err != nil {
		t.Fatal(err)
	}
	if len(a.reminders.notify.notices) != 0 {
		t.Fatal("opt out retained notices")
	}
	if err := a.r.Settings().Set(settingReminderInApp, true); err != nil {
		t.Fatal(err)
	}
	if len(a.shownNotices()) != 1 {
		t.Fatal("opt in did not show due reminder")
	}
	_, err := a.r.Execute(ochazuke.Request{Noun: reminderNoun, Verb: "check", Args: ochazuke.Arguments{"name": id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.shownNotices()) != 0 {
		t.Fatal("checked reminder's notice survived")
	}
}
func slicesContainsNotice(ns []ochazuke.NoticeID, id ochazuke.NoticeID) bool {
	for _, n := range ns {
		if n == id {
			return true
		}
	}
	return false
}

func TestReminderTimerRunsOffLoopAndRetries(t *testing.T) {
	t.Parallel()
	a, _ := reminderApp(t)
	createReminder(t, a, "water the plants")
	a.reminders.notify.live = true
	if err := a.r.Settings().Set(settingReminderNotify, "os"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.reminders.notify.exec = func(context.Context, []string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("offline")
		}
		return "", nil
	}
	var delay time.Duration
	a.reminders.notify.after = func(d time.Duration, msg tea.Msg) tea.Cmd { delay = d; return func() tea.Msg { return msg } }
	cmd := a.reminderNotificationsUpdate(reminderDue{})
	if calls != 0 {
		t.Fatal("notification ran on the update loop")
	}
	first := cmd().(reminderNotified)
	if first.err == nil || first.count != 0 {
		t.Fatalf("first %+v", first)
	}
	tick := a.reminderNotificationsUpdate(first)
	if delay != time.Minute {
		t.Fatalf("retry delay %v", delay)
	}
	second := a.reminderNotificationsUpdate(tick())().(reminderNotified)
	if second.err != nil || second.count != 1 {
		t.Fatalf("retry %+v", second)
	}
	a.reminderNotificationsUpdate(second)
	third := a.reminderNotificationsUpdate(reminderDue{})().(reminderNotified)
	if third.err != nil || third.count != 0 || calls != 2 {
		t.Fatalf("repeat %+v, calls %d", third, calls)
	}
}

func TestReminderShowOptOutRemovesListAndFocusStop(t *testing.T) {
	t.Parallel()
	a, _ := reminderApp(t)
	createReminder(t, a, "water the plants")
	if err := a.r.Settings().Set(settingRemindersShow, false); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(schedulesPage); err != nil {
		t.Fatal(err)
	}
	a.m.Width, a.m.Height = 120, 36
	if frame := ansi.Strip(a.View().Content); strings.Contains(frame, "Daily reminders") || strings.Contains(frame, "water the plants") {
		t.Fatalf("hidden reminder is visible:\n%s", frame)
	}
	if err := a.sched.list.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "tab")
	if strings.HasPrefix(a.r.Focus, reminderListID) || strings.HasPrefix(a.r.Focus, "reminder:") {
		t.Fatalf("hidden reminder has focus %s", a.r.Focus)
	}
}
