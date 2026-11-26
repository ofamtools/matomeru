package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// Space and commands of the Focus & Pomodoro Control Center.
const (
	SpaceFocus = "focus"

	CmdFocusCenterOpen    = "focus.center.open"
	CmdFocusCaptureBucket = "focus.capture"

	cmdFocusRowsMove       = "focus.rows.move"
	cmdFocusPriorityToggle = "focus.priority.toggle"
	cmdFocusPomodoroStart  = "focus.pomodoro.start"
	cmdFocusPomodoroStop   = "focus.pomodoro.stop"
	cmdFocusBudgetEdit     = "focus.budget.edit"
	cmdFocusWindowEdit     = "focus.window.edit"
	cmdFocusWindowToggle   = "focus.window.toggle"
	cmdFocusProjectOpen    = "focus.project.open"

	focusFilterField    = "search"
	focusFormID         = "focus.form"
	focusCaptureFormID  = "focus.capture.form"
	focusAllowFocused   = "selected project"
	focusAllowPriority  = "today's priority projects"
	focusDefaultWindow  = "PR Reviews"
	focusDefaultWinTime = "10:00"
	focusDefaultWinMin  = 45
)

// focusController owns the focus state (focus.json), the page's filterable
// table and the usage clock.
type focusController struct {
	list *table.Model
	// cfg is the focus state; every change goes through focusUpdate.
	cfg     data.FocusConfig
	visible []data.FocusProjectRow
	total   int

	// The usage clock: work is credited to trackProject from lastBeat on,
	// while the owner was active (lastInput) and the app was awake.
	lastBeat     time.Time
	lastInput    time.Time
	lastPage     string
	trackProject string
	carry        time.Duration
	// nextBeat is the wall-clock time (Unix nanoseconds) the watcher wakes
	// the UI goroutine for the next beat; 0 is no beat wanted.
	nextBeat atomic.Int64
	// runReport runs a scheduled window's report command.
	runReport func(ctx context.Context, command string) error
}

func (a *App) initFocus() {
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = focusFilterField
	a.focus = &focusController{list: list, runReport: runFocusReport}
	cfg, err := data.LoadFocusConfig(a.opts.ConfigDir)
	if err != nil {
		a.warn("%s: %v", data.FocusFileName, err)
	}
	a.focus.cfg = cfg
	a.registerFilterable(views.FocusProjectsID, list)
}

func (a *App) focusSetting(name string) bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(name, true)
}

// focusUpdate replaces the focus state and writes it to focus.json.
func (a *App) focusUpdate(cfg data.FocusConfig) {
	a.focus.cfg = cfg
	if err := data.SaveFocusConfig(a.opts.ConfigDir, cfg); err != nil {
		a.fail("save "+data.FocusFileName, err)
	}
	a.viewMemo.gen++
}

// --- projects and rows -------------------------------------------------------------

// focusProjects are the top-level tasks: the projects, in outline order.
func (a *App) focusProjects() []data.FocusProject {
	if a.Store == nil {
		return nil
	}
	nodes, err := a.recordTreeNodes(data.KindTask)
	if err != nil {
		a.fail("load projects", err)
		return nil
	}
	schema := a.schema(data.KindTask)
	out := make([]data.FocusProject, 0, len(nodes))
	for _, nd := range nodes {
		out = append(out, data.FocusProject{ID: nd.Record.ID, Title: views.Title(schema, nd.Record)})
	}
	return out
}

// syncFocus brings the table in line with the projects and the focus state,
// keeping the selected project selected.
func (a *App) syncFocus() (titles map[string]string) {
	fc := a.focus
	projects := a.focusProjects()
	titles = make(map[string]string, len(projects))
	for _, p := range projects {
		titles[p.ID] = p.Title
	}
	all := data.BuildFocusProjectRows(fc.cfg, projects, a.now())
	fc.total = len(all)
	selected, _ := fc.list.FocusedRow()
	rows := make([]table.Row, 0, len(all))
	byID := make(map[string]data.FocusProjectRow, len(all))
	for _, r := range all {
		byID[r.ProjectID] = r
		words := []string{r.Title, r.Access.Reason}
		if r.Priority {
			words = append(words, "priority")
		}
		if r.Access.Locked {
			words = append(words, "locked")
		}
		rows = append(rows, table.Row{ID: r.ProjectID, Cells: map[string]any{focusFilterField: strings.Join(words, " ")}})
	}
	fc.list.Rows = rows
	shown := fc.list.Filtered()
	if i := slices.IndexFunc(shown, func(r table.Row) bool { return r.ID == selected.ID }); i >= 0 {
		fc.list.SelectedIdx = i
	} else {
		fc.list.SelectedIdx = min(fc.list.SelectedIdx, max(0, len(shown)-1))
	}
	fc.visible = fc.visible[:0]
	for _, r := range shown {
		fc.visible = append(fc.visible, byID[r.ID])
	}
	return titles
}

// selectedFocusProject is the project the table's selection is on.
func (a *App) selectedFocusProject() (data.FocusProjectRow, bool) {
	a.syncFocus()
	row, ok := a.focus.list.FocusedRow()
	if !ok || row.ID == "" {
		a.say("no project selected")
		return data.FocusProjectRow{}, false
	}
	i := slices.IndexFunc(a.focus.visible, func(r data.FocusProjectRow) bool { return r.ProjectID == row.ID })
	if i < 0 {
		a.say("no project selected")
		return data.FocusProjectRow{}, false
	}
	return a.focus.visible[i], true
}

// --- view --------------------------------------------------------------------------

func (a *App) focusSpace() ui.ViewNode {
	titles := a.syncFocus()
	fc, now := a.focus, a.now()
	cfg := fc.cfg
	spec := views.FocusCenterSpec{
		Rows: fc.visible, SelectedIdx: fc.list.SelectedIdx, Total: fc.total,
		FilterQuery: fc.list.FilterQuery(), Budgets: cfg.TimeBudgets, Windows: cfg.ScheduledWindows,
		ProjectTitles: titles, CaptureBucketID: cfg.CaptureBucketID,
		PriorityCount:   len(data.TodaysPriorityProjects(cfg, now)),
		HideNonPriority: a.focusSetting(SetFocusHideNonPriority), EnforceLocks: a.focusSetting(SetFocusEnforceLocks),
		Now: now,
	}
	if cfg.ActivePomodoro.Running(now) {
		spec.Pomodoro = cfg.ActivePomodoro
	}
	if w, ok := data.ActiveScheduledWindow(cfg, now); ok {
		spec.Window = w
	} else if i := slices.IndexFunc(cfg.ScheduledWindows, func(r data.FocusWindowRule) bool { return r.Due(now) }); i >= 0 {
		spec.Pending = &cfg.ScheduledWindows[i]
	}
	if len(fc.visible) == 0 {
		spec.SelectedIdx = -1
	}
	return views.FocusCenterView(spec)
}

// --- specs, bindings, handlers -----------------------------------------------------

func focusCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdFocusCenterOpen, "Focus & Pomodoro Control Center (productivity & focus)"),
		spec(CmdFocusCaptureBucket, "Capture a pop-up item into the Daily Capture Bucket, from anywhere"),
		{ID: cmdFocusRowsMove, Description: "Move the Focus & Pomodoro Control Center selection", Params: []core.ParamSpec{
			{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}},
		}, Meta: map[string]any{"internal": true}},
		spec(cmdFocusPriorityToggle, "Toggle the selected project in today's priority projects"),
		spec(cmdFocusPomodoroStart, "Start a Pomodoro locked to the selected project or today's priority projects"),
		spec(cmdFocusPomodoroStop, "Stop the running Pomodoro"),
		spec(cmdFocusBudgetEdit, "Configure the selected project's time-budget guardrail (rolling minutes, daily maximum, cooldown)"),
		spec(cmdFocusWindowEdit, "Add or edit a scheduled focus window on the selected project (for example PR Reviews at 10:00)"),
		spec(cmdFocusWindowToggle, "Turn the selected project's scheduled focus windows on or off"),
		spec(cmdFocusProjectOpen, "Open the selected project's portal from the Focus & Pomodoro Control Center"),
	}
}

func focusBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeFocusCenter
	sel := "facts.hasSelection"
	return []core.Binding{
		bind(n, "", "g Z", CmdFocusCenterOpen, "productivity & focus"),
		when(bind(n, scope, "space", cmdFocusPriorityToggle, "priority today"), sel),
		when(hidden(bind(n, scope, "p", cmdFocusPriorityToggle, "priority today")), sel),
		when(bind(n, scope, "s", cmdFocusPomodoroStart, "start Pomodoro"), sel),
		when(bind(n, scope, "x", cmdFocusPomodoroStop, "stop Pomodoro"), "facts.pomodoroActive"),
		when(bind(n, scope, "b", cmdFocusBudgetEdit, "time budget"), sel),
		when(bind(n, scope, "w", cmdFocusWindowEdit, "focus window"), sel),
		when(bind(n, scope, "W", cmdFocusWindowToggle, "window on/off"), "facts.hasWindow"),
		bind(n, scope, "c", CmdFocusCaptureBucket, "capture"),
		when(hidden(bind(n, scope, "enter", cmdFocusProjectOpen, "open project")), sel),
		with(bind(n, scope, "j", cmdFocusRowsMove, "next project"), map[string]any{"direction": "down"}),
		with(bind(n, scope, "k", cmdFocusRowsMove, "previous project"), map[string]any{"direction": "up"}),
		hidden(with(bind(n, scope, "down", cmdFocusRowsMove, "next project"), map[string]any{"direction": "down"})),
		hidden(with(bind(n, scope, "up", cmdFocusRowsMove, "previous project"), map[string]any{"direction": "up"})),
		bind(ModeFormEdit, focusCaptureFormID, "enter", form.CmdSubmit, "capture"),
		bind(ModeForm, focusCaptureFormID, "enter", form.CmdSubmit, "capture"),
		bind(ModeFormEdit, focusCaptureFormID, "esc", CmdModalCancel, "cancel"),
	}
}

func (a *App) registerFocus() {
	a.handle(CmdFocusCenterOpen, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceFocus))
	})
	a.handle(cmdFocusRowsMove, func(s core.State, c core.Command) core.StepResult {
		a.syncFocus()
		c.ID = "motion." + c.Arg("direction")
		a.focus.list.HandleCommand(c)
		return core.Next(s)
	})
	a.handle(cmdFocusPriorityToggle, a.focusPriorityToggle)
	a.handle(cmdFocusPomodoroStart, a.focusPomodoroStart)
	a.handle(cmdFocusPomodoroStop, a.focusPomodoroStop)
	a.handle(cmdFocusBudgetEdit, a.focusBudgetEdit)
	a.handle(cmdFocusWindowEdit, a.focusWindowEdit)
	a.handle(cmdFocusWindowToggle, a.focusWindowToggle)
	a.handle(cmdFocusProjectOpen, a.focusProjectOpen)
	a.handle(CmdFocusCaptureBucket, a.focusCapture)
}

func (a *App) focusPriorityToggle(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedFocusProject()
	if !ok {
		return core.Next(s)
	}
	a.focusUpdate(data.TogglePriorityProject(a.focus.cfg, row.ProjectID, a.now()))
	n := len(data.TodaysPriorityProjects(a.focus.cfg, a.now()))
	switch {
	case row.Priority && n == 0:
		a.say("%s is no longer a priority today · no priority projects: the outline shows every project", row.Title)
	case row.Priority:
		a.say("%s is no longer a priority today · %d left", row.Title, n)
	default:
		a.say("%s %s is a priority today · %d in all", views.FocusPriorityMark, row.Title, n)
	}
	return core.Next(s)
}

func (a *App) focusProjectOpen(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedFocusProject()
	if !ok {
		return core.Next(s)
	}
	return core.Next(a.openSpace(s, projectTabID(row.ProjectID)))
}

// focusForm opens one of the page's small settings forms.
func (a *App) focusForm(s core.State, title string, specs []core.ParamSpec, submit func(vals map[string]any) error) core.StepResult {
	fm := form.New(title, specs, nil)
	fm.SubmitRow = true
	fm.SubmitLabel = "Save"
	fm.Wrap = a.wrapInputs()
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID: focusFormID, Scope: focusFormID, Title: title,
		WidthPct: 64, HeightPct: 60, DisableDrafts: true,
		OnSubmit: func(vals map[string]any) modal.Result {
			if err := submit(vals); err != nil {
				return modal.Result{Handled: true, Status: err.Error(), SetStatus: true}
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("i edits · l/h cycle a choice · ctrl+s saves")
	return core.Next(a.pushModal(s, mod))
}

// focusPomodoroStart asks how long and on which projects, then starts the
// Pomodoro: every other project is locked until it ends.
func (a *App) focusPomodoroStart(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedFocusProject()
	if !ok {
		return core.Next(s)
	}
	titles := a.syncFocus()
	allow := []string{focusAllowFocused}
	priority := data.TodaysPriorityProjects(a.focus.cfg, a.now())
	if len(priority) > 0 {
		allow = append(allow, focusAllowPriority)
	}
	return a.focusForm(s, "Start Pomodoro · "+row.Title, []core.ParamSpec{
		{Name: "minutes", Type: core.ParamInt, Default: data.PomodoroMinutes(a.focus.cfg), Description: "length in minutes"},
		{Name: "allow", Type: core.ParamString, Enum: allow, Default: focusAllowFocused, Description: "the projects that stay open; every other project is locked until the Pomodoro ends"},
		{Name: "also", Type: core.ParamString, Description: "more projects to keep open: comma-separated titles (optional)"},
		{Name: "title", Type: core.ParamString, Description: "what this Pomodoro is for (optional)"},
	}, func(vals map[string]any) error {
		allowed := []string{row.ProjectID}
		if data.Text(vals["allow"]) == focusAllowPriority {
			allowed = slices.Clone(priority)
		}
		for _, name := range strings.Split(data.Text(vals["also"]), ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			id := focusProjectByTitle(titles, name)
			if id == "" {
				return fmt.Errorf("no project titled %q", name)
			}
			if !slices.Contains(allowed, id) {
				allowed = append(allowed, id)
			}
		}
		minutes := intArg(vals["minutes"])
		if minutes <= 0 {
			return fmt.Errorf("a Pomodoro needs a length in minutes")
		}
		now := a.now()
		cfg := data.StartPomodoro(a.focus.cfg, data.Text(vals["title"]), allowed, minutes, data.PomodoroReasonManual, now)
		a.focusUpdate(cfg)
		a.focusSchedule()
		a.say("Pomodoro started · %s · ends %s · every other project is locked · x stops", views.FocusCountdown(cfg.ActivePomodoro.Remaining(now)), views.FocusClock(cfg.ActivePomodoro.EndsAt))
		return nil
	})
}

// focusProjectByTitle finds a project's ID by its title or ID, ignoring case.
func focusProjectByTitle(titles map[string]string, name string) string {
	for id, title := range titles {
		if strings.EqualFold(title, name) || id == name {
			return id
		}
	}
	return ""
}

func (a *App) focusPomodoroStop(s core.State, _ core.Command) core.StepResult {
	cfg, stopped := data.CancelPomodoro(a.focus.cfg)
	if stopped == nil {
		a.say("no Pomodoro is running · s starts one")
		return core.Next(s)
	}
	a.focusUpdate(cfg)
	a.say("Pomodoro stopped · projects unlocked")
	return core.Next(s)
}

func (a *App) focusBudgetEdit(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedFocusProject()
	if !ok {
		return core.Next(s)
	}
	cur, _ := data.TimeBudgetFor(a.focus.cfg, row.ProjectID)
	return a.focusForm(s, "Time budget · "+row.Title, []core.ParamSpec{
		{Name: "rollingMinutes", Type: core.ParamInt, Default: cur.WindowMinutes, Description: "most minutes of work in the rolling span; going over locks the project for the cooldown (0: no rolling limit)"},
		{Name: "rollingSpanMinutes", Type: core.ParamInt, Default: cur.RollingSpanMinutes, Description: "the rolling span in minutes (0: twice the rolling minutes)"},
		{Name: "dailyMaxMinutes", Type: core.ParamInt, Default: cur.DailyMaxMinutes, Description: "most minutes a day; going over locks the project until tomorrow (0: no daily limit)"},
		{Name: "cooldownPomodoros", Type: core.ParamInt, Default: max(cur.CooldownPomodoros, 1), Description: "how many Pomodoros the project stays locked after the rolling limit"},
	}, func(vals map[string]any) error {
		budget := data.ProjectTimeBudget{
			ProjectID:          row.ProjectID,
			WindowMinutes:      max(intArg(vals["rollingMinutes"]), 0),
			RollingSpanMinutes: max(intArg(vals["rollingSpanMinutes"]), 0),
			DailyMaxMinutes:    max(intArg(vals["dailyMaxMinutes"]), 0),
			CooldownPomodoros:  max(intArg(vals["cooldownPomodoros"]), 0),
		}
		cfg := data.SetTimeBudget(a.focus.cfg, budget)
		cfg.CooldownLocks = data.EvaluateTimeGuardrails(cfg, a.now())
		a.focusUpdate(cfg)
		if budget.Empty() {
			a.say("time budget removed from %s", row.Title)
		} else {
			a.say("time budget saved on %s", row.Title)
		}
		return nil
	})
}

func (a *App) focusWindowEdit(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedFocusProject()
	if !ok {
		return core.Next(s)
	}
	cur, found := data.WindowFor(a.focus.cfg, row.ProjectID, "")
	if !found {
		cur = data.FocusWindowRule{Name: focusDefaultWindow, DailyTimeHHMM: focusDefaultWinTime, DurationMin: focusDefaultWinMin, LockOtherProjects: true, Enabled: true}
	}
	return a.focusForm(s, "Scheduled focus window · "+row.Title, []core.ParamSpec{
		{Name: "name", Type: core.ParamString, Default: cur.Name, Description: "the window's name; another name adds another window on this project"},
		{Name: "time", Type: core.ParamString, Default: cur.DailyTimeHHMM, Description: "when it opens every day, HH:MM (24-hour)"},
		{Name: "minutes", Type: core.ParamInt, Default: cur.DurationMin, Description: "how long it stays open"},
		{Name: "lockOtherProjects", Type: core.ParamBool, Default: cur.LockOtherProjects, Description: "lock every other project while the window is open"},
		{Name: "enabled", Type: core.ParamBool, Default: cur.Enabled, Description: "off keeps the window but never opens it"},
		{Name: "reportCommand", Type: core.ParamString, Default: cur.ReportCommand, Description: "shell command that prepares a preliminary report; the window opens when it is done (optional)"},
	}, func(vals map[string]any) error {
		rule := data.FocusWindowRule{
			Name: data.Text(vals["name"]), TargetProjectID: row.ProjectID,
			DailyTimeHHMM: data.Text(vals["time"]), DurationMin: intArg(vals["minutes"]),
			LockOtherProjects: boolArg(vals["lockOtherProjects"]), Enabled: boolArg(vals["enabled"]),
			ReportCommand: strings.TrimSpace(data.Text(vals["reportCommand"])),
		}
		cfg, err := data.UpsertWindow(a.focus.cfg, rule)
		if err != nil {
			return err
		}
		a.focusUpdate(cfg)
		a.focusSchedule()
		a.say("%s window saved on %s · every day at %s for %d min", strings.TrimSpace(rule.Name), row.Title, strings.TrimSpace(rule.DailyTimeHHMM), rule.DurationMin)
		return nil
	})
}

func (a *App) focusWindowToggle(s core.State, _ core.Command) core.StepResult {
	row, ok := a.selectedFocusProject()
	if !ok {
		return core.Next(s)
	}
	cfg, enabled, found := data.ToggleProjectWindows(a.focus.cfg, row.ProjectID)
	if !found {
		a.say("%s has no scheduled focus window · w adds one", row.Title)
		return core.Next(s)
	}
	a.focusUpdate(cfg)
	a.focusSchedule()
	state := "off"
	if enabled {
		state = "on"
	}
	a.say("scheduled focus windows on %s are %s", row.Title, state)
	return core.Next(s)
}

// --- capture bucket ----------------------------------------------------------------

// focusCapture asks for one line and files it under the capture bucket,
// whatever page the owner is on.
func (a *App) focusCapture(s core.State, _ core.Command) core.StepResult {
	if a.Store == nil {
		a.say("no store to capture into")
		return core.Next(s)
	}
	title := "Capture into " + data.CaptureBucketTitle
	fm := form.New(title, []core.ParamSpec{
		{Name: "item", Type: core.ParamString, Required: true, Description: "what popped up (enter captures)"},
	}, nil)
	fm.Wrap = a.wrapInputs()
	// Typing starts at once.
	fm.HandleCommand(core.Command{ID: form.CmdEdit})
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID: focusCaptureFormID, Scope: focusCaptureFormID, Title: title, Tone: "accent",
		WidthPct: 60, DisableDrafts: true,
		OnSubmit: func(vals map[string]any) modal.Result {
			item := strings.TrimSpace(data.Text(vals["item"]))
			if item == "" {
				return modal.Result{Handled: true, Mode: ModeFormEdit, Status: "type the item to capture", SetStatus: true}
			}
			if err := a.captureIntoBucket(item); err != nil {
				a.fail("capture", err)
				return modal.Result{Handled: true, Mode: ModeFormEdit}
			}
			msg := "captured into " + data.CaptureBucketTitle + ": " + item
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit,
				Status: msg, SetStatus: true, Toast: &modal.ToastSpec{Text: msg, Level: toast.Ok}}
		},
	})
	a.say("type the item · enter captures · esc cancels")
	return core.Next(a.pushModal(s, mod))
}

// captureBucket is the capture bucket's task, created as a top-level
// "Daily Capture Bucket" when none is set or the one set is gone.
func (a *App) captureBucket() (core.Ref, error) {
	if id := a.focus.cfg.CaptureBucketID; id != "" {
		ref := core.Ref{Kind: data.KindTask, ID: id}
		if rec, err := a.Store.Get(ref); err == nil && rec != nil {
			return ref, nil
		}
	}
	rec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": data.CaptureBucketTitle}}, User)
	if err != nil {
		return core.Ref{}, err
	}
	cfg := a.focus.cfg
	cfg.CaptureBucketID = rec.ID
	a.focusUpdate(cfg)
	return rec.Ref(), nil
}

func (a *App) captureIntoBucket(item string) error {
	bucket, err := a.captureBucket()
	if err != nil {
		return err
	}
	_, _, err = a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": item, "parent": bucket}}, User)
	return err
}
