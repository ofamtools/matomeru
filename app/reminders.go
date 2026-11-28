package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// Space, modes, scopes, and commands for the top-level Reminders page.
const (
	SpaceReminders = "reminders"

	CmdRemindersOpen        = "reminders.open"
	cmdRemindersMove        = "reminders.rows.move"
	CmdRemindersToggle      = "reminders.toggle"
	CmdRemindersAck         = "reminders.ack"
	CmdRemindersAckAll      = "reminders.ack_all"
	CmdRemindersNew         = "reminders.new"
	CmdRemindersEdit        = "reminders.edit"
	CmdRemindersPauseToggle = "reminders.pause"
	CmdRemindersTestFire    = "reminders.test_fire"
	CmdRemindersViewCycle   = "reminders.view.cycle"
	CmdRemindersArchive     = "reminders.archive"

	remindersFilterField = "search"
)

// remindersController owns the Reminders page state and top-right notification
// overlay state (temporary 30s toasts + persistent pending alerts).
type remindersController struct {
	list           *table.Model
	viewMode       string
	visible        []data.ReminderRow
	history        []data.ReminderHistoryRow
	pendingAlerts  []data.ReminderAlert
	totalDaily     int
	doneDaily      int
	totalScheduled int
	totalHistory   int

	toasts      *toast.Model
	seenTempOcc map[string]bool
	lastSyncRev int64
	lastSyncOK  bool
	notifier    daemon.Notifier
}

func (a *App) initReminders() {
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = remindersFilterField

	tm := toast.NewWithClock(a.now)
	tm.DefaultTTL = time.Duration(data.DefaultReminderTTLSeconds) * time.Second
	var notifier daemon.Notifier
	if a.opts.Live {
		notifier = daemon.MacOSNotifier{}
	}
	a.reminders = &remindersController{
		list:        list,
		viewMode:    views.RemindersViewAll,
		toasts:      tm,
		seenTempOcc: map[string]bool{},
		notifier:    notifier,
	}
	a.registerFilterable(views.RemindersListID, list)
	a.syncReminderNotifications(a.now())
}

func remindersCommandSpecs() []core.CommandSpec {
	out := []core.CommandSpec{
		spec(CmdRemindersOpen, "Open the Reminders page (daily check-offs, scheduled notifications, history)"),
		{
			ID:          cmdRemindersMove,
			Description: "Move selection on the Reminders page",
			Params:      []core.ParamSpec{{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down", "top", "bottom"}}},
			Meta:        map[string]any{"internal": true},
		},
		spec(CmdRemindersToggle, "Toggle today's daily check-off or acknowledge the selected pending alert", "editing"),
		spec(CmdRemindersAck, "Acknowledge the selected pending persistent notification", "editing"),
		spec(CmdRemindersAckAll, "Acknowledge all pending persistent notifications", "editing"),
		{
			ID:          CmdRemindersNew,
			Description: "Create a new daily check-off or scheduled notification reminder",
			Tags:        []string{"editing"},
			Params: []core.ParamSpec{
				{Name: "kind", Type: core.ParamString, Enum: []string{data.ScheduleKindDaily, data.ScheduleKindRemind}, Description: "optional scheduleKind default (daily or remind)"},
			},
		},
		spec(CmdRemindersEdit, "Edit the selected reminder", "editing"),
		spec(CmdRemindersPauseToggle, "Pause or resume the selected reminder", "editing"),
		spec(CmdRemindersTestFire, "Test-fire the selected reminder notification (OS + Matomeru top-right)", "editing"),
		{
			ID:          CmdRemindersViewCycle,
			Description: "Cycle the Reminders sub-view (all, daily, scheduled, pending, history)",
			Params: []core.ParamSpec{
				{Name: "dir", Type: core.ParamInt, Default: 1, Description: "direction: 1 (next) or -1 (prev)"},
			},
		},
		spec(CmdRemindersArchive, "Archive or restore the selected reminder", "editing"),
	}
	return append(out, reminderAlertCommandSpecs()...)
}

func remindersBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeReminders
	sel := "facts.hasSelection"
	canEdit := "facts.canEdit"
	out := []core.Binding{
		// Global binding to open Reminders (auto-mirrored via ctrl+a g M in live terminal).
		bind(n, "", "g M", CmdRemindersOpen, "reminders"),

		// Scoped navigation and actions on the Reminders page.
		with(bind(n, scope, "j", cmdRemindersMove, "next reminder"), map[string]any{"direction": "down"}),
		with(bind(n, scope, "k", cmdRemindersMove, "previous reminder"), map[string]any{"direction": "up"}),
		hidden(with(bind(n, scope, "down", cmdRemindersMove, "next reminder"), map[string]any{"direction": "down"})),
		hidden(with(bind(n, scope, "up", cmdRemindersMove, "previous reminder"), map[string]any{"direction": "up"})),
		hidden(with(bind(n, scope, "g g", cmdRemindersMove, "first reminder"), map[string]any{"direction": "top"})),
		hidden(with(bind(n, scope, "G", cmdRemindersMove, "last reminder"), map[string]any{"direction": "bottom"})),

		when(bind(n, scope, "space", CmdRemindersToggle, "check-off / ack"), "facts.canToggle"),
		when(bind(n, scope, "x", CmdRemindersToggle, "check-off / ack"), "facts.canToggle"),
		when(bind(n, scope, "a", CmdRemindersAck, "acknowledge alert"), "facts.pendingCount > 0"),
		when(bind(n, scope, "A", CmdRemindersAckAll, "acknowledge all"), "facts.pendingCount > 0"),
		when(bind(n, scope, "n", CmdRemindersNew, "new reminder"), "!facts.hasFilter && !facts.hasSearch"),
		bind(n, scope, "c", CmdRemindersNew, "new reminder"),
		when(bind(n, scope, "e", CmdRemindersEdit, "edit reminder"), canEdit),
		when(bind(n, scope, "p", CmdRemindersPauseToggle, "pause/resume"), canEdit),
		when(bind(n, scope, "t", CmdRemindersTestFire, "test-fire notify"), "facts.canTestFire"),
		with(bind(n, scope, "v", CmdRemindersViewCycle, "cycle view"), map[string]any{"dir": 1}),
		when(bind(n, scope, "d", CmdRemindersArchive, "archive/restore"), sel),
	}
	return append(out, reminderAlertBindings()...)
}

func (a *App) registerReminders() {
	a.handle(CmdRemindersOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceReminders))
	})
	a.handle(cmdRemindersMove, func(s core.State, c core.Command) core.StepResult {
		a.syncReminders()
		c.ID = "motion." + c.Arg("direction")
		a.reminders.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(CmdRemindersToggle, a.remindersToggle)
	a.handle(CmdRemindersAck, a.remindersAck)
	a.handle(CmdRemindersAckAll, a.remindersAckAll)
	a.handle(CmdRemindersNew, a.remindersNew)
	a.handle(CmdRemindersEdit, a.remindersEdit)
	a.handle(CmdRemindersPauseToggle, a.remindersPauseToggle)
	a.handle(CmdRemindersTestFire, a.remindersTestFire)
	a.handle(CmdRemindersViewCycle, a.remindersViewCycle)
	a.handle(CmdRemindersArchive, a.remindersArchive)
	a.registerReminderAlertHandlers()
}

// --- Sync & projection ---------------------------------------------------------

func (a *App) queryReminderRecords() (schedules, occurrences []data.Record) {
	if a.Store == nil {
		return nil, nil
	}
	if res, err := a.Store.Query(data.Query{Kind: data.KindSchedule, IncludeArchived: a.nav.showArchived}); err == nil {
		schedules = res.Items
	}
	if res, err := a.Store.Query(data.Query{Kind: data.KindOccurrence}); err == nil {
		occurrences = res.Items
	}
	return schedules, occurrences
}

func (a *App) syncReminders() {
	if a.reminders == nil {
		return
	}
	rc := a.reminders
	now := a.now()
	schedules, occurrences := a.queryReminderRecords()
	pending, daily, scheduled := data.BuildReminderRows(schedules, occurrences, now)
	history := data.BuildReminderHistory(schedules, occurrences, now.Location())

	rc.pendingAlerts = data.PendingPersistentAlerts(schedules, occurrences)
	rc.totalDaily = len(daily)
	rc.doneDaily = countDoneDaily(daily)
	rc.totalScheduled = len(scheduled)
	rc.totalHistory = len(history)

	if rc.viewMode == views.RemindersViewHistory {
		a.syncRemindersHistoryList(rc, history)
		return
	}
	a.syncRemindersMainList(rc, collectRemindersRowsForView(rc.viewMode, pending, daily, scheduled))
}

func countDoneDaily(daily []data.ReminderRow) int {
	n := 0
	for _, r := range daily {
		if r.Daily.CheckedToday {
			n++
		}
	}
	return n
}

func collectRemindersRowsForView(mode string, pending, daily, scheduled []data.ReminderRow) []data.ReminderRow {
	switch mode {
	case views.RemindersViewDaily:
		return daily
	case views.RemindersViewScheduled:
		return scheduled
	case views.RemindersViewPending:
		return pending
	default:
		out := make([]data.ReminderRow, 0, len(pending)+len(daily)+len(scheduled))
		out = append(out, pending...)
		out = append(out, daily...)
		out = append(out, scheduled...)
		return out
	}
}

func (a *App) syncRemindersMainList(rc *remindersController, all []data.ReminderRow) {
	rows := make([]table.Row, 0, len(all))
	byID := make(map[string]data.ReminderRow, len(all))
	for _, r := range all {
		byID[r.ID] = r
		rows = append(rows, table.Row{
			ID:    r.ID,
			Cells: map[string]any{remindersFilterField: reminderRowFilterText(r)},
		})
	}
	shown := syncTableSelection(rc.list, rows, "")
	rc.visible = rc.visible[:0]
	for _, r := range shown {
		rc.visible = append(rc.visible, byID[r.ID])
	}
	rc.history = rc.history[:0]
}

func (a *App) syncRemindersHistoryList(rc *remindersController, all []data.ReminderHistoryRow) {
	rows := make([]table.Row, 0, len(all))
	byID := make(map[string]data.ReminderHistoryRow, len(all))
	for _, h := range all {
		byID[h.ID] = h
		rows = append(rows, table.Row{
			ID:    h.ID,
			Cells: map[string]any{remindersFilterField: reminderHistoryFilterText(h)},
		})
	}
	shown := syncTableSelection(rc.list, rows, "")
	rc.history = rc.history[:0]
	for _, r := range shown {
		rc.history = append(rc.history, byID[r.ID])
	}
	rc.visible = rc.visible[:0]
}

func reminderRowFilterText(r data.ReminderRow) string {
	status := "active"
	if r.Paused {
		status = "paused"
	}
	if r.RowKind == "daily" && r.Daily.CheckedToday {
		status = "done checked"
	}
	return strings.Join([]string{r.Title, r.Message, r.RowKind, r.ScheduleKind, r.NotifyChannel, r.Persistence, r.TimeOfDay, r.Cron, status}, " ")
}

func reminderHistoryFilterText(h data.ReminderHistoryRow) string {
	return strings.Join([]string{h.ScheduleTitle, h.EventType, h.OutcomeStatus, h.DueDate, h.NotifyChannel, h.Message}, " ")
}

func (a *App) selectedReminderRow() (data.ReminderRow, bool) {
	a.syncReminders()
	if a.reminders == nil || a.reminders.viewMode == views.RemindersViewHistory {
		return data.ReminderRow{}, false
	}
	row, ok := a.reminders.list.FocusedRow()
	if !ok || row.ID == "" {
		return data.ReminderRow{}, false
	}
	idx := slices.IndexFunc(a.reminders.visible, func(r data.ReminderRow) bool { return r.ID == row.ID })
	if idx < 0 {
		return data.ReminderRow{}, false
	}
	return a.reminders.visible[idx], true
}

func (a *App) selectedReminderHistoryRow() (data.ReminderHistoryRow, bool) {
	a.syncReminders()
	if a.reminders == nil || a.reminders.viewMode != views.RemindersViewHistory {
		return data.ReminderHistoryRow{}, false
	}
	row, ok := a.reminders.list.FocusedRow()
	if !ok || row.ID == "" {
		return data.ReminderHistoryRow{}, false
	}
	idx := slices.IndexFunc(a.reminders.history, func(h data.ReminderHistoryRow) bool { return h.ID == row.ID })
	if idx < 0 {
		return data.ReminderHistoryRow{}, false
	}
	return a.reminders.history[idx], true
}

// --- View & tab helpers --------------------------------------------------------

func (a *App) remindersSpace() ui.ViewNode {
	a.syncReminders()
	rc := a.reminders
	selIdx := rc.list.SelectedIdx
	if rc.viewMode == views.RemindersViewHistory && len(rc.history) == 0 {
		selIdx = -1
	} else if rc.viewMode != views.RemindersViewHistory && len(rc.visible) == 0 {
		selIdx = -1
	}
	return views.RemindersPageView(views.RemindersPageSpec{
		ViewMode:       rc.viewMode,
		Rows:           rc.visible,
		HistoryRows:    rc.history,
		PendingAlerts:  rc.pendingAlerts,
		SelectedIdx:    selIdx,
		TotalDaily:     rc.totalDaily,
		DoneDaily:      rc.doneDaily,
		TotalScheduled: rc.totalScheduled,
		TotalHistory:   rc.totalHistory,
		FilterQuery:    rc.list.FilterQuery(),
		Now:            a.now(),
	})
}

func (a *App) remindersTabTitle() string {
	if n := a.remindersPendingCount(); n > 0 {
		return fmt.Sprintf("Reminders ▲%d", n)
	}
	return "Reminders"
}

func (a *App) remindersPendingCount() int {
	if a == nil || a.reminders == nil {
		return 0
	}
	a.syncReminderNotifications(a.now())
	return len(a.reminders.pendingAlerts)
}

func (a *App) remindersMemoKey() string {
	if a == nil || a.reminders == nil {
		return ""
	}
	rc := a.reminders
	return fmt.Sprintf("|rem:%t:%s:%d:%s:%d",
		a.remindersTopNavTabEnabled(),
		rc.viewMode,
		rc.list.SelectedIdx,
		data.DayKey(a.now(), a.now().Location()),
		len(rc.pendingAlerts),
	)
}

// --- Actions -------------------------------------------------------------------

func (a *App) remindersToggle(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedReminderRow()
	if !ok || a.Store == nil {
		a.say("no daily reminder or pending alert selected")
		return core.Next(s)
	}
	switch row.RowKind {
	case "pending":
		return a.ackPendingOccurrence(s, row.Ref, row.Title)
	case "daily":
		checked, _, err := a.Store.ToggleDailyCheckOff(row.ScheduleRef, a.now(), User)
		if err != nil {
			a.fail("toggle daily check-off", err)
			return core.Next(s)
		}
		if checked {
			a.say("checked off today: %s", row.Title)
		} else {
			a.say("unchecked today: %s", row.Title)
		}
		return core.Next(s)
	default:
		a.say("%s is a scheduled notification · t test-fires · p pauses", row.Title)
		return core.Next(s)
	}
}

func (a *App) ackPendingOccurrence(s core.State, ref core.Ref, title string) core.StepResult {
	if _, err := a.Store.AcknowledgeOccurrence(ref, a.now(), User); err != nil {
		a.fail("acknowledge reminder", err)
		return core.Next(s)
	}
	a.invalidateReminderSync()
	a.say("acknowledged: %s", title)
	return core.Next(s)
}

func (a *App) remindersAck(s core.State, _ core.Command) core.StepResult {
	if a.Store == nil {
		return core.Next(s)
	}
	if row, ok := a.selectedReminderRow(); ok && row.RowKind == "pending" {
		return a.ackPendingOccurrence(s, row.Ref, row.Title)
	}
	a.syncReminders()
	if len(a.reminders.pendingAlerts) == 0 {
		a.say("no pending persistent notifications")
		return core.Next(s)
	}
	first := a.reminders.pendingAlerts[0]
	return a.ackPendingOccurrence(s, first.OccRef, first.Title)
}

func (a *App) remindersAckAll(s core.State, _ core.Command) core.StepResult {
	if a.Store == nil {
		return core.Next(s)
	}
	n, err := a.Store.AcknowledgeAllPendingOccurrences(a.now(), User)
	if err != nil {
		a.fail("acknowledge all reminders", err)
		return core.Next(s)
	}
	if n == 0 {
		a.say("no pending persistent notifications")
		return core.Next(s)
	}
	a.invalidateReminderSync()
	a.say("acknowledged %d persistent notifications", n)
	return core.Next(s)
}

func (a *App) remindersNew(s core.State, c core.Command) core.StepResult {
	kind := strings.TrimSpace(c.Arg("kind"))
	if kind != data.ScheduleKindDaily && kind != data.ScheduleKindRemind {
		kind = data.ScheduleKindDaily
		if a.reminders != nil && a.reminders.viewMode == views.RemindersViewScheduled {
			kind = data.ScheduleKindRemind
		}
	}
	return a.create(s, core.Command{
		ID: views.CmdNew,
		Args: map[string]any{
			"kind":  data.KindSchedule,
			"title": "New Reminder",
			"fields": []string{
				"title", "scheduleKind", "timeOfDay", "cron",
				"notifyChannel", "notifyPersistence", "notifyTTLSeconds",
				"reminderMessage", "paused",
			},
			"choices": map[string][]string{
				"scheduleKind": {data.ScheduleKindDaily, data.ScheduleKindRemind},
			},
			"defaults": map[string]any{
				"scheduleKind":      kind,
				"notifyChannel":     data.NotifyChannelBoth,
				"notifyPersistence": data.NotifyPersistenceTemporary,
				"notifyTTLSeconds":  data.DefaultReminderTTLSeconds,
			},
		},
	})
}

func (a *App) remindersEdit(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedReminderRow()
	if !ok || row.ScheduleID == "" {
		a.say("no reminder selected to edit")
		return core.Next(s)
	}
	target := row.ScheduleRef
	return a.edit(s, core.Command{ID: views.CmdEdit, Target: &target})
}

func (a *App) remindersPauseToggle(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedReminderRow()
	if !ok || row.ScheduleID == "" {
		a.say("no reminder selected")
		return core.Next(s)
	}
	target := row.ScheduleRef
	if row.Paused {
		return a.scheduleResume(s, core.Command{ID: CmdScheduleResume, Target: &target})
	}
	return a.schedulePause(s, core.Command{ID: CmdSchedulePause, Target: &target})
}

func (a *App) remindersTestFire(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedReminderRow()
	if !ok || row.ScheduleID == "" || a.Store == nil {
		a.say("no reminder selected to test-fire")
		return core.Next(s)
	}
	rec, err := a.Store.Get(row.ScheduleRef)
	if err != nil || rec == nil {
		a.fail("test-fire reminder", err)
		return core.Next(s)
	}
	a.fireReminderNow(*rec)
	return core.Next(s)
}

func (a *App) fireReminderNow(rec data.Record) {
	now := a.now()
	channel := data.EffectiveNotifyChannel(rec)
	persistence := data.EffectiveNotifyPersistence(rec)
	ttlSec := data.EffectiveNotifyTTLSeconds(rec)
	title := strings.TrimSpace(rec.Text("title"))
	msg := strings.TrimSpace(rec.Text("reminderMessage"))
	body := msg
	if body == "" {
		body = "Test reminder notification"
	}

	osErr := a.deliverOSNotification(title, body, channel)
	outcome := "done"
	ackRequired := false
	if persistence == data.NotifyPersistencePersistent && data.ChannelIncludesMTM(channel) {
		outcome = "pending"
		ackRequired = true
	} else if osErr != nil {
		outcome = "failed"
	}
	nowStr := now.UTC().Format(data.TimeFormat)
	_, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindOccurrence,
		Fields: map[string]any{
			"schedule":      rec.Ref(),
			"dueAt":         nowStr,
			"checkedAt":     nowStr,
			"notifiedAt":    nowStr,
			"outcomeStatus": outcome,
			"ackRequired":   ackRequired,
			"notifyChannel": channel,
			"ttlSeconds":    ttlSec,
			"message":       msg,
		},
	}, User)
	if err != nil {
		a.fail("test-fire reminder", err)
		return
	}
	a.invalidateReminderSync()
	a.syncReminderNotifications(now)
	a.say("test-fired %s (%s · %s)", title, channel, persistence)
}

func (a *App) deliverOSNotification(title, body, channel string) error {
	if !data.ChannelIncludesOS(channel) || a.reminders == nil || a.reminders.notifier == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return a.reminders.notifier.Notify(ctx, title, body)
}

func (a *App) remindersViewCycle(s core.State, c core.Command) core.StepResult {
	dir := 1
	if d := intArg(c.Args["dir"]); d < 0 {
		dir = -1
	}
	return a.cycleRemindersViewDir(s, dir)
}

func (a *App) cycleRemindersViewDir(s core.State, dir int) core.StepResult {
	if a.reminders == nil {
		return core.Next(s)
	}
	next, _ := traits.Cycle(views.RemindersViewOrder, a.reminders.viewMode, dir, true)
	a.reminders.viewMode = next
	a.reminders.list.SelectedIdx, a.reminders.list.Offset = 0, 0
	a.say("reminders view: %s", next)
	return core.Next(s)
}

func (a *App) remindersArchive(s core.State, _ core.Command) core.StepResult {
	if a.Store == nil {
		return core.Next(s)
	}
	if h, ok := a.selectedReminderHistoryRow(); ok {
		return a.archiveOrRestoreReminder(s, h.Ref, h.ScheduleTitle, false)
	}
	row, ok := a.selectedReminderRow()
	if !ok {
		a.say("no reminder selected")
		return core.Next(s)
	}
	return a.archiveOrRestoreReminder(s, row.Ref, row.Title, row.Archived)
}

func (a *App) archiveOrRestoreReminder(s core.State, ref core.Ref, title string, archived bool) core.StepResult {
	if archived {
		if _, _, err := a.Store.Restore(ref, User); err != nil {
			a.fail("restore reminder", err)
			return core.Next(s)
		}
		a.say("restored reminder: %s", title)
		return core.Next(s)
	}
	if _, _, err := a.Store.Archive(ref, User); err != nil {
		a.fail("archive reminder", err)
		return core.Next(s)
	}
	a.say("archived reminder: %s", title)
	return core.Next(s)
}
