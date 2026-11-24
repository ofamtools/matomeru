// Package runs is Matomeru's one way to execute an OS command. Every command
// becomes a run record in the store: created as running when it starts, its
// combined output streamed into the record's output field as it arrives
// (debounced, and capped to a tail), and finished with its exit code,
// duration and final state (done, failed or cancelled).
package runs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Run states written to a run's runStatus.
const (
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// RelRerunOf relates a run to the run it repeats.
const RelRerunOf = "rerunOf"

// Defaults for Options left zero.
const (
	DefaultMaxOutputBytes = 64 << 10
	DefaultFlushInterval  = 100 * time.Millisecond
	DefaultWaitDelay      = 2 * time.Second
)

// ErrClosed reports a Start on a closed Runner.
var ErrClosed = errors.New("runs: runner is closed")

// Options tunes a Runner. Zero values take the defaults.
type Options struct {
	// MaxOutputBytes caps the output kept per run; past it only the tail is
	// kept and the record is marked outputTruncated.
	MaxOutputBytes int
	// FlushInterval is how often new output is written to the record.
	FlushInterval time.Duration
	// WaitDelay bounds how long a killed command's pipes are drained.
	WaitDelay time.Duration
	// OnChange, when set, is called after every write to a run record.
	OnChange func(runID string)
}

// StartSpec describes a command to run. Args, when set, runs argv directly
// (Args[0] is the program); otherwise Command runs under /bin/sh -c.
type StartSpec struct {
	Title   string
	Command string
	Args    []string
	Dir     string
	Env     []string
	TaskRef core.Ref
	Actor   core.Actor
}

// Runner starts commands and keeps their run records current.
type Runner struct {
	store *data.Store
	opts  Options

	mu     sync.Mutex
	active map[string]*activeRun
	closed bool
	wg     sync.WaitGroup
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a Runner writing run records to store.
func New(store *data.Store, opts Options) *Runner {
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = DefaultFlushInterval
	}
	if opts.WaitDelay <= 0 {
		opts.WaitDelay = DefaultWaitDelay
	}
	return &Runner{store: store, opts: opts, active: map[string]*activeRun{}}
}

// Store returns the store the runner writes to.
func (r *Runner) Store() *data.Store { return r.store }

// Start creates a running run record, spawns the command and returns the
// record; the command finishes in the background. When the command cannot
// be spawned the record is still written, as failed with the error in its
// output, and returned with the error. Cancelling ctx cancels the run.
func (r *Runner) Start(ctx context.Context, spec StartSpec) (*data.Record, error) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	if len(spec.Args) == 0 && strings.TrimSpace(spec.Command) == "" {
		return nil, errors.New("runs: nothing to run: no command or args")
	}
	command := spec.Command
	if len(spec.Args) > 0 {
		command = joinArgv(spec.Args)
	}
	title := spec.Title
	if title == "" {
		title = command
	}
	started := time.Now()
	fields := map[string]any{
		"title":     title,
		"command":   command,
		"runStatus": StatusRunning,
		"output":    "",
		"startedAt": started.UTC().Format(data.TimeFormat),
	}
	if len(spec.Args) > 0 {
		fields["args"] = spec.Args
	}
	if spec.Dir != "" {
		fields["cwd"] = spec.Dir
	}
	if spec.TaskRef.ID != "" {
		fields["task"] = spec.TaskRef
	}
	rec, _, err := r.store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, spec.Actor)
	if err != nil {
		return nil, fmt.Errorf("runs: create run: %w", err)
	}
	r.changed(rec.ID)

	runCtx, cancel := context.WithCancel(ctx)
	var cmd *exec.Cmd
	if len(spec.Args) > 0 {
		cmd = exec.CommandContext(runCtx, spec.Args[0], spec.Args[1:]...)
	} else {
		cmd = exec.CommandContext(runCtx, "/bin/sh", "-c", spec.Command)
	}
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	out := &tailBuffer{max: r.opts.MaxOutputBytes}
	cmd.Stdout, cmd.Stderr = out, out
	killGroup(cmd)
	cmd.WaitDelay = r.opts.WaitDelay

	ar := &activeRun{cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		cancel()
		return r.finish(rec.ID, spec.Actor, started, out, nil, StatusCancelled, -1, ErrClosed), ErrClosed
	}
	r.active[rec.ID] = ar
	r.wg.Add(1)
	r.mu.Unlock()

	if err := cmd.Start(); err != nil {
		cancel()
		final := r.finish(rec.ID, spec.Actor, started, out, nil, StatusFailed, -1, err)
		r.release(rec.ID, ar)
		return final, fmt.Errorf("runs: start %s: %w", command, err)
	}
	go r.watch(runCtx, cmd, rec.ID, spec.Actor, started, out, ar)
	return rec, nil
}

// watch streams the output into the record until the command exits, then
// writes the final state.
func (r *Runner) watch(ctx context.Context, cmd *exec.Cmd, id string, actor core.Actor, started time.Time, out *tailBuffer, ar *activeRun) {
	defer r.release(id, ar)
	defer ar.cancel()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	tick := time.NewTicker(r.opts.FlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if text, truncated, dirty := out.take(); dirty {
				r.patch(id, actor, map[string]any{"output": text, "outputTruncated": truncatedValue(truncated)})
			}
		case err := <-waited:
			status, code := StatusDone, -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			var exitErr *exec.ExitError
			switch {
			case err == nil:
			case ctx.Err() != nil:
				status, err = StatusCancelled, nil
			case errors.As(err, &exitErr):
				status, err = StatusFailed, nil
			default:
				status = StatusFailed
			}
			r.finish(id, actor, started, out, cmd, status, code, err)
			return
		}
	}
}

// finish writes a run's final state. code is the exit status, or negative
// when there is none (never started, or killed by a signal); err, when set,
// is appended to the output.
func (r *Runner) finish(id string, actor core.Actor, started time.Time, out *tailBuffer, cmd *exec.Cmd, status string, code int, err error) *data.Record {
	if err != nil {
		out.Write([]byte(err.Error() + "\n"))
	}
	text, truncated, _ := out.take()
	now := time.Now()
	fields := map[string]any{
		"runStatus":       status,
		"output":          text,
		"outputTruncated": truncatedValue(truncated),
		"durationMs":      now.Sub(started).Milliseconds(),
		"finishedAt":      now.UTC().Format(data.TimeFormat),
	}
	if code >= 0 {
		fields["exitCode"] = code
	}
	return r.patch(id, actor, fields)
}

func truncatedValue(truncated bool) any {
	if truncated {
		return true
	}
	return nil
}

func (r *Runner) patch(id string, actor core.Actor, fields map[string]any) *data.Record {
	rec, _, err := r.store.Patch(data.PatchInput{Ref: core.Ref{Kind: data.KindRun, ID: id}, Fields: fields}, actor)
	if err != nil {
		return nil
	}
	r.changed(id)
	return rec
}

func (r *Runner) changed(id string) {
	if r.opts.OnChange != nil {
		r.opts.OnChange(id)
	}
}

func (r *Runner) release(id string, ar *activeRun) {
	r.mu.Lock()
	if r.active[id] == ar {
		delete(r.active, id)
	}
	r.mu.Unlock()
	close(ar.done)
	r.wg.Done()
}

// Cancel stops an active run, killing its whole process group. It reports
// whether the run was active.
func (r *Runner) Cancel(runID string) bool {
	r.mu.Lock()
	ar, ok := r.active[runID]
	r.mu.Unlock()
	if ok {
		ar.cancel()
	}
	return ok
}

// Active lists the IDs of the runs still going.
func (r *Runner) Active() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	return ids
}

var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// Done returns a channel closed once the run's final state is written. For
// a run that is not active (finished, or unknown) it is already closed.
func (r *Runner) Done(runID string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ar, ok := r.active[runID]; ok {
		return ar.done
	}
	return closedChan
}

// Wait blocks until every active run has finished.
func (r *Runner) Wait() { r.wg.Wait() }

// Close refuses new runs, cancels the active ones and waits for them.
func (r *Runner) Close() {
	r.mu.Lock()
	r.closed = true
	for _, ar := range r.active {
		ar.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// Rerun starts a new run from an old one's title, command (or args),
// directory and task, related to the old run by RelRerunOf.
func (r *Runner) Rerun(ctx context.Context, runID string, actor core.Actor) (*data.Record, error) {
	old, err := r.store.Get(core.Ref{Kind: data.KindRun, ID: runID})
	if err != nil {
		return nil, fmt.Errorf("runs: rerun %s: %w", runID, err)
	}
	spec := StartSpec{Title: old.Text("title"), Command: old.Text("command"), Dir: old.Text("cwd"), Actor: actor}
	if v, ok := old.Value("args"); ok {
		if list, ok := v.([]any); ok {
			for _, a := range list {
				spec.Args = append(spec.Args, data.Text(a))
			}
		}
	}
	if ref, ok := old.RefField("task"); ok {
		spec.TaskRef = ref
	}
	rec, err := r.Start(ctx, spec)
	if rec != nil {
		if _, _, rerr := r.store.Relate(data.RelateInput{From: rec.Ref(), Kind: RelRerunOf, To: old.Ref()}, actor); rerr != nil && err == nil {
			err = rerr
		}
	}
	return rec, err
}

// killGroup runs the command in its own process group and makes
// cancellation kill the whole group, so a shell's children die with it.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return cmd.Process.Kill()
		}
		return nil
	}
}

// joinArgv renders argv as a shell-like line for display.
func joinArgv(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`;&|<>()*?[]{}~#!") {
			parts[i] = a
			continue
		}
		parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

// tailBuffer keeps the last max bytes written to it and how many were
// dropped. It is safe for concurrent use.
type tailBuffer struct {
	mu      sync.Mutex
	max     int
	buf     []byte
	dropped int64
	dirty   bool
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.dropped += int64(over)
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	if n > 0 {
		b.dirty = true
	}
	return n, nil
}

// take returns the kept text (prefixed with a truncation marker when bytes
// were dropped), whether any were, and whether anything changed since the
// last take.
func (b *tailBuffer) take() (string, bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	dirty := b.dirty
	b.dirty = false
	if b.dropped == 0 {
		return string(b.buf), false, dirty
	}
	tail := b.buf
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return fmt.Sprintf("[... %d bytes truncated ...]\n%s", b.dropped+int64(len(b.buf)-len(tail)), tail), true, dirty
}
