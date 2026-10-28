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

// Def is one schedule as configured.
type Def struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Command     []string `json:"command"`
	Interval    Duration `json:"interval"`
	Timeout     Duration `json:"timeout,omitempty"`
	// Enabled is on, off or auto; a file row without it is on.
	Enabled string `json:"enabled,omitempty"`
	// Requires are checks (argv) that must exit 0 for auto to run it.
	Requires [][]string `json:"requires,omitempty"`
	// Env is added to the command's environment.
	Env map[string]string `json:"env,omitempty"`
}

// Every is the interval as a time.Duration.
func (d Def) Every() time.Duration { return time.Duration(d.Interval) }

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
			Command: []string{"o-gh", "signals"}, Interval: Duration(5 * time.Minute), Enabled: Auto,
			Requires: [][]string{{"gh", "auth", "status", "--active"}},
			Env:      map[string]string{"O_GH_MTM": "{mtm}", "O_GH_MTM_ARGS": "{mtm-args}"}},
		{Name: "slack-signals", Description: "Slack mentions, DMs and threads into the inbox (o-slack signals; the work machine)",
			Command: []string{"o-slack", "signals"}, Interval: Duration(5 * time.Minute), Enabled: Off,
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
		if err := d.check(); err != nil {
			return nil, fmt.Errorf("schedule %s: %w", named.Name, err)
		}
		if at >= 0 {
			out[at] = d
		} else {
			out = append(out, d)
		}
	}
	return out, nil
}

func (d Def) check() error {
	switch {
	case len(d.Command) == 0 || d.Command[0] == "":
		return errors.New("command is required (an argv array)")
	case d.Interval < Duration(time.Minute):
		return errors.New("interval is at least 1m")
	case !slices.Contains(EnabledValues, d.Enabled):
		return fmt.Errorf("enabled is one of %s", strings.Join(EnabledValues, ", "))
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
	ID          string `json:"id"`
	Title       string `json:"title"`
	Last        int64  `json:"last"` // the last run's start, Unix ms
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

// DueAt is when the schedule is due next, Unix ms; 0 for never run (due now).
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
	Kind       string   `json:"kind"` // command
	Command    []string `json:"command,omitempty"`
	Started    int64    `json:"started"`
	Finished   int64    `json:"finished"`
	Duration   int64    `json:"duration_ms"`
	Outcome    string   `json:"outcome"`
	Summary    string   `json:"summary"`
	Error      string   `json:"error,omitempty"`
	Manual     bool     `json:"manual,omitempty"`
	ExitCode   int      `json:"exit_code,omitempty"`
	Output     string   `json:"output,omitempty"`
}

// Failed reports a run that did not succeed.
func (r Record) Failed() bool { return r.Outcome != OutcomeOK }

// ExecFunc runs argv with env added and returns its combined output and
// exit; tests replace it.
type ExecFunc func(ctx context.Context, argv []string, env []string) ([]byte, error)

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
	for _, t := range []store.Table{Table, RunsTable} {
		if err := db.Declare(t); err != nil {
			return nil, err
		}
	}
	host, _ := os.Hostname()
	return &Runner{db: db, Now: time.Now, Exec: RealExec, LookPath: exec.LookPath, Keep: DefaultKeep,
		Owner: fmt.Sprintf("%d@%s", os.Getpid(), host), Done: make(chan Record, 16),
		running: map[string]bool{}, probes: map[string]probe{}}, nil
}

func (r *Runner) now() int64 { return r.Now().UnixMilli() }

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
	if _, err := r.LookPath(r.expand(def.Command[0])); err != nil {
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
		if s.Running(now) || !manual && s.DueAt(def.Every()) > now {
			return nil
		}
		s.ID, s.Title = def.Name, cmp.Or(def.Description, def.Name)
		s.Last, s.Updated = now, now
		s.Next = now + Delay(def.Every(), s.Failures).Milliseconds()
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

func (r *Runner) execute(ctx context.Context, def Def, state State, manual bool) Record {
	defer r.release(def.Name)
	argv := r.argv(def.Command)
	rec := Record{ScheduleID: def.Name, Title: cmp.Or(def.Description, def.Name), Kind: "command", Command: argv,
		Started: state.Last, Manual: manual}
	rec.ID = r.newID(rec.Started)
	var out []byte
	var err error
	if _, lookErr := r.LookPath(argv[0]); lookErr != nil {
		err = fmt.Errorf("%s %w", argv[0], ErrMissing)
	} else {
		runCtx, cancel := context.WithTimeout(ctx, def.Limit())
		out, err = r.Exec(runCtx, argv, r.env(def))
		if err != nil && runCtx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("timed out after %s: %w", def.Limit(), context.DeadlineExceeded)
		}
		cancel()
	}
	rec.Finished = r.now()
	rec.Duration = rec.Finished - rec.Started
	rec.Output = tail(string(out), outputLimit)
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
		s.Next = s.Last + Delay(def.Every(), s.Failures).Milliseconds()
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
