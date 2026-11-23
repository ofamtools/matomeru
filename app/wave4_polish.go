package app

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/hints"
	"ofam.dev/ochazuke/widgets/toast"
)

// --- 1. Toast notifications (ochazuke/widgets/toast) ----------------------------

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

// --- 2. Tree guide lines (ui.treeGuides) ----------------------------------------

// applyTreeGuides sets the "guides" prop on every ui.KindTree node in the view
// tree from the ui.treeGuides setting (one general mechanism for Outline and
// any tree view).
func applyTreeGuides(n *ui.ViewNode, guides bool) {
	if n == nil {
		return
	}
	if n.Kind == ui.KindTree {
		if n.Props == nil {
			n.Props = map[string]any{}
		}
		n.Props["guides"] = guides
	}
	for i := range n.Children {
		applyTreeGuides(&n.Children[i], guides)
	}
}

// --- 3. Focus jump hints (ochazuke/widgets/hints over layout.Solved.FocusOrder) -

// focusHints activates focus-jump hint mode over layout.Solved.FocusOrder using
// the same shared ochazuke/widgets/hints engine and ModeHints as terminal hints.
func (a *App) focusHints(s core.State, _ core.Command) core.StepResult {
	solved := s.Layout
	if len(solved.FocusOrder) == 0 && a.Shell != nil {
		solved = a.Shell.State.Layout
	}
	if len(solved.FocusOrder) == 0 {
		w, h := 120, 36
		if a.Shell != nil && a.Shell.W > 0 && a.Shell.H > 0 {
			w, h = a.Shell.W, a.Shell.H
		}
		_, solved = ui.Render(a.View(s), a.theme(), s.ActiveFrame().FocusID, w, h)
	}
	targets := hints.TargetsFromFocusOrder(solved)
	if len(targets) == 0 {
		a.say("no focusable regions on screen")
		return core.Next(s)
	}
	sess := hints.NewSession(targets)
	curFrame := s.ActiveFrame()
	a.hints = &hintsState{
		session:     sess,
		focusJump:   true,
		returnMode:  curFrame.Mode,
		returnFocus: curFrame.FocusID,
		returnPage:  s.ActivePage,
	}
	a.say("focus hints (%d) · type label to jump · esc/q cancels", len(targets))
	return core.Next(withMode(s, ModeHints))
}
