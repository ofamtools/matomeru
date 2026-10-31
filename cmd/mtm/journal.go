package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/history"
	"ofam.dev/matomeru/internal/work"
)

// The history journal (internal/history): what the agents in the panes
// did, journaled as it happens, and the turns projected from it.
//
// While the TUI runs, a background reader subscribes to the daemon's
// agent-activity events and appends each one to the journal as it
// arrives, never on the loop. The daemon does not replay activity on a
// resume and keeps only the newest 256 entries of a pane, in memory, so the
// reader also reads each agent pane's ring once per daemon boot (from the
// last sequence the journal holds): turns finished while mtm was closed
// are journaled when it opens, and a daemon restart loses nothing mtm saw.
// The full prompt and answer come from mtm's capture hooks (mtm setup),
// which write the journal through the CLI (history capture-turn) with or
// without the TUI. Each burst that wrote rows becomes one historyChanged
// message, and the views read the journal again.

// journalSource is the part of the daemon the reader needs; tests replace it.
type journalSource interface {
	Subscribe(ctx context.Context, types []string, afterSeq uint64, bootID string) (<-chan daemon.Event, <-chan error)
	Activity(ctx context.Context, p daemon.ActivityParams) (daemon.ActivityResult, error)
}

// historyChanged is a burst of journal rows the reader wrote.
type historyChanged struct{}
type historyDue struct{}

// journalFeed is the reader's state: the panes it knows (the loop hands
// them over after each listing) and the panes whose ring it still has to
// read under the current boot.
type journalFeed struct {
	mu      sync.Mutex
	panes   map[string]history.Pane // window → the pane, from the last listing
	agents  map[string]bool         // windows with an agent: their ring is read
	read    map[string]bool         // windows whose ring was read under boot
	boot    string
	wake    chan struct{}
	changed chan struct{}
	started bool
	pending bool // owned by the UI loop, not the background reader
	// contextAdded says a burst may have added items or source provenance: the loop reloads the
	// Context page and the picker with the journal views.
	contextAdded atomic.Bool
}

func newJournalFeed() *journalFeed {
	return &journalFeed{panes: map[string]history.Pane{}, agents: map[string]bool{}, read: map[string]bool{},
		wake: make(chan struct{}, 1), changed: make(chan struct{}, 1)}
}

// setPanes is the loop handing over a listing: the reader names entries
// after these panes and reads the ring of every agent pane it has not read
// under this boot.
func (f *journalFeed) setPanes(panes []pane) {
	f.mu.Lock()
	for _, p := range panes {
		if p.ID == "" {
			continue
		}
		f.panes[p.ID] = history.Pane{Session: p.Session, Window: p.ID, Name: p.label(), Harness: p.Harness, Cwd: p.Cwd}
		if p.isAgent() {
			f.agents[p.ID] = true
		}
	}
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// pane is what the feed knows of a window, else its ids alone.
func (f *journalFeed) pane(session, window string) history.Pane {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.panes[window]; ok {
		return p
	}
	return history.Pane{Session: session, Window: window}
}

// signal says the journal changed; one pending signal covers a burst.
func (f *journalFeed) signal() {
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

// journalEvents starts the reader once (the TUI's Init; the CLI never
// does) and waits for its first burst.
func (a *App) journalEvents() tea.Cmd {
	f := a.feed
	if f == nil || a.journalSrc == nil {
		return nil
	}
	f.mu.Lock()
	start := !f.started
	f.started = true
	f.mu.Unlock()
	if start {
		go a.readJournal(context.Background())
	}
	return a.waitJournal()
}

// waitJournal waits for the next burst of journal rows.
func (a *App) waitJournal() tea.Cmd {
	ch := a.feed.changed
	return func() tea.Msg {
		<-ch
		return historyChanged{}
	}
}

// readJournal subscribes and resubscribes (quietly, every few seconds,
// while the daemon is away) for the life of the process.
func (a *App) readJournal(ctx context.Context) {
	for ctx.Err() == nil {
		a.followActivity(ctx)
		select {
		case <-ctx.Done():
		case <-time.After(journalRetry):
		}
	}
}

// journalRetry is the pause before the reader subscribes again.
var journalRetry = 5 * time.Second

// followActivity is one subscription: the ack names the daemon's boot (a
// new boot reads every agent pane's ring again), then each event and each
// new listing journals what it brings until the stream fails.
func (a *App) followActivity(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	f := a.feed
	events, failures := a.journalSrc.Subscribe(ctx, []string{"agent-activity"}, 0, "")
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				<-failures
				return
			}
			switch ev.Type {
			case "subscribed":
				f.mu.Lock()
				if ev.BootID != f.boot {
					f.boot, f.read = ev.BootID, map[string]bool{}
				}
				f.mu.Unlock()
				a.readRings(ctx)
			case "agent-activity":
				f.mu.Lock()
				boot := f.boot
				f.mu.Unlock()
				entry, _ := ev.Data["entry"].(map[string]any)
				if e, ok := history.FromActivity(boot, f.pane(ev.Session, ev.Window), activityOf(entry)); ok {
					a.journal(e)
				}
			}
		case <-f.wake:
			a.readRings(ctx)
		}
	}
}

// readRings reads the ring of every agent pane not read under this boot,
// from the last sequence the journal holds for it.
func (a *App) readRings(ctx context.Context) {
	f := a.feed
	f.mu.Lock()
	boot := f.boot
	var todo []history.Pane
	for w := range f.agents {
		if !f.read[w] {
			todo = append(todo, f.panes[w])
		}
	}
	f.mu.Unlock()
	if boot == "" {
		return
	}
	for _, p := range todo {
		since, err := a.hist.LastSequence(boot, p.Window)
		if err != nil {
			continue
		}
		c, cancel := context.WithTimeout(ctx, daemonTimeout)
		res, err := a.journalSrc.Activity(c, daemon.ActivityParams{Session: p.Session, Window: p.Window, SinceSeq: since, Limit: 256})
		cancel()
		if err != nil {
			continue
		}
		var entries []history.Entry
		for _, e := range res.Entries {
			if entry, ok := history.FromActivity(boot, p, history.Activity{Seq: e.Seq, At: e.At, Kind: e.Kind, Tool: e.Tool, Target: e.Target, Files: e.Files, OK: e.OK, Text: e.Text}); ok {
				entries = append(entries, entry)
			}
		}
		f.mu.Lock()
		if f.boot == boot {
			f.read[p.Window] = true
		}
		f.mu.Unlock()
		a.journal(entries...)
	}
}

// journal appends entries and signals the loop when any were new; the
// turns of the new ones yield context items (context_turns.go) here, off
// the loop.
func (a *App) journal(entries ...history.Entry) {
	if len(entries) == 0 {
		return
	}
	ids, err := a.hist.Append(entries...)
	if err != nil || len(ids) == 0 {
		return
	}
	var fresh []history.Entry
	for _, e := range entries {
		if slices.Contains(ids, e.ID) {
			fresh = append(fresh, e)
		}
	}
	if _, err := turnContext(a.hist, a.contexts, fresh, a.contextExtraction()); err == nil {
		a.feed.contextAdded.Store(true)
	}
	a.feed.signal()
}

// activityOf reads an agent-activity event's entry.
func activityOf(m map[string]any) history.Activity {
	str := func(k string) string { s, _ := m[k].(string); return s }
	num := func(k string) float64 { n, _ := m[k].(float64); return n }
	a := history.Activity{Seq: uint64(num("seq")), At: int64(num("at")), Kind: str("kind"), Tool: str("tool"), Target: str("target"), Text: str("text")}
	if ok, present := m["ok"].(bool); present {
		a.OK = &ok
	}
	if files, ok := m["files"].([]any); ok {
		for _, file := range files {
			if s, ok := file.(string); ok {
				a.Files = append(a.Files, s)
			}
		}
	}
	return a
}

// journalUpdate runs after every message: a burst of journal rows reloads
// the views that read the journal, and the wait starts again.
func (a *App) journalUpdate(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case historyChanged:
		wait := a.waitJournal()
		if a.feed.pending {
			return wait
		}
		delay := a.r.Settings().Duration(settingHistoryCoalesce)
		if delay <= 0 {
			a.historyChanged()
			return wait
		}
		a.feed.pending = true
		if a.pv.after != nil {
			return tea.Batch(wait, a.pv.after(delay, historyDue{}))
		}
		return wait
	case historyDue:
		if a.feed.pending {
			a.feed.pending = false
			a.historyChanged()
		}
	}
	return nil
}

// historyChanged reads the journal again into the views that show it: the
// Turns tab of the pane it shows and the Review page.
func (a *App) historyChanged() {
	var errs []error
	visibleOnly := a.r.Settings().Bool(settingHistoryVisible)
	if a.turnsHistoryActive() && a.pv.turnsFor != "" && a.r.Instances[a.pv.turnsFor] != nil {
		errs = append(errs, a.showTurns(a.pv.turnsFor))
	}
	if !visibleOnly || a.r.Page == reviewsPage {
		errs = append(errs, a.refreshReviews())
	}
	errs = append(errs, a.pickerTurnsChanged())
	if a.feed != nil && a.feed.contextAdded.Swap(false) {
		errs = append(errs, a.reloadContext())
		a.contextChanged()
	}
	if err := errors.Join(errs...); err != nil {
		a.r.UI().Error("history: " + err.Error())
	}
}

// turnsHistoryActive shares the history.visible-only policy between the
// journal, listing polls and the detail's lazy card/subagent work. An
// attached live screen or copy view can cover Turns without changing its tab.
func (a *App) turnsHistoryActive() bool {
	return !a.r.Settings().Bool(settingHistoryVisible) || a.r.Page == panesPage && a.pv.tab == tabTurns && !a.liveShown() && !a.copyShown(paneTermID)
}

// journalOwner is the owner the journal gives an entry: the pane's own
// assignment, else its session's owner (looked up by name, since a
// capture hook knows only the session's name). It reads the store only,
// so it is safe off the loop.
func journalOwner(w *work.Store) history.Owner {
	return func(session, window string) (string, string, error) {
		s, err := w.SessionOwnerNamed(session)
		if err != nil {
			return "", "", err
		}
		o, err := w.PaneOwner(window, s.ID)
		return o.TaskID, o.ProjectID, err
	}
}
