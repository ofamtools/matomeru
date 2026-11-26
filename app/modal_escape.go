package app

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
)

const effectEscapeGuardTick = "escape_guard_tick"

func (a *App) escapeGuardConfig() traits.EscapeGuardConfig {
	policy := traits.EscapeGuardDouble
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		policy = a.cfg.Settings.String(SetModalEscapeGuard, traits.EscapeGuardDouble)
	}
	return traits.EscapeGuardConfig{
		Policy:  traits.NormalizeEscapeGuardPolicy(policy),
		Cooloff: time.Second,
	}
}

func (a *App) resetEscapeGuard() {
	a.escGuard = traits.EscapeGuardState{}
}

func (a *App) syncEscapeGuardModal(modalID string) {
	if modalID != a.escGuardModal {
		a.escGuard = traits.EscapeGuardState{}
		a.escGuardModal = modalID
	}
}

func (a *App) escapeGuardStatus() (active bool, tone, badge string) {
	top := a.Modal()
	if top == "" {
		return false, "", ""
	}
	if a.escGuardModal != "" && a.escGuardModal != top {
		return false, "", ""
	}
	return traits.EscapeGuardStatus(a.escGuard, a.escapeGuardConfig(), a.now())
}

func (a *App) escapeGuardViewKey() string {
	active, tone, badge := a.escapeGuardStatus()
	if !active {
		return ""
	}
	return fmt.Sprintf("%t|%s|%s", active, tone, badge)
}

func (a *App) decorateEscapeGuardOverlay(ov ui.ViewNode) ui.ViewNode {
	active, tone, badge := a.escapeGuardStatus()
	return modal.ApplyEscapeGuardOverlay(ov, active, tone, badge)
}

func (a *App) tickEscapeGuard() bool {
	if a.escGuard.CooloffUntil.IsZero() {
		return false
	}
	if !a.now().Before(a.escGuard.CooloffUntil) {
		a.escGuard.CooloffUntil = time.Time{}
		if strings.Contains(a.status, "esc locked") {
			a.status = ""
		}
		return true
	}
	return false
}

func isModalInsertMode(mode string) bool {
	switch mode {
	case ModeFormEdit, ModePickerFilter, ModeOutlineEdit, ModeFeaturesNote, ModeThreadsCompose, ModeBindingsFilter, ModeBindingsCapture:
		return true
	default:
		return false
	}
}

func isModalNormalMode(mode string) bool {
	switch mode {
	case ModeForm, ModePicker, ModeOutline, ModeBindings:
		return true
	default:
		return false
	}
}

func isEscapeModalCancel(c core.Command) bool {
	return c.ID == CmdModalCancel && c.Arg("via") == "esc"
}

func (a *App) modalCancel(s core.State, c core.Command) core.StepResult {
	top := a.Modal()
	if top != "" && isEscapeModalCancel(c) {
		a.syncEscapeGuardModal(top)
		cfg := a.escapeGuardConfig()
		next, dec := traits.EscapeGuardOnKey(a.escGuard, cfg, true, a.now())
		a.escGuard = next
		switch dec {
		case traits.EscapeSwallowCooloff:
			a.say("esc locked · cool-off")
			a.viewMemo.gen++
			return core.Next(s)
		case traits.EscapeArm:
			a.say("press esc again to close")
			a.viewMemo.gen++
			return core.Next(s)
		case traits.EscapeAllowClose:
			a.resetEscapeGuard()
		}
	}
	return a.handleModal(s, c)
}

func (a *App) afterCommandEscapeGuard(prevModal, prevMode string, c core.Command, out *core.StepResult) {
	curModal := a.Modal()
	if curModal == "" || curModal != prevModal {
		a.resetEscapeGuard()
		a.escGuardModal = curModal
		return
	}

	curMode := a.Engine.Mode(out.State)
	if isModalInsertMode(prevMode) && isModalNormalMode(curMode) {
		cfg := a.escapeGuardConfig()
		now := a.now()
		a.escGuard = traits.EscapeGuardOnExitInsert(a.escGuard, cfg, now)
		a.escGuardModal = curModal
		a.viewMemo.gen++
		if !a.escGuard.CooloffUntil.IsZero() && now.Before(a.escGuard.CooloffUntil) {
			wait := a.escGuard.CooloffUntil.Sub(now)
			if wait > 0 {
				out.Effects = append(out.Effects, core.CustomEffect{Kind: effectEscapeGuardTick, Data: wait})
			}
		}
		return
	}

	if !isEscapeModalCancel(c) && a.escGuard.Armed {
		cfg := a.escapeGuardConfig()
		a.escGuard, _ = traits.EscapeGuardOnKey(a.escGuard, cfg, false, a.now())
		if a.status == "press esc again to close" {
			a.say("")
		}
		a.viewMemo.gen++
	}
}
