// Package schedules runs commands on an interval: the signal producers
// (o-gh signals, o-slack signals) and anything else a schedule row names,
// so outside signals reach the inbox without anyone pressing a key.
//
// A schedule is configuration: a row with a name, a command (argv), an
// interval, a timeout and whether it is on. The built-in rows (Defaults)
// merge by name with <config>/schedules.json (Load), the way harnesses.json
// merges over the built-in harnesses; mtm's settings flip each row
// (schedule.NAME.enabled, schedule.NAME.interval). The package knows
// nothing of settings or the TUI: the caller hands Tick the rows as they
// are now.
//
// What a schedule did is state, in the database: one row per schedule in
// the schedules table (last run, next run, last outcome, the failure
// count, who runs it now) and a short history in schedule_runs. Both
// tables are matomeru-old's declarations (owner schedules, key id, version
// 1), so the owner's database opens them unchanged; the run rows keep
// matomeru-old's RunRecord field names.
//
// Runs never overlap and never double up across processes: a run first
// claims its schedule in a write transaction (BEGIN IMMEDIATE, so two
// processes queue), which fails while another claim is live, and a
// scheduled run also re-checks that the schedule is due inside the same
// transaction. Two TUIs that tick at once therefore run each occurrence
// once. A claim lapses after the run's timeout and a grace, so a process
// that died mid-run frees its schedule.
//
// Stored rows (defs.go) are the schedules a person adds from mtm: the
// schedule_defs table, merged over the configured rows by name. A row's
// kind picks its Executor in Runner.Kinds (command here; mtm adds prompt),
// and its timing is an interval, days and times, or cron (when.go).
//
// The run logic is lifted from matomeru-old's internal/schedules (launch,
// execute, record): advance before running so a failure never repeats the
// same occurrence, skip (not queue) an overlapping run, a timeout per run,
// a bounded history. New here: backoff after failures, the cross-process
// claim, a command that is not installed recorded quietly as missing, and
// output kept with each run.
package schedules

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/unixsuperhero/matomeru/store"
)

// Tables are matomeru-old's declarations, unchanged.
var (
	Table     = store.Table{Name: "schedules", Owner: "schedules", Key: "id", Version: 1}
	RunsTable = store.Table{Name: "schedule_runs", Owner: "schedules", Key: "id", Version: 1}
)

// Enabled values. Auto runs the schedule when its command is on PATH and
// every Requires check exits 0.
const (
	On   = "on"
	Off  = "off"
	Auto = "auto"
)

// EnabledValues are the values a schedule's enabled field takes.
var EnabledValues = []string{On, Off, Auto}

// Outcomes of a run.
const (
	OutcomeOK      = "ok"
	OutcomeFailed  = "failed"
	OutcomeTimeout = "timeout"
	// OutcomeMissing is a command that is not installed: recorded on the
	// row and backed off like a failure, never announced.
	OutcomeMissing = "missing"
)

const (
	// DefaultTimeout bounds a run whose row gives none.
	DefaultTimeout = 2 * time.Minute
	// DefaultKeep is how many runs each schedule's history keeps.
	DefaultKeep = 20
	// claimGrace is how long past its timeout a claim stays live.
	claimGrace = 30 * time.Second
	// maxBackoffShift caps the doubling: interval << 6 at most.
	maxBackoffShift = 6
	// outputLimit is how much of a run's output is kept (its tail).
	outputLimit = 4096
	// probeEvery is how long an auto schedule's checks are trusted.
	probeEvery = 10 * time.Minute
	// probeTimeout bounds one Requires check.
	probeTimeout = 10 * time.Second
)

// Duration is a time.Duration written as "5m" in JSON.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("a duration is a string such as \"5m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Kinds of schedule: what a run does. A kind is a name in Runner.Kinds,
// so the caller adds kinds (mtm adds prompt) without a branch here.
const (
	KindCommand = "command" // run Command (matomeru-old's command kind)
	KindPrompt  = "prompt"  // start an agent with Prompt, or send it to a pane (matomeru-old's prompt kind)
)

// Sources of a schedule row.
const (
	SourceBuiltin = "built-in"
	SourceFile    = "file" // schedules.json
	SourceStored  = "mtm"  // the schedule_defs table: added with mtm schedule add
)

// Targets of a prompt schedule's agent.
const (
	TargetNew  = "new"  // a new pane each run
	TargetPane = "pane" // the pane named Agent.Pane, sent the prompt when it is ready (a new one when none is open)
)

// TargetValues are the values of Agent.Target.
var TargetValues = []string{TargetNew, TargetPane}

// Agent is how a prompt schedule starts its agent: the harness profile
// (harnesses.json) and the session settings it launches with, where it
// works, and which pane gets the prompt. Empty fields take mtm's
// defaults (the project's harness, else default-harness; the harness's
// first model; the project's directory, else home).
type Agent struct {
	Harness    string `json:"harness,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Fast       string `json:"fast,omitempty"`
	Permission string `json:"permission_mode,omitempty"`
	Project    string `json:"project,omitempty"`
	Task       string `json:"task,omitempty"`
	Dir        string `json:"dir,omitempty"`
	Target     string `json:"target,omitempty"` // new (default) or pane
	// Pane names the pane: the window a target pane run looks for and
	// the name of a pane a run opens (default: the schedule's name).
	Pane string `json:"pane,omitempty"`
}

// Def is one schedule as configured.
type Def struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Kind is what a run does: command (the default when Command is
	// given) or prompt (the default when Prompt is).
	Kind    string   `json:"kind,omitempty"`
	Command []string `json:"command,omitempty"`
	// Prompt is a prompt schedule's text; Agent says how it is sent.
	Prompt string `json:"prompt,omitempty"`
	Agent  Agent  `json:"agent,omitzero"`
	// The timing (when.go): an interval, or days and times, or cron;
	// start and end bound it.
	Interval Duration `json:"interval,omitempty"`
	Days     []string `json:"days,omitempty"`
	At       []string `json:"at,omitempty"`
	Cron     string   `json:"cron,omitempty"`
	Start    string   `json:"start,omitempty"`
	End      string   `json:"end,omitempty"`
	// CatchUp is none or once (CatchUpValues); empty takes the caller's
	// default (mtm's schedules.catch-up setting).
	CatchUp string   `json:"catch_up,omitempty"`
	Timeout Duration `json:"timeout,omitempty"`
	// Enabled is on, off or auto; a file row without it is on.
	Enabled string `json:"enabled,omitempty"`
	// Requires are checks (argv) that must exit 0 for auto to run it.
	Requires [][]string `json:"requires,omitempty"`
	// Env is added to the command's environment.
	Env map[string]string `json:"env,omitempty"`
	// Created is when a stored row was added (Unix ms): calendar and
	// interval timing count from it until the first run.
	Created int64 `json:"created,omitempty"`
	Updated int64 `json:"updated,omitempty"`
	// Source is where the row came from (SourceBuiltin, SourceFile,
	// SourceStored); not stored.
	Source string `json:"-"`
}

// Every is the interval as a time.Duration.
func (d Def) Every() time.Duration { return time.Duration(d.Interval) }

// When is the row's timing.
func (d Def) When() When { return When{Interval: d.Interval, Days: d.Days, At: d.At, Cron: d.Cron} }

// SetWhen replaces the row's timing.
func (d *Def) SetWhen(w When) { d.Interval, d.Days, d.At, d.Cron = w.Interval, w.Days, w.At, w.Cron }

// KindOf is the row's kind: Kind, else prompt for a row with a prompt and
// no command, else command.
func (d Def) KindOf() string {
	switch {
	case d.Kind != "":
		return d.Kind
	case d.Prompt != "" && len(d.Command) == 0:
		return KindPrompt
	}
	return KindCommand
}

// CatchUpOf is the row's catch-up, def when it gives none.
func (d Def) CatchUpOf(def string) string { return cmp.Or(d.CatchUp, def, CatchUpNone) }

// bounds are the row's start and end in loc (zero when unset or unreadable).
func (d Def) bounds(loc *time.Location) (time.Time, time.Time) {
	start, _ := ParseBound(d.Start, false, loc)
	end, _ := ParseBound(d.End, true, loc)
	return start, end
}

// after is the first occurrence strictly after t (Unix ms) within the
// row's bounds: t plus the interval (backed off by failures) or the next
// calendar time; false when there is none (no timing, or past the end).
func (d Def) after(t int64, failures int, loc *time.Location) (int64, bool) {
	start, end := d.bounds(loc)
	w := d.When()
	var at time.Time
	switch {
	case w.Calendar():
		from := time.UnixMilli(t)
		if !start.IsZero() && from.Before(start) {
			from = start.Add(-time.Nanosecond)
		}
		var ok bool
		if at, ok = w.nextAfter(from, loc); !ok {
			return 0, false
		}
	case w.Interval > 0:
		at = time.UnixMilli(t + Delay(d.Every(), failures).Milliseconds())
		if !start.IsZero() && at.Before(start) {
			at = start
		}
	default:
		return 0, false
	}
	if !end.IsZero() && at.After(end) {
		return 0, false
	}
	return at.UnixMilli(), true
}

// Occurrence is when the row is due next given its state: ok false when
// it never is (no timing, ended); first for an interval row that never
// ran and has no anchor (due at once, or at its start, and never counted
// as missed); anchor for a calendar row with nothing to count from yet
// (the runner anchors it at its first check).
func (d Def) Occurrence(s State, now int64, loc *time.Location) (at int64, ok, first, anchor bool) {
	start, end := d.bounds(loc)
	if !end.IsZero() && now > end.UnixMilli() {
		return 0, false, false, false
	}
	base := max(s.Last, s.Since, d.Created)
	if base == 0 {
		if d.When().Calendar() {
			return 0, false, false, true
		}
		if d.Interval <= 0 {
			return 0, false, false, false
		}
		if !start.IsZero() {
			return start.UnixMilli(), true, true, false
		}
		return 0, true, true, false
	}
	at, ok = d.after(base, s.Failures, loc)
	return at, ok, false, false
}

// Upcoming is when the row runs next as a person reads it at now: the
// occurrence, or (an occurrence that is missed and will be skipped, or a
// calendar row not yet anchored) the next one after now; false for never.
func (d Def) Upcoming(s State, now int64, catchUp string, loc *time.Location) (int64, bool) {
	at, ok, first, anchor := d.Occurrence(s, now, loc)
	switch {
	case anchor:
		return d.after(now, 0, loc)
	case !ok:
		return 0, false
	case !first && now-at > MissGrace.Milliseconds() && d.CatchUpOf(catchUp) == CatchUpNone:
		return d.after(now, 0, loc)
	}
	return at, true
}

// Limit is the timeout, DefaultTimeout when unset.
func (d Def) Limit() time.Duration {
	if d.Timeout <= 0 {
		return DefaultTimeout
	}
	return time.Duration(d.Timeout)
}

// Defaults are the built-in schedules, both shipped off but o-gh's, which
// is auto: it runs where o-gh is installed and gh is signed in. o-slack
// runs only on the owner's work machine; elsewhere turning it on records
// "missing" on its row and nothing else. {mtm} and {mtm-args} in a value
// are this mtm and the words that point a child at the same database.
func Defaults() []Def {
	return []Def{
		{Name: "gh-signals", Description: "GitHub review requests, CI and PR signals into the inbox (o-gh signals)",
			Command: []string{"o-gh", "signals"}, Interval: Duration(5 * time.Minute), Enabled: Auto, CatchUp: CatchUpOnce, Source: SourceBuiltin,
			Requires: [][]string{{"gh", "auth", "status", "--active"}},
			Env:      map[string]string{"O_GH_MTM": "{mtm}", "O_GH_MTM_ARGS": "{mtm-args}"}},
		{Name: "slack-signals", Description: "Slack mentions, DMs and threads into the inbox (o-slack signals; the work machine)",
			Command: []string{"o-slack", "signals"}, Interval: Duration(5 * time.Minute), Enabled: Off, CatchUp: CatchUpOnce, Source: SourceBuiltin,
			Env: map[string]string{"O_SLACK_MTM": "{mtm}", "O_SLACK_MTM_ARGS": "{mtm-args}"}},
	}
}

// Load is Defaults merged with the file at path, a JSON array of schedule
// objects: an entry naming a default changes only the fields it gives,
// any other entry adds a schedule (on unless it says otherwise). A missing
// file (or an empty path) is the defaults.
func Load(path string) ([]Def, error) {
	defs := Defaults()
	if path == "" {
		return defs, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defs, nil
	}
	if err != nil {
		return nil, err
	}
	merged, err := Merge(defs, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return merged, nil
}

// Merge applies a schedules file to base by name (see Load).
func Merge(base []Def, data []byte) ([]Def, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("the schedules file is a JSON array of schedule objects: %w", err)
	}
	out := slices.Clone(base)
	for i, raw := range entries {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &named); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if !ValidName(named.Name) {
			return nil, fmt.Errorf("entry %d: name %q: use letters, digits and dashes", i, named.Name)
		}
		at := slices.IndexFunc(out, func(d Def) bool { return d.Name == named.Name })
		d := Def{Enabled: On}
		if at >= 0 {
			d = out[at]
			d.Env = nil // a given env replaces the default's
			if base := out[at].Env; base != nil {
				d.Env = make(map[string]string, len(base))
				for k, v := range base {
					d.Env[k] = v
				}
			}
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			return nil, fmt.Errorf("schedule %s: %w", named.Name, err)
		}
		if err := d.Check(); err != nil {
			return nil, fmt.Errorf("schedule %s: %w", named.Name, err)
		}
		d.Source = SourceFile
		if at >= 0 {
			out[at] = d
		} else {
			out = append(out, d)
		}
	}
	return out, nil
}

// Check reports what makes the row unusable: a command row without its
// command, timing that does not read, values outside their choices. A
// prompt row may be incomplete (no prompt, no timing): it is recorded
// and simply never due, or fails its run, so a form never blocks on it.
func (d Def) Check() error {
	switch {
	case d.KindOf() == KindCommand && (len(d.Command) == 0 || d.Command[0] == ""):
		return errors.New("command is required (an argv array)")
	case d.KindOf() == KindCommand && d.When().Zero():
		return errors.New("a command schedule needs an interval, days/at or cron")
	case !slices.Contains(EnabledValues, d.Enabled):
		return fmt.Errorf("enabled is one of %s", strings.Join(EnabledValues, ", "))
	case d.CatchUp != "" && !slices.Contains(CatchUpValues, d.CatchUp):
		return fmt.Errorf("catch_up is one of %s", strings.Join(CatchUpValues, ", "))
	case d.Agent.Target != "" && !slices.Contains(TargetValues, d.Agent.Target):
		return fmt.Errorf("agent target is one of %s", strings.Join(TargetValues, ", "))
	}
	if err := d.When().Check(); err != nil {
		return err
	}
	for _, b := range []struct {
		v   string
		end bool
	}{{d.Start, false}, {d.End, true}} {
		if _, err := ParseBound(b.v, b.end, time.Local); err != nil {
			return err
		}
	}
	return nil
}

// ValidName reports whether name can name a schedule (and its settings).
func ValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// State is a schedule's row in the schedules table, keyed by its name.
// The field names that matomeru-old's Schedule had keep their JSON names.
type State struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Last  int64  `json:"last"` // the last run's start, Unix ms
	// Since is when the schedule started counting without a run: its first
	// check (a calendar row), or a skipped catch-up.
	Since int64 `json:"since,omitempty"`
	// Missed counts the occurrences skipped because mtm was not running.
	Missed      int    `json:"missed,omitempty"`
	Next        int64  `json:"next"` // when it is due next (shown; Due recomputes)
	Updated     int64  `json:"updated"`
	LastRunID   string `json:"last_run_id,omitempty"`
	LastOutcome string `json:"last_outcome,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	// Failures counts the runs since the last ok one; it drives backoff.
	Failures int `json:"failures,omitempty"`
	// RunningBy and RunningUntil are the live claim: who runs it, and
	// when the claim lapses if that process never records the run.
	RunningBy    string `json:"running_by,omitempty"`
	RunningUntil int64  `json:"running_until,omitempty"`
}

// Running reports a live claim at now (Unix ms).
func (s State) Running(now int64) bool { return s.RunningBy != "" && s.RunningUntil > now }

// Delay is how long after a run's start the next one is due: the interval,
// doubled per failure since the last success, at most max(interval, 1h).
func Delay(interval time.Duration, failures int) time.Duration {
	if failures <= 0 {
		return interval
	}
	limit := max(interval, time.Hour)
	d := interval << min(failures, maxBackoffShift)
	if d > limit || d <= 0 {
		return limit
	}
	return d
}

// DueAt is when an interval schedule is due next, Unix ms; 0 for never run (due now).
func (s State) DueAt(interval time.Duration) int64 {
	if s.Last == 0 {
		return 0
	}
	return s.Last + Delay(interval, s.Failures).Milliseconds()
}

// Record is one run, in schedule_runs (matomeru-old's RunRecord names).
type Record struct {
	ID         string   `json:"id"`
	ScheduleID string   `json:"schedule_id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"` // command, prompt
	Command    []string `json:"command,omitempty"`
	// Window is the pane a prompt run reached (matomeru-old's name).
	Window string `json:"window,omitempty"`
	// Launched is what a prompt run started or sent to.
	Launched *Launched `json:"launched,omitempty"`
	Started  int64     `json:"started"`
	Finished int64     `json:"finished"`
	Duration int64     `json:"duration_ms"`
	Outcome  string    `json:"outcome"`
	Summary  string    `json:"summary"`
	Error    string    `json:"error,omitempty"`
	Manual   bool      `json:"manual,omitempty"`
	ExitCode int       `json:"exit_code,omitempty"`
	Output   string    `json:"output,omitempty"`
}

// Launched is what a prompt run did: the pane it opened (target new) or
// queued the prompt in (target pane), with the agent configuration it
// used, so a run's history links to its pane.
type Launched struct {
	Target   string            `json:"target"` // new, or queued (sent to an open pane when it is ready)
	Session  string            `json:"session,omitempty"`
	Window   string            `json:"window,omitempty"`
	Name     string            `json:"name,omitempty"`
	Pane     string            `json:"pane,omitempty"` // the pane row: pane:SESSION/WINDOW
	Harness  string            `json:"harness,omitempty"`
	Model    string            `json:"model,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
	Argv     []string          `json:"argv,omitempty"`
	Typed    []string          `json:"typed,omitempty"` // setting commands typed (or queued) before the prompt
	Cwd      string            `json:"cwd,omitempty"`
	Project  string            `json:"project,omitempty"`
	Task     string            `json:"task,omitempty"`
	Prompt   string            `json:"prompt,omitempty"` // sent, argv, queued, not sent, none
	QueueID  string            `json:"queue_id,omitempty"`
	Note     string            `json:"note,omitempty"`
}

// Failed reports a run that did not succeed.
func (r Record) Failed() bool { return r.Outcome != OutcomeOK }

// ExecFunc runs argv with env added and returns its combined output and
// exit; tests replace it.
type ExecFunc func(ctx context.Context, argv []string, env []string) ([]byte, error)

// Executor runs one occurrence of a kind of schedule, filling the record
// (its command, output, what it launched); its error is the run's
// failure (ErrMissing for something not installed, the context's
// deadline for a timeout).
type Executor func(ctx context.Context, def Def, rec *Record) error

// ErrRunning is a run asked for while the schedule already runs.
var ErrRunning = errors.New("the schedule is already running")

// ErrMissing wraps a command that is not on PATH.
var ErrMissing = errors.New("not on PATH")

// Runner runs schedules. Every field but the store may be replaced
// before the first Tick; New sets real ones.
type Runner struct {
	db *store.Store
	// Now is the clock; tests replace it.
	Now func() time.Time
	// Exec runs a command (the real one runs it under nice in its own
	// process group, killed with the group on timeout).
	Exec ExecFunc
	// LookPath finds a command; tests replace it.
	LookPath func(string) (string, error)
	// Owner names this process in claims (pid@host).
	Owner string
	// Keep is how many runs per schedule the history keeps.
	Keep int
	// Expand rewrites {mtm}-style placeholders in commands and env.
	Expand func(string) string
	// Done receives every finished run Tick started (never blocks: a full
	// channel drops the message; the rows are in the store anyway).
	Done chan Record
	// Kinds run each kind of schedule; New sets command.
	Kinds map[string]Executor
	// Location is the time zone days and times are read in.
	Location *time.Location

	mu      sync.Mutex
	running map[string]bool
	probes  map[string]probe
	wg      sync.WaitGroup
	seq     uint64
}

type probe struct {
	at     time.Time
	ok     bool
	reason string
}

// New declares the tables and returns a runner with the real clock and exec.
func New(db *store.Store) (*Runner, error) {
	fresh := !db.Declared(DefsTable.Name)
	for _, t := range []store.Table{Table, RunsTable, DefsTable} {
		if err := db.Declare(t); err != nil {
			return nil, err
		}
	}
	host, _ := os.Hostname()
	r := &Runner{db: db, Now: time.Now, Exec: RealExec, LookPath: exec.LookPath, Keep: DefaultKeep,
		Owner: fmt.Sprintf("%d@%s", os.Getpid(), host), Done: make(chan Record, 16), Location: time.Local,
		running: map[string]bool{}, probes: map[string]probe{}}
	r.Kinds = map[string]Executor{KindCommand: r.runCommand}
	if fresh {
		// The table is new to this database: bring over matomeru-old's
		// prompt schedules, once.
		if _, err := r.ImportLegacy(); err != nil {
			return nil, fmt.Errorf("schedules: importing matomeru-old's prompt schedules: %w", err)
		}
	}
	return r, nil
}

func (r *Runner) loc() *time.Location {
	if r.Location == nil {
		return time.Local
	}
	return r.Location
}

func (r *Runner) now() int64 { return r.Now().UnixMilli() }

func (r *Runner) installed(name string) bool {
	_, err := r.LookPath(r.expand(name))
	return err == nil
}

func (r *Runner) expand(s string) string {
	if r.Expand == nil {
		return s
	}
	return r.Expand(s)
}

// Active reports whether def runs now and, when it does not or is auto,
// why: "off", "auto: o-gh not on PATH", "auto: gh auth status --active failed".
// An auto schedule's checks run at most every probeEvery and may take a
// few seconds; call it off the UI loop (Tick does).
func (r *Runner) Active(ctx context.Context, def Def) (bool, string) {
	switch def.Enabled {
	case On:
		return true, ""
	case Auto:
	default:
		return false, Off
	}
	r.mu.Lock()
	p, ok := r.probes[def.Name]
	r.mu.Unlock()
	if ok && r.Now().Sub(p.at) < probeEvery {
		return p.ok, p.reason
	}
	p = probe{at: r.Now(), ok: true, reason: "auto: checks pass"}
	if len(def.Command) > 0 && !r.installed(def.Command[0]) {
		p.ok, p.reason = false, "auto: "+def.Command[0]+" not on PATH"
	}
	for _, check := range def.Requires {
		if !p.ok || len(check) == 0 {
			break
		}
		cctx, cancel := context.WithTimeout(ctx, probeTimeout)
		_, err := r.Exec(cctx, r.argv(check), nil)
		cancel()
		if err != nil {
			p.ok, p.reason = false, "auto: "+strings.Join(check, " ")+" failed"
		}
	}
	r.mu.Lock()
	r.probes[def.Name] = p
	r.mu.Unlock()
	return p.ok, p.reason
}

// Probed is the last auto check of def without running one: ok, why and
// whether there was one.
func (r *Runner) Probed(def Def) (bool, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.probes[def.Name]
	return p.ok, p.reason, ok
}

// Tick starts every active schedule that is due and not running, each in
// its own goroutine, and returns their names. A run's record goes to the
// store and to Done.
func (r *Runner) Tick(ctx context.Context, defs []Def) []string {
	var started []string
	for _, def := range defs {
		if ctx.Err() != nil {
			break
		}
		if on, _ := r.Active(ctx, def); !on {
			continue
		}
		state, ok, err := r.claim(def, false)
		if err != nil || !ok {
			continue
		}
		started = append(started, def.Name)
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			rec := r.execute(ctx, def, state, false)
			select {
			case r.Done <- rec:
			default:
			}
		}()
	}
	return started
}

// RunNow runs def at once and waits, whether or not it is on or due; it
// fails with ErrRunning while a run of it is live (here or in another
// process). The record is returned, not sent to Done.
func (r *Runner) RunNow(ctx context.Context, def Def) (Record, error) {
	state, ok, err := r.claim(def, true)
	if err != nil {
		return Record{}, err
	}
	if !ok {
		return Record{}, ErrRunning
	}
	return r.execute(ctx, def, state, true), nil
}

// Wait waits for the runs Tick started.
func (r *Runner) Wait() { r.wg.Wait() }

// claim takes the schedule for one run: in one write transaction it
// fails when a claim is live, and (for a scheduled run) when the schedule
// is not due; otherwise it records the claim and the run's start.
func (r *Runner) claim(def Def, manual bool) (State, bool, error) {
	r.mu.Lock()
	if r.running[def.Name] {
		r.mu.Unlock()
		return State{}, false, nil
	}
	r.running[def.Name] = true
	r.mu.Unlock()
	now := r.now()
	var claimed State
	ok := false
	err := r.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		var s State
		if err := tx.Get(Table.Name, def.Name, &s); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if s.Running(now) {
			return nil
		}
		s.ID, s.Title = def.Name, cmp.Or(def.Description, def.Name)
		if !manual {
			at, ok, first, anchor := def.Occurrence(s, now, r.loc())
			switch {
			case anchor:
				// A calendar row counts from its first check.
				s.Since, s.Updated = now, now
				s.Next, _ = def.after(now, 0, r.loc())
				return tx.Put(Table.Name, s.ID, s)
			case !ok || at > now:
				return nil
			case !first && now-at > MissGrace.Milliseconds() && def.CatchUpOf("") == CatchUpNone:
				// Missed while no mtm ran: skip to the next one on time.
				s.Since, s.Updated = now, now
				s.Missed++
				s.Next, _ = def.after(now, 0, r.loc())
				return tx.Put(Table.Name, s.ID, s)
			}
		}
		s.Last, s.Updated = now, now
		s.Next, _ = def.after(now, s.Failures, r.loc())
		s.RunningBy, s.RunningUntil = r.Owner, now+(def.Limit()+claimGrace).Milliseconds()
		claimed, ok = s, true
		return tx.Put(Table.Name, s.ID, s)
	})
	if err != nil || !ok {
		r.release(def.Name)
	}
	return claimed, ok, err
}

func (r *Runner) release(name string) {
	r.mu.Lock()
	delete(r.running, name)
	r.mu.Unlock()
}

func (r *Runner) argv(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = r.expand(a)
	}
	return out
}

func (r *Runner) env(def Def) []string {
	keys := make([]string, 0, len(def.Env))
	for k := range def.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+r.expand(def.Env[k]))
	}
	return out
}

func (r *Runner) newID(at int64) string {
	r.mu.Lock()
	r.seq++
	n := r.seq
	r.mu.Unlock()
	return fmt.Sprintf("%d-%d-%s", at, n, r.Owner)
}

// runCommand is the command kind: Command under nice with Env, its output
// kept; a command not on PATH is ErrMissing.
func (r *Runner) runCommand(ctx context.Context, def Def, rec *Record) error {
	if len(def.Command) == 0 {
		return errors.New("the schedule has no command")
	}
	argv := r.argv(def.Command)
	rec.Command = argv
	if _, err := r.LookPath(argv[0]); err != nil {
		return fmt.Errorf("%s %w", argv[0], ErrMissing)
	}
	out, err := r.Exec(ctx, argv, r.env(def))
	rec.Output = tail(string(out), outputLimit)
	return err
}

func (r *Runner) execute(ctx context.Context, def Def, state State, manual bool) Record {
	defer r.release(def.Name)
	rec := Record{ScheduleID: def.Name, Title: cmp.Or(def.Description, def.Name), Kind: def.KindOf(),
		Started: state.Last, Manual: manual}
	rec.ID = r.newID(rec.Started)
	var err error
	if run := r.Kinds[rec.Kind]; run == nil {
		err = fmt.Errorf("mtm runs no %q schedules", rec.Kind)
	} else {
		runCtx, cancel := context.WithTimeout(ctx, def.Limit())
		err = run(runCtx, def, &rec)
		if err != nil && runCtx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("timed out after %s: %w", def.Limit(), context.DeadlineExceeded)
		}
		cancel()
	}
	rec.Finished = r.now()
	rec.Duration = rec.Finished - rec.Started
	switch {
	case err == nil:
		rec.Outcome = OutcomeOK
	case errors.Is(err, ErrMissing):
		rec.Outcome = OutcomeMissing
	case errors.Is(err, context.DeadlineExceeded):
		rec.Outcome = OutcomeTimeout
	default:
		rec.Outcome = OutcomeFailed
	}
	if err != nil {
		rec.Error = err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			rec.ExitCode = exit.ExitCode()
		}
		if line := lastLine(rec.Output); line != "" && rec.Outcome == OutcomeFailed {
			rec.Error += ": " + line
		}
	}
	rec.Summary = def.Name + " · " + rec.Outcome
	if err := r.record(def, rec); err != nil {
		rec.Error = strings.TrimPrefix(rec.Error+"; ", "; ") + "recording the run: " + err.Error()
	}
	return rec
}

// record stores the run, trims the schedule's history to Keep, and
// updates the state: the claim released, the outcome, the failure count
// and the next due time.
func (r *Runner) record(def Def, rec Record) error {
	keep := cmp.Or(r.Keep, DefaultKeep)
	return r.db.Tx([]string{Table.Name, RunsTable.Name}, func(tx *store.Tx) error {
		if err := tx.Put(RunsTable.Name, rec.ID, rec); err != nil {
			return err
		}
		runs, err := runsOf(tx, def.Name)
		if err != nil {
			return err
		}
		for len(runs) > keep {
			if err := tx.Delete(RunsTable.Name, runs[len(runs)-1].ID); err != nil {
				return err
			}
			runs = runs[:len(runs)-1]
		}
		var s State
		if err := tx.Get(Table.Name, def.Name, &s); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		s.ID, s.Title = def.Name, rec.Title
		if s.RunningBy == r.Owner {
			s.RunningBy, s.RunningUntil = "", 0
		}
		s.LastRunID, s.LastOutcome, s.LastError = rec.ID, rec.Outcome, rec.Error
		if rec.Failed() {
			s.Failures++
		} else {
			s.Failures = 0
		}
		s.Next, _ = def.after(s.Last, s.Failures, r.loc())
		s.Updated = rec.Finished
		return tx.Put(Table.Name, s.ID, s)
	})
}

// runsOf is a schedule's runs, newest first.
func runsOf(tx *store.Tx, name string) ([]Record, error) {
	rows, err := tx.All(RunsTable.Name)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, raw := range rows {
		var rec Record
		if json.Unmarshal(raw, &rec) == nil && rec.ScheduleID == name {
			out = append(out, rec)
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int {
		return cmp.Or(cmp.Compare(b.Started, a.Started), strings.Compare(b.ID, a.ID))
	})
	return out, nil
}

// States are every schedule's state row by name.
func (r *Runner) States() (map[string]State, error) {
	out := map[string]State{}
	err := r.db.View([]string{Table.Name}, func(tx *store.Tx) error {
		rows, err := tx.All(Table.Name)
		if err != nil {
			return err
		}
		for _, raw := range rows {
			var s State
			if json.Unmarshal(raw, &s) == nil && s.ID != "" {
				out[s.ID] = s
			}
		}
		return nil
	})
	return out, err
}

// History is a schedule's runs, newest first, at most limit (0: all kept).
func (r *Runner) History(name string, limit int) ([]Record, error) {
	var out []Record
	err := r.db.View([]string{RunsTable.Name}, func(tx *store.Tx) error {
		var err error
		out, err = runsOf(tx, name)
		return err
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, err
}

// RealExec runs argv under nice (when nice is on PATH) in its own process
// group, with env added to this process's, and kills the group when ctx
// ends.
func RealExec(ctx context.Context, argv []string, env []string) ([]byte, error) {
	if nice, err := exec.LookPath("nice"); err == nil {
		argv = append([]string{nice, "-n", "10"}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…"
	}
	return s
}
