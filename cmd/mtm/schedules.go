package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/schedules"
)

// Schedules: commands mtm runs on an interval, so the signal producers
// (o-gh signals, o-slack signals) feed the inbox without anyone pressing
// a key (internal/schedules).
//
// Where they live: the rows are configuration. The built-ins merge by
// name with <config>/schedules.json (a JSON array, like harnesses.json),
// and two settings per row flip it: schedule.NAME.enabled (on, off, auto)
// and schedule.NAME.interval. The settings are what the owner turns, from
// the Settings page, mtm settings set or the schedule verbs (which write
// them); the file is only for adding a schedule or changing its command.
// What ran is state in the database (the schedules and schedule_runs
// tables): the last run, the next, the outcome and a short history.
//
// Who runs them: the TUI, while it is open. mtm has no daemon of its own
// (internal/daemon is the TUIOS client), and a CLI command is one-shot.
// Every run first claims its schedule in a write transaction, so two open
// TUIs run each occurrence once and never overlap, and mtm schedule
// run-now from the CLI waits its turn the same way. Runs go under nice,
// one at a time per schedule, each with a timeout; failures back off and
// show on the row and, the first time, as a notice; a command that is not
// installed only shows on its row.

const (
	scheduleNoun     = "schedule"
	schedulesPage    = "schedules"
	scheduleListID   = "schedules/list"
	scheduleDetailID = "schedules/detail"
	scheduleFile     = "schedules.json"
	// scheduleEvery is how often the TUI checks for due schedules;
	// scheduleFirst is the first check after it opens.
	scheduleEvery = 15 * time.Second
	scheduleFirst = 2 * time.Second
	historyShown  = 10
)

var scheduleColumns = []ochazuke.Column{
	{Title: "schedule", Min: 12},
	{Title: "on", Priority: 1},
	{Title: "every", Priority: 2},
	{Title: "last", Priority: 1},
	{Title: "next", Align: ochazuke.AlignRight, Priority: 3},
	{Title: "command", Priority: 4},
}

// intervalChoices are the set-interval chooser's rows.
var intervalChoices = []string{"1m", "5m", "10m", "15m", "30m", "1h", "4h", "24h"}

// scheduleState is the feature's state: the rows as configured, the
// runner, the page's list and detail, and the TUI's timers.
type scheduleState struct {
	defs    []schedules.Def
	loadErr string // why schedules.json did not load (the built-ins run)
	runner  *schedules.Runner
	list    *ochazuke.List
	detail  *linkedDetail
	// live is set when the TUI starts: only then do schedules run and
	// pane signals reach the inbox (a CLI command is one-shot).
	live   bool
	ctx    context.Context
	cancel context.CancelFunc
	// after schedules a timer (tea.Tick); tests set it nil and deliver
	// scheduleDue themselves.
	after   func(time.Duration, tea.Msg) tea.Cmd
	ticking bool
}

// scheduleDue is the runner's timer; scheduleTicked is a check that
// finished starting what was due; scheduleRan is a finished run.
type (
	scheduleDue    struct{}
	scheduleTicked struct{ started []string }
	scheduleRan    struct{ rec schedules.Record }
)

// openSchedules loads the rows and the runner (New calls it before the
// settings are declared, which need the rows).
func (a *App) openSchedules(configDir string) error {
	runner, err := schedules.New(a.db)
	if err != nil {
		return err
	}
	runner.Expand = a.expandSchedule
	defs, err := schedules.Load(filepath.Join(configDir, scheduleFile))
	st := &scheduleState{defs: defs, runner: runner}
	if err != nil {
		st.defs, st.loadErr = schedules.Defaults(), err.Error()
	}
	st.after = func(d time.Duration, msg tea.Msg) tea.Cmd {
		return tea.Tick(d, func(time.Time) tea.Msg { return msg })
	}
	st.ctx, st.cancel = context.WithCancel(context.Background())
	a.sched = st
	return nil
}

// expandSchedule fills {mtm} (this binary) and {mtm-args} (the words that
// point a child mtm at this database and config, empty for the default
// database) in a schedule's command and env.
func (a *App) expandSchedule(s string) string {
	if !strings.Contains(s, "{mtm") {
		return s
	}
	bin, err := os.Executable()
	if err != nil {
		bin = "mtm"
	}
	args := ""
	if a.dataPath != "" && !samePath(a.dataPath, DefaultData()) {
		args = "--data " + a.dataPath
		if a.configDir != "" {
			args += " --config " + a.configDir
		}
	}
	return strings.NewReplacer("{mtm-args}", args, "{mtm}", bin).Replace(s)
}

func scheduleEnabledSetting(name string) string  { return "schedule." + name + ".enabled" }
func scheduleIntervalSetting(name string) string { return "schedule." + name + ".interval" }

// scheduleSettings are the two settings of every row.
func (a *App) scheduleSettings() []ochazuke.Setting {
	var out []ochazuke.Setting
	for _, d := range a.sched.defs {
		out = append(out,
			ochazuke.Setting{Name: scheduleEnabledSetting(d.Name), Type: ochazuke.SettingString, Default: d.Enabled, Enum: schedules.EnabledValues,
				Description: "Run " + strings.Join(d.Command, " ") + " on its interval: on, off, or auto (when it is installed and its checks pass)"},
			ochazuke.Setting{Name: scheduleIntervalSetting(d.Name), Type: ochazuke.SettingDuration, Default: d.Every(),
				Description: "How often " + d.Name + " runs (at least 1m); failures back off from it",
				Validate: func(v any) error {
					if d, ok := v.(time.Duration); ok && d < time.Minute {
						return errors.New("an interval is at least 1m")
					}
					return nil
				}})
	}
	return out
}

// scheduleDefs are the rows with the settings applied, as they are now.
func (a *App) scheduleDefs() []schedules.Def {
	out := make([]schedules.Def, 0, len(a.sched.defs))
	s := a.r.Settings()
	for _, d := range a.sched.defs {
		if v := s.String(scheduleEnabledSetting(d.Name)); v != "" {
			d.Enabled = v
		}
		if v := s.Duration(scheduleIntervalSetting(d.Name)); v >= time.Minute {
			d.Interval = schedules.Duration(v)
		}
		out = append(out, d)
	}
	return out
}

func (a *App) scheduleDef(name string) (schedules.Def, bool) {
	for _, d := range a.scheduleDefs() {
		if d.Name == name {
			return d, true
		}
	}
	return schedules.Def{}, false
}

// scheduleNameParam is the schedule every one-schedule verb takes.
var scheduleNameParam = ochazuke.Parameter{Type: "string", Noun: scheduleNoun, Source: scheduleNoun + ".list", Description: "The schedule's name; omitted, the selected row"}

func (a *App) defineSchedules() {
	n := scheduleNoun
	a.r.Define(n).Columns(scheduleColumns...).
		Verb("list", a.listSchedules, "List the schedules: whether each runs, its interval, its last run and when it is due").Rows(n).Fetch().
		Verb("show", a.showSchedule, "Show one schedule with its recent runs").Positional("name").Fetch().
		Verb("history", a.scheduleHistory, "List a schedule's recent runs, newest first, with their output").Positional("name").Fetch().
		Verb("run-now", a.runScheduleNow, "Run the schedule now, on or off, and wait for it; refused while it already runs").Positional("name").
		Verb("enable", a.enableSchedule, "Turn the schedule on (schedule.NAME.enabled = on)").Positional("name").
		Verb("disable", a.disableSchedule, "Turn the schedule off (schedule.NAME.enabled = off)").Positional("name").
		Verb("toggle", a.toggleSchedule, "Turn a running schedule off, else on").Positional("name").
		Verb("set-interval", a.setScheduleInterval, "Set how often the schedule runs (--interval 5m); omitted, a chooser asks").Positional("name").
		Verb("reset", a.resetSchedule, "Put the schedule's enabled and interval back to their defaults").Positional("name").
		Mode("normal").
		Bind("r", ochazuke.Do(n+".run-now").Hinted("run now")).
		Bind("e", ochazuke.Do(n+".toggle").Hinted("on/off")).
		Bind("i", ochazuke.Do(n+".set-interval").Hinted("interval"))
	for _, verb := range []string{"show", "run-now", "enable", "disable", "toggle", "reset"} {
		a.parameters(n+"."+verb, map[string]ochazuke.Parameter{"name": scheduleNameParam})
	}
	a.parameters(n+".history", map[string]ochazuke.Parameter{"name": scheduleNameParam,
		"limit": {Type: "integer", Description: "At most this many runs (default all kept)"}})
	a.parameters(n+".set-interval", map[string]ochazuke.Parameter{"name": scheduleNameParam,
		"interval": {Type: "string", Description: "How often: 1m, 5m, 1h, …"}})
	a.ownedBy(n, scheduleListID)
}

func (a *App) addSchedulesPage() error {
	a.r.Instance(schedulesPage, ochazuke.Kind("page"))
	st := a.sched
	var err error
	if st.list, err = a.r.AddList(scheduleListID, nil, ochazuke.Parent(schedulesPage), ochazuke.Columns(scheduleColumns...)); err != nil {
		return err
	}
	if st.detail, err = a.addLinkedDetail(scheduleDetailID, schedulesPage, func() *ochazuke.List { return st.list }, a.scheduleSubject); err != nil {
		return err
	}
	if _, err = a.r.AddPage(schedulesPage, "Schedules", a.renderSchedules, ochazuke.Focus(scheduleListID)); err != nil {
		return err
	}
	// S on the Inbox's list (empty or not) opens the Schedules page: what
	// feeds the inbox.
	row := ochazuke.Do("ochazuke.page.open", ochazuke.Arguments{"id": schedulesPage}).Hinted("schedules")
	row.Instance, row.Mode, row.Keys = inboxListID, "normal", "S"
	if err := a.r.Bind(row); err != nil {
		return err
	}
	return a.refreshSchedules()
}

// scheduleView is a schedule as list, show and the rows answer it.
type scheduleView struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Command     []string `json:"command"`
	Interval    string   `json:"interval"`
	Timeout     string   `json:"timeout"`
	Enabled     string   `json:"enabled"`
	Active      bool     `json:"active"`
	Why         string   `json:"why,omitempty"`
	Running     bool     `json:"running,omitempty"`
	LastRun     int64    `json:"last_run,omitempty"`
	LastOutcome string   `json:"last_outcome,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
	Failures    int      `json:"failures,omitempty"`
	NextRun     int64    `json:"next_run,omitempty"`
	// Runs are the recent runs (show only).
	Runs []schedules.Record `json:"runs,omitempty"`
}

// viewSchedule joins a row with its state. In the TUI an auto row is shown
// by its last check (the runner checks off the loop; before one it reads
// "not checked yet"); the CLI checks it.
func (a *App) viewSchedule(d schedules.Def, s schedules.State, now int64) scheduleView {
	v := scheduleView{Name: d.Name, Description: d.Description, Command: d.Command, Interval: scheduleSpan(d.Every()), Timeout: scheduleSpan(d.Limit()),
		Enabled: d.Enabled, Running: s.Running(now), LastRun: s.Last, LastOutcome: s.LastOutcome, LastError: s.LastError, Failures: s.Failures}
	switch d.Enabled {
	case schedules.On:
		v.Active = true
	case schedules.Auto:
		if a.cli {
			// A CLI answer is off the TUI's loop: check now.
			a.sched.runner.Active(a.sched.ctx, d)
		}
		if ok, why, probed := a.sched.runner.Probed(d); probed {
			v.Active, v.Why = ok, why
		} else {
			v.Why = "auto: not checked yet"
		}
	}
	if v.Active {
		v.NextRun = max(s.DueAt(d.Every()), now)
	}
	return v
}

// onCell is the row's enabled cell: on, off, auto (on) or auto (off).
func (v scheduleView) onCell() string {
	if v.Enabled != schedules.Auto {
		return v.Enabled
	}
	if v.Active {
		return "auto (on)"
	}
	return "auto (off)"
}

// lastCell is the last run: its glyph, outcome and age, with the failure
// count while it fails.
func (v scheduleView) lastCell(now int64) string {
	if v.Running {
		return "● running"
	}
	if v.LastRun == 0 {
		return "never"
	}
	glyph := "✓"
	if v.LastOutcome != schedules.OutcomeOK {
		glyph = "✗"
	}
	cell := glyph + " " + v.LastOutcome + " " + ago(time.Duration(now-v.LastRun)*time.Millisecond)
	if v.Failures > 1 {
		cell += fmt.Sprintf(" ×%d", v.Failures)
	}
	return cell
}

func (v scheduleView) nextCell(now int64) string {
	switch {
	case !v.Active:
		return ""
	case v.NextRun <= now:
		return "due"
	}
	return "in " + ago(time.Duration(v.NextRun-now)*time.Millisecond)
}

func (a *App) scheduleNow() int64 { return a.sched.runner.Now().UnixMilli() }

// scheduleViews are every row with its state.
func (a *App) scheduleViews() ([]scheduleView, error) {
	states, err := a.sched.runner.States()
	if err != nil {
		return nil, err
	}
	now := a.scheduleNow()
	var out []scheduleView
	for _, d := range a.scheduleDefs() {
		out = append(out, a.viewSchedule(d, states[d.Name], now))
	}
	return out, nil
}

func scheduleRowID(name string) string { return "schedule:" + name }

func (a *App) scheduleRow(v scheduleView, now int64) ocli.Row {
	return ocli.Row{Noun: scheduleNoun, ID: scheduleRowID(v.Name), Label: v.Name, Data: v, Cells: []string{
		v.Name, v.onCell(), v.Interval, v.lastCell(now), v.nextCell(now), strings.Join(v.Command, " ")}}
}

// refreshSchedules rebuilds the page's rows.
func (a *App) refreshSchedules() error {
	if a.sched == nil || a.sched.list == nil {
		return nil
	}
	views, err := a.scheduleViews()
	if err != nil {
		return err
	}
	now := a.scheduleNow()
	rows := make([]ochazuke.ListItem, 0, len(views))
	for _, v := range views {
		row := a.scheduleRow(v, now)
		rows = append(rows, ochazuke.ListItem{ID: row.ID, Noun: scheduleNoun, Label: row.Label, Cells: row.Cells, Data: v})
	}
	return a.sched.list.SetItems(rows)
}

func (a *App) renderSchedules(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	return a.withLinkedDetail(a.sched.detail, width, height, func(w, h int) ochazuke.Block {
		s := ochazuke.NewStyles(*a.m.Appearance)
		head := "schedules · r run now · e on/off · i interval · settings schedule.NAME.enabled|interval"
		if !a.sched.live {
			head = "schedules · they run while the TUI is open"
		}
		lines := []string{s.Muted.Render(ansi.Truncate(head, w, "…"))}
		if a.sched.loadErr != "" {
			lines = append(lines, s.Muted.Render(ansi.Truncate(scheduleFile+": "+a.sched.loadErr+" (the built-ins run)", w, "…")))
		}
		lines = append(lines, a.view(a.sched.list, w, max(1, h-len(lines))))
		return ochazuke.Text(strings.Join(lines, "\n"))
	})
}

// scheduleSubject is the detail: the selected schedule and its runs.
func (a *App) scheduleSubject() (string, string, []link) {
	item, ok := a.sched.list.Selected()
	if !ok {
		return "", "", nil
	}
	v, ok := item.Data.(scheduleView)
	if !ok {
		return "", "", nil
	}
	return item.ID, a.scheduleFacts(v), nil
}

func (a *App) scheduleFacts(v scheduleView) string {
	now := a.scheduleNow()
	next := ""
	if v.NextRun > 0 {
		next = stamp(v.NextRun) + " (" + v.nextCell(now) + ")"
	}
	failures := ""
	if v.Failures > 0 {
		failures = fmt.Sprintf("%d in a row; backing off to %s", v.Failures, scheduleSpan(schedules.Delay(mustDuration(v.Interval), v.Failures)))
	}
	text := v.Name + "\n\n" + facts(
		"what", v.Description,
		"command", strings.Join(v.Command, " "),
		"enabled", v.onCell(),
		"why", v.Why,
		"every", v.Interval,
		"timeout", v.Timeout,
		"last", strings.TrimSpace(stamp(v.LastRun)+" "+v.LastOutcome),
		"error", v.LastError,
		"failing", failures,
		"next", next,
		"settings", scheduleEnabledSetting(v.Name)+", "+scheduleIntervalSetting(v.Name),
	)
	runs, err := a.sched.runner.History(v.Name, historyShown)
	if err != nil {
		return text + "\n\nhistory: " + err.Error()
	}
	if len(runs) == 0 {
		return text + "\n\nNo runs yet."
	}
	text += "\n\nruns"
	for _, r := range runs {
		line := fmt.Sprintf("%s  %-7s %5s", time.UnixMilli(r.Started).In(a.location()).Format("Jan 2 15:04"), r.Outcome, (time.Duration(r.Duration) * time.Millisecond).Round(time.Millisecond*100))
		if r.Manual {
			line += "  run now"
		}
		if r.Error != "" {
			line += "  " + r.Error
		}
		text += "\n" + line
	}
	if out := runs[0].Output; out != "" {
		text += "\n\nlast output\n" + out
	}
	return text
}

func mustDuration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }

// scheduleSpan is a duration without its zero tail: 5m, 1h, 1h30m.
func scheduleSpan(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// scheduleAt is the schedule a verb acts on: --name, else the targeted row.
func (a *App) scheduleAt(ctx ochazuke.Ctx) (schedules.Def, error) {
	name := strings.TrimPrefix(ochazuke.StringArg(ctx.Args, "name", ""), "schedule:")
	if name == "" && ctx.Instance != nil && ctx.Instance.Noun == scheduleNoun {
		name = strings.TrimPrefix(ctx.Instance.ID, "schedule:")
	}
	if name == "" {
		return schedules.Def{}, errors.New("specify a schedule name or select a schedule row")
	}
	d, ok := a.scheduleDef(name)
	if !ok {
		var names []string
		for _, d := range a.sched.defs {
			names = append(names, d.Name)
		}
		return schedules.Def{}, fmt.Errorf("unknown schedule %q; known: %s", name, strings.Join(names, ", "))
	}
	return d, nil
}

// scheduleAnswer is the schedule's view after a change, the rows rebuilt.
func (a *App) scheduleAnswer(d schedules.Def) ochazuke.Result {
	if err := a.refreshSchedules(); err != nil {
		return ochazuke.Failed(err)
	}
	if d, ok := a.scheduleDef(d.Name); ok {
		states, err := a.sched.runner.States()
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(a.viewSchedule(d, states[d.Name], a.scheduleNow()))
	}
	return ochazuke.Done(nil)
}

func (a *App) listSchedules(ochazuke.Ctx) ochazuke.Result {
	views, err := a.scheduleViews()
	if err != nil {
		return ochazuke.Failed(err)
	}
	now := a.scheduleNow()
	rows := []ocli.Row{}
	for _, v := range views {
		rows = append(rows, a.scheduleRow(v, now))
	}
	return ochazuke.Done(rows)
}

func (a *App) showSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	states, err := a.sched.runner.States()
	if err != nil {
		return ochazuke.Failed(err)
	}
	v := a.viewSchedule(d, states[d.Name], a.scheduleNow())
	if v.Runs, err = a.sched.runner.History(d.Name, historyShown); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(v)
}

func (a *App) scheduleHistory(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	runs, err := a.sched.runner.History(d.Name, ochazuke.IntArg(ctx.Args, "limit", 0))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if runs == nil {
		runs = []schedules.Record{}
	}
	return ochazuke.Done(runs)
}

// runScheduleNow runs the schedule off the loop and answers its record; a
// failed run is the record's outcome, not a failed verb (a notice says
// it), so it never opens anything.
func (a *App) runScheduleNow(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	runner, runCtx := a.sched.runner, a.sched.ctx
	return ochazuke.Pending(func(context.Context) (any, error) {
		return runner.RunNow(runCtx, d)
	}).Then(func(c ochazuke.Ctx, v any, err error) {
		if err := a.refreshSchedules(); err != nil {
			a.r.UI().Error(err.Error())
		}
		if rec, ok := v.(schedules.Record); ok && err == nil {
			c.Notify(runNotice(rec))
			if err := a.refreshInbox(); err != nil {
				a.r.UI().Error(err.Error())
			}
		}
	})
}

// runNotice is the line a run's notice says.
func runNotice(rec schedules.Record) string {
	text := rec.ScheduleID + " · " + rec.Outcome
	if rec.Error != "" {
		text += ": " + rec.Error
	}
	return text
}

func (a *App) setScheduleEnabled(ctx ochazuke.Ctx, value string) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.r.Settings().Set(scheduleEnabledSetting(d.Name), value); err != nil {
		return ochazuke.Failed(err)
	}
	return a.scheduleAnswer(d)
}

func (a *App) enableSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setScheduleEnabled(ctx, schedules.On)
}
func (a *App) disableSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setScheduleEnabled(ctx, schedules.Off)
}

// toggleSchedule turns a schedule that runs (on, or auto whose checks
// pass) off, and any other on.
func (a *App) toggleSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if a.viewSchedule(d, schedules.State{}, a.scheduleNow()).Active {
		return a.setScheduleEnabled(ctx, schedules.Off)
	}
	return a.setScheduleEnabled(ctx, schedules.On)
}

func (a *App) setScheduleInterval(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if every := ochazuke.StringArg(ctx.Args, "interval", ""); every != "" {
		v, err := time.ParseDuration(every)
		if err != nil {
			return ochazuke.Failed(fmt.Errorf("interval %q: %w", every, err))
		}
		if err := a.r.Settings().Set(scheduleIntervalSetting(d.Name), v.String()); err != nil {
			return ochazuke.Failed(err)
		}
		return a.scheduleAnswer(d)
	}
	if a.cli {
		return ochazuke.Failed(errors.New("set-interval needs --interval (1m, 5m, 1h, …)"))
	}
	rows := make([]ochazuke.ChooserRow, 0, len(intervalChoices))
	for _, c := range intervalChoices {
		label := "every " + c
		if mustDuration(c) == d.Every() {
			label += " (now)"
		}
		rows = append(rows, ochazuke.ChooserRow{ID: c, Label: label, Data: c})
	}
	_, err = a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  "interval-chooser",
		Title: "Run " + d.Name,
		Rows:  rows,
		Choose: func(c ochazuke.ChooserRow) ochazuke.Result {
			out, err := a.r.Execute(ochazuke.Request{Noun: scheduleNoun, Verb: "set-interval", Args: ochazuke.Arguments{"name": d.Name, "interval": c.Data.(string)}})
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(out)
		},
	})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

func (a *App) resetSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	s := a.r.Settings()
	if err := errors.Join(s.Reset(scheduleEnabledSetting(d.Name)), s.Reset(scheduleIntervalSetting(d.Name))); err != nil {
		return ochazuke.Failed(err)
	}
	return a.scheduleAnswer(d)
}

// startSchedules arms the runner when the TUI starts: the first check
// soon after, then every scheduleEvery, and a reader of finished runs.
func (a *App) startSchedules() tea.Cmd {
	if a.sched == nil {
		return nil
	}
	a.sched.live = true
	if a.sched.after == nil {
		return nil
	}
	return tea.Batch(a.sched.after(scheduleFirst, scheduleDue{}), a.waitScheduleRun())
}

// waitScheduleRun waits for the next run the timer started to finish.
func (a *App) waitScheduleRun() tea.Cmd {
	done, ctx := a.sched.runner.Done, a.sched.ctx
	return func() tea.Msg {
		select {
		case rec := <-done:
			return scheduleRan{rec}
		case <-ctx.Done():
			return nil
		}
	}
}

// schedulesUpdate drives the runner: on the timer it reads the rows (on
// the loop, settings included) and checks them off the loop; a finished
// run rebuilds the rows and, when a schedule starts failing, says so once.
func (a *App) schedulesUpdate(msg tea.Msg) tea.Cmd {
	if a.sched == nil || !a.sched.live {
		return nil
	}
	switch msg := msg.(type) {
	case scheduleDue:
		if a.sched.ticking {
			return nil
		}
		a.sched.ticking = true
		defs, runner, ctx := a.scheduleDefs(), a.sched.runner, a.sched.ctx
		return func() tea.Msg { return scheduleTicked{runner.Tick(ctx, defs)} }
	case scheduleTicked:
		a.sched.ticking = false
		if err := a.refreshSchedules(); err != nil {
			a.r.UI().Error(err.Error())
		}
		if a.sched.after == nil {
			return nil
		}
		return a.sched.after(scheduleEvery, scheduleDue{})
	case scheduleRan:
		if err := a.refreshSchedules(); err != nil {
			a.r.UI().Error(err.Error())
		}
		if err := a.refreshInbox(); err != nil {
			a.r.UI().Error(err.Error())
		}
		if msg.rec.Failed() && msg.rec.Outcome != schedules.OutcomeMissing && a.scheduleFailures(msg.rec.ScheduleID) == 1 {
			a.r.UI().Notify(runNotice(msg.rec))
		}
		return a.waitScheduleRun()
	}
	return nil
}

func (a *App) scheduleFailures(name string) int {
	states, err := a.sched.runner.States()
	if err != nil {
		return 0
	}
	return states[name].Failures
}

// stopSchedules cancels the runs in flight (their process groups are
// killed) and waits for them.
func (a *App) stopSchedules() {
	if a.sched == nil {
		return
	}
	a.sched.cancel()
	a.sched.runner.Wait()
}
