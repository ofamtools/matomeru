package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/drive"
	"github.com/unixsuperhero/matomeru/internal/schedules"
)

// Schedules: what mtm runs on its own, on an interval or by the clock:
// commands (the signal producers, o-gh signals and o-slack signals, so
// they feed the inbox without anyone pressing a key) and prompts (an agent
// started, or a pane sent a prompt, schedule_prompts.go). Each is a row of
// a kind (internal/schedules); nothing here knows one row from another.
//
// Rows a person adds (schedule add, edit, duplicate, delete, the form)
// are stored in the database (schedule_defs) and merge over the
// configured rows by name (schedule_edit.go).
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
	{Title: "when", Priority: 2},
	{Title: "last", Priority: 1},
	{Title: "next", Align: ochazuke.AlignRight, Priority: 3},
	{Title: "runs", Priority: 4},
}

// settingCatchUp is what every schedule without its own catch_up does
// about the runs it missed while mtm was closed.
const settingCatchUp = "schedules.catch-up"

// intervalChoices are the set-interval chooser's rows.
var intervalChoices = []string{"1m", "5m", "10m", "15m", "30m", "1h", "4h", "24h"}

// scheduleState is the feature's state: the rows as configured, the
// runner, the page's list and detail, and the TUI's timers.
type scheduleState struct {
	defs    []schedules.Def
	stored  []schedules.Def // the rows schedule add stored, over defs by name
	env     atomic.Pointer[promptEnv]
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
	runner.Kinds[schedules.KindPrompt] = a.runPromptSchedule
	runner.Drives[drive.Terminal] = a.runTerminalCommand
	runner.Location = a.location()
	defs, err := schedules.Load(filepath.Join(configDir, scheduleFile))
	st := &scheduleState{defs: defs, runner: runner}
	if err != nil {
		st.defs, st.loadErr = schedules.Defaults(), err.Error()
	}
	if st.stored, err = runner.Stored(); err != nil {
		return err
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

// scheduleSettings are schedules.catch-up and the two settings of every
// configured row (a stored row holds its own values).
func (a *App) scheduleSettings() []ochazuke.Setting {
	out := []ochazuke.Setting{{Name: settingCatchUp, Type: ochazuke.SettingString, Default: schedules.CatchUpNone, Enum: schedules.CatchUpValues,
		Description: "Runs a schedule missed while mtm was closed: none skips them, once runs it once on start; a schedule's own catch-up wins"}}
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

// scheduleDefs are the rows as they are now: the configured rows, the
// stored rows over them by name, a row's settings applied when they are
// set, and schedules.catch-up for the rows that give no catch-up.
func (a *App) scheduleDefs() []schedules.Def {
	s := a.r.Settings()
	set := func(name string) bool { v, err := s.Get(name); return err == nil && v.Set }
	catchUp := s.String(settingCatchUp)
	out := schedules.Layer(a.sched.defs, a.sched.stored)
	for i := range out {
		d := &out[i]
		if name := scheduleEnabledSetting(d.Name); set(name) {
			d.Enabled = s.String(name)
		}
		if name := scheduleIntervalSetting(d.Name); set(name) && !d.When().Calendar() {
			if v := s.Duration(name); v >= time.Minute {
				d.Interval = schedules.Duration(v)
			}
		}
		d.CatchUp = d.CatchUpOf(catchUp)
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
		Verb("list", a.listSchedules, "List the schedules: whether each runs, when, its last run and when it is due next").Rows(n).Fetch().
		Verb("add", a.addSchedule, "Add a schedule: a prompt (--prompt, --when, the agent's --harness, --model, --effort, --fast, --permission-mode, --project, --task, --dir, --target, --pane) or a command; in the TUI with no fields, a form").
		Verb("edit", a.editSchedule, "Change a schedule's fields (stored by mtm; a built-in or schedules.json row gets a stored copy); in the TUI with none, its form").Positional("name").
		Verb("duplicate", a.duplicateSchedule, "Copy a schedule to a new stored row, off (--as NAME, else NAME-copy)").Positional("name").
		Verb("delete", a.deleteSchedule, "Delete a stored schedule (one that overrode a built-in or file row brings it back)").Positional("name").Confirm("Delete schedule {{label}}?").
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
		Bind("i", ochazuke.Do(n+".set-interval").Hinted("interval")).
		Bind("n", ochazuke.Do(n+".add").Hinted("add")).
		Bind("E", ochazuke.Do(n+".edit").Hinted("edit")).
		Bind("d d", ochazuke.Do(n+".delete").Hinted("delete"))
	for _, verb := range []string{"show", "run-now", "enable", "disable", "toggle", "reset", "delete"} {
		a.parameters(n+"."+verb, map[string]ochazuke.Parameter{"name": scheduleNameParam})
	}
	a.parameters(n+".run-now", map[string]ochazuke.Parameter{"name": scheduleNameParam,
		"drive": {Type: "string", Enum: drive.Values, Description: "A command row's drive for this run only: exec, terminal or api (not available yet); empty: the row's"}})
	a.parameters(n+".history", map[string]ochazuke.Parameter{"name": scheduleNameParam,
		"limit": {Type: "integer", Description: "At most this many runs (default all kept)"}})
	a.parameters(n+".set-interval", map[string]ochazuke.Parameter{"name": scheduleNameParam,
		"interval": {Type: "string", Description: "How often: 1m, 5m, 1h, …"}})
	add, edit := a.scheduleParams(), a.scheduleParams()
	add["name"] = ochazuke.Parameter{Type: "string", Description: "The schedule's name (letters, digits, dashes); omitted, made from the prompt"}
	edit["name"] = scheduleNameParam
	a.parameters(n+".add", add)
	a.parameters(n+".edit", edit)
	a.parameters(n+".duplicate", map[string]ochazuke.Parameter{"name": scheduleNameParam,
		"as": {Type: "string", Description: "The copy's name (default NAME-copy)"}})
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
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Kind        string          `json:"kind"`
	Source      string          `json:"source"`
	Command     []string        `json:"command,omitempty"`
	Drive       string          `json:"drive,omitempty"` // a command row's: exec (default), terminal or api
	Prompt      string          `json:"prompt,omitempty"`
	Agent       schedules.Agent `json:"agent,omitzero"`
	When        string          `json:"when"`
	Start       string          `json:"start,omitempty"`
	End         string          `json:"end,omitempty"`
	CatchUp     string          `json:"catch_up"`
	Missed      int             `json:"missed,omitempty"`
	Interval    string          `json:"interval,omitempty"`
	Timeout     string          `json:"timeout"`
	Enabled     string          `json:"enabled"`
	Active      bool            `json:"active"`
	Why         string          `json:"why,omitempty"`
	Running     bool            `json:"running,omitempty"`
	LastRun     int64           `json:"last_run,omitempty"`
	LastOutcome string          `json:"last_outcome,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
	Failures    int             `json:"failures,omitempty"`
	NextRun     int64           `json:"next_run,omitempty"`
	// Runs are the recent runs (show only).
	Runs []schedules.Record `json:"runs,omitempty"`
}

// viewSchedule joins a row with its state. In the TUI an auto row is shown
// by its last check (the runner checks off the loop; before one it reads
// "not checked yet"); the CLI checks it.
func (a *App) viewSchedule(d schedules.Def, s schedules.State, now int64) scheduleView {
	v := scheduleView{Name: d.Name, Description: d.Description, Kind: d.KindOf(), Source: d.Source, Command: d.Command, Prompt: d.Prompt, Agent: d.Agent,
		Drive: commandDrive(d),
		When:  d.When().Text(), Start: d.Start, End: d.End, CatchUp: d.CatchUp, Missed: s.Missed, Timeout: scheduleSpan(d.Limit()),
		Enabled: d.Enabled, Running: s.Running(now), LastRun: s.Last, LastOutcome: s.LastOutcome, LastError: s.LastError, Failures: s.Failures}
	if d.Interval > 0 {
		v.Interval = scheduleSpan(d.Every())
	}
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
		if at, ok := d.Upcoming(s, now, d.CatchUp, a.sched.runner.Location); ok {
			v.NextRun = max(at, now)
		}
	}
	return v
}

// runsCell says what a run does: the command, or the agent and its prompt.
func (v scheduleView) runsCell() string {
	if v.Kind != schedules.KindPrompt {
		return strings.Join(v.Command, " ")
	}
	who := cmp.Or(v.Agent.Harness, "agent")
	if v.Agent.Target == schedules.TargetPane {
		who += " → " + cmp.Or(v.Agent.Pane, v.Name)
	}
	prompt := strings.Join(strings.Fields(v.Prompt), " ")
	if prompt == "" {
		prompt = "(no prompt)"
	}
	return who + ": " + prompt
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
	case !v.Active || v.NextRun == 0 && v.When == "never":
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

// commandDrive is a command row's drive for its view; "" for a prompt row.
func commandDrive(d schedules.Def) string {
	if d.KindOf() != schedules.KindCommand {
		return ""
	}
	return d.DriveOf()
}

func scheduleRowID(name string) string { return "schedule:" + name }

func (a *App) scheduleRow(v scheduleView, now int64) ocli.Row {
	return ocli.Row{Noun: scheduleNoun, ID: scheduleRowID(v.Name), Label: v.Name, Data: v, Cells: []string{
		v.Name, v.onCell(), v.When, v.lastCell(now), v.nextCell(now), v.runsCell()}}
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
		head := "schedules · n add · E edit · r run now · e on/off · i interval · settings schedules.catch-up, schedule.NAME.enabled|interval"
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
		failures = fmt.Sprintf("%d in a row", v.Failures)
		if v.Interval != "" {
			failures += "; backing off to " + scheduleSpan(schedules.Delay(mustDuration(v.Interval), v.Failures))
		}
	}
	missed := ""
	if v.Missed > 0 {
		missed = fmt.Sprintf("%d skipped while mtm was closed (catch-up %s)", v.Missed, v.CatchUp)
	}
	settings := v.Source + " · " + scheduleEnabledSetting(v.Name) + ", " + scheduleIntervalSetting(v.Name)
	if v.Source == schedules.SourceStored {
		settings = "stored by mtm · E edits it"
	}
	ag := v.Agent
	text := v.Name + "\n\n" + facts(
		"what", v.Description,
		"kind", v.Kind,
		"command", strings.Join(v.Command, " "),
		"drive", v.Drive,
		"prompt", v.Prompt,
		"harness", strings.TrimSpace(ag.Harness+" "+ag.Model),
		"settings", strings.Join(settingPairs(ag), " "),
		"project", strings.TrimSpace(ag.Project+" "+ag.Task),
		"folder", ag.Dir,
		"target", strings.TrimSpace(ag.Target+" "+ag.Pane),
		"enabled", v.onCell(),
		"why", v.Why,
		"when", v.When,
		"from", v.Start,
		"until", v.End,
		"catch-up", v.CatchUp,
		"missed", missed,
		"timeout", v.Timeout,
		"last", strings.TrimSpace(stamp(v.LastRun)+" "+v.LastOutcome),
		"error", v.LastError,
		"failing", failures,
		"next", next,
		"source", settings,
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
		if l := r.Launched; l != nil {
			line += "  " + cmp.Or(l.Pane, l.Target) + " " + l.Prompt
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
func scheduleSpan(d time.Duration) string { return schedules.Span(d) }

// settingPairs are a prompt row's launch settings, effort=high fast=on.
func settingPairs(ag schedules.Agent) []string {
	var out []string
	for _, kv := range [][2]string{{"effort", ag.Effort}, {"fast", ag.Fast}, {"permission-mode", ag.Permission}} {
		if kv[1] != "" {
			out = append(out, kv[0]+"="+kv[1])
		}
	}
	return out
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
		for _, d := range a.scheduleDefs() {
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
	if v := ochazuke.StringArg(ctx.Args, "drive", ""); v != "" {
		if err := drive.Check(v); err != nil {
			return ochazuke.Failed(err)
		}
		d.Drive = v // this run only
	}
	runner, runCtx := a.sched.runner, a.sched.ctx
	a.scheduleEnv()
	return ochazuke.Pending(func(context.Context) (any, error) {
		return runner.RunNow(runCtx, d)
	}).Then(func(c ochazuke.Ctx, v any, err error) {
		if err := a.refreshSchedules(); err != nil {
			a.r.UI().Error(err.Error())
		}
		if rec, ok := v.(schedules.Record); ok && err == nil {
			c.Notify(runNotice(rec))
			a.afterScheduleRun(rec)
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

// setScheduleEnabled turns a row on or off where its value lives: a
// stored row's own field (a setting set for it is reset), a configured
// row's setting.
func (a *App) setScheduleEnabled(ctx ochazuke.Ctx, value string) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if d.Source == schedules.SourceStored {
		d.Enabled = value
		if _, declared := a.r.Settings().Get(scheduleEnabledSetting(d.Name)); declared == nil {
			_ = a.r.Settings().Reset(scheduleEnabledSetting(d.Name))
		}
		if err := a.saveSchedule(d); err != nil {
			return ochazuke.Failed(err)
		}
		return a.scheduleAnswer(d)
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
		if d.Source == schedules.SourceStored || d.When().Calendar() {
			// A stored row, or one timed by the clock, holds its timing
			// itself: the interval replaces it.
			if v < time.Minute {
				return ochazuke.Failed(errors.New("an interval is at least 1m"))
			}
			d.SetWhen(schedules.When{Interval: schedules.Duration(v)})
			if err := a.saveSchedule(d); err != nil {
				return ochazuke.Failed(err)
			}
			return a.scheduleAnswer(d)
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
		// Rows another mtm or the CLI stored since are read each check.
		if stored, err := a.sched.runner.Stored(); err == nil {
			a.sched.stored = stored
		}
		a.scheduleEnv()
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
		a.afterScheduleRun(msg.rec)
		if msg.rec.Failed() && msg.rec.Outcome != schedules.OutcomeMissing && a.scheduleFailures(msg.rec.ScheduleID) == 1 {
			a.r.UI().Notify(runNotice(msg.rec))
		}
		return a.waitScheduleRun()
	}
	return nil
}

// afterScheduleRun brings the run's results in: the inbox (signals), and
// the pane list when it started or reached a pane.
func (a *App) afterScheduleRun(rec schedules.Record) {
	if err := a.refreshInbox(); err != nil {
		a.r.UI().Error(err.Error())
	}
	if rec.Launched != nil && a.r.Instances[paneListID] != nil {
		if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "refresh", Target: paneListID}); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
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
