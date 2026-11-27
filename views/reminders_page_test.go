package views

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

func TestRemindersPageViewRendersDailyScheduledPendingAndHistory(t *testing.T) {
	now := time.Date(2026, 11, 27, 14, 30, 0, 0, time.UTC)

	spec := RemindersPageSpec{
		ViewMode: RemindersViewAll,
		PendingAlerts: []data.ReminderAlert{
			{
				OccurrenceID:  "occ-1",
				ScheduleID:    "sch-2",
				Title:         "Submit expense report",
				Message:       "Due before 5pm",
				NotifyChannel: data.NotifyChannelBoth,
				FiredAt:       now.Add(-5 * time.Minute),
			},
		},
		Rows: []data.ReminderRow{
			{
				ID:            "alert:occ-1",
				RowKind:       "pending",
				ScheduleID:    "sch-2",
				OccurrenceID:  "occ-1",
				Title:         "Submit expense report",
				Message:       "Due before 5pm",
				NotifyChannel: data.NotifyChannelBoth,
				Persistence:   data.NotifyPersistencePersistent,
				FiredAt:       now.Add(-5 * time.Minute),
			},
			{
				ID:            "sch-1",
				RowKind:       "daily",
				ScheduleID:    "sch-1",
				Title:         "Take vitamins",
				TimeOfDay:     "09:00",
				NotifyChannel: data.NotifyChannelBoth,
				Persistence:   data.NotifyPersistenceTemporary,
				TTLSeconds:    30,
				Daily: data.DailyStatus{
					CheckedToday: true,
					Streak:       5,
					Last7Days:    "✓✓·✓✓✓✓",
					TotalChecks:  12,
				},
			},
			{
				ID:            "sch-3",
				RowKind:       "scheduled",
				ScheduleID:    "sch-3",
				Title:         "Weekly demo prep",
				Cron:          "0 10 * * 5",
				NextDueAt:     now.Add(2 * time.Hour),
				NotifyChannel: data.NotifyChannelOS,
				Persistence:   data.NotifyPersistenceTemporary,
				TTLSeconds:    30,
			},
		},
		SelectedIdx:    1,
		TotalDaily:     1,
		DoneDaily:      1,
		TotalScheduled: 1,
		TotalHistory:   4,
		Now:            now,
	}

	node := RemindersPageView(spec)
	if node.Scope != ScopeReminders || node.ID != RemindersPageID {
		t.Fatalf("unexpected root node: id=%q scope=%q", node.ID, node.Scope)
	}
	if node.Facts["pendingCount"] != 1 || node.Facts["rowKind"] != "daily" || node.Facts["canToggle"] != true {
		t.Fatalf("unexpected facts: %+v", node.Facts)
	}
	if node.Find(RemindersAlertBarID) == nil {
		t.Fatal("expected pending alert bar when PendingAlerts non-empty")
	}
	tbl := node.Find(RemindersListID)
	if tbl == nil || tbl.Kind != ui.KindTable || tbl.Filter == nil {
		t.Fatalf("expected filterable table node, got %+v", tbl)
	}

	rendered := frame(t, node, RemindersListID, 120, 24)
	for _, want := range []string{"Reminders", "Pending(1)", "Submit expense report", "Take vitamins", "✓✓·✓✓✓✓", "OS+MTM", "30s", "sticky", "Weekly demo prep"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("frame missing %q:\n%s", want, rendered)
		}
	}

	// History view renders permanent log cleanly.
	spec.ViewMode = RemindersViewHistory
	spec.SelectedIdx = 0
	spec.HistoryRows = []data.ReminderHistoryRow{
		{
			ID:            "occ-h1",
			ScheduleTitle: "Take vitamins",
			EventType:     "checkoff",
			OutcomeStatus: "done",
			DueDate:       "2026-11-26",
			Timestamp:     now.Add(-24 * time.Hour),
		},
	}
	histNode := RemindersPageView(spec)
	histFrame := frame(t, histNode, RemindersListID, 120, 20)
	for _, want := range []string{"[History]", "✓ Check-off", "Take vitamins", "2026-11-26"} {
		if !strings.Contains(histFrame, want) {
			t.Errorf("history frame missing %q:\n%s", want, histFrame)
		}
	}
}
