package main

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// Continuous extraction. mtm subscribes to the daemon's output events (a
// pane wrote something) in the background and marks the pane dirty; about
// once a second (contextBatch) after the first mark, context-item.follow
// reads each dirty pane's buffer from the last offset it saw (the history
// rows already read, less a two-line overlap, plus the visible screen),
// extracts, and merges what is new, all off the loop. Nothing runs on a
// keypress. Items are owned by the pane's saved owner at extraction time,
// else unassociated. Keys a pane already yielded this session are not
// written again, so a screen that only redraws costs a capture and no
// write; a reprint after a restart updates the stored row's last seen.

const (
	contextBatch   = time.Second
	contextOverlap = 2 // history lines read again, so a line split by the offset is whole
)

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
	// after schedules msg after d (tea.Tick); tests replace it.
	after func(d time.Duration, msg tea.Msg) tea.Cmd
}

func newContextFollower() *contextFollower {
	return &contextFollower{dirty: map[string]bool{}, offset: map[string]int{}, seen: map[string]map[string]bool{}, after: func(d time.Duration, msg tea.Msg) tea.Cmd {
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

// waitOutput waits for the next wake-up; nil without a subscription.
func (f *contextFollower) waitOutput() tea.Cmd {
	ch := f.signal
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		<-ch
		return contextOutput{}
	}
}

// arm sets the batch timer unless it is set.
func (f *contextFollower) arm() tea.Cmd {
	if f.armed {
		return nil
	}
	f.armed = true
	return f.after(contextBatch, contextScanDue{})
}

// followContext starts the output subscription (Init); without a daemon
// that can be read for extraction it does nothing. A dropped stream is
// resumed after a pause, quietly.
func (a *App) followContext() tea.Cmd {
	f := a.cx.follow
	if _, ok := a.contextd(); !ok || a.daemon == nil || f.signal != nil {
		return nil
	}
	f.signal = make(chan struct{}, 1)
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
	return f.waitOutput()
}

// followUpdate is continuous extraction's part of every update: a wake-up
// sets the batch timer, the timer starts a follow job (or waits for the
// one in flight).
func (a *App) followUpdate(msg tea.Msg) tea.Cmd {
	f := a.cx.follow
	switch msg.(type) {
	case contextOutput:
		return tea.Batch(f.waitOutput(), f.arm())
	case contextScanDue:
		f.armed = false
		if f.running {
			return f.arm()
		}
		if !f.pending() {
			return nil
		}
		if _, err := a.r.Execute(ochazuke.Request{Verb: "follow", Noun: contextNoun, Target: contextListID}); err != nil {
			a.m.Status = err.Error()
			return nil
		}
		return a.r.PendingCommands()
	}
	return nil
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
func (a *App) followPanes(ochazuke.Ctx) ochazuke.Result {
	f := a.cx.follow
	d, ok := a.contextd()
	if !ok {
		f.take()
		return ochazuke.Done(nil)
	}
	var reads []followRead
	for _, w := range f.take() {
		i := slices.IndexFunc(a.panes, func(p pane) bool { return p.ID == w && !p.Exited })
		if i < 0 {
			continue // not listed yet; its next output marks it again
		}
		p := a.panes[i]
		reads = append(reads, followRead{p: p, owner: a.contextOwner(p), offset: f.offset[w], seen: maps.Clone(f.seen[w])})
	}
	if len(reads) == 0 {
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
			for _, it := range contextitems.Extract(back.Text, r.p.Width, contextSource(r.p, "scrollback")) {
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
	}).Then(func(_ ochazuke.Ctx, v any, _ error) {
		f.running = false
		results, _ := v.([]followResult)
		changed := false
		for _, res := range results {
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
				a.m.Status = err.Error()
			}
			a.contextChanged()
		}
		if f.pending() {
			a.cx.cmds = append(a.cx.cmds, f.arm())
		}
	})
}

// contextChanged tells views beyond the page that items landed; the live
// picker refreshes its rows (context_picker.go).
func (a *App) contextChanged() {}
