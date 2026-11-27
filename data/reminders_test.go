package data

import (
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func TestDailyReminderCheckOffNightlyResetAndHistory(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	loc := time.FixedZone("EST", -5*3600)

	sched, _, err := store.Create(CreateInput{
		Kind: KindSchedule,
		Fields: map[string]any{
			"title":        "Stretch & hydrate",
			"scheduleKind": ScheduleKindDaily,
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// Day 1 (Nov 25, 21:00 EST): check off.
	day1 := time.Date(2026, 11, 25, 21, 0, 0, 0, loc)
	checked, _, err := store.ToggleDailyCheckOff(sched.Ref(), day1, owner)
	if err != nil || !checked {
		t.Fatalf("day1 check-off: checked=%v err=%v", checked, err)
	}

	// Day 2 (Nov 26, 23:55 EST): check off -> streak 2.
	day2 := time.Date(2026, 11, 26, 23, 55, 0, 0, loc)
	checked, _, err = store.ToggleDailyCheckOff(sched.Ref(), day2, owner)
	if err != nil || !checked {
		t.Fatalf("day2 check-off: checked=%v err=%v", checked, err)
	}

	occs, err := store.Query(Query{Kind: KindOccurrence})
	if err != nil {
		t.Fatal(err)
	}
	st2 := ComputeDailyStatus(sched.ID, occs.Items, day2)
	if !st2.CheckedToday || st2.Streak != 2 || st2.TotalChecks != 2 || st2.Last7Days != "·····✓✓" {
		t.Fatalf("day2 status = %+v", st2)
	}

	// Cross midnight -> Day 3 (Nov 27, 00:05 EST): automatically resets to unchecked while keeping prior 2 days!
	day3 := time.Date(2026, 11, 27, 0, 5, 0, 0, loc)
	st3 := ComputeDailyStatus(sched.ID, occs.Items, day3)
	if st3.CheckedToday {
		t.Fatalf("expected nightly reset to unchecked at %v, got %+v", day3, st3)
	}
	if st3.Streak != 2 || st3.TotalChecks != 2 || st3.Last7Days != "····✓✓○" {
		t.Fatalf("day3 pre-check status = %+v, want streak=2 total=2 last7=····✓✓○", st3)
	}

	// Check off Day 3, then uncheck Day 3 -> Day 1 & Day 2 stay permanently intact!
	checked, _, err = store.ToggleDailyCheckOff(sched.Ref(), day3, owner)
	if err != nil || !checked {
		t.Fatalf("day3 check-off: checked=%v err=%v", checked, err)
	}
	checked, _, err = store.ToggleDailyCheckOff(sched.Ref(), day3.Add(time.Minute), owner)
	if err != nil || checked {
		t.Fatalf("day3 uncheck: checked=%v err=%v", checked, err)
	}
	occs, err = store.Query(Query{Kind: KindOccurrence})
	if err != nil {
		t.Fatal(err)
	}
	st3After := ComputeDailyStatus(sched.ID, occs.Items, day3.Add(2*time.Minute))
	if st3After.CheckedToday || st3After.TotalChecks != 2 || st3After.Streak != 2 {
		t.Fatalf("after uncheck day3 status = %+v, want unchecked with 2 prior days preserved", st3After)
	}

	// Permanent history log keeps all 3 occurrence records newest-first.
	hist := BuildReminderHistory([]Record{*sched}, occs.Items, loc)
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}
	if hist[0].DueDate != "2026-11-27" || hist[0].OutcomeStatus != "skipped" {
		t.Fatalf("newest history entry = %+v", hist[0])
	}
	if hist[1].DueDate != "2026-11-26" || hist[1].OutcomeStatus != "done" ||
		hist[2].DueDate != "2026-11-25" || hist[2].OutcomeStatus != "done" {
		t.Fatalf("prior day history entries altered: %+v", hist)
	}
}

func TestReminderChannelPersistenceDefaultsAndAck(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	silentDaily := Record{Fields: map[string]any{"scheduleKind": ScheduleKindDaily}}
	if got := EffectiveNotifyChannel(silentDaily); got != NotifyChannelNone {
		t.Fatalf("silent daily channel = %q, want %q", got, NotifyChannelNone)
	}
	timedDaily := Record{Fields: map[string]any{"scheduleKind": ScheduleKindDaily, "timeOfDay": "09:30"}}
	if got := EffectiveNotifyChannel(timedDaily); got != NotifyChannelBoth {
		t.Fatalf("timed daily channel = %q, want %q", got, NotifyChannelBoth)
	}
	if got := CronForTimeOfDay("09:30"); got != "30 9 * * *" {
		t.Fatalf("CronForTimeOfDay(09:30) = %q, want 30 9 * * *", got)
	}
	legacyRemind := Record{Fields: map[string]any{"scheduleKind": ScheduleKindRemind}}
	if got := EffectiveNotifyChannel(legacyRemind); got != NotifyChannelBoth {
		t.Fatalf("legacy remind channel = %q, want %q", got, NotifyChannelBoth)
	}
	if got := EffectiveNotifyPersistence(legacyRemind); got != NotifyPersistenceTemporary {
		t.Fatalf("legacy persistence = %q, want %q", got, NotifyPersistenceTemporary)
	}
	if got := EffectiveNotifyTTL(legacyRemind); got != 30*time.Second {
		t.Fatalf("default TTL = %v, want 30s", got)
	}

	sched, _, err := store.Create(CreateInput{
		Kind: KindSchedule,
		Fields: map[string]any{
			"title":             "Submit payroll",
			"scheduleKind":      ScheduleKindRemind,
			"notifyChannel":     NotifyChannelMTM,
			"notifyPersistence": NotifyPersistencePersistent,
			"reminderMessage":   "Before 5pm Friday",
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 16, 0, 0, 0, time.UTC)
	occ, _, err := store.Create(CreateInput{
		Kind: KindOccurrence,
		Fields: map[string]any{
			"schedule":      sched.Ref(),
			"dueAt":         now.Format(TimeFormat),
			"notifiedAt":    now.Format(TimeFormat),
			"outcomeStatus": "pending",
			"ackRequired":   true,
			"notifyChannel": NotifyChannelMTM,
			"message":       "Before 5pm Friday",
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	alerts := PendingPersistentAlerts([]Record{*sched}, []Record{*occ})
	if len(alerts) != 1 || alerts[0].Title != "Submit payroll" || alerts[0].Message != "Before 5pm Friday" {
		t.Fatalf("PendingPersistentAlerts = %+v", alerts)
	}

	ackAt := now.Add(5 * time.Minute)
	acked, err := store.AcknowledgeOccurrence(occ.Ref(), ackAt, owner)
	if err != nil {
		t.Fatal(err)
	}
	if acked.Bool("ackRequired") || acked.Text("outcomeStatus") != "done" || acked.Time("acknowledgedAt").IsZero() {
		t.Fatalf("acked occurrence = %+v", acked.Fields)
	}
	if got := PendingPersistentAlerts([]Record{*sched}, []Record{*acked}); len(got) != 0 {
		t.Fatalf("expected 0 pending alerts after ack, got %+v", got)
	}
}
