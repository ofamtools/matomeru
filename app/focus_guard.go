package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/toast"
)

// The focus guard keeps the focus state true as time passes (the usage
// clock, Pomodoro ends, scheduled windows, time-budget cooldowns) and is
// where the rest of the app asks about it: project locks, the outline's
// priority filter and the chrome countdown chip. Every lock question is
// answered by data.EvaluateProjectAccess.
const (
	// focusBeatEvery is the least time between two beats on the same page.
	focusBeatEvery = 5 * time.Second
	// focusHeartbeat is how often the watcher wakes an idle app for a beat.
	focusHeartbeat = 20 * time.Second
	// focusMaxGap is the longest stretch between beats credited as work: a
	// longer one means the app was asleep.
	focusMaxGap = 2 * time.Minute
	// focusIdleAfter is how long after the owner's last command work on a
	// project is still credited.
	focusIdleAfter = 5 * time.Minute
	// focusReportTimeout bounds a scheduled window's report command.
	focusReportTimeout = 5 * time.Minute
)

// runFocusReport runs a scheduled window's report command in a shell.
func runFocusReport(ctx context.Context, command string) error {
	ctx, cancel := context.WithTimeout(ctx, focusReportTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "sh", "-c", command).Run()
}

// --- the beat ----------------------------------------------------------------------

// focusTick runs after every command and at every wake-up. It notes the owner's activity and,
// when the page changed or enough time passed, runs a beat. It reports
// whether the focus state as shown changed.
func (a *App) focusTick(s core.State, c core.Command) bool {
	fc := a.focus
	if fc == nil {
		return false
	}
	now := a.now()
	if c.ID != CmdWake {
		fc.lastInput = now
	}
	if !fc.lastBeat.IsZero() && s.ActivePage == fc.lastPage && now.Sub(fc.lastBeat) < focusBeatEvery {
		return false
	}
	return a.focusBeat(s, now)
}

// focusBeat credits the time since the last beat to the project that was
// active, then ends a finished Pomodoro, triggers scheduled windows that came
// due and re-evaluates the time-budget guardrails.
func (a *App) focusBeat(s core.State, now time.Time) bool {
	fc := a.focus
	cfg, changed := fc.cfg, false

	if d := now.Sub(fc.lastBeat); !fc.lastBeat.IsZero() && fc.trackProject != "" && d > 0 && d <= focusMaxGap && now.Sub(fc.lastInput) <= focusIdleAfter {
		fc.carry += d
		if mins := int(fc.carry / time.Minute); mins > 0 {
			spent := time.Duration(mins) * time.Minute
			cfg = data.RecordUsage(cfg, fc.trackProject, now.Add(-fc.carry), mins)
			fc.carry -= spent
			changed = true
		}
	}
	project := a.focusActiveProject(s)
	if project != fc.trackProject {
		fc.trackProject, fc.carry = project, 0
	}
	fc.lastBeat, fc.lastPage = now, s.ActivePage

	if next, done := data.CompletePomodoro(cfg, now); done != nil {
		cfg, changed = next, true
		a.focusAnnounce(toast.Ok, "Pomodoro complete · %d min · projects unlocked", done.DurationMin)
	}
	next, triggered := data.TriggerDueWindows(cfg, now)
	for _, w := range triggered {
		cfg, changed = next, true
		a.focusWindowTriggered(w, now)
	}
	locks := data.EvaluateTimeGuardrails(cfg, now)
	for _, lock := range locks {
		if !now.Before(lock.UnlockAt) || focusHasCooldown(cfg.CooldownLocks, lock) {
			continue
		}
		a.focusAnnounce(toast.Warn, "%s %s is locked until %s · %s", views.FocusLockMark,
			a.title(core.Ref{Kind: data.KindTask, ID: lock.ProjectID}), views.FocusClock(lock.UnlockAt), lock.Reason)
	}
	if len(locks) != len(cfg.CooldownLocks) || len(locks) > 0 && !focusSameCooldowns(locks, cfg.CooldownLocks) {
		cfg.CooldownLocks, changed = locks, true
	}
	if changed {
		a.focusUpdate(cfg)
	}
	a.focusSchedule()
	return changed || cfg.ActivePomodoro.Running(now)
}

func focusHasCooldown(locks []data.ProjectCooldown, lock data.ProjectCooldown) bool {
	for _, c := range locks {
		if c.ProjectID == lock.ProjectID && c.UnlockAt.Equal(lock.UnlockAt) {
			return true
		}
	}
	return false
}

func focusSameCooldowns(a, b []data.ProjectCooldown) bool {
	for _, c := range a {
		if !focusHasCooldown(b, c) {
			return false
		}
	}
	return true
}

// focusAnnounce says something the owner did not ask for: on the status
// line and as a toast.
func (a *App) focusAnnounce(level toast.Level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.say("%s", msg)
	a.pushToast(msg, level)
}

// focusActiveProject is the project the owner is working in: the open
// project tab.
func (a *App) focusActiveProject(s core.State) string {
	if isProjectTab(s.ActivePage) {
		return projectIDFromTab(s.ActivePage)
	}
	return ""
}

// focusSchedule asks the watcher for a heartbeat while time matters: a
// project is being worked in, a Pomodoro runs or a scheduled window is on.
func (a *App) focusSchedule() {
	fc := a.focus
	need := fc.trackProject != "" || fc.cfg.ActivePomodoro != nil
	for _, w := range fc.cfg.ScheduledWindows {
		need = need || w.Enabled
	}
	if !need {
		fc.nextBeat.Store(0)
		return
	}
	fc.nextBeat.Store(time.Now().Add(focusHeartbeat).UnixNano())
}

// focusBeatDue reports, once per heartbeat, that the UI goroutine should be
// woken for a beat. It is safe to call from the watcher.
func (a *App) focusBeatDue() bool {
	fc := a.focus
	if fc == nil {
		return false
	}
	due := fc.nextBeat.Load()
	return due != 0 && time.Now().UnixNano() >= due && fc.nextBeat.CompareAndSwap(due, 0)
}

// focusWindowTriggered announces a scheduled window that came due and, when
// it wants a preliminary report, runs the report command off the UI
// goroutine; the window opens (and locks) once the report is done.
func (a *App) focusWindowTriggered(w data.FocusWindowRule, now time.Time) {
	project := a.title(core.Ref{Kind: data.KindTask, ID: w.TargetProjectID})
	command := strings.TrimSpace(w.ReportCommand)
	if command == "" {
		a.focusAnnounce(toast.Info, "%s", focusWindowOpenText(w, project, now))
		return
	}
	a.focusAnnounce(toast.Info, "%s window is due · preparing its report", w.Name)
	run := a.focus.runReport
	a.background(func(ctx context.Context) func(core.State) core.State {
		err := run(ctx, command)
		return func(s core.State) core.State {
			a.focusUpdate(data.MarkWindowReportReady(a.focus.cfg, w.ID))
			text := focusWindowOpenText(w, project, a.now())
			if err != nil {
				// A failed report must not cancel the window it was for.
				a.focusAnnounce(toast.Warn, "%s · its report command failed: %v", text, err)
			} else {
				a.focusAnnounce(toast.Ok, "%s · report ready", text)
			}
			return s
		}
	})
}

func focusWindowOpenText(w data.FocusWindowRule, project string, now time.Time) string {
	text := fmt.Sprintf("%s window open on %s", w.Name, project)
	if _, end, ok := w.Span(now); ok {
		text += " until " + views.FocusClock(end)
	}
	if w.LockOtherProjects {
		text += " · other projects locked"
	}
	return text
}

// --- project locks -----------------------------------------------------------------

// focusAccess is the lock decision on a project: data.EvaluateProjectAccess,
// or always allowed when focus.enforceLocks is off.
func (a *App) focusAccess(projectID string) data.FocusAccessDecision {
	if a.focus == nil || projectID == "" || !a.focusSetting(SetFocusEnforceLocks) {
		return data.FocusAccessDecision{Allowed: true}
	}
	return data.EvaluateProjectAccess(a.focus.cfg, projectID, a.now())
}

// focusRefuseProject reports whether the project is locked and, when it is,
// tells the owner why and until when.
func (a *App) focusRefuseProject(projectID string) bool {
	d := a.focusAccess(projectID)
	if !d.Locked {
		return false
	}
	a.focusAnnounce(toast.Warn, "%s %s is locked: %s · unlocks at %s · g Z shows the locks", views.FocusLockMark,
		a.title(core.Ref{Kind: data.KindTask, ID: projectID}), d.Reason, views.FocusClock(d.UnlockAt))
	return true
}

// --- outline priority filter -------------------------------------------------------

// focusHidesOutlineRoots reports whether the task outline shows only today's
// priority projects (and the capture bucket).
func (a *App) focusHidesOutlineRoots(kind string) bool {
	return a.focus != nil && kind == data.KindTask && a.focusSetting(SetFocusHideNonPriority) &&
		len(data.TodaysPriorityProjects(a.focus.cfg, a.now())) > 0
}

// focusOutlineRoots keeps the top-level projects the outline shows in focus
// mode.
func (a *App) focusOutlineRoots(nodes []data.TreeNode, kind string) []data.TreeNode {
	if !a.focusHidesOutlineRoots(kind) {
		return nodes
	}
	now := a.now()
	out := make([]data.TreeNode, 0, len(nodes))
	for _, nd := range nodes {
		if data.IsPriorityProject(a.focus.cfg, nd.Record.ID, now) {
			out = append(out, nd)
		}
	}
	return out
}

// focusOutlineKey is the part of an outline's cache key the priority filter
// decides.
func (a *App) focusOutlineKey(kind string) string {
	if !a.focusHidesOutlineRoots(kind) {
		return ""
	}
	cfg := a.focus.cfg
	return "|focus:" + cfg.PriorityDate + ":" + strings.Join(cfg.PriorityProjects, ",") + ":" + cfg.CaptureBucketID
}

// focusOutlineBadges mark an outline that hides the non-priority projects.
func (a *App) focusOutlineBadges(kind string) []ui.Badge {
	if !a.focusHidesOutlineRoots(kind) {
		return nil
	}
	n := len(data.TodaysPriorityProjects(a.focus.cfg, a.now()))
	return []ui.Badge{{Text: fmt.Sprintf("%s focus: %d priority today · g Z", views.FocusPriorityMark, n), Tone: "accent"}}
}

// --- chrome ------------------------------------------------------------------------

// focusChromeChip is the short countdown the chrome shows while a Pomodoro
// runs, "" otherwise or when focus.chromeCountdown is off.
func (a *App) focusChromeChip() string {
	if a.focus == nil || !a.focusSetting(SetFocusChromeCountdown) {
		return ""
	}
	now := a.now()
	p := a.focus.cfg.ActivePomodoro
	if !p.Running(now) {
		return ""
	}
	return "🍅 " + views.FocusCountdown(p.Remaining(now))
}
