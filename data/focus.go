package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Productivity & Focus: today's priority projects, the capture bucket,
// Pomodoro project locks, time-budget guardrails and scheduled focus windows.
// Everything here is plain data and pure functions over it; the clock is
// always an argument.
const (
	// FocusFileName is the focus state file in the config directory.
	FocusFileName = "focus.json"
	// DefaultPomodoroMinutes is a Pomodoro's length when none is configured.
	DefaultPomodoroMinutes = 25
	// CaptureBucketTitle names the task the capture bucket is created as.
	CaptureBucketTitle = "Daily Capture Bucket"

	PomodoroRunning   = "running"
	PomodoroPaused    = "paused"
	PomodoroCompleted = "completed"
	PomodoroCancelled = "cancelled"

	PomodoroReasonManual = "manual"
	PomodoroReasonWindow = "scheduled_window"

	// Sources of a project lock, as FocusAccessDecision.Source names them.
	FocusSourcePomodoro = "pomodoro"
	FocusSourceCooldown = "cooldown"
	FocusSourceWindow   = "scheduled_window"

	CooldownRolling = "rolling"
	CooldownDaily   = "daily"

	focusDateLayout = "2006-01-02"
	// focusUsageKeep is how long a usage slice stays in the log.
	focusUsageKeep = 14 * 24 * time.Hour
)

// FocusConfig is the whole focus state, as focus.json holds it.
type FocusConfig struct {
	PriorityDate       string              `json:"priorityDate,omitempty"` // YYYY-MM-DD
	PriorityProjects   []string            `json:"priorityProjects,omitempty"`
	PinnedProjectID    string              `json:"pinnedProjectId,omitempty"`
	ScopeMode          string              `json:"scopeMode,omitempty"` // global | today | project
	CaptureBucketID    string              `json:"captureBucketId,omitempty"`
	DefaultPomodoroMin int                 `json:"defaultPomodoroMin,omitempty"` // default 25
	ActivePomodoro     *PomodoroSession    `json:"activePomodoro,omitempty"`
	TimeBudgets        []ProjectTimeBudget `json:"timeBudgets,omitempty"`
	CooldownLocks      []ProjectCooldown   `json:"cooldownLocks,omitempty"`
	ScheduledWindows   []FocusWindowRule   `json:"scheduledWindows,omitempty"`
	UsageLog           []ProjectUsageSlice `json:"usageLog,omitempty"`
}

// PomodoroSession is one timed stretch of work locked to its allowed projects.
type PomodoroSession struct {
	ID                string    `json:"id"`
	Title             string    `json:"title,omitempty"`
	AllowedProjectIDs []string  `json:"allowedProjectIds,omitempty"`
	DurationMin       int       `json:"durationMin"`
	StartedAt         time.Time `json:"startedAt"`
	EndsAt            time.Time `json:"endsAt"`
	Status            string    `json:"status"`
	Reason            string    `json:"reason,omitempty"`
}

// ProjectTimeBudget is a guardrail on one project: at most WindowMinutes of
// work in any rolling RollingSpanMinutes, and at most DailyMaxMinutes a day.
// A zero limit is off. Going over the rolling limit locks the project for
// CooldownPomodoros Pomodoros; going over the daily limit locks it for the
// rest of the day.
type ProjectTimeBudget struct {
	ProjectID     string `json:"projectId"`
	WindowMinutes int    `json:"windowMinutes,omitempty"`
	// RollingSpanMinutes is the rolling span WindowMinutes is counted over;
	// zero means twice WindowMinutes.
	RollingSpanMinutes int `json:"rollingSpanMinutes,omitempty"`
	DailyMaxMinutes    int `json:"dailyMaxMinutes,omitempty"`
	CooldownPomodoros  int `json:"cooldownPomodoros,omitempty"`
}

// RollingSpan is the span the rolling limit is counted over.
func (b ProjectTimeBudget) RollingSpan() time.Duration {
	span := b.RollingSpanMinutes
	if span <= 0 {
		span = 2 * b.WindowMinutes
	}
	return time.Duration(span) * time.Minute
}

// Empty reports a budget that limits nothing.
func (b ProjectTimeBudget) Empty() bool { return b.WindowMinutes <= 0 && b.DailyMaxMinutes <= 0 }

// ProjectUsageSlice is DurationMin minutes of active work on a project.
type ProjectUsageSlice struct {
	ProjectID   string    `json:"projectId"`
	StartedAt   time.Time `json:"startedAt"`
	DurationMin int       `json:"durationMin"`
}

func (u ProjectUsageSlice) end() time.Time {
	return u.StartedAt.Add(time.Duration(u.DurationMin) * time.Minute)
}

// ProjectCooldown is a guardrail lock on one project, until UnlockAt.
type ProjectCooldown struct {
	ProjectID string    `json:"projectId"`
	Kind      string    `json:"kind,omitempty"` // rolling | daily
	Reason    string    `json:"reason,omitempty"`
	LockedAt  time.Time `json:"lockedAt"`
	UnlockAt  time.Time `json:"unlockAt"`
}

// FocusWindowRule is a daily timed window on one project ("PR Reviews" at
// 10:00 for 45 minutes). With LockOtherProjects every other project is locked
// while it is open. A rule with a ReportCommand opens only once its
// preliminary report is ready for the day.
type FocusWindowRule struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	TargetProjectID   string `json:"targetProjectId"`
	DailyTimeHHMM     string `json:"dailyTimeHHMM"`
	DurationMin       int    `json:"durationMin"`
	LockOtherProjects bool   `json:"lockOtherProjects,omitempty"`
	Enabled           bool   `json:"enabled"`
	ReportCommand     string `json:"reportCommand,omitempty"`
	// ReportReady holds only for LastTriggeredDate.
	ReportReady       bool   `json:"reportReady,omitempty"`
	LastTriggeredDate string `json:"lastTriggeredDate,omitempty"`
}

// FocusAccessDecision is the one answer to "may this project be opened now".
type FocusAccessDecision struct {
	Allowed  bool
	Locked   bool
	Reason   string
	UnlockAt time.Time
	// Source is "pomodoro", "cooldown", "scheduled_window" or "".
	Source string
}

// --- persistence -------------------------------------------------------------------

// LoadFocusConfig reads focus.json from dir. A missing file, or no dir, is an
// empty config.
func LoadFocusConfig(dir string) (FocusConfig, error) {
	var cfg FocusConfig
	if dir == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, FocusFileName))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return FocusConfig{}, err
	}
	return cfg, nil
}

// SaveFocusConfig writes cfg to focus.json in dir, replacing the file whole.
// With no dir it does nothing.
func SaveFocusConfig(dir string, cfg FocusConfig) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, FocusFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// --- daily priority projects -------------------------------------------------------

// FocusDate is the day of now as focus.json writes it.
func FocusDate(now time.Time) string { return now.Format(focusDateLayout) }

// TodaysPriorityProjects are the priority projects set for the day of now;
// yesterday's set is not today's.
func TodaysPriorityProjects(cfg FocusConfig, now time.Time) []string {
	if cfg.PriorityDate != FocusDate(now) {
		return nil
	}
	return cfg.PriorityProjects
}

// IsPriorityProject reports whether projectID is in today's priority set, or
// is the capture bucket. With no set for today nothing is a priority.
func IsPriorityProject(cfg FocusConfig, projectID string, now time.Time) bool {
	today := TodaysPriorityProjects(cfg, now)
	if len(today) == 0 || projectID == "" {
		return false
	}
	return projectID == cfg.CaptureBucketID || slices.Contains(today, projectID)
}

// TogglePriorityProject adds projectID to today's priority set, or takes it
// out. A set left from another day starts over.
func TogglePriorityProject(cfg FocusConfig, projectID string, now time.Time) FocusConfig {
	today := slices.Clone(TodaysPriorityProjects(cfg, now))
	if i := slices.Index(today, projectID); i >= 0 {
		today = slices.Delete(today, i, i+1)
	} else if projectID != "" {
		today = append(today, projectID)
	}
	cfg.PriorityDate, cfg.PriorityProjects = FocusDate(now), today
	return cfg
}

// --- Pomodoro ----------------------------------------------------------------------

// PomodoroMinutes is the configured Pomodoro length.
func PomodoroMinutes(cfg FocusConfig) int {
	if cfg.DefaultPomodoroMin > 0 {
		return cfg.DefaultPomodoroMin
	}
	return DefaultPomodoroMinutes
}

// StartPomodoro starts a Pomodoro locked to allowed, replacing any other.
// A durationMin of zero or less is the configured length.
func StartPomodoro(cfg FocusConfig, title string, allowed []string, durationMin int, reason string, now time.Time) FocusConfig {
	if durationMin <= 0 {
		durationMin = PomodoroMinutes(cfg)
	}
	if reason == "" {
		reason = PomodoroReasonManual
	}
	cfg.ActivePomodoro = &PomodoroSession{
		ID:                "pomodoro-" + strconv.FormatInt(now.UnixNano(), 36),
		Title:             strings.TrimSpace(title),
		AllowedProjectIDs: slices.Clone(allowed),
		DurationMin:       durationMin,
		StartedAt:         now,
		EndsAt:            now.Add(time.Duration(durationMin) * time.Minute),
		Status:            PomodoroRunning,
		Reason:            reason,
	}
	return cfg
}

// Running reports a Pomodoro that is under way at now.
func (p *PomodoroSession) Running(now time.Time) bool {
	return p != nil && p.Status == PomodoroRunning && now.Before(p.EndsAt)
}

// Remaining is the time left at now, never negative.
func (p *PomodoroSession) Remaining(now time.Time) time.Duration {
	if p == nil || p.Status != PomodoroRunning {
		return 0
	}
	return max(p.EndsAt.Sub(now), 0)
}

// Allows reports whether the Pomodoro lets projectID be worked on.
func (p *PomodoroSession) Allows(projectID string) bool {
	return p == nil || slices.Contains(p.AllowedProjectIDs, projectID)
}

// CompletePomodoro ends the active Pomodoro once its time is up. It returns
// the finished session, or nil when none finished.
func CompletePomodoro(cfg FocusConfig, now time.Time) (FocusConfig, *PomodoroSession) {
	p := cfg.ActivePomodoro
	if p == nil || p.Status != PomodoroRunning || now.Before(p.EndsAt) {
		return cfg, nil
	}
	return endPomodoro(cfg, PomodoroCompleted)
}

// CancelPomodoro stops the active Pomodoro early. It returns the stopped
// session, or nil when none was active.
func CancelPomodoro(cfg FocusConfig) (FocusConfig, *PomodoroSession) {
	if cfg.ActivePomodoro == nil {
		return cfg, nil
	}
	return endPomodoro(cfg, PomodoroCancelled)
}

func endPomodoro(cfg FocusConfig, status string) (FocusConfig, *PomodoroSession) {
	done := *cfg.ActivePomodoro
	done.Status = status
	cfg.ActivePomodoro = nil
	return cfg, &done
}

// --- usage and time-budget guardrails ----------------------------------------------

// RecordUsage adds minutes of work on projectID starting at start, joining
// the slice it continues, and drops slices too old to matter.
func RecordUsage(cfg FocusConfig, projectID string, start time.Time, minutes int) FocusConfig {
	if projectID == "" || minutes <= 0 {
		return cfg
	}
	log := slices.Clone(cfg.UsageLog)
	oldest := start.Add(-focusUsageKeep)
	log = slices.DeleteFunc(log, func(u ProjectUsageSlice) bool { return u.end().Before(oldest) })
	if n := len(log); n > 0 && log[n-1].ProjectID == projectID {
		// A slice that picks up where the last one stopped, on the same day.
		gap := start.Sub(log[n-1].end())
		if gap >= 0 && gap <= time.Minute && FocusDate(log[n-1].StartedAt) == FocusDate(start) {
			log[n-1].DurationMin += minutes
			cfg.UsageLog = log
			return cfg
		}
	}
	cfg.UsageLog = append(log, ProjectUsageSlice{ProjectID: projectID, StartedAt: start, DurationMin: minutes})
	return cfg
}

// UsageMinutes is the work logged on projectID between from and to.
func UsageMinutes(cfg FocusConfig, projectID string, from, to time.Time) int {
	var total time.Duration
	for _, u := range cfg.UsageLog {
		if u.ProjectID != projectID {
			continue
		}
		start, end := u.StartedAt, u.end()
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	return int(total.Round(time.Minute) / time.Minute)
}

func startOfDay(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

// TodayMinutes is the work logged on projectID since midnight.
func TodayMinutes(cfg FocusConfig, projectID string, now time.Time) int {
	return UsageMinutes(cfg, projectID, startOfDay(now), now)
}

// TimeBudgetFor is the guardrail configured on projectID.
func TimeBudgetFor(cfg FocusConfig, projectID string) (ProjectTimeBudget, bool) {
	i := slices.IndexFunc(cfg.TimeBudgets, func(b ProjectTimeBudget) bool { return b.ProjectID == projectID })
	if i < 0 {
		return ProjectTimeBudget{}, false
	}
	return cfg.TimeBudgets[i], true
}

// SetTimeBudget sets the guardrail of budget.ProjectID; an empty budget
// removes it, and its cooldowns with it.
func SetTimeBudget(cfg FocusConfig, budget ProjectTimeBudget) FocusConfig {
	budgets := slices.DeleteFunc(slices.Clone(cfg.TimeBudgets), func(b ProjectTimeBudget) bool { return b.ProjectID == budget.ProjectID })
	if budget.Empty() {
		cfg.CooldownLocks = slices.DeleteFunc(slices.Clone(cfg.CooldownLocks), func(c ProjectCooldown) bool { return c.ProjectID == budget.ProjectID })
	} else {
		budgets = append(budgets, budget)
	}
	cfg.TimeBudgets = budgets
	return cfg
}

// EvaluateTimeGuardrails computes the cooldown locks that hold at now: the
// ones still running, a new one for every budgeted project over its rolling
// or daily limit, and the rolling ones that ended inside their span (work
// done before a cooldown ended is not counted twice).
func EvaluateTimeGuardrails(cfg FocusConfig, now time.Time) []ProjectCooldown {
	var out []ProjectCooldown
	for _, b := range cfg.TimeBudgets {
		if b.Empty() {
			continue
		}
		var last *ProjectCooldown
		for i := range cfg.CooldownLocks {
			c := &cfg.CooldownLocks[i]
			if c.ProjectID == b.ProjectID && (last == nil || c.UnlockAt.After(last.UnlockAt)) {
				last = c
			}
		}
		if last != nil && now.Before(last.UnlockAt) {
			out = append(out, *last)
			continue
		}
		if b.DailyMaxMinutes > 0 {
			if used := TodayMinutes(cfg, b.ProjectID, now); used >= b.DailyMaxMinutes {
				out = append(out, ProjectCooldown{
					ProjectID: b.ProjectID, Kind: CooldownDaily, LockedAt: now, UnlockAt: startOfDay(now).AddDate(0, 0, 1),
					Reason: fmt.Sprintf("daily budget used: %d of %d min today", used, b.DailyMaxMinutes),
				})
				continue
			}
		}
		if b.WindowMinutes > 0 {
			span := b.RollingSpan()
			from := now.Add(-span)
			if last != nil && last.Kind == CooldownRolling && last.UnlockAt.After(from) {
				from = last.UnlockAt
			}
			if used := UsageMinutes(cfg, b.ProjectID, from, now); used >= b.WindowMinutes {
				cooldown := time.Duration(max(b.CooldownPomodoros, 1)*PomodoroMinutes(cfg)) * time.Minute
				out = append(out, ProjectCooldown{
					ProjectID: b.ProjectID, Kind: CooldownRolling, LockedAt: now, UnlockAt: now.Add(cooldown),
					Reason: fmt.Sprintf("time budget used: %d of %d min in the last %d min", used, b.WindowMinutes, int(span/time.Minute)),
				})
				continue
			}
			if last != nil && last.Kind == CooldownRolling && last.UnlockAt.After(now.Add(-span)) {
				out = append(out, *last)
			}
		}
	}
	return out
}

// --- scheduled focus windows -------------------------------------------------------

// ParseHHMM reads a 24-hour "HH:MM" time of day.
func ParseHHMM(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("time %q is not HH:MM (24-hour)", s)
	}
	return t.Hour(), t.Minute(), nil
}

// Span is when the rule's window is open on the day of now.
func (r FocusWindowRule) Span(now time.Time) (start, end time.Time, ok bool) {
	h, m, err := ParseHHMM(r.DailyTimeHHMM)
	if err != nil || r.DurationMin <= 0 {
		return time.Time{}, time.Time{}, false
	}
	start = startOfDay(now).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	return start, start.Add(time.Duration(r.DurationMin) * time.Minute), true
}

// Due reports an enabled rule whose time of day has come and not passed.
func (r FocusWindowRule) Due(now time.Time) bool {
	start, end, ok := r.Span(now)
	return ok && r.Enabled && !now.Before(start) && now.Before(end)
}

// Ready reports whether the rule's preliminary report, if it wants one, is
// ready for the day of now.
func (r FocusWindowRule) Ready(now time.Time) bool {
	return strings.TrimSpace(r.ReportCommand) == "" || (r.ReportReady && r.LastTriggeredDate == FocusDate(now))
}

// ActiveScheduledWindow is the scheduled window open at now: enabled, inside
// its time of day, and with its report ready.
func ActiveScheduledWindow(cfg FocusConfig, now time.Time) (*FocusWindowRule, bool) {
	for i := range cfg.ScheduledWindows {
		if r := cfg.ScheduledWindows[i]; r.Due(now) && r.Ready(now) {
			return &r, true
		}
	}
	return nil, false
}

// TriggerDueWindows marks every window that came due and was not yet
// triggered today, and returns those windows as they were triggered. A
// window with a report command waits for MarkWindowReportReady.
func TriggerDueWindows(cfg FocusConfig, now time.Time) (FocusConfig, []FocusWindowRule) {
	today := FocusDate(now)
	var triggered []FocusWindowRule
	for i, r := range cfg.ScheduledWindows {
		if !r.Due(now) || r.LastTriggeredDate == today {
			continue
		}
		if triggered == nil {
			cfg.ScheduledWindows = slices.Clone(cfg.ScheduledWindows)
		}
		r.LastTriggeredDate, r.ReportReady = today, false
		cfg.ScheduledWindows[i] = r
		triggered = append(triggered, r)
	}
	return cfg, triggered
}

// MarkWindowReportReady records that the report of window id is ready.
func MarkWindowReportReady(cfg FocusConfig, id string) FocusConfig {
	cfg.ScheduledWindows = slices.Clone(cfg.ScheduledWindows)
	for i := range cfg.ScheduledWindows {
		if cfg.ScheduledWindows[i].ID == id {
			cfg.ScheduledWindows[i].ReportReady = true
		}
	}
	return cfg
}

// WindowFor is the scheduled window named name on projectID.
func WindowFor(cfg FocusConfig, projectID, name string) (FocusWindowRule, bool) {
	i := slices.IndexFunc(cfg.ScheduledWindows, func(r FocusWindowRule) bool {
		return r.TargetProjectID == projectID && (name == "" || strings.EqualFold(r.Name, name))
	})
	if i < 0 {
		return FocusWindowRule{}, false
	}
	return cfg.ScheduledWindows[i], true
}

// UpsertWindow saves rule in place of the window of the same project and
// name, or adds it. It refuses a rule with no usable time or length.
func UpsertWindow(cfg FocusConfig, rule FocusWindowRule) (FocusConfig, error) {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return cfg, errors.New("a focus window needs a name")
	}
	if _, _, err := ParseHHMM(rule.DailyTimeHHMM); err != nil {
		return cfg, err
	}
	if rule.DurationMin <= 0 {
		return cfg, errors.New("a focus window needs a length in minutes")
	}
	rule.DailyTimeHHMM = strings.TrimSpace(rule.DailyTimeHHMM)
	windows := slices.Clone(cfg.ScheduledWindows)
	if old, ok := WindowFor(cfg, rule.TargetProjectID, rule.Name); ok {
		i := slices.IndexFunc(windows, func(r FocusWindowRule) bool { return r.ID == old.ID })
		rule.ID = old.ID
		if old.DailyTimeHHMM == rule.DailyTimeHHMM && old.ReportCommand == rule.ReportCommand {
			rule.LastTriggeredDate, rule.ReportReady = old.LastTriggeredDate, old.ReportReady
		}
		windows[i] = rule
	} else {
		if rule.ID == "" {
			rule.ID = fmt.Sprintf("window-%d", len(windows)+1)
			for slices.ContainsFunc(windows, func(r FocusWindowRule) bool { return r.ID == rule.ID }) {
				rule.ID += "x"
			}
		}
		windows = append(windows, rule)
	}
	cfg.ScheduledWindows = windows
	return cfg, nil
}

// ToggleProjectWindows turns the windows of projectID on, or off when any is
// on. It reports the new state and whether the project has a window at all.
func ToggleProjectWindows(cfg FocusConfig, projectID string) (out FocusConfig, enabled, found bool) {
	anyOn := false
	for _, r := range cfg.ScheduledWindows {
		if r.TargetProjectID == projectID {
			found = true
			anyOn = anyOn || r.Enabled
		}
	}
	if !found {
		return cfg, false, false
	}
	cfg.ScheduledWindows = slices.Clone(cfg.ScheduledWindows)
	for i := range cfg.ScheduledWindows {
		if cfg.ScheduledWindows[i].TargetProjectID == projectID {
			cfg.ScheduledWindows[i].Enabled = !anyOn
		}
	}
	return cfg, !anyOn, true
}

// --- the one lock evaluator --------------------------------------------------------

// EvaluateProjectAccess is the single place a project lock is decided. A
// project is locked while a Pomodoro runs that does not allow it, while a
// scheduled window that locks other projects is open on another project, or
// while a guardrail cooldown holds on it. With more than one lock the
// decision is the one that holds longest. The capture bucket is never locked
// by a Pomodoro or a window: a jot is always possible.
func EvaluateProjectAccess(cfg FocusConfig, projectID string, now time.Time) FocusAccessDecision {
	var locks []FocusAccessDecision
	bucket := projectID != "" && projectID == cfg.CaptureBucketID
	if p := cfg.ActivePomodoro; p.Running(now) && !p.Allows(projectID) && !bucket {
		name := p.Title
		if name == "" {
			name = "Pomodoro"
		}
		locks = append(locks, FocusAccessDecision{Source: FocusSourcePomodoro, UnlockAt: p.EndsAt,
			Reason: fmt.Sprintf("%s is running on other projects", name)})
	}
	if w, ok := ActiveScheduledWindow(cfg, now); ok && w.LockOtherProjects && w.TargetProjectID != projectID && !bucket {
		_, end, _ := w.Span(now)
		locks = append(locks, FocusAccessDecision{Source: FocusSourceWindow, UnlockAt: end,
			Reason: fmt.Sprintf("the %s window is open", w.Name)})
	}
	for _, c := range cfg.CooldownLocks {
		if c.ProjectID == projectID && now.Before(c.UnlockAt) {
			locks = append(locks, FocusAccessDecision{Source: FocusSourceCooldown, UnlockAt: c.UnlockAt, Reason: c.Reason})
		}
	}
	if len(locks) == 0 {
		return FocusAccessDecision{Allowed: true}
	}
	d := slices.MaxFunc(locks, func(a, b FocusAccessDecision) int { return a.UnlockAt.Compare(b.UnlockAt) })
	d.Locked = true
	return d
}

// --- rows of the Focus & Pomodoro Control Center -----------------------------------

// FocusProject is a project as the focus rows need it.
type FocusProject struct {
	ID    string
	Title string
}

// FocusProjectRow is one project of the Focus & Pomodoro Control Center.
type FocusProjectRow struct {
	ProjectID     string
	Title         string
	Priority      bool
	CaptureBucket bool
	Access        FocusAccessDecision
	TodayMinutes  int
	Budget        *ProjectTimeBudget
	// Windows are the scheduled windows on the project.
	Windows []FocusWindowRule
}

// BuildFocusProjectRows joins projects with the focus state at now: today's
// priorities first, then the rest, each in the order given.
func BuildFocusProjectRows(cfg FocusConfig, projects []FocusProject, now time.Time) []FocusProjectRow {
	rows := make([]FocusProjectRow, 0, len(projects))
	for _, p := range projects {
		row := FocusProjectRow{
			ProjectID: p.ID, Title: p.Title,
			Priority:      slices.Contains(TodaysPriorityProjects(cfg, now), p.ID),
			CaptureBucket: p.ID == cfg.CaptureBucketID,
			Access:        EvaluateProjectAccess(cfg, p.ID, now),
			TodayMinutes:  TodayMinutes(cfg, p.ID, now),
		}
		if b, ok := TimeBudgetFor(cfg, p.ID); ok {
			row.Budget = &b
		}
		for _, w := range cfg.ScheduledWindows {
			if w.TargetProjectID == p.ID {
				row.Windows = append(row.Windows, w)
			}
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b FocusProjectRow) int {
		switch {
		case a.Priority == b.Priority:
			return 0
		case a.Priority:
			return -1
		}
		return 1
	})
	return rows
}
