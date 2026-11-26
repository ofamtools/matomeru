package data

import (
	"slices"
	"testing"
	"time"
)

var focusNoon = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func TestFocusConfigRoundTripsThroughFocusJSON(t *testing.T) {
	dir := t.TempDir()
	if cfg, err := LoadFocusConfig(dir); err != nil || cfg.PriorityDate != "" || cfg.ActivePomodoro != nil {
		t.Fatalf("a missing focus.json is not an empty config: %+v, %v", cfg, err)
	}
	cfg := TogglePriorityProject(FocusConfig{CaptureBucketID: "bucket"}, "apollo", focusNoon)
	cfg = StartPomodoro(cfg, "Parser", []string{"apollo"}, 0, "", focusNoon)
	cfg = SetTimeBudget(cfg, ProjectTimeBudget{ProjectID: "apollo", WindowMinutes: 90, CooldownPomodoros: 2})
	if err := SaveFocusConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFocusConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.PriorityDate != "2026-11-26" || !slices.Equal(got.PriorityProjects, []string{"apollo"}) || got.CaptureBucketID != "bucket" ||
		got.ActivePomodoro == nil || got.ActivePomodoro.DurationMin != DefaultPomodoroMinutes || !got.ActivePomodoro.EndsAt.Equal(focusNoon.Add(25*time.Minute)) ||
		len(got.TimeBudgets) != 1 || got.TimeBudgets[0].CooldownPomodoros != 2 {
		t.Fatalf("focus.json did not round-trip: %+v", got)
	}
	if err := SaveFocusConfig("", cfg); err != nil {
		t.Fatalf("saving with no config dir: %v", err)
	}
}

func TestPriorityProjectsHoldForTheirDayOnly(t *testing.T) {
	cfg := FocusConfig{CaptureBucketID: "bucket"}
	if IsPriorityProject(cfg, "bucket", focusNoon) {
		t.Fatal("with no priority set the capture bucket is a priority")
	}
	cfg = TogglePriorityProject(cfg, "apollo", focusNoon)
	for id, want := range map[string]bool{"apollo": true, "bucket": true, "borealis": false, "": false} {
		if got := IsPriorityProject(cfg, id, focusNoon); got != want {
			t.Errorf("IsPriorityProject(%q) = %v, want %v", id, got, want)
		}
	}
	tomorrow := focusNoon.AddDate(0, 0, 1)
	if IsPriorityProject(cfg, "apollo", tomorrow) {
		t.Fatal("yesterday's priority is still a priority")
	}
	// Tomorrow's first toggle starts a new set; a second toggle takes it out.
	cfg = TogglePriorityProject(cfg, "borealis", tomorrow)
	if !slices.Equal(cfg.PriorityProjects, []string{"borealis"}) || cfg.PriorityDate != "2026-11-27" {
		t.Fatalf("a new day did not start a new set: %+v", cfg)
	}
	if cfg = TogglePriorityProject(cfg, "borealis", tomorrow); len(cfg.PriorityProjects) != 0 {
		t.Fatalf("toggling again kept the project: %+v", cfg.PriorityProjects)
	}
}

func TestPomodoroLocksOtherProjectsUntilItEnds(t *testing.T) {
	cfg := StartPomodoro(FocusConfig{CaptureBucketID: "bucket"}, "", []string{"apollo"}, 30, "", focusNoon)
	p := cfg.ActivePomodoro
	if p.Status != PomodoroRunning || p.Reason != PomodoroReasonManual || p.Remaining(focusNoon.Add(10*time.Minute)) != 20*time.Minute {
		t.Fatalf("started Pomodoro: %+v", p)
	}
	at := focusNoon.Add(10 * time.Minute)
	if d := EvaluateProjectAccess(cfg, "apollo", at); !d.Allowed || d.Locked {
		t.Fatalf("the allowed project is locked: %+v", d)
	}
	if d := EvaluateProjectAccess(cfg, "bucket", at); d.Locked {
		t.Fatalf("the capture bucket is locked by a Pomodoro: %+v", d)
	}
	d := EvaluateProjectAccess(cfg, "borealis", at)
	if !d.Locked || d.Allowed || d.Source != FocusSourcePomodoro || !d.UnlockAt.Equal(p.EndsAt) || d.Reason == "" {
		t.Fatalf("another project is not locked by the Pomodoro: %+v", d)
	}
	// Time is up: the lock is gone even before CompletePomodoro runs.
	end := focusNoon.Add(30 * time.Minute)
	if EvaluateProjectAccess(cfg, "borealis", end).Locked {
		t.Fatal("an ended Pomodoro still locks")
	}
	if _, done := CompletePomodoro(cfg, at); done != nil {
		t.Fatal("CompletePomodoro ended a Pomodoro with time left")
	}
	next, done := CompletePomodoro(cfg, end)
	if done == nil || done.Status != PomodoroCompleted || next.ActivePomodoro != nil {
		t.Fatalf("CompletePomodoro: %+v, %+v", done, next.ActivePomodoro)
	}
	if next, stopped := CancelPomodoro(cfg); stopped == nil || stopped.Status != PomodoroCancelled || next.ActivePomodoro != nil {
		t.Fatalf("CancelPomodoro: %+v", stopped)
	}
}

func TestTimeGuardrailsLockAProjectOverItsRollingBudget(t *testing.T) {
	cfg := SetTimeBudget(FocusConfig{}, ProjectTimeBudget{ProjectID: "apollo", WindowMinutes: 60, CooldownPomodoros: 2})
	cfg = RecordUsage(cfg, "apollo", focusNoon.Add(-50*time.Minute), 30)
	cfg = RecordUsage(cfg, "apollo", focusNoon.Add(-20*time.Minute), 20) // joins the first slice
	cfg = RecordUsage(cfg, "borealis", focusNoon.Add(-3*time.Hour), 170)
	if len(cfg.UsageLog) != 2 || cfg.UsageLog[0].DurationMin != 50 {
		t.Fatalf("contiguous usage was not joined: %+v", cfg.UsageLog)
	}
	if locks := EvaluateTimeGuardrails(cfg, focusNoon); len(locks) != 0 {
		t.Fatalf("50 of 60 minutes already locks: %+v", locks)
	}
	cfg = RecordUsage(cfg, "apollo", focusNoon, 10)
	now := focusNoon.Add(10 * time.Minute)
	locks := EvaluateTimeGuardrails(cfg, now)
	if len(locks) != 1 || locks[0].ProjectID != "apollo" || locks[0].Kind != CooldownRolling || !locks[0].UnlockAt.Equal(now.Add(50*time.Minute)) {
		t.Fatalf("60 of 60 minutes in 120 did not lock for 2 Pomodoros: %+v", locks)
	}
	cfg.CooldownLocks = locks
	d := EvaluateProjectAccess(cfg, "apollo", now.Add(time.Minute))
	if !d.Locked || d.Source != FocusSourceCooldown || !d.UnlockAt.Equal(locks[0].UnlockAt) {
		t.Fatalf("the cooldown does not lock the project: %+v", d)
	}
	if EvaluateProjectAccess(cfg, "borealis", now).Locked {
		t.Fatal("a project with no budget is locked")
	}
	// The lock is stable while it holds, and does not come back when it
	// ends: the minutes before the cooldown are not counted twice.
	if again := EvaluateTimeGuardrails(cfg, now.Add(20*time.Minute)); len(again) != 1 || !again[0].UnlockAt.Equal(locks[0].UnlockAt) {
		t.Fatalf("a running cooldown moved: %+v", again)
	}
	after := locks[0].UnlockAt.Add(time.Minute)
	cfg.CooldownLocks = EvaluateTimeGuardrails(cfg, after)
	if EvaluateProjectAccess(cfg, "apollo", after).Locked {
		t.Fatalf("the project is locked again right after its cooldown: %+v", cfg.CooldownLocks)
	}
	// Removing the budget removes its cooldowns.
	if cfg = SetTimeBudget(cfg, ProjectTimeBudget{ProjectID: "apollo"}); len(cfg.TimeBudgets) != 0 || len(cfg.CooldownLocks) != 0 {
		t.Fatalf("an empty budget was kept: %+v", cfg)
	}
}

func TestTimeGuardrailsDailyMaximumLocksUntilTomorrow(t *testing.T) {
	cfg := SetTimeBudget(FocusConfig{}, ProjectTimeBudget{ProjectID: "apollo", DailyMaxMinutes: 120})
	cfg = RecordUsage(cfg, "apollo", focusNoon.AddDate(0, 0, -1), 300) // yesterday does not count
	cfg = RecordUsage(cfg, "apollo", focusNoon.Add(-3*time.Hour), 119)
	if TodayMinutes(cfg, "apollo", focusNoon) != 119 || len(EvaluateTimeGuardrails(cfg, focusNoon)) != 0 {
		t.Fatalf("119 of 120 minutes today: %d min, %+v", TodayMinutes(cfg, "apollo", focusNoon), EvaluateTimeGuardrails(cfg, focusNoon))
	}
	cfg = RecordUsage(cfg, "apollo", focusNoon.Add(-time.Minute), 1)
	locks := EvaluateTimeGuardrails(cfg, focusNoon)
	midnight := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	if len(locks) != 1 || locks[0].Kind != CooldownDaily || !locks[0].UnlockAt.Equal(midnight) {
		t.Fatalf("the daily maximum did not lock until tomorrow: %+v", locks)
	}
}

func TestScheduledWindowLocksOtherProjectsOnceItsReportIsReady(t *testing.T) {
	cfg, err := UpsertWindow(FocusConfig{CaptureBucketID: "bucket"}, FocusWindowRule{
		Name: "PR Reviews", TargetProjectID: "reviews", DailyTimeHHMM: "10:00", DurationMin: 45, LockOtherProjects: true, Enabled: true,
		ReportCommand: "make pr-report",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []FocusWindowRule{
		{Name: "", DailyTimeHHMM: "10:00", DurationMin: 5},
		{Name: "x", DailyTimeHHMM: "25:00", DurationMin: 5},
		{Name: "x", DailyTimeHHMM: "10:00"},
	} {
		if _, err := UpsertWindow(cfg, bad); err == nil {
			t.Errorf("UpsertWindow accepted %+v", bad)
		}
	}
	before := time.Date(2026, 11, 26, 9, 59, 0, 0, time.UTC)
	open := time.Date(2026, 11, 26, 10, 5, 0, 0, time.UTC)
	closed := time.Date(2026, 11, 26, 10, 45, 0, 0, time.UTC)

	if next, triggered := TriggerDueWindows(cfg, before); len(triggered) != 0 || next.ScheduledWindows[0].LastTriggeredDate != "" {
		t.Fatalf("a window triggered before its time: %+v", triggered)
	}
	cfg, triggered := TriggerDueWindows(cfg, open)
	if len(triggered) != 1 || triggered[0].Name != "PR Reviews" || cfg.ScheduledWindows[0].LastTriggeredDate != "2026-11-26" {
		t.Fatalf("the due window did not trigger: %+v", triggered)
	}
	if _, again := TriggerDueWindows(cfg, open.Add(time.Minute)); len(again) != 0 {
		t.Fatal("the window triggered twice in one day")
	}
	// Due, but its report is not ready: nothing is locked yet.
	if _, ok := ActiveScheduledWindow(cfg, open); ok || EvaluateProjectAccess(cfg, "apollo", open).Locked {
		t.Fatal("the window opened before its report was ready")
	}
	cfg = MarkWindowReportReady(cfg, cfg.ScheduledWindows[0].ID)
	w, ok := ActiveScheduledWindow(cfg, open)
	if !ok || w.TargetProjectID != "reviews" {
		t.Fatalf("the window is not open with its report ready: %+v", w)
	}
	d := EvaluateProjectAccess(cfg, "apollo", open)
	if !d.Locked || d.Source != FocusSourceWindow || !d.UnlockAt.Equal(closed) {
		t.Fatalf("another project is not locked by the window: %+v", d)
	}
	if EvaluateProjectAccess(cfg, "reviews", open).Locked || EvaluateProjectAccess(cfg, "bucket", open).Locked {
		t.Fatal("the window locks its own project or the capture bucket")
	}
	if EvaluateProjectAccess(cfg, "apollo", closed).Locked {
		t.Fatal("the window still locks after it closed")
	}
	// Yesterday's ready report does not open today's window.
	if _, ok := ActiveScheduledWindow(cfg, open.AddDate(0, 0, 1)); ok {
		t.Fatal("yesterday's report opened today's window")
	}
	// W turns the project's windows off and on.
	off, enabled, found := ToggleProjectWindows(cfg, "reviews")
	if !found || enabled || EvaluateProjectAccess(off, "apollo", open).Locked {
		t.Fatalf("a window turned off still locks (enabled %v, found %v)", enabled, found)
	}
	if _, _, found := ToggleProjectWindows(cfg, "apollo"); found {
		t.Fatal("a project with no window was toggled")
	}
}

func TestEvaluateProjectAccessReportsTheLongestLock(t *testing.T) {
	cfg := StartPomodoro(FocusConfig{}, "Deep work", []string{"apollo"}, 25, "", focusNoon)
	cfg.CooldownLocks = []ProjectCooldown{{ProjectID: "borealis", Kind: CooldownRolling, Reason: "time budget used", LockedAt: focusNoon, UnlockAt: focusNoon.Add(time.Hour)}}
	d := EvaluateProjectAccess(cfg, "borealis", focusNoon.Add(time.Minute))
	if !d.Locked || d.Source != FocusSourceCooldown || !d.UnlockAt.Equal(focusNoon.Add(time.Hour)) {
		t.Fatalf("the longer lock is not the decision: %+v", d)
	}
	if d := EvaluateProjectAccess(FocusConfig{}, "apollo", focusNoon); !d.Allowed || d.Locked || d.Source != "" {
		t.Fatalf("an empty config locks: %+v", d)
	}
}

func TestBuildFocusProjectRowsPutsPrioritiesFirst(t *testing.T) {
	cfg := TogglePriorityProject(FocusConfig{CaptureBucketID: "bucket"}, "borealis", focusNoon)
	cfg = SetTimeBudget(cfg, ProjectTimeBudget{ProjectID: "apollo", DailyMaxMinutes: 60})
	cfg = RecordUsage(cfg, "apollo", focusNoon.Add(-30*time.Minute), 30)
	cfg = StartPomodoro(cfg, "", []string{"borealis"}, 25, "", focusNoon)
	rows := BuildFocusProjectRows(cfg, []FocusProject{{"apollo", "Apollo"}, {"borealis", "Borealis"}, {"bucket", CaptureBucketTitle}}, focusNoon)
	if len(rows) != 3 || rows[0].ProjectID != "borealis" || !rows[0].Priority || rows[0].Access.Locked {
		t.Fatalf("the priority project is not first and open: %+v", rows)
	}
	if rows[1].ProjectID != "apollo" || !rows[1].Access.Locked || rows[1].TodayMinutes != 30 || rows[1].Budget == nil {
		t.Fatalf("apollo row: %+v", rows[1])
	}
	if !rows[2].CaptureBucket || rows[2].Access.Locked || rows[2].Priority {
		t.Fatalf("capture bucket row: %+v", rows[2])
	}
}
