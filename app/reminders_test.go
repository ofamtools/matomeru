package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
)

type testRemindersFixture struct {
	app   *App
	drv   *Driver
	store *data.Store
	now   time.Time
	osLog []string
}

func newRemindersFixture(t *testing.T) *testRemindersFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}
	f := &testRemindersFixture{
		store: store,
		now:   time.Date(2026, 11, 27, 9, 0, 0, 0, time.Local),
	}
	a, err := New(store, Options{
		FS:  smokeFS(),
		Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	a.reminders.notifier = daemon.NotifierFunc(func(_ context.Context, title, body string) error {
		f.osLog = append(f.osLog, title+"|"+body)
		return nil
	})
	f.app = a
	f.drv = NewDriver(a, 120, 36)
	return f
}

func TestRemindersTopNavTabAndOptOut(t *testing.T) {
	f := newRemindersFixture(t)
	a, d := f.app, f.drv

	// 1. Reminders is a top-level chrome tab by default, leaving Runs completely untouched.
	tabs := a.topTabs()
	if !slices.ContainsFunc(tabs, func(tb topTabDef) bool { return tb.id == SpaceReminders }) {
		t.Fatalf("topTabs() missing SpaceReminders: %+v", tabs)
	}
	if !slices.ContainsFunc(tabs, func(tb topTabDef) bool { return tb.id == SpaceRuns }) {
		t.Fatalf("topTabs() missing SpaceRuns: %+v", tabs)
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Reminders") || !strings.Contains(frame, "Runs") {
		t.Fatalf("top chrome bar missing Reminders or Runs:\n%s", frame)
	}

	// 2. Open via global g M binding.
	d.Keys("g", "M")
	if a.Space() != SpaceReminders || a.Focus() != views.RemindersListID {
		t.Fatalf("g M -> space=%q focus=%q", a.Space(), a.Focus())
	}

	// 3. Open via ' Pages Picker.
	d.Keys("1", "'")
	d.Type("Reminders")
	d.Keys("enter")
	if a.Space() != SpaceReminders {
		t.Fatalf("' Pages Picker -> Reminders failed: space=%q", a.Space())
	}

	// 4. Open via Command Palette (space.reminders).
	d.Keys("3", ":")
	d.Type("space.reminders")
	d.Keys("enter")
	if a.Space() != SpaceReminders {
		t.Fatalf("palette space.reminders failed: space=%q", a.Space())
	}

	// 5. Opt-out via ui.remindersTopNavTab = false hides top tab, while g M still opens it.
	a.cfg.userSettings[SetRemindersTopNavTab] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("1")
	if slices.ContainsFunc(a.topTabs(), func(tb topTabDef) bool { return tb.id == SpaceReminders }) {
		t.Fatalf("topTabs() still contains SpaceReminders when %s=false", SetRemindersTopNavTab)
	}
	d.Keys("g", "M")
	if a.Space() != SpaceReminders {
		t.Fatalf("g M with opt-out failed to open standalone Reminders: space=%q", a.Space())
	}
}

func TestRemindersDailyCheckOffNightlyResetAndPermanentHistory(t *testing.T) {
	f := newRemindersFixture(t)
	a, d := f.app, f.drv

	_, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":         "Take morning vitamins",
			"scheduleKind":  data.ScheduleKindDaily,
			"timeOfDay":     "09:00",
			"notifyChannel": data.NotifyChannelBoth,
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	d.Keys("g", "M", "v") // cycle all -> daily
	if a.reminders.viewMode != views.RemindersViewDaily {
		t.Fatalf("v did not switch to daily view: %q", a.reminders.viewMode)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Take morning vitamins") || !strings.Contains(frame, "[ ]") || !strings.Contains(frame, "Daily 0/1") {
		t.Fatalf("daily view before check-off:\n%s", frame)
	}

	// Check off with space.
	d.Keys("space")
	if frame := d.Frame(); !strings.Contains(frame, "[✓]") || !strings.Contains(frame, "Daily 1/1") {
		t.Fatalf("daily view after space check-off:\n%s", frame)
	}

	// Uncheck today with x, then re-check with space.
	d.Keys("x")
	if frame := d.Frame(); !strings.Contains(frame, "[ ]") || !strings.Contains(frame, "Daily 0/1") {
		t.Fatalf("daily view after x uncheck:\n%s", frame)
	}
	d.Keys("space")
	if frame := d.Frame(); !strings.Contains(frame, "[✓]") || !strings.Contains(frame, "Daily 1/1") {
		t.Fatalf("daily view after re-check:\n%s", frame)
	}

	// Advance past local midnight -> resets to unchecked today while preserving streak & permanent history.
	f.now = f.now.AddDate(0, 0, 1)
	a.viewMemo.gen++
	a.Shell.Update(wakeMsg())
	nextDayFrame := d.Frame()
	if !strings.Contains(nextDayFrame, "[ ]") || !strings.Contains(nextDayFrame, "Daily 0/1") || !strings.Contains(nextDayFrame, "1d") {
		t.Fatalf("after nightly reset expected unchecked today + 1d streak:\n%s", nextDayFrame)
	}

	// Switch to History sub-view via ) (canonical view.cycle): daily -> scheduled -> pending -> history.
	d.Keys(")", ")", ")")
	if a.reminders.viewMode != views.RemindersViewHistory {
		t.Fatalf("expected history view, got %q", a.reminders.viewMode)
	}
	histFrame := d.Frame()
	if !strings.Contains(histFrame, "Take morning vitamins") || !strings.Contains(histFrame, "2026-11-27") || !strings.Contains(histFrame, "✓ Check-off") {
		t.Fatalf("permanent history missing yesterday's check-off:\n%s", histFrame)
	}
}

func TestRemindersTopRightTemporary30sAndPersistentNotifications(t *testing.T) {
	f := newRemindersFixture(t)
	a, d := f.app, f.drv

	tempSched, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":             "Standup check-in",
			"scheduleKind":      data.ScheduleKindRemind,
			"cron":              "0 10 * * *",
			"notifyChannel":     data.NotifyChannelBoth,
			"notifyPersistence": data.NotifyPersistenceTemporary,
			"reminderMessage":   "Join Zoom room 2",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	persSched, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":             "Submit expense report",
			"scheduleKind":      data.ScheduleKindRemind,
			"cron":              "0 16 * * 5",
			"notifyChannel":     data.NotifyChannelBoth,
			"notifyPersistence": data.NotifyPersistencePersistent,
			"reminderMessage":   "Finance cutoff at 5pm",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Test-fire temporary reminder (default 30s TTL) -> OS notified + top-right overlay rendered.
	a.fireReminderNow(*tempSched)
	a.Shell.Update(wakeMsg())
	if len(f.osLog) != 1 || !strings.Contains(f.osLog[0], "Standup check-in") {
		t.Fatalf("expected 1 OS notification for Standup check-in, got %v", f.osLog)
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Standup check-in") || !strings.Contains(frame, "Join Zoom room 2") {
		t.Fatalf("top-right temporary notification not rendered:\n%s", frame)
	}
	// Verify top-right placement in solved layout (X >= 60, Y <= 4 on 120x36).
	ovRect, ok := a.State().Layout.Rects["modal."+reminderNotifyOverlayID]
	if !ok || ovRect.X < 60 || ovRect.Y > 4 {
		t.Fatalf("expected top-right overlay placement (X>=60, Y<=4), got ok=%v rect=%+v", ok, ovRect)
	}

	// Dedupe check: re-syncing does not duplicate the same occurrence toast.
	a.reminders.lastSyncOK = false
	a.syncReminderNotifications(f.now)
	if got := len(a.activeReminderToasts(f.now)); got != 1 {
		t.Fatalf("expected 1 deduped temporary toast, got %d", got)
	}

	// Still visible at +29s, auto-disappears at +31s.
	f.now = f.now.Add(29 * time.Second)
	a.Shell.Update(wakeMsg())
	if got := len(a.activeReminderToasts(f.now)); got != 1 {
		t.Fatalf("temporary 30s toast expired early at +29s: %d", got)
	}
	f.now = f.now.Add(2 * time.Second)
	a.Shell.Update(wakeMsg())
	if got := len(a.activeReminderToasts(f.now)); got != 0 {
		t.Fatalf("temporary 30s toast did not expire at +31s: %d", got)
	}
	if strings.Contains(d.Frame(), "Join Zoom room 2") {
		t.Fatalf("expired temporary toast still rendered:\n%s", d.Frame())
	}

	// 2. Test-fire persistent reminder -> stays pinned even after +5 minutes until acknowledged.
	a.fireReminderNow(*persSched)
	a.Shell.Update(wakeMsg())
	if len(f.osLog) != 2 || !strings.Contains(f.osLog[1], "Submit expense report") {
		t.Fatalf("expected OS notification for persistent reminder, got %v", f.osLog)
	}
	f.now = f.now.Add(5 * time.Minute)
	a.Shell.Update(wakeMsg())
	if !strings.Contains(d.Frame(), "Submit expense report") || !strings.Contains(d.Frame(), "Finance cutoff at 5pm") {
		t.Fatalf("persistent alert disappeared before acknowledgement:\n%s", d.Frame())
	}

	// 3. Opt-out ui.reminderToasts = false suppresses the top-right overlay.
	a.cfg.userSettings[SetReminderToasts] = false
	a.reloadSettings()
	a.applySettings()
	a.Shell.Update(wakeMsg())
	if strings.Contains(d.Frame(), "Finance cutoff at 5pm") {
		t.Fatalf("ui.reminderToasts=false still rendered top-right overlay:\n%s", d.Frame())
	}
	a.cfg.userSettings[SetReminderToasts] = true
	a.reloadSettings()
	a.applySettings()
	a.Shell.Update(wakeMsg())

	// 4. Fire a second persistent alert, jump via g N from 2 Work, navigate j/k, ack one, esc returns focus, then ctrl+a g N from live terminal!
	a.fireReminderNow(*persSched)
	a.Shell.Update(wakeMsg())
	d.Keys("2")
	prevFocus := a.Focus()
	d.Keys("g", "N")
	if a.Modal() != reminderAlertsModalID || a.Mode() != ModeReminderAlerts {
		t.Fatalf("g N did not focus top-right persistent alerts overlay: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	modalRect, ok := a.State().Layout.Rects["modal."+reminderAlertsModalID]
	if !ok || modalRect.X < 55 || modalRect.Y > 4 {
		t.Fatalf("focused persistent alerts overlay not anchored top-right: ok=%v rect=%+v", ok, modalRect)
	}
	// Navigate down and acknowledge 1 of 2; modal stays open with 1 remaining.
	d.Keys("j", "a")
	if a.Modal() != reminderAlertsModalID || len(a.mtmPendingAlerts()) != 1 {
		t.Fatalf("after acking 1 of 2: modal=%q remaining=%d", a.Modal(), len(a.mtmPendingAlerts()))
	}
	// Esc closes overlay and returns cleanly to previous focus on 2 Work.
	d.Keys("esc")
	if a.Modal() != "" || a.Space() != SpaceWork || a.Focus() != prevFocus {
		t.Fatalf("esc did not restore previous focus: modal=%q space=%q focus=%q (want %q)", a.Modal(), a.Space(), a.Focus(), prevFocus)
	}

	// 5. Leader-mirrored ctrl+a g N from inside live terminal!
	d.Keys("1", "enter") // enter ModeTerminal on selected pane
	if a.Mode() != ModeTerminal {
		t.Fatalf("expected ModeTerminal, got %q", a.Mode())
	}
	d.Keys("ctrl+a", "g", "N")
	if a.Modal() != reminderAlertsModalID || a.Mode() != ModeReminderAlerts {
		t.Fatalf("ctrl+a g N from live terminal failed: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Acknowledge remaining alert -> closes overlay and returns to ModeTerminal!
	d.Keys("enter")
	if a.Modal() != "" || a.Mode() != ModeTerminal || len(a.mtmPendingAlerts()) != 0 {
		t.Fatalf("acking last alert from terminal did not restore ModeTerminal: modal=%q mode=%q pending=%d",
			a.Modal(), a.Mode(), len(a.mtmPendingAlerts()))
	}
}

func TestRemindersPageActionsCreateEditPauseFilterArchive(t *testing.T) {
	f := newRemindersFixture(t)
	a, d := f.app, f.drv

	d.Keys("g", "M")
	// 1. Create a new reminder via n -> form modal.
	d.Keys("n")
	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("n on Reminders did not open create form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("i")
	d.Type("Water plants")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not submit new reminder form: modal=%q status=%q", a.Modal(), a.Status())
	}
	if !strings.Contains(d.Frame(), "Water plants") {
		t.Fatalf("newly created reminder not visible on Reminders page:\n%s", d.Frame())
	}

	// 2. Universal filter via / narrows list to Water plants.
	d.Keys("/")
	d.Type("Water plants")
	d.Keys("enter")
	if len(a.reminders.visible) != 1 || a.reminders.visible[0].Title != "Water plants" {
		t.Fatalf("filter did not narrow to Water plants: %+v", a.reminders.visible)
	}

	// 3. Edit via e opens record edit form on the schedule.
	d.Keys("e")
	if a.Modal() != "form" {
		t.Fatalf("e did not open edit form: modal=%q", a.Modal())
	}
	d.Keys("esc", "esc")

	// 4. Pause and resume via p.
	d.Keys("p")
	if !a.reminders.visible[0].Paused {
		// Re-sync after command.
		a.syncReminders()
	}
	if !a.reminders.visible[0].Paused {
		t.Fatalf("p did not pause reminder: %+v", a.reminders.visible[0])
	}
	d.Keys("p")
	a.syncReminders()
	if a.reminders.visible[0].Paused {
		t.Fatalf("second p did not resume reminder: %+v", a.reminders.visible[0])
	}

	// 5. Test-fire via t creates a notification occurrence.
	d.Keys("t")
	if !strings.Contains(a.Status(), "test-fired Water plants") {
		t.Fatalf("t did not test-fire reminder: status=%q", a.Status())
	}

	// 6. Archive via d removes it from active list.
	d.Keys("d")
	a.syncReminders()
	if len(a.reminders.visible) != 0 {
		t.Fatalf("d did not archive Water plants: visible=%+v", a.reminders.visible)
	}

	// 7. Verify 'o' in persistent alerts modal opens Reminders page cleanly.
	sched, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindSchedule,
		Fields: map[string]any{
			"title":             "Renew TLS cert",
			"scheduleKind":      data.ScheduleKindRemind,
			"notifyChannel":     data.NotifyChannelMTM,
			"notifyPersistence": data.NotifyPersistencePersistent,
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	a.fireReminderNow(*sched)
	d.Keys("2", "g", "N", "o")
	if a.Modal() != "" || a.Space() != SpaceReminders {
		t.Fatalf("o in persistent alerts modal did not open Reminders: modal=%q space=%q", a.Modal(), a.Space())
	}
	// A acknowledges all pending alerts directly from Reminders page.
	d.Keys("A")
	if len(a.mtmPendingAlerts()) != 0 {
		t.Fatalf("A on Reminders page did not ack all pending alerts: %d", len(a.mtmPendingAlerts()))
	}
}
