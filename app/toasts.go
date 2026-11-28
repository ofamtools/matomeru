package app

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/toast"
)

// --- Toast notifications (ochazuke/widgets/toast) --------------------------------

func (a *App) initToasts() {
	a.rt.toasts = toast.NewWithClock(a.now)
	a.syncToastSetting()
}

func (a *App) syncToastSetting() {
	if a.rt.toasts == nil {
		return
	}
	a.rt.toasts.SetEnabled(a.cfg.Settings.Bool(SetToast, true))
}

// pushToast adds a transient non-blocking notification when ui.toast is enabled.
func (a *App) pushToast(text string, level toast.Level, ttl ...time.Duration) {
	if a.rt.toasts == nil {
		return
	}
	a.syncToastSetting()
	if !a.rt.toasts.Enabled() || strings.TrimSpace(text) == "" {
		return
	}
	a.rt.toasts.Tick(a.now())
	a.rt.toasts.Push(text, level, ttl...)
	a.viewMemo.gen++
}

// tickToasts prunes expired notifications and reports whether any expired.
func (a *App) tickToasts() bool {
	expired := a.tickReminderToasts()
	if a.rt.toasts == nil {
		return expired
	}
	return a.rt.toasts.Tick(a.now()) || expired
}

// toastOverlay returns the floating toast overlay node when ui.toast is enabled
// and at least one non-expired toast is active.
func (a *App) toastOverlay() (ui.ViewNode, bool) {
	if a.rt.toasts == nil || !a.cfg.Settings.Bool(SetToast, true) {
		return ui.ViewNode{}, false
	}
	a.rt.toasts.Tick(a.now())
	if a.rt.toasts.Len() == 0 {
		return ui.ViewNode{}, false
	}
	ov := a.rt.toasts.ViewOverlay("app.toast")
	if ov.Props == nil {
		ov.Props = map[string]any{}
	}
	ov.Props["anchorId"] = "status"
	ov.Props["widthPct"] = 48
	return ov, true
}

func (a *App) toastViewKey() string {
	remKey := a.reminderNotifyViewKey()
	if a.rt.toasts == nil || !a.cfg.Settings.Bool(SetToast, true) {
		return remKey
	}
	now := a.now()
	active := a.rt.toasts.Active()
	if len(active) == 0 {
		return remKey
	}
	var b strings.Builder
	for _, t := range active {
		if t.Expired(now) {
			continue
		}
		fmt.Fprintf(&b, "%s:%s|", t.ID, t.Text)
	}
	if remKey != "" {
		b.WriteString(remKey)
	}
	return b.String()
}
