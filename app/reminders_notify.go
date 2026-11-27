package app

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/toast"
)

// Top-right Matomeru reminder notifications (temporary 30s default + persistent
// until acknowledged) and the interactive persistent-alert focus overlay.
const (
	ModeReminderAlerts       = "reminder-alerts"
	ScopeReminderNotify      = "reminders.notify"
	ScopeReminderAlerts      = "reminders.alerts"
	reminderTopRightAnchorID = "reminders.anchor"
	reminderNotifyOverlayID  = "reminders.notify"
	reminderAlertsModalID    = "reminders.alerts"
	reminderAlertsFocusID    = "reminders.alerts.list"

	CmdRemindersAlertFocus    = "reminders.alert.focus"
	CmdRemindersAlertAck      = "reminders.alert.ack"
	CmdRemindersAlertAckAll   = "reminders.alert.ack_all"
	CmdRemindersAlertOpenPage = "reminders.alert.open"
)

func reminderAlertCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdRemindersAlertFocus, "Jump focus to the top-right persistent reminder notifications overlay"),
		{ID: CmdRemindersAlertAck, Description: "Acknowledge the selected persistent notification", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: CmdRemindersAlertAckAll, Description: "Acknowledge all persistent notifications", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: CmdRemindersAlertOpenPage, Description: "Open the Reminders page from the notification overlay", Meta: map[string]any{"internal": true}},
	}
}

func reminderAlertBindings() []core.Binding {
	m := ModeReminderAlerts
	return []core.Binding{
		// Global binding (also leader-mirrored via ctrl+a g N inside live terminal).
		bind(ModeNormal, "", "g N", CmdRemindersAlertFocus, "focus notifications"),

		// Scoped actions inside the focused top-right persistent alerts overlay.
		bind(m, "", "j", "motion.down", "next alert"),
		bind(m, "", "k", "motion.up", "previous alert"),
		hidden(bind(m, "", "down", "motion.down", "next alert")),
		hidden(bind(m, "", "up", "motion.up", "previous alert")),
		hidden(bind(m, "", "g g", "motion.top", "first alert")),
		hidden(bind(m, "", "G", "motion.bottom", "last alert")),
		bind(m, "", "enter", CmdRemindersAlertAck, "acknowledge"),
		bind(m, "", "space", CmdRemindersAlertAck, "acknowledge"),
		bind(m, "", "a", CmdRemindersAlertAck, "acknowledge"),
		bind(m, "", "A", CmdRemindersAlertAckAll, "acknowledge all"),
		bind(m, "", "o", CmdRemindersAlertOpenPage, "open reminders"),
		bind(m, "", "esc", CmdModalCancel, "return"),
		hidden(bind(m, "", "q", CmdModalCancel, "return")),
	}
}

func (a *App) registerReminderAlertHandlers() {
	a.handle(CmdRemindersAlertFocus, a.remindersAlertFocus)
	a.handle(CmdRemindersAlertAck, a.handleModal)
	a.handle(CmdRemindersAlertAckAll, a.handleModal)
	a.handle(CmdRemindersAlertOpenPage, a.handleModal)
}

// --- Notification syncing, deduplication, and TTL expiry -----------------------

func (a *App) syncReminderNotifications(now time.Time) {
	if a == nil || a.reminders == nil || a.Store == nil {
		return
	}
	rc := a.reminders
	rev := a.storeRev()
	if rc.lastSyncOK && rc.lastSyncRev == rev {
		return
	}
	rc.lastSyncRev = rev
	rc.lastSyncOK = true

	schedules, occurrences := a.queryReminderRecords()
	rc.pendingAlerts = data.PendingPersistentAlerts(schedules, occurrences)
	schedByID := make(map[string]data.Record, len(schedules))
	for _, s := range schedules {
		schedByID[s.ID] = s
	}
	for _, occ := range occurrences {
		a.ingestTemporaryReminderOccurrence(rc, occ, schedByID, now)
	}
	if rc.toasts != nil {
		rc.toasts.Tick(now)
	}
}

func (a *App) ingestTemporaryReminderOccurrence(rc *remindersController, occ data.Record, schedByID map[string]data.Record, now time.Time) {
	if occ.IsArchived() || occ.Bool("ackRequired") || rc.seenTempOcc[occ.ID] {
		return
	}
	notifiedAt := occ.Time("notifiedAt")
	if notifiedAt.IsZero() {
		return
	}
	if !data.ChannelIncludesMTM(data.EffectiveNotifyChannel(occ)) {
		rc.seenTempOcc[occ.ID] = true
		return
	}
	rc.seenTempOcc[occ.ID] = true
	ttl := data.EffectiveNotifyTTL(occ)
	if !notifiedAt.Add(ttl).After(now) {
		return
	}
	title := resolveReminderOccTitle(occ, schedByID)
	msg := strings.TrimSpace(occ.Text("message"))
	rc.toasts.PushToast(toast.Toast{
		ID:        occ.ID,
		Text:      formatReminderToastText(title, msg),
		Level:     toast.Info,
		CreatedAt: notifiedAt,
		TTL:       ttl,
	})
}

func resolveReminderOccTitle(occ data.Record, schedByID map[string]data.Record) string {
	if ref, ok := occ.RefField("schedule"); ok {
		if sched, exists := schedByID[ref.ID]; exists {
			if t := strings.TrimSpace(sched.Text("title")); t != "" {
				return t
			}
		}
	}
	if m := strings.TrimSpace(occ.Text("message")); m != "" {
		return m
	}
	return "Reminder"
}

func formatReminderToastText(title, msg string) string {
	if msg == "" || msg == title {
		return title
	}
	return title + " · " + msg
}

func (a *App) tickReminderToasts() bool {
	if a == nil || a.reminders == nil || a.reminders.toasts == nil {
		return false
	}
	now := a.now()
	a.syncReminderNotifications(now)
	a.reminders.toasts.SetEnabled(a.reminderToastsEnabled())
	return a.reminders.toasts.Tick(now)
}

func (a *App) reminderToastDue() bool {
	if a == nil || a.reminders == nil || a.reminders.toasts == nil || !a.reminderToastsEnabled() {
		return false
	}
	exp, ok := a.reminders.toasts.NextExpiry()
	return ok && !a.now().Before(exp)
}

func (a *App) mtmPendingAlerts() []data.ReminderAlert {
	if a == nil || a.reminders == nil {
		return nil
	}
	a.syncReminderNotifications(a.now())
	var out []data.ReminderAlert
	for _, al := range a.reminders.pendingAlerts {
		if data.ChannelIncludesMTM(al.NotifyChannel) {
			out = append(out, al)
		}
	}
	return out
}

func (a *App) activeReminderToasts(now time.Time) []toast.Toast {
	if a == nil || a.reminders == nil || a.reminders.toasts == nil || !a.reminderToastsEnabled() {
		return nil
	}
	a.reminders.toasts.SetEnabled(true)
	a.reminders.toasts.Tick(now)
	return a.reminders.toasts.Active()
}

func (a *App) reminderNotifyViewKey() string {
	if a == nil || a.reminders == nil || !a.reminderToastsEnabled() {
		return ""
	}
	now := a.now()
	alerts := a.mtmPendingAlerts()
	toasts := a.activeReminderToasts(now)
	if len(alerts) == 0 && len(toasts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, al := range alerts {
		fmt.Fprintf(&b, "p:%s|", al.OccurrenceID)
	}
	for _, t := range toasts {
		fmt.Fprintf(&b, "t:%s|", t.ID)
	}
	return b.String()
}

// --- Passive top-right notification overlay ------------------------------------

func (a *App) appendReminderOverlay(overlays []ui.ViewNode) []ui.ViewNode {
	ov, ok := a.reminderOverlay()
	if !ok {
		return overlays
	}
	return append(overlays, ov)
}

func (a *App) reminderOverlay() (ui.ViewNode, bool) {
	if a == nil || a.reminders == nil || !a.reminderToastsEnabled() {
		return ui.ViewNode{}, false
	}
	if a.modals != nil && a.modals.Get(reminderAlertsModalID) != nil {
		return ui.ViewNode{}, false
	}
	now := a.now()
	a.syncReminderNotifications(now)
	alerts := a.mtmPendingAlerts()
	toasts := a.activeReminderToasts(now)
	if len(alerts) == 0 && len(toasts) == 0 {
		return ui.ViewNode{}, false
	}
	return buildReminderNotifyOverlay(alerts, toasts, now, a.topChromeRedesignEnabled()), true
}

func buildReminderNotifyOverlay(alerts []data.ReminderAlert, toasts []toast.Toast, now time.Time, hasTopAnchor bool) ui.ViewNode {
	kids := make([]ui.ViewNode, 0, len(alerts)+len(toasts)+1)
	for i, al := range alerts {
		if i >= 4 {
			kids = append(kids, text(fmt.Sprintf("rem.more.%d", i), fmt.Sprintf("  +%d more persistent alerts", len(alerts)-i), "muted"))
			break
		}
		kids = append(kids, text("rem.p."+al.OccurrenceID, "▲ "+formatReminderToastText(al.Title, al.Message), "warning"))
	}
	for i := len(toasts) - 1; i >= 0; i-- {
		t := toasts[i]
		rem := int(t.CreatedAt.Add(t.TTL).Sub(now).Round(time.Second).Seconds())
		if rem < 1 {
			rem = 1
		}
		kids = append(kids, text("rem.t."+t.ID, fmt.Sprintf("🔔 %s (%ds)", t.Text, rem), "info"))
	}
	title, tone, badgeTone := "🔔 Reminders", "info", "accent"
	if len(alerts) > 0 {
		title, tone, badgeTone = "▲ Reminder Alert", "warning", "warning"
		kids = append(kids, text("rem.hint", "g N focus · a ack · A ack all", "muted"))
	}
	ov := modal.Frame{
		ID:       reminderNotifyOverlayID,
		Scope:    ScopeReminderNotify,
		Title:    title,
		Tone:     tone,
		WidthPct: 44,
		Badges:   []ui.Badge{{Text: fmt.Sprint(len(alerts) + len(toasts)), Tone: badgeTone}},
		Facts:    map[string]any{"pendingCount": len(alerts), "toastCount": len(toasts)},
	}.Overlay(kids...)
	if hasTopAnchor {
		if ov.Props == nil {
			ov.Props = map[string]any{}
		}
		ov.Props["anchorId"] = reminderTopRightAnchorID
	}
	return ov
}

// --- Interactive top-right persistent alerts modal (g N / ctrl+a g N) ----------

type reminderAlertsModal struct {
	a        *App
	selected int
}

var _ modal.Modal = (*reminderAlertsModal)(nil)

func (m *reminderAlertsModal) ID() string      { return reminderAlertsModalID }
func (m *reminderAlertsModal) Mode() string    { return ModeReminderAlerts }
func (m *reminderAlertsModal) Scope() string   { return ScopeReminderAlerts }
func (m *reminderAlertsModal) FocusID() string { return reminderAlertsFocusID }
func (m *reminderAlertsModal) OnClose(modal.CloseReason) {
}

func (a *App) remindersAlertFocus(s core.State, _ core.Command) core.StepResult {
	alerts := a.mtmPendingAlerts()
	if len(alerts) == 0 {
		a.say("no pending persistent notifications")
		return core.Next(s)
	}
	return core.Next(a.pushModal(s, &reminderAlertsModal{a: a}))
}

func (m *reminderAlertsModal) View(_, _ int) ui.ViewNode {
	alerts := m.a.mtmPendingAlerts()
	if len(alerts) == 0 {
		return m.buildOverlay([]ui.ViewNode{text(reminderAlertsFocusID, "All caught up!", "muted")}, 0)
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(alerts) {
		m.selected = len(alerts) - 1
	}
	kids := make([]ui.ViewNode, 0, len(alerts)+1)
	for i, al := range alerts {
		prefix, tone := "  ▲ ", "warning"
		if i == m.selected {
			prefix, tone = "▸ ▲ ", "accent"
		}
		lineText := prefix + formatReminderToastText(al.Title, al.Message)
		nodeID := fmt.Sprintf("reminders.alert.%d", i)
		focusable := false
		if i == m.selected {
			nodeID = reminderAlertsFocusID
			focusable = true
		}
		kids = append(kids, ui.ViewNode{
			ID:        nodeID,
			Kind:      ui.KindText,
			Tone:      tone,
			Focusable: focusable,
			Props:     map[string]any{"text": lineText},
		})
	}
	kids = append(kids, text("reminders.alerts.hint", "j/k pick · enter/space/a ack · A ack all · o reminders · esc close", "muted"))
	return m.buildOverlay(kids, len(alerts))
}

func (m *reminderAlertsModal) buildOverlay(kids []ui.ViewNode, count int) ui.ViewNode {
	ov := modal.Frame{
		ID:       reminderAlertsModalID,
		Scope:    ScopeReminderAlerts,
		Title:    "▲ Persistent Notifications",
		Tone:     "warning",
		WidthPct: 48,
		Badges:   []ui.Badge{{Text: fmt.Sprint(count), Tone: "warning"}},
		Facts:    map[string]any{"pendingCount": count},
	}.Overlay(kids...)
	if m.a.topChromeRedesignEnabled() {
		if ov.Props == nil {
			ov.Props = map[string]any{}
		}
		ov.Props["anchorId"] = reminderTopRightAnchorID
	}
	return ov
}

func (m *reminderAlertsModal) Handle(c core.Command) modal.Result {
	alerts := m.a.mtmPendingAlerts()
	if m.handleMotion(c.ID, len(alerts)) {
		return modal.Result{Handled: true}
	}
	switch c.ID {
	case CmdRemindersAlertAck:
		return m.ackSelected(alerts)
	case CmdRemindersAlertAckAll:
		return m.ackAll()
	case CmdRemindersAlertOpenPage:
		m.a.afterModal(func(st core.State) core.State { return m.a.openSpace(st, SpaceReminders) })
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	case CmdModalCancel:
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel}
	}
	return modal.Result{}
}

func (m *reminderAlertsModal) handleMotion(id string, n int) bool {
	switch id {
	case "motion.down":
		if n > 0 && m.selected < n-1 {
			m.selected++
		}
		return true
	case "motion.up":
		if m.selected > 0 {
			m.selected--
		}
		return true
	case "motion.top":
		m.selected = 0
		return true
	case "motion.bottom":
		if n > 0 {
			m.selected = n - 1
		}
		return true
	}
	return false
}

func (m *reminderAlertsModal) ackSelected(alerts []data.ReminderAlert) modal.Result {
	if len(alerts) == 0 || m.a.Store == nil {
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	}
	if m.selected >= len(alerts) {
		m.selected = len(alerts) - 1
	}
	al := alerts[m.selected]
	if _, err := m.a.Store.AcknowledgeOccurrence(al.OccRef, m.a.now(), User); err != nil {
		return modal.Result{Handled: true, Status: "acknowledge failed: " + err.Error(), SetStatus: true}
	}
	m.a.invalidateReminderSync()
	remaining := m.a.mtmPendingAlerts()
	if len(remaining) == 0 {
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "acknowledged: " + al.Title, SetStatus: true}
	}
	if m.selected >= len(remaining) {
		m.selected = len(remaining) - 1
	}
	return modal.Result{Handled: true, Status: fmt.Sprintf("acknowledged: %s (%d remaining)", al.Title, len(remaining)), SetStatus: true}
}

func (m *reminderAlertsModal) ackAll() modal.Result {
	if m.a.Store == nil {
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	}
	n, err := m.a.Store.AcknowledgeAllPendingOccurrences(m.a.now(), User)
	if err != nil {
		return modal.Result{Handled: true, Status: "acknowledge all failed: " + err.Error(), SetStatus: true}
	}
	m.a.invalidateReminderSync()
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: fmt.Sprintf("acknowledged %d persistent notifications", n), SetStatus: true}
}

func (a *App) invalidateReminderSync() {
	if a == nil {
		return
	}
	a.refreshStoreRev()
	if a.reminders != nil {
		a.reminders.lastSyncOK = false
	}
	a.viewMemo.gen++
}
