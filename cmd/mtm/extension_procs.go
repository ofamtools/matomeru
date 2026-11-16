package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"ofam.dev/ochazuke"
)

// The processes mtm starts for its extensions. Every call of a tool passes
// through one gate per App (extProcs): a mounted verb (by exec or on a kept
// process), its preflight, a page's loads, a parameter's choices, a store's
// calls (the Nouns page's counts among them), catalogs and setup. At most
// extensions.max-processes calls run at once; the rest wait in a queue, a
// change (a call whose verb is not a read) ahead of the reads, each kind in
// arrival order. A call never fails because of the limit, and the time it
// waits does not count against its own deadline.
//
// The gate also ends what mtm started: closing it (App.Close, or SIGHUP and
// SIGTERM) cancels every call in flight, which stops its process (and the
// process group a mounted verb runs in), stops every watch process, and
// waits a moment for them to end. A cancelled job's call stops the same
// way: its context is the job's.

const settingExtMaxProcesses = "extensions.max-processes"

// extMaxProcessesDefault is how many extension calls run at once by default.
const extMaxProcessesDefault = 4

var extProcSettings = []ochazuke.Setting{
	{Name: settingExtMaxProcesses, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection, Type: ochazuke.SettingInteger, Default: extMaxProcessesDefault,
		Description: "The most extension calls (each a process, or a kept process busy with it) mtm runs at once; more wait their turn in arrival order, a change before reads, and never fail for it; 0 (or less): no limit"},
}

// extProcs is the gate. A nil gate runs every call at once and tracks
// nothing (a test's bare call).
type extProcs struct {
	mu      sync.Mutex
	limit   int // 0: none
	running int // calls holding a slot, limited or not
	peak    int // the most running at once (measurement and tests)
	queue   []*extWaiter
	idle    chan struct{} // closed when running drops to 0 while closing
	watches map[*extWatchStop]struct{}

	life context.Context
	end  context.CancelCauseFunc
}

type extWaiter struct {
	ready chan struct{}
	write bool
}

// extWatchStop is one running watch's stop.
type extWatchStop struct{ stop func() }

func newExtProcs(limit int) *extProcs {
	life, end := context.WithCancelCause(context.Background())
	return &extProcs{limit: limit, life: life, end: end, watches: map[*extWatchStop]struct{}{}}
}

// setLimit changes the limit; waiting calls a raised limit allows start.
func (p *extProcs) setLimit(n int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.limit = max(n, 0)
	p.grant()
}

// grant starts queued calls while the limit allows (p.mu held).
func (p *extProcs) grant() {
	for len(p.queue) > 0 && (p.limit == 0 || p.running < p.limit) {
		w := p.queue[0]
		p.queue = p.queue[1:]
		p.take()
		close(w.ready)
	}
}

// take counts one more call running (p.mu held).
func (p *extProcs) take() {
	p.running++
	p.peak = max(p.peak, p.running)
}

// acquire waits for a slot; the answer gives it back. It fails only when
// ctx ends first.
func (p *extProcs) acquire(ctx context.Context, write bool) (func(), error) {
	p.mu.Lock()
	if len(p.queue) == 0 && (p.limit == 0 || p.running < p.limit) {
		p.take()
		p.mu.Unlock()
		return p.releaser(), nil
	}
	w := &extWaiter{ready: make(chan struct{}), write: write}
	at := len(p.queue)
	if write {
		// A change goes after the changes already waiting, before the reads.
		at = slices.IndexFunc(p.queue, func(q *extWaiter) bool { return !q.write })
		if at < 0 {
			at = len(p.queue)
		}
	}
	p.queue = slices.Insert(p.queue, at, w)
	p.mu.Unlock()
	select {
	case <-w.ready:
		return p.releaser(), nil
	case <-ctx.Done():
	}
	p.mu.Lock()
	if i := slices.Index(p.queue, w); i >= 0 {
		p.queue = slices.Delete(p.queue, i, i+1)
		p.mu.Unlock()
		return nil, context.Cause(ctx)
	}
	p.mu.Unlock()
	p.releaser()() // granted while it was cancelled
	return nil, context.Cause(ctx)
}

func (p *extProcs) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.running--
			p.grant()
			if p.running == 0 && p.idle != nil {
				close(p.idle)
				p.idle = nil
			}
		})
	}
}

// call runs fn with a slot. fn's context ends when ctx is cancelled, when
// the gate closes, or when ctx's deadline (as it stood when the call came)
// has passed since the slot was given: waiting never uses up a call's time.
func (p *extProcs) call(ctx context.Context, write bool, fn func(context.Context) error) error {
	if p == nil {
		return fn(ctx)
	}
	var budget time.Duration
	if dl, ok := ctx.Deadline(); ok {
		if budget = time.Until(dl); budget <= 0 {
			return fn(ctx) // already past its deadline: it fails as it did
		}
	}
	run, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancel(nil)
	// The caller's cancellation (a closed page, a cancelled job) and the
	// gate's closing end the call; the caller's deadline does not while
	// it waits (the budget below takes its place).
	stopCaller := context.AfterFunc(ctx, func() {
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			cancel(context.Cause(ctx))
		}
	})
	defer stopCaller()
	stopLife := context.AfterFunc(p.life, func() { cancel(context.Cause(p.life)) })
	defer stopLife()
	release, err := p.acquire(run, write)
	if err != nil {
		return err
	}
	defer release()
	if budget > 0 {
		var stop context.CancelFunc
		run, stop = context.WithTimeout(run, budget)
		defer stop()
	}
	return fn(run)
}

// exec runs one call of bin by exec (execExt) through the gate.
func (p *extProcs) exec(ctx context.Context, write bool, bin string, env []string, args ...string) ([]byte, error) {
	var out []byte
	err := p.call(ctx, write, func(c context.Context) error {
		var err error
		out, err = execExt(c, bin, env, args...)
		return err
	})
	return out, err
}

// result runs a mounted verb's captured result through the gate: before
// (a preflight, nil for none) runs first, outside the slot (its own calls
// take theirs, so one call never holds two), then the result's work
// (RunMounted's, whose own timeout starts once it runs) is awaited inside
// the slot. A foreground result takes the terminal and is not passed here.
func (p *extProcs) result(res ochazuke.Result, write bool, before func(context.Context) error) ochazuke.Result {
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		if before != nil {
			if err := before(ctx); err != nil {
				return nil, err
			}
		}
		var v any
		err := p.call(ctx, write, func(c context.Context) error {
			var err error
			v, err = res.Await(c)
			return err
		})
		return v, err
	})
}

// watch starts a watch through start and keeps its stop until it is
// called or the gate closes. A watch holds no slot: it waits for events,
// it does not work, and a slot held for its life would starve every call.
func (p *extProcs) watch(start func(context.Context) (func(), error)) (func(), error) {
	if p == nil {
		return start(context.Background())
	}
	stop, err := start(p.life)
	if err != nil {
		return nil, err
	}
	w := &extWatchStop{stop: stop}
	p.mu.Lock()
	p.watches[w] = struct{}{}
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.watches, w)
		p.mu.Unlock()
		stop()
	}, nil
}

// stats are the calls running now and the most that ran at once.
func (p *extProcs) stats() (running, peak int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running, p.peak
}

// close cancels every call in flight and every watch, then waits up to
// wait for the calls to end (each stops its process: exec's kill, a
// mounted verb's process group).
func (p *extProcs) close(wait time.Duration) {
	if p == nil {
		return
	}
	p.end(nil) // the calls see context.Canceled, as a cancelled job's do
	p.mu.Lock()
	stops := make([]func(), 0, len(p.watches))
	for w := range p.watches {
		stops = append(stops, w.stop)
	}
	clear(p.watches)
	var idle chan struct{}
	if p.running > 0 {
		if p.idle == nil {
			p.idle = make(chan struct{})
		}
		idle = p.idle
	}
	p.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	if idle == nil {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-idle:
	case <-timer.C:
	}
}

// extGroup runs cmd in a process group of its own and makes its
// cancellation end the group: SIGTERM, then SIGKILL a second later (the
// pattern of ochazuke's captured mounted verbs, cli/host_proxy_unix.go).
// A tool's own children end with it. The answer, called once the command
// has ended, kills what is left of the group.
func extGroup(cmd *exec.Cmd) func() {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var kill *time.Timer
	end := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		kill = time.AfterFunc(time.Second, end)
		return err
	}
	return func() {
		if kill != nil {
			kill.Stop()
		}
		end()
	}
}

// extCloseWait is how long closing waits for the calls it cancelled.
const extCloseWait = 2 * extWaitDelay

// observeExtProcs applies extensions.max-processes now and on each change.
func (a *App) observeExtProcs() {
	s := a.r.Settings()
	a.procs.setLimit(s.Int(settingExtMaxProcesses))
	s.Observe(settingExtMaxProcesses, func(_ string, v any) {
		if n, ok := v.(int); ok {
			a.procs.setLimit(n)
		}
	})
}

// closeExtProcs stops what the extensions' calls started (App.Close).
func (a *App) closeExtProcs() error {
	a.procs.close(extCloseWait)
	return nil
}

// stopExtOnSignals stops the extension processes when one of sigs comes,
// then lets the signal do what it does by default (re-raised). A process
// a mounted verb started runs in a process group of its own, so the
// terminal's signals do not reach it: without this it outlives mtm. The
// answer stops listening.
func (a *App) stopExtOnSignals(sigs ...os.Signal) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			a.procs.close(extCloseWait)
			signal.Reset(sig)
			if s, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(os.Getpid(), s)
			}
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
		})
	}
}
