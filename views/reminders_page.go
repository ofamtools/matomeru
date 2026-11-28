package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Reminders page scope, node IDs, and view filter names.
const (
	ScopeReminders      = "reminders.page"
	RemindersPageID     = "reminders.page"
	RemindersListID     = "reminders.list"
	RemindersBannerID   = "reminders.banner"
	RemindersAlertBarID = "reminders.alertbar"
	RemindersSummaryID  = "reminders.summary"
	RemindersHintID     = "reminders.hint"

	RemindersViewAll       = "all"
	RemindersViewDaily     = "daily"
	RemindersViewScheduled = "scheduled"
	RemindersViewPending   = "pending"
	RemindersViewHistory   = "history"

	RemindersHint = "space/x check-off or ack · a ack · n/c new · e edit · p pause · t test-fire · v/() view · / filter · d archive"
)

// RemindersViewOrder is the cycle order of Reminders page sub-views.
var RemindersViewOrder = []string{
	RemindersViewAll,
	RemindersViewDaily,
	RemindersViewScheduled,
	RemindersViewPending,
	RemindersViewHistory,
}

// RemindersPageSpec is the pure data contract of RemindersPageView.
type RemindersPageSpec struct {
	ViewMode       string // all | daily | scheduled | pending | history
	Rows           []data.ReminderRow
	HistoryRows    []data.ReminderHistoryRow
	PendingAlerts  []data.ReminderAlert
	SelectedIdx    int
	TotalDaily     int
	DoneDaily      int
	TotalScheduled int
	TotalHistory   int
	FilterQuery    string
	Now            time.Time
}

// RemindersPageView presents the top-level Reminders page purely from spec.
func RemindersPageView(spec RemindersPageSpec) ui.ViewNode {
	mode := normalizeRemindersView(spec.ViewMode)
	filtering := strings.TrimSpace(spec.FilterQuery) != ""
	facts := remindersPageFacts(spec, mode, filtering)

	children := []ui.ViewNode{remindersBanner(spec, mode)}
	if len(spec.PendingAlerts) > 0 && mode != RemindersViewHistory {
		children = append(children, remindersPendingAlertBar(len(spec.PendingAlerts)))
	}
	if filtering {
		children = append(children, fixed(text("reminders.filter", " /"+spec.FilterQuery, "accent"), 1))
	}
	children = append(children,
		remindersTableNode(spec, mode, filtering),
		remindersSummaryBar(spec, mode),
		fixed(text(RemindersHintID, " "+RemindersHint, "muted"), 1),
	)
	return ui.ViewNode{
		ID:       RemindersPageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeReminders,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
}

func normalizeRemindersView(v string) string {
	switch v {
	case RemindersViewDaily, RemindersViewScheduled, RemindersViewPending, RemindersViewHistory:
		return v
	default:
		return RemindersViewAll
	}
}

func remindersPageFacts(spec RemindersPageSpec, mode string, filtering bool) map[string]any {
	facts := map[string]any{
		"view":         mode,
		"hasFilter":    filtering,
		"pendingCount": len(spec.PendingAlerts),
		"hasSelection": false,
		"rowKind":      "",
		"paused":       false,
		"archived":     false,
		"canToggle":    false,
		"canEdit":      false,
		"canTestFire":  false,
	}
	if mode == RemindersViewHistory {
		if i := spec.SelectedIdx; i >= 0 && i < len(spec.HistoryRows) {
			facts["hasSelection"] = true
			facts["rowKind"] = "history"
		}
		return facts
	}
	if i := spec.SelectedIdx; i >= 0 && i < len(spec.Rows) {
		r := spec.Rows[i]
		facts["hasSelection"] = true
		facts["rowKind"] = r.RowKind
		facts["paused"] = r.Paused
		facts["archived"] = r.Archived
		facts["canToggle"] = r.RowKind == "daily" || r.RowKind == "pending"
		facts["canEdit"] = r.ScheduleID != ""
		facts["canTestFire"] = r.ScheduleID != ""
	}
	return facts
}

func remindersBanner(spec RemindersPageSpec, mode string) ui.ViewNode {
	var line spanLine
	line.write(" Reminders ", "accent")
	labels := [][2]string{
		{RemindersViewAll, "All"},
		{RemindersViewDaily, "Daily"},
		{RemindersViewScheduled, "Scheduled"},
		{RemindersViewPending, "Pending"},
		{RemindersViewHistory, "History"},
	}
	for _, l := range labels {
		pill := l[1]
		if l[0] == RemindersViewPending && len(spec.PendingAlerts) > 0 {
			pill = fmt.Sprintf("Pending(%d)", len(spec.PendingAlerts))
		}
		if l[0] == mode {
			line.write(" ["+pill+"]", "accent")
		} else if l[0] == RemindersViewPending && len(spec.PendingAlerts) > 0 {
			line.write(" "+pill, "warning")
		} else {
			line.write(" "+pill, "muted")
		}
	}
	line.write("   ", "")
	dailyTone := "muted"
	if spec.TotalDaily > 0 && spec.DoneDaily == spec.TotalDaily {
		dailyTone = "state.done"
	} else if spec.TotalDaily > 0 {
		dailyTone = "info"
	}
	line.write(fmt.Sprintf("Daily %d/%d", spec.DoneDaily, spec.TotalDaily), dailyTone)
	line.write(fmt.Sprintf(" · Scheduled %d", spec.TotalScheduled), "muted")
	if len(spec.PendingAlerts) > 0 {
		line.write(fmt.Sprintf(" · ▲ %d pending", len(spec.PendingAlerts)), "warning")
	}
	line.write(fmt.Sprintf(" · Log %d", spec.TotalHistory), "muted")
	return fixed(ui.ViewNode{
		ID:    RemindersBannerID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

func remindersPendingAlertBar(n int) ui.ViewNode {
	var line spanLine
	line.write(" ▲ ", "warning")
	word := "persistent notification awaiting acknowledgement"
	if n > 1 {
		word = "persistent notifications awaiting acknowledgement"
	}
	line.write(fmt.Sprintf("%d %s", n, word), "warning")
	line.write(" · a ack · A ack all · g N focus top-right overlay", "muted")
	return fixed(ui.ViewNode{
		ID:    RemindersAlertBarID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

func remindersTableNode(spec RemindersPageSpec, mode string, filtering bool) ui.ViewNode {
	if mode == RemindersViewHistory {
		return remindersHistoryTable(spec, filtering)
	}
	cols := remindersColumnsForView(mode)
	rows := make([]any, 0, len(spec.Rows))
	for _, r := range spec.Rows {
		rows = append(rows, remindersRowSpec(r, mode, spec.Now))
	}
	selected := spec.SelectedIdx
	if len(rows) == 0 {
		selected = -1
	}
	node := ui.ViewNode{
		ID:        RemindersListID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: RemindersListID, Prefix: "reminders ", Fields: []string{"title", "kind", "channel", "persistence", "status"}},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":  cols,
			"rows":     rows,
			"selected": selected,
		},
	}
	if len(rows) == 0 {
		node.Kind = ui.KindText
		node.Tone = "muted"
		node.Props = map[string]any{"text": " " + remindersEmptyMessage(mode, filtering)}
	}
	return node
}

func remindersColumnsForView(mode string) []any {
	switch mode {
	case RemindersViewDaily:
		return []any{"Today", "Daily Reminder", "Last 7 Days", "Streak", "Notify"}
	case RemindersViewScheduled:
		return []any{"State", "Scheduled Reminder", "Schedule / Next Due", "Channel", "Mode"}
	case RemindersViewPending:
		return []any{"Alert", "Reminder", "Message", "Fired", "Channel"}
	default:
		return []any{"Status", "Reminder", "Schedule / 7-Day", "Channel", "Mode"}
	}
}

func remindersRowSpec(r data.ReminderRow, mode string, now time.Time) ui.TableRowSpec {
	switch mode {
	case RemindersViewDaily:
		return dailyReminderTableRow(r)
	case RemindersViewScheduled:
		return scheduledReminderTableRow(r, now)
	case RemindersViewPending:
		return pendingReminderTableRow(r, now)
	default:
		return unifiedReminderTableRow(r, now)
	}
}

func dailyReminderTableRow(r data.ReminderRow) ui.TableRowSpec {
	check, checkTone, rowTone := "[ ]", "accent", ""
	if r.Daily.CheckedToday {
		check, checkTone, rowTone = "[✓]", "state.done", "row.dim"
	}
	streak := "—"
	streakTone := "muted"
	if r.Daily.Streak > 0 {
		streak = fmt.Sprintf("%dd", r.Daily.Streak)
		streakTone = "accent"
	}
	notify, notifyTone := formatDailyNotifyCell(r)
	return ui.TableRowSpec{
		Cells:     []any{check, r.Title, r.Daily.Last7Days, streak, notify},
		CellTones: []string{checkTone, "", "info", streakTone, notifyTone},
		Tone:      rowTone,
	}
}

func scheduledReminderTableRow(r data.ReminderRow, now time.Time) ui.TableRowSpec {
	state, stateTone, rowTone := "▶ active", "state.working", ""
	if r.Paused {
		state, stateTone, rowTone = "⏸ paused", "warning", "row.muted"
	}
	ch, chTone := formatChannelBadge(r.NotifyChannel)
	pers, persTone := formatPersistenceBadge(r.Persistence, r.TTLSeconds)
	return ui.TableRowSpec{
		Cells:     []any{state, r.Title, formatScheduleTiming(r, now), ch, pers},
		CellTones: []string{stateTone, "", "muted", chTone, persTone},
		Tone:      rowTone,
	}
}

func pendingReminderTableRow(r data.ReminderRow, now time.Time) ui.TableRowSpec {
	ch, chTone := formatChannelBadge(r.NotifyChannel)
	msg := r.Message
	if msg == "" {
		msg = "Awaiting acknowledgement"
	}
	return ui.TableRowSpec{
		Cells:     []any{"▲ PENDING", r.Title, msg, formatReminderTime(r.FiredAt, now), ch},
		CellTones: []string{"warning", "warning", "", "muted", chTone},
		Tone:      "row.attention",
	}
}

func unifiedReminderTableRow(r data.ReminderRow, now time.Time) ui.TableRowSpec {
	switch r.RowKind {
	case "pending":
		ch, chTone := formatChannelBadge(r.NotifyChannel)
		return ui.TableRowSpec{
			Cells:     []any{"▲ ALERT", r.Title, "fired " + formatReminderTime(r.FiredAt, now), ch, "sticky"},
			CellTones: []string{"warning", "warning", "warning", chTone, "warning"},
			Tone:      "row.attention",
		}
	case "daily":
		check, checkTone, rowTone := "[ ] daily", "accent", ""
		if r.Daily.CheckedToday {
			check, checkTone, rowTone = "[✓] daily", "state.done", "row.dim"
		}
		progress := r.Daily.Last7Days
		if r.Daily.Streak > 0 {
			progress += fmt.Sprintf("  %dd", r.Daily.Streak)
		}
		if r.TimeOfDay != "" {
			progress += "  @" + r.TimeOfDay
		}
		ch, chTone := formatChannelBadge(r.NotifyChannel)
		pers, persTone := formatPersistenceBadge(r.Persistence, r.TTLSeconds)
		if r.NotifyChannel == data.NotifyChannelNone {
			pers, persTone = "silent", "muted"
		}
		return ui.TableRowSpec{
			Cells:     []any{check, r.Title, progress, ch, pers},
			CellTones: []string{checkTone, "", "info", chTone, persTone},
			Tone:      rowTone,
		}
	default:
		state, stateTone, rowTone := "⏰ sched", "info", ""
		if r.Paused {
			state, stateTone, rowTone = "⏸ paused", "warning", "row.muted"
		}
		ch, chTone := formatChannelBadge(r.NotifyChannel)
		pers, persTone := formatPersistenceBadge(r.Persistence, r.TTLSeconds)
		return ui.TableRowSpec{
			Cells:     []any{state, r.Title, formatScheduleTiming(r, now), ch, pers},
			CellTones: []string{stateTone, "", "muted", chTone, persTone},
			Tone:      rowTone,
		}
	}
}

func remindersHistoryTable(spec RemindersPageSpec, filtering bool) ui.ViewNode {
	cols := []any{"When", "Type", "Reminder", "Outcome", "Details"}
	rows := make([]any, 0, len(spec.HistoryRows))
	for _, h := range spec.HistoryRows {
		evLabel, evTone := formatHistoryEventType(h.EventType)
		outLabel, outTone := formatHistoryOutcome(h)
		details := h.Message
		if details == "" && h.NotifyChannel != "" {
			ch, _ := formatChannelBadge(h.NotifyChannel)
			details = ch
		}
		if details == "" && h.DueDate != "" {
			details = "day " + h.DueDate
		}
		rows = append(rows, ui.TableRowSpec{
			Cells:     []any{formatHistoryTimestamp(h.Timestamp, spec.Now), evLabel, h.ScheduleTitle, outLabel, details},
			CellTones: []string{"muted", evTone, "", outTone, "muted"},
		})
	}
	selected := spec.SelectedIdx
	if len(rows) == 0 {
		selected = -1
	}
	node := ui.ViewNode{
		ID:        RemindersListID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: RemindersListID, Prefix: "history ", Fields: []string{"title", "type", "outcome", "date"}},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":  cols,
			"rows":     rows,
			"selected": selected,
		},
	}
	if len(rows) == 0 {
		node.Kind = ui.KindText
		node.Tone = "muted"
		node.Props = map[string]any{"text": " " + remindersEmptyMessage(RemindersViewHistory, filtering)}
	}
	return node
}

func remindersSummaryBar(spec RemindersPageSpec, mode string) ui.ViewNode {
	var line spanLine
	line.write(" Detail ", "accent")
	if mode == RemindersViewHistory {
		if i := spec.SelectedIdx; i >= 0 && i < len(spec.HistoryRows) {
			h := spec.HistoryRows[i]
			line.write(h.ScheduleTitle, "")
			if h.DueDate != "" {
				line.write(" · day "+h.DueDate, "muted")
			}
			if !h.AckedAt.IsZero() {
				line.write(" · acked "+formatHistoryTimestamp(h.AckedAt, spec.Now), "state.done")
			}
			if h.Message != "" {
				line.write(" · "+h.Message, "muted")
			}
		} else {
			line.write("Permanent check-off & notification history log (never erased on nightly reset)", "muted")
		}
	} else if i := spec.SelectedIdx; i >= 0 && i < len(spec.Rows) {
		r := spec.Rows[i]
		line.write(r.Title, "")
		if r.Message != "" {
			line.write(" · "+r.Message, "muted")
		}
		if r.RowKind == "daily" {
			line.write(fmt.Sprintf(" · %d total check-offs · streak %dd · resets at 00:00 local", r.Daily.TotalChecks, r.Daily.Streak), "muted")
		} else if r.Cron != "" {
			line.write(" · cron "+r.Cron, "muted")
		}
	} else {
		line.write("Press n or c to create a daily check-off or scheduled notification", "muted")
	}
	return fixed(ui.ViewNode{
		ID:    RemindersSummaryID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

func remindersEmptyMessage(mode string, filtering bool) string {
	if filtering {
		return "No reminders match the filter."
	}
	switch mode {
	case RemindersViewDaily:
		return "No daily reminders yet · press n to add a daily check-off habit."
	case RemindersViewScheduled:
		return "No scheduled notifications yet · press n to add a scheduled reminder."
	case RemindersViewPending:
		return "No pending persistent notifications — all caught up!"
	case RemindersViewHistory:
		return "No check-off or notification history yet."
	default:
		return "No reminders yet · press n to add a daily check-off or scheduled notification."
	}
}

func formatChannelBadge(ch string) (string, string) {
	switch ch {
	case data.NotifyChannelBoth:
		return "OS+MTM", "accent"
	case data.NotifyChannelMTM:
		return "MTM", "info"
	case data.NotifyChannelOS:
		return "OS", "success"
	default:
		return "none", "muted"
	}
}

func formatPersistenceBadge(p string, ttlSec int) (string, string) {
	if p == data.NotifyPersistencePersistent {
		return "sticky", "warning"
	}
	if ttlSec <= 0 {
		ttlSec = data.DefaultReminderTTLSeconds
	}
	return fmt.Sprintf("%ds", ttlSec), "muted"
}

func formatDailyNotifyCell(r data.ReminderRow) (string, string) {
	if r.NotifyChannel == data.NotifyChannelNone {
		return "silent", "muted"
	}
	ch, tone := formatChannelBadge(r.NotifyChannel)
	pers, _ := formatPersistenceBadge(r.Persistence, r.TTLSeconds)
	if r.TimeOfDay != "" {
		return fmt.Sprintf("%s · %s · %s", r.TimeOfDay, ch, pers), tone
	}
	return fmt.Sprintf("%s · %s", ch, pers), tone
}

func formatScheduleTiming(r data.ReminderRow, now time.Time) string {
	var parts []string
	if r.Cron != "" {
		parts = append(parts, r.Cron)
	}
	if !r.NextDueAt.IsZero() {
		parts = append(parts, "next "+formatReminderTime(r.NextDueAt, now))
	}
	if len(parts) == 0 {
		return "manual / test-fire"
	}
	return strings.Join(parts, " · ")
}

func formatReminderTime(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	lt := t.In(loc)
	if data.DayKey(lt, loc) == data.DayKey(now, loc) {
		return lt.Format("15:04")
	}
	return lt.Format("Jan 02 15:04")
}

func formatHistoryTimestamp(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02 15:04")
}

func formatHistoryEventType(ev string) (string, string) {
	switch ev {
	case "checkoff":
		return "✓ Check-off", "state.done"
	case "persistent":
		return "▲ Persistent", "warning"
	case "temporary":
		return "🔔 Notified", "info"
	default:
		return "· Event", "muted"
	}
}

func formatHistoryOutcome(h data.ReminderHistoryRow) (string, string) {
	if !h.AckedAt.IsZero() {
		return "acked", "state.done"
	}
	switch h.OutcomeStatus {
	case "done":
		return "done", "state.done"
	case "pending":
		return "pending", "warning"
	case "failed":
		return "failed", "error"
	case "skipped":
		return "unchecked", "muted"
	default:
		return h.OutcomeStatus, "muted"
	}
}
