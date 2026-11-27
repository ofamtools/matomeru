package data

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// Schedule kind, notification channel, and persistence constants for Reminders.
const (
	ScheduleKindRemind     = "remind"
	ScheduleKindDaily      = "daily"
	ScheduleKindRun        = "run"
	ScheduleKindCheck      = "check"
	ScheduleKindAgentQueue = "agent_queue"

	NotifyChannelBoth = "both"
	NotifyChannelMTM  = "mtm"
	NotifyChannelOS   = "os"
	NotifyChannelNone = "none"

	NotifyPersistenceTemporary  = "temporary"
	NotifyPersistencePersistent = "persistent"

	DefaultReminderTTLSeconds = 30
)

// DailyStatus summarizes a daily reminder's check-off status for today and its
// recent history, computed purely from permanent schedule.occurrence records.
type DailyStatus struct {
	CheckedToday  bool
	TodayOccID    string
	Streak        int
	Last7Days     string
	TotalChecks   int
	LastCheckedAt time.Time
}

// ReminderAlert is one active persistent notification awaiting owner acknowledgement.
type ReminderAlert struct {
	OccurrenceID  string
	ScheduleID    string
	Title         string
	Message       string
	NotifyChannel string
	FiredAt       time.Time
	DueAt         time.Time
	OccRef        core.Ref
	ScheduleRef   core.Ref
}

// ReminderRow is one projected row on the Reminders page (daily check-off,
// scheduled notification, or pending persistent alert).
type ReminderRow struct {
	ID            string
	RowKind       string // "pending" | "daily" | "scheduled"
	ScheduleID    string
	OccurrenceID  string
	Title         string
	Message       string
	ScheduleKind  string
	Cron          string
	TimeOfDay     string
	NextDueAt     time.Time
	NotifyChannel string
	Persistence   string
	TTLSeconds    int
	Paused        bool
	Archived      bool
	Daily         DailyStatus
	FiredAt       time.Time
	Ref           core.Ref
	ScheduleRef   core.Ref
}

// ReminderHistoryRow is one permanent log entry from schedule.occurrence.
type ReminderHistoryRow struct {
	ID            string
	ScheduleID    string
	ScheduleTitle string
	ScheduleKind  string
	EventType     string // "checkoff" | "persistent" | "temporary" | "occurrence"
	OutcomeStatus string
	DueDate       string
	Timestamp     time.Time
	AckedAt       time.Time
	NotifyChannel string
	Message       string
	AckRequired   bool
	Ref           core.Ref
	ScheduleRef   core.Ref
}

// IsReminderSchedule reports whether rec is a daily check-off or reminder schedule.
func IsReminderSchedule(rec Record) bool {
	k := strings.TrimSpace(rec.Text("scheduleKind"))
	return k == "" || k == ScheduleKindRemind || k == ScheduleKindDaily
}

// EffectiveNotifyChannel returns the normalized delivery channel for a schedule
// or occurrence record.
func EffectiveNotifyChannel(rec Record) string {
	switch strings.ToLower(strings.TrimSpace(rec.Text("notifyChannel"))) {
	case NotifyChannelBoth:
		return NotifyChannelBoth
	case NotifyChannelMTM:
		return NotifyChannelMTM
	case NotifyChannelOS:
		return NotifyChannelOS
	case NotifyChannelNone:
		return NotifyChannelNone
	}
	if strings.TrimSpace(rec.Text("scheduleKind")) == ScheduleKindDaily {
		if strings.TrimSpace(rec.Text("timeOfDay")) == "" && strings.TrimSpace(rec.Text("cron")) == "" {
			return NotifyChannelNone
		}
	}
	return NotifyChannelBoth
}

// EffectiveNotifyPersistence returns "persistent" or "temporary" (default).
func EffectiveNotifyPersistence(rec Record) string {
	if strings.EqualFold(strings.TrimSpace(rec.Text("notifyPersistence")), NotifyPersistencePersistent) {
		return NotifyPersistencePersistent
	}
	return NotifyPersistenceTemporary
}

// EffectiveNotifyTTLSeconds returns the configured temporary toast TTL in
// seconds, defaulting to DefaultReminderTTLSeconds (30).
func EffectiveNotifyTTLSeconds(rec Record) int {
	if n := recordIntField(rec, "notifyTTLSeconds"); n > 0 {
		return n
	}
	if n := recordIntField(rec, "ttlSeconds"); n > 0 {
		return n
	}
	return DefaultReminderTTLSeconds
}

func recordIntField(rec Record, field string) int {
	v, ok := rec.Value(field)
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err == nil {
			return parsed
		}
	}
	return 0
}

// EffectiveNotifyTTL returns the temporary toast TTL duration (default 30s).
func EffectiveNotifyTTL(rec Record) time.Duration {
	return time.Duration(EffectiveNotifyTTLSeconds(rec)) * time.Second
}

// ChannelIncludesOS reports whether ch delivers a macOS desktop notification.
func ChannelIncludesOS(ch string) bool {
	return ch == NotifyChannelOS || ch == NotifyChannelBoth
}

// ChannelIncludesMTM reports whether ch delivers an in-app Matomeru overlay notification.
func ChannelIncludesMTM(ch string) bool {
	return ch == NotifyChannelMTM || ch == NotifyChannelBoth
}

// DayKey formats t as YYYY-MM-DD in loc (defaulting to time.Local).
func DayKey(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02")
}

// ParseTimeOfDay parses an HH:MM (24-hour) clock string.
func ParseTimeOfDay(s string) (hour, minute int, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h := parseTwoDigits(s[0], s[1])
	m := parseTwoDigits(s[3], s[4])
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func parseTwoDigits(a, b byte) int {
	if a < '0' || a > '9' || b < '0' || b > '9' {
		return -1
	}
	return int(a-'0')*10 + int(b-'0')
}

// CronForTimeOfDay converts "HH:MM" into a 5-field daily cron expression "MM HH * * *".
func CronForTimeOfDay(tod string) string {
	h, m, ok := ParseTimeOfDay(tod)
	if !ok {
		return ""
	}
	return formatCronNumber(m) + " " + formatCronNumber(h) + " * * *"
}

func formatCronNumber(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string([]rune{rune('0' + n/10), rune('0' + n%10)})
}

// OccurrenceDayKey resolves the local calendar day (YYYY-MM-DD) for an occurrence.
func OccurrenceDayKey(occ Record, loc *time.Location) string {
	if d := strings.TrimSpace(occ.Text("dueDate")); len(d) == 10 {
		return d
	}
	if ts := occ.Time("checkedAt"); !ts.IsZero() {
		return DayKey(ts, loc)
	}
	if ts := occ.Time("dueAt"); !ts.IsZero() {
		return DayKey(ts, loc)
	}
	if !occ.CreatedAt.IsZero() {
		return DayKey(occ.CreatedAt, loc)
	}
	return ""
}

// IsCheckOffOccurrence reports whether occ represents a daily check-off record.
func IsCheckOffOccurrence(occ Record) bool {
	if occ.Bool("checkOff") {
		return true
	}
	return strings.TrimSpace(occ.Text("dueDate")) != "" && strings.TrimSpace(occ.Text("notifiedAt")) == ""
}

// ComputeDailyStatus computes today's check-off status, streak, and 7-day
// sparkline for scheduleID purely against now's calendar day. At midnight in
// now.Location(), CheckedToday resets to false while prior days stay intact.
func ComputeDailyStatus(scheduleID string, occurrences []Record, now time.Time) DailyStatus {
	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	today := DayKey(now, loc)
	checkedDays := map[string]bool{}
	var status DailyStatus

	for _, occ := range occurrences {
		if occ.IsArchived() || !IsCheckOffOccurrence(occ) {
			continue
		}
		if ref, ok := occ.RefField("schedule"); !ok || ref.ID != scheduleID {
			continue
		}
		applyCheckOffOccurrence(&status, checkedDays, occ, today, loc)
	}

	status.TotalChecks = len(checkedDays)
	status.Streak = computeDailyStreak(checkedDays, now, loc)
	status.Last7Days = formatLast7Days(checkedDays, now, loc)
	return status
}

func applyCheckOffOccurrence(status *DailyStatus, checkedDays map[string]bool, occ Record, today string, loc *time.Location) {
	day := OccurrenceDayKey(occ, loc)
	if day == "" {
		return
	}
	if occ.Text("outcomeStatus") != "done" {
		if day == today && status.TodayOccID == "" {
			status.TodayOccID = occ.ID
		}
		return
	}
	checkedDays[day] = true
	if day == today && status.TodayOccID == "" {
		status.CheckedToday = true
		status.TodayOccID = occ.ID
	}
	ts := occ.Time("checkedAt")
	if ts.IsZero() {
		ts = occ.UpdatedAt
	}
	if ts.After(status.LastCheckedAt) {
		status.LastCheckedAt = ts
	}
}

func computeDailyStreak(checkedDays map[string]bool, now time.Time, loc *time.Location) int {
	base := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 12, 0, 0, 0, loc)
	startOffset := 0
	if !checkedDays[base.Format("2006-01-02")] {
		startOffset = 1
	}
	streak := 0
	for i := startOffset; i < 3650; i++ {
		day := base.AddDate(0, 0, -i).Format("2006-01-02")
		if !checkedDays[day] {
			break
		}
		streak++
	}
	return streak
}

func formatLast7Days(checkedDays map[string]bool, now time.Time, loc *time.Location) string {
	base := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 12, 0, 0, 0, loc)
	var b strings.Builder
	for i := 6; i >= 0; i-- {
		day := base.AddDate(0, 0, -i).Format("2006-01-02")
		switch {
		case checkedDays[day]:
			b.WriteRune('✓')
		case i == 0:
			b.WriteRune('○')
		default:
			b.WriteRune('·')
		}
	}
	return b.String()
}

// PendingPersistentAlerts returns all active persistent notifications awaiting
// acknowledgement, ordered newest-first.
func PendingPersistentAlerts(schedules, occurrences []Record) []ReminderAlert {
	schedByID := make(map[string]Record, len(schedules))
	for _, s := range schedules {
		schedByID[s.ID] = s
	}
	var alerts []ReminderAlert
	for _, occ := range occurrences {
		if occ.IsArchived() || !occ.Bool("ackRequired") || occ.Text("outcomeStatus") != "pending" {
			continue
		}
		schedRef, _ := occ.RefField("schedule")
		sched := schedByID[schedRef.ID]
		title := strings.TrimSpace(sched.Text("title"))
		if title == "" {
			title = strings.TrimSpace(occ.Text("message"))
		}
		if title == "" {
			title = "Reminder"
		}
		msg := strings.TrimSpace(occ.Text("message"))
		if msg == "" {
			msg = strings.TrimSpace(sched.Text("reminderMessage"))
		}
		firedAt := occ.Time("notifiedAt")
		if firedAt.IsZero() {
			firedAt = occ.CreatedAt
		}
		alerts = append(alerts, ReminderAlert{
			OccurrenceID:  occ.ID,
			ScheduleID:    schedRef.ID,
			Title:         title,
			Message:       msg,
			NotifyChannel: EffectiveNotifyChannel(occ),
			FiredAt:       firedAt,
			DueAt:         occ.Time("dueAt"),
			OccRef:        occ.Ref(),
			ScheduleRef:   schedRef,
		})
	}
	slices.SortStableFunc(alerts, func(a, b ReminderAlert) int {
		if c := b.FiredAt.Compare(a.FiredAt); c != 0 {
			return c
		}
		return strings.Compare(b.OccurrenceID, a.OccurrenceID)
	})
	return alerts
}

// BuildReminderRows projects pending persistent alerts, daily reminders, and
// scheduled notifications into display-ready rows for the Reminders page.
func BuildReminderRows(schedules, occurrences []Record, now time.Time) (pending, daily, scheduled []ReminderRow) {
	alerts := PendingPersistentAlerts(schedules, occurrences)
	for _, al := range alerts {
		pending = append(pending, ReminderRow{
			ID:            "alert:" + al.OccurrenceID,
			RowKind:       "pending",
			ScheduleID:    al.ScheduleID,
			OccurrenceID:  al.OccurrenceID,
			Title:         al.Title,
			Message:       al.Message,
			ScheduleKind:  ScheduleKindRemind,
			NotifyChannel: al.NotifyChannel,
			Persistence:   NotifyPersistencePersistent,
			FiredAt:       al.FiredAt,
			Ref:           al.OccRef,
			ScheduleRef:   al.ScheduleRef,
		})
	}

	for _, rec := range schedules {
		if !IsReminderSchedule(rec) {
			continue
		}
		kind := strings.TrimSpace(rec.Text("scheduleKind"))
		if kind == "" {
			kind = ScheduleKindRemind
		}
		row := ReminderRow{
			ID:            rec.ID,
			ScheduleID:    rec.ID,
			Title:         strings.TrimSpace(rec.Text("title")),
			Message:       strings.TrimSpace(rec.Text("reminderMessage")),
			ScheduleKind:  kind,
			Cron:          strings.TrimSpace(rec.Text("cron")),
			TimeOfDay:     strings.TrimSpace(rec.Text("timeOfDay")),
			NextDueAt:     rec.Time("nextDueAt"),
			NotifyChannel: EffectiveNotifyChannel(rec),
			Persistence:   EffectiveNotifyPersistence(rec),
			TTLSeconds:    EffectiveNotifyTTLSeconds(rec),
			Paused:        rec.Bool("paused"),
			Archived:      rec.IsArchived(),
			Ref:           rec.Ref(),
			ScheduleRef:   rec.Ref(),
		}
		if kind == ScheduleKindDaily {
			row.RowKind = "daily"
			row.Daily = ComputeDailyStatus(rec.ID, occurrences, now)
			daily = append(daily, row)
		} else {
			row.RowKind = "scheduled"
			scheduled = append(scheduled, row)
		}
	}

	sortDailyRows(daily)
	sortScheduledRows(scheduled)
	return pending, daily, scheduled
}

func sortDailyRows(rows []ReminderRow) {
	slices.SortStableFunc(rows, func(a, b ReminderRow) int {
		if a.Daily.CheckedToday != b.Daily.CheckedToday {
			if !a.Daily.CheckedToday {
				return -1
			}
			return 1
		}
		if a.TimeOfDay != b.TimeOfDay {
			if a.TimeOfDay == "" {
				return 1
			}
			if b.TimeOfDay == "" {
				return -1
			}
			return strings.Compare(a.TimeOfDay, b.TimeOfDay)
		}
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
}

func sortScheduledRows(rows []ReminderRow) {
	slices.SortStableFunc(rows, func(a, b ReminderRow) int {
		if a.Paused != b.Paused {
			if !a.Paused {
				return -1
			}
			return 1
		}
		if !a.NextDueAt.IsZero() && !b.NextDueAt.IsZero() {
			if c := a.NextDueAt.Compare(b.NextDueAt); c != 0 {
				return c
			}
		} else if !a.NextDueAt.IsZero() {
			return -1
		} else if !b.NextDueAt.IsZero() {
			return 1
		}
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
}

// BuildReminderHistory builds the permanent newest-first history log from all
// schedule.occurrence records belonging to reminder/daily schedules.
func BuildReminderHistory(schedules, occurrences []Record, loc *time.Location) []ReminderHistoryRow {
	schedByID := make(map[string]Record, len(schedules))
	for _, s := range schedules {
		schedByID[s.ID] = s
	}
	var out []ReminderHistoryRow
	for _, occ := range occurrences {
		if occ.IsArchived() {
			continue
		}
		schedRef, _ := occ.RefField("schedule")
		sched, hasSched := schedByID[schedRef.ID]
		if hasSched && !IsReminderSchedule(sched) {
			continue
		}
		out = append(out, projectHistoryRow(occ, sched, schedRef, loc))
	}
	slices.SortStableFunc(out, func(a, b ReminderHistoryRow) int {
		if c := b.Timestamp.Compare(a.Timestamp); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out
}

func projectHistoryRow(occ, sched Record, schedRef core.Ref, loc *time.Location) ReminderHistoryRow {
	title := strings.TrimSpace(sched.Text("title"))
	if title == "" {
		title = strings.TrimSpace(occ.Text("message"))
	}
	if title == "" {
		title = schedRef.ID
	}
	ts := occ.Time("acknowledgedAt")
	if ts.IsZero() {
		ts = occ.Time("checkedAt")
	}
	if ts.IsZero() {
		ts = occ.Time("notifiedAt")
	}
	if ts.IsZero() {
		ts = occ.Time("dueAt")
	}
	if ts.IsZero() {
		ts = occ.CreatedAt
	}
	evType := classifyOccurrenceEvent(occ)
	return ReminderHistoryRow{
		ID:            occ.ID,
		ScheduleID:    schedRef.ID,
		ScheduleTitle: title,
		ScheduleKind:  strings.TrimSpace(sched.Text("scheduleKind")),
		EventType:     evType,
		OutcomeStatus: strings.TrimSpace(occ.Text("outcomeStatus")),
		DueDate:       OccurrenceDayKey(occ, loc),
		Timestamp:     ts,
		AckedAt:       occ.Time("acknowledgedAt"),
		NotifyChannel: strings.TrimSpace(occ.Text("notifyChannel")),
		Message:       strings.TrimSpace(occ.Text("message")),
		AckRequired:   occ.Bool("ackRequired"),
		Ref:           occ.Ref(),
		ScheduleRef:   schedRef,
	}
}

func classifyOccurrenceEvent(occ Record) string {
	if IsCheckOffOccurrence(occ) {
		return "checkoff"
	}
	if occ.Bool("ackRequired") || !occ.Time("acknowledgedAt").IsZero() {
		return "persistent"
	}
	if strings.TrimSpace(occ.Text("notifiedAt")) != "" {
		return "temporary"
	}
	return "occurrence"
}

// ToggleDailyCheckOff toggles today's check-off occurrence for scheduleRef.
// Checking creates or updates today's check-off to outcomeStatus="done".
// Unchecking marks ONLY today's occurrence as outcomeStatus="skipped", leaving
// all previous days' check-off history permanently untouched.
func (s *Store) ToggleDailyCheckOff(scheduleRef core.Ref, now time.Time, actor core.Actor) (checked bool, occ *Record, err error) {
	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	today := DayKey(now, loc)
	res, err := s.Query(Query{Kind: KindOccurrence})
	if err != nil {
		return false, nil, err
	}
	var existing *Record
	for i := range res.Items {
		cand := res.Items[i]
		if !IsCheckOffOccurrence(cand) {
			continue
		}
		if ref, ok := cand.RefField("schedule"); ok && ref.ID == scheduleRef.ID && OccurrenceDayKey(cand, loc) == today {
			cp := cand
			existing = &cp
			break
		}
	}
	nowStr := now.UTC().Format(TimeFormat)
	if existing != nil {
		nextStatus := "done"
		checked = true
		summary := "check off daily reminder"
		if existing.Text("outcomeStatus") == "done" {
			nextStatus = "skipped"
			checked = false
			summary = "uncheck daily reminder for today"
		}
		rec, _, err := s.Patch(PatchInput{
			Ref: existing.Ref(),
			Fields: map[string]any{
				"outcomeStatus": nextStatus,
				"checkedAt":     nowStr,
				"dueDate":       today,
				"checkOff":      true,
			},
			Summary: summary,
		}, actor)
		return checked, rec, err
	}
	rec, _, err := s.Create(CreateInput{
		Kind: KindOccurrence,
		Fields: map[string]any{
			"schedule":      scheduleRef,
			"dueAt":         nowStr,
			"dueDate":       today,
			"outcomeStatus": "done",
			"checkedAt":     nowStr,
			"checkOff":      true,
		},
	}, actor)
	return true, rec, err
}

// AcknowledgeOccurrence marks a pending persistent reminder occurrence as
// acknowledged (outcomeStatus="done", ackRequired=false, acknowledgedAt=now).
func (s *Store) AcknowledgeOccurrence(occRef core.Ref, now time.Time, actor core.Actor) (*Record, error) {
	nowStr := now.UTC().Format(TimeFormat)
	rec, _, err := s.Patch(PatchInput{
		Ref: occRef,
		Fields: map[string]any{
			"outcomeStatus":  "done",
			"ackRequired":    false,
			"acknowledgedAt": nowStr,
			"checkedAt":      nowStr,
		},
		Summary: "acknowledge persistent reminder notification",
	}, actor)
	return rec, err
}

// AcknowledgeAllPendingOccurrences acknowledges every pending persistent alert.
func (s *Store) AcknowledgeAllPendingOccurrences(now time.Time, actor core.Actor) (int, error) {
	res, err := s.Query(Query{Kind: KindOccurrence})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, occ := range res.Items {
		if !occ.Bool("ackRequired") || occ.Text("outcomeStatus") != "pending" {
			continue
		}
		if _, err := s.AcknowledgeOccurrence(occ.Ref(), now, actor); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
