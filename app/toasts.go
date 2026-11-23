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
	a.toasts = toast.NewWithClock(a.now)
	a.syncToastSetting()
}

func (a *App) syncToastSetting() {
	if a.toasts == nil {
		return
	}
	a.toasts.SetEnabled(a.Settings.Bool(SetToast, true))
}

// pushToast adds a transient non-blocking notification when ui.toast is enabled.
func (a *App) pushToast(text string, level toast.Level, ttl ...time.Duration) {
	if a.toasts == nil {
		return
	}
	a.syncToastSetting()
	if !a.toasts.Enabled() || strings.TrimSpace(text) == "" {
		return
	}
	a.toasts.Tick(a.now())
	a.toasts.Push(text, level, ttl...)
	a.gen++
}

// tickToasts prunes expired notifications and reports whether any expired.
func (a *App) tickToasts() bool {
	if a.toasts == nil {
		return false
	}
	return a.toasts.Tick(a.now())
}

// toastOverlay returns the floating toast overlay node when ui.toast is enabled
// and at least one non-expired toast is active.
func (a *App) toastOverlay() (ui.ViewNode, bool) {
	if a.toasts == nil || !a.Settings.Bool(SetToast, true) {
		return ui.ViewNode{}, false
	}
	a.toasts.Tick(a.now())
	if a.toasts.Len() == 0 {
		return ui.ViewNode{}, false
	}
	ov := a.toasts.ViewOverlay("app.toast")
	if ov.Props == nil {
		ov.Props = map[string]any{}
	}
	ov.Props["anchorId"] = "status"
	ov.Props["widthPct"] = 48
	return ov, true
}

func (a *App) toastViewKey() string {
	if a.toasts == nil || !a.Settings.Bool(SetToast, true) {
		return ""
	}
	now := a.now()
	active := a.toasts.Active()
	if len(active) == 0 {
		return ""
	}
	var b strings.Builder
	for _, t := range active {
		if t.Expired(now) {
			continue
		}
		fmt.Fprintf(&b, "%s:%s|", t.ID, t.Text)
	}
	return b.String()
}
