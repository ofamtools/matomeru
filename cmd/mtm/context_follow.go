package main

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/work"
)

// Continuous extraction. mtm subscribes to the daemon's output events (a
// pane wrote something) in the background and marks the pane dirty; once
// the follow interval (settingContextFollowInterval, 1s by default, never
// under contextFollowFloor) after the first mark, context-item.follow reads
// each due dirty pane's buffer from the last offset it saw (the history
// rows already read, less a two-line overlap, plus the visible screen),
// extracts, and merges what is new, all off the loop. Nothing runs on a
// keypress. Items are owned by the pane's saved owner at extraction time,
// else unassociated. Keys a pane already yielded this session are not
// written again, so a screen that only redraws costs a capture and no
// write; a reprint after a restart updates the stored row's last seen.
//
// Cost per fire: one scrollback read per due pane (from its offset), the
// extraction over that text, and a keyed lookup of the items found that the
// pane has not yielded yet (contextitems.Store.Merge); nothing when no pane
// is due, and no list reload or watch refresh unless something was kept.
// Idle behaviour: no output means no timer; a pane that prints but yields
// nothing new is read less and less often: its own interval doubles with
// each empty read, up to contextIdleMax, and returns to the follow interval
// when a read keeps something. A pane waiting out its interval stays dirty
// and is read when due. Pause: nothing is read while follow is off or no
// daemon can be read for extraction. Off: context.follow false stops the
// subscription's wake-ups and every read (turning it on again resumes).
const (
	contextBatch   = time.Second // the default follow interval
	contextOverlap = 2           // history lines read again, so a line split by the offset is whole
	// contextFollowFloor is the least follow interval the setting takes.
	contextFollowFloor = 250 * time.Millisecond
	// contextIdleMax is the longest a pane that keeps yielding nothing new
	// waits between two reads.
	contextIdleMax = 30 * time.Second

	settingContextFollow         = "context.follow"
	settingContextFollowInterval = "context.follow-interval"
)

var contextFollowSetting = ochazuke.Setting{Name: settingContextFollow, Type: ochazuke.SettingBoolean, Default: true,
	Description: "Read panes as they print and keep the paths, links, branches and commits they show on the Context page; off stops it (the extract verb still reads a pane on request)"}

var contextFollowIntervalSetting = ochazuke.Setting{Name: settingContextFollowInterval, Type: ochazuke.SettingDuration, Default: contextBatch,
	Description: "How long after a pane prints before it is read for context (never under 250ms); a pane that yields nothing new is read at doubling intervals up to 30s"}

// followOn is the context.follow setting.
func (a *App) followOn() bool { return a.r.Settings().Bool(settingContextFollow) }

// followInterval is the follow interval setting, at least the floor; an
// unset or invalid value reads contextBatch.
func (a *App) followInterval() time.Duration {
	d := a.r.Settings().Duration(settingContextFollowInterval)
	if d <= 0 {
		d = contextBatch
	}
	return max(d, contextFollowFloor)
}

// observeContextFollow starts reading again when the setting is turned on.
func (a *App) observeContextFollow() {
	a.r.Settings().Observe(settingContextFollow, func(_ string, value any) {
		if on, _ := value.(bool); on {
			a.cx.cmds = append(a.cx.cmds, a.followContext())
		}
	})
}

// contextOutput is a wake-up: some pane wrote output.
type contextOutput struct{}

// contextScanDue is the batch timer: read the dirty panes now.
type contextScanDue struct{}

// contextFollower is continuous extraction's state. dirty is written by
// the subscription goroutine; everything else belongs to the loop.
type contextFollower struct {
	mu     sync.Mutex
	dirty  map[string]bool // windows with output since the last read
	signal chan struct{}   // a non-blocking wake-up per burst; nil without a subscription

	offset  map[string]int             // window → history rows already read
	seen    map[string]map[string]bool // window → kind\x00text keys already written
	armed   bool                       // the batch timer is set
	running bool                       // a follow job is in flight
	waiting bool                       // a wake-up command is out
	step    map[string]time.Duration   // window → its interval now (backoff); absent: the follow interval
	next    map[string]time.Time       // window → when it is read next
	now     func() time.Time
	// after schedules msg after d (tea.Tick); tests replace it.
	after func(d time.Duration, msg tea.Msg) tea.Cmd
	// pace is the least time between two wake-ups: output only marks
	// dirty panes (mark), and the loop hears of it at most this often, so
	// busy panes do not make the loop update and redraw per burst.
	pace time.Duration
}

func newContextFollower() *contextFollower {
	return &contextFollower{pace: contextBatch, dirty: map[string]bool{}, offset: map[string]int{}, seen: map[string]map[string]bool{}, step: map[string]time.Duration{}, next: map[string]time.Time{}, now: time.Now, after: func(d time.Duration, msg tea.Msg) tea.Cmd {
		return tea.Tick(d, func(time.Time) tea.Msg { return msg })
	}}
}

// mark records output from window and wakes the loop.
func (f *contextFollower) mark(window string) {
	if window == "" {
		return
	}
	f.mu.Lock()
	f.dirty[window] = true
	f.mu.Unlock()
	if f.signal != nil {
		select {
		case f.signal <- struct{}{}:
		default:
		}
	}
}

// take empties the dirty set and returns it, sorted.
func (f *contextFollower) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.dirty))
	for w := range f.dirty {
		out = append(out, w)
	}
	clear(f.dirty)
	slices.Sort(out)
	return out
}

// takeDue removes from the dirty set and returns, sorted, the panes whose
// own interval has passed; the others stay dirty.
func (f *contextFollower) takeDue() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	var due []string
	for w := range f.dirty {
		if f.next[w].After(now) {
			continue
		}
		due = append(due, w)
		delete(f.dirty, w)
	}
	slices.Sort(due)
	return due
}

// scanned records a read of window: a read that kept something returns it
// to the follow interval, an empty one doubles its interval up to
// contextIdleMax.
func (f *contextFollower) scanned(window string, kept bool, base time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	step := base
	if !kept {
		if prev, ok := f.step[window]; ok {
			step = prev
		}
		step = min(max(step*2, base), max(contextIdleMax, base))
	}
	f.step[window] = step
	f.next[window] = f.now().Add(step)
}

func (f *contextFollower) pending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dirty) > 0
}

// forget lets a forgotten item come back when it is printed again.
func (f *contextFollower) forget(it contextitems.Item) {
	for _, keys := range f.seen {
		delete(keys, it.Kind+"\x00"+it.Text)
	}
}

// waitOutput waits for the next wake-up; nil without a subscription. It
// first rests for pause, so wake-ups come at most that often; output in
// the meantime keeps its dirty mark and its one pending signal.
func (f *contextFollower) waitOutput(pause time.Duration) tea.Cmd {
	ch := f.signal
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		if pause > 0 {
			time.Sleep(pause)
		}
		<-ch
		return contextOutput{}
	}
}

// arm sets the batch timer unless it is set.
func (f *contextFollower) arm(d time.Duration) tea.Cmd {
	if f.armed {
		return nil
	}
	f.armed = true
	return f.after(d, contextScanDue{})
}

// followContext starts the output subscription (Init); without a daemon
// that can be read for extraction it does nothing. A dropped stream is
// resumed after a pause, quietly.
func (a *App) followContext() tea.Cmd {
	f := a.cx.follow
	if !a.followOn() {
		return nil
	}
	if f.signal != nil { // subscribed before: only the wake-up lapsed
		if f.waiting {
			return nil
		}
		f.waiting = true
		return f.waitOutput(0)
	}
	if _, ok := a.contextd(); !ok || a.daemon == nil {
		return nil
	}
	f.signal = make(chan struct{}, 1)
	f.waiting = true
	client := a.daemon
	go func() {
		for {
			events, failures := client.Subscribe(context.Background(), []string{"output"}, 0, "")
			for ev := range events {
				f.mark(ev.Window)
			}
			<-failures
			time.Sleep(5 * time.Second)
		}
	}()
	return f.waitOutput(0)
}

// followUpdate is continuous extraction's part of every update: a wake-up
// sets the batch timer, the timer starts a follow job (or waits for the
// one in flight).
func (a *App) followUpdate(msg tea.Msg) tea.Cmd {
	f := a.cx.follow
	switch msg.(type) {
	case contextOutput:
		if !a.followOn() {
			f.waiting = false
			f.take()
			return nil
		}
		f.pace = a.followInterval()
		return tea.Batch(f.waitOutput(f.pace), f.arm(f.pace))
	case contextScanDue:
		f.armed = false
		if !a.followOn() {
			f.take()
			return nil
		}
		base := a.followInterval()
		if f.running {
			return f.arm(base)
		}
		if !f.pending() {
			return nil
		}
		if due, wait := f.peekDue(); !due {
			return f.arm(max(wait, base))
		}
		if _, err := a.r.Execute(ochazuke.Request{Verb: "follow", Noun: contextNoun, Target: contextListID}); err != nil {
			a.r.UI().Error(err.Error())
			return nil
		}
		return a.r.PendingCommands()
	}
	return nil
}

// peekDue says whether a dirty pane is due, and when the first will be.
func (f *contextFollower) peekDue() (due bool, wait time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for w := range f.dirty {
		at := f.next[w]
		if !at.After(now) {
			return true, 0
		}
		if d := at.Sub(now); wait == 0 || d < wait {
			wait = d
		}
	}
	return false, wait
}

// followRead is one pane's part of a follow job.
type followRead struct {
	p      pane
	owner  contextitems.Owner
	offset int
	seen   map[string]bool
}

// followResult is what a follow job read from one pane.
type followResult struct {
	window  string
	history int
	keys    []string
}

// followPanes is context-item.follow: read the dirty panes from their
// offsets, extract and merge what they have not yielded yet, off the loop.
// Bookkeeping, so it is Quiet (no jobs ticker row).
func (a *App) followPanes(ctx ochazuke.Ctx) ochazuke.Result {
	f := a.cx.follow
	d, ok := a.contextd()
	if !ok || !extractsFrom(a.contextExtraction(), contextitems.Source{Kind: "scrollback"}) {
		f.take()
		ctx.NothingChanged()
		return ochazuke.Done(nil)
	}
	var reads []followRead
	for _, w := range f.takeDue() {
		i := slices.IndexFunc(a.panes, func(p pane) bool { return p.ID == w && !p.Exited })
		if i < 0 {
			continue // not listed yet; its next output marks it again
		}
		p := a.panes[i]
		reads = append(reads, followRead{p: p, owner: a.contextOwner(p), offset: f.offset[w], seen: maps.Clone(f.seen[w])})
	}
	if len(reads) == 0 {
		ctx.NothingChanged() // no pane to read: nothing for the watches to refresh
		return ochazuke.Done(nil)
	}
	f.running = true
	store := a.contexts
	return ochazuke.Pending(func(c context.Context) (any, error) {
		var out []followResult
		var errs []error
		for _, r := range reads {
			from := max(0, r.offset-contextOverlap)
			back, err := d.Scrollback(c, r.p, from)
			if err == nil && back.History < r.offset {
				// The buffer was cleared or trimmed: read it whole.
				from = 0
				back, err = d.Scrollback(c, r.p, 0)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			res := followResult{window: r.p.ID, history: back.History}
			var fresh []contextitems.Item
			for _, it := range a.extractItems(back.Text, r.p.Width, contextSource(r.p, "scrollback")) {
				k := it.Kind + "\x00" + it.Text
				if !r.seen[k] {
					fresh = append(fresh, it)
					res.keys = append(res.keys, k)
				}
			}
			if _, _, err := store.Merge(fresh, r.owner, work.Now()); err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, res)
		}
		if len(errs) > 0 && len(out) == 0 {
			return out, errs[0]
		}
		return out, nil
	}).Then(func(ctx ochazuke.Ctx, v any, _ error) {
		f.running = false
		results, _ := v.([]followResult)
		changed := false
		base := a.followInterval()
		for _, res := range results {
			f.scanned(res.window, len(res.keys) > 0, base)
			f.offset[res.window] = res.history
			if f.seen[res.window] == nil {
				f.seen[res.window] = map[string]bool{}
			}
			for _, k := range res.keys {
				f.seen[res.window][k] = true
			}
			changed = changed || len(res.keys) > 0
		}
		if changed {
			if err := a.reloadContext(); err != nil {
				ctx.Error(err.Error())
			}
			a.contextChanged()
		}
		if f.pending() {
			a.cx.cmds = append(a.cx.cmds, f.arm(base))
		}
	})
}
