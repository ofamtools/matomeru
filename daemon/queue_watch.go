package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

const (
	queueWatchBuffer  = 64
	queueWatchTimeout = 30 * time.Second
	// queueWatchScheduleID prefixes the synthetic schedule a watch dispatch
	// runs under, so its audit trail names the watching pane.
	queueWatchScheduleID = "queue.watch:"
)

// queueWatcher hands the next queued item to agent panes that watch a queue:
// on the agent.done edge for watchMode "on_done", and on the scheduler's
// cadence for watchMode "schedule". Panes in "manual" mode are never touched.
type queueWatcher struct {
	store      *data.Store
	dispatcher QueueDispatcher
	clock      func() time.Time
	// ready reports whether a pane can take a prompt right now; nil means yes.
	ready func(paneID string) bool

	events  chan AgentDoneEvent
	dropped atomic.Uint64
	started atomic.Bool

	mu       sync.Mutex // one dispatch at a time, so two panes never take the same item
	lastPoll map[string]time.Time
}

func newQueueWatcher(store *data.Store, dispatcher QueueDispatcher, clock func() time.Time, ready func(string) bool) *queueWatcher {
	if clock == nil {
		clock = time.Now
	}
	return &queueWatcher{
		store:      store,
		dispatcher: dispatcher,
		clock:      clock,
		ready:      ready,
		events:     make(chan AgentDoneEvent, queueWatchBuffer),
		lastPoll:   map[string]time.Time{},
	}
}

// publish queues an agent.done event without blocking the hook that raised it.
func (w *queueWatcher) publish(ev AgentDoneEvent) {
	if w == nil {
		return
	}
	select {
	case w.events <- ev:
	default:
		w.dropped.Add(1)
	}
}

// start runs the watcher until ctx is cancelled.
func (w *queueWatcher) start(ctx context.Context, wg *sync.WaitGroup) {
	if w == nil || !w.started.CompareAndSwap(false, true) {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-w.events:
				dctx, cancel := context.WithTimeout(ctx, queueWatchTimeout)
				_, _ = w.handleAgentDone(dctx, ev)
				cancel()
			}
		}
	}()
}

// handleAgentDone dispatches the next queued item to the pane that just
// finished its turn, when that pane watches a queue with watchMode "on_done".
// It returns the dispatcher's detail, or "" when nothing was dispatched.
func (w *queueWatcher) handleAgentDone(ctx context.Context, ev AgentDoneEvent) (string, error) {
	if w == nil || w.store == nil || w.dispatcher == nil || ev.PaneID == "" {
		return "", nil
	}
	pane, err := w.store.Get(core.Ref{Kind: data.KindPane, ID: ev.PaneID})
	if err != nil || pane == nil || pane.IsArchived() {
		return "", nil
	}
	if data.NormalizeWatchMode(pane.Text(data.FieldWatchMode)) != data.WatchModeOnDone {
		return "", nil
	}
	return w.dispatchNext(ctx, *pane)
}

// pollScheduled dispatches the next queued item to every idle pane whose
// watchMode is "schedule" and whose interval has elapsed. The scheduler calls
// it once per tick.
func (w *queueWatcher) pollScheduled(ctx context.Context, now time.Time) {
	if w == nil || w.store == nil || w.dispatcher == nil {
		return
	}
	res, err := w.store.Query(data.Query{Kind: data.KindPane})
	if err != nil {
		return
	}
	live := make(map[string]bool, len(res.Items))
	for _, pane := range res.Items {
		if ctx.Err() != nil {
			return
		}
		if data.NormalizeWatchMode(pane.Text(data.FieldWatchMode)) != data.WatchModeSchedule {
			continue
		}
		live[pane.ID] = true
		interval := time.Duration(data.WatchInterval(pane)) * time.Second
		w.mu.Lock()
		last, polled := w.lastPoll[pane.ID]
		w.mu.Unlock()
		if polled && now.Sub(last) < interval {
			continue
		}
		if w.ready != nil && !w.ready(pane.ID) {
			continue // busy: try again next tick instead of waiting out the interval
		}
		w.mu.Lock()
		w.lastPoll[pane.ID] = now
		w.mu.Unlock()
		_, _ = w.dispatchNext(ctx, pane)
	}
	w.mu.Lock()
	for id := range w.lastPoll {
		if !live[id] {
			delete(w.lastPoll, id)
		}
	}
	w.mu.Unlock()
}

// dispatchNext sends the first never-dispatched queued item of the pane's
// watched queues to it, with the full ID handoff prompt.
func (w *queueWatcher) dispatchNext(ctx context.Context, pane data.Record) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	projectID := ""
	if ref, ok := pane.RefField("task"); ok && ref.Kind == data.KindTask {
		projectID = w.store.RootTaskID(ref.ID)
	}
	queues, err := w.store.NamedQueues("")
	if err != nil {
		return "", err
	}
	titles := data.QueueTitlesByID(queues)
	include := true
	all, err := w.store.QueryAgentQueue(data.AgentQueueFilter{
		ProjectID:             projectID,
		Status:                data.AgentStatusQueued,
		IncludeSubtree:        true,
		IncludeNotes:          true,
		IncludeContext:        true,
		IncludeProjectContext: &include,
	})
	if err != nil {
		return "", err
	}
	watch := pane.Text(data.FieldWatchQueues)
	var next []data.AgentQueueItem
	for _, item := range data.UndispatchedQueueItems(all) {
		if data.WatchesQueue(watch, data.QueueNameOf(item.Task, titles)) {
			next = []data.AgentQueueItem{item}
			break
		}
	}
	if len(next) == 0 {
		return "", nil
	}

	queueName := data.QueueNameOf(next[0].Task, titles)
	target := data.DispatchTargetSpec{
		ExistingPaneID:        pane.ID,
		Harness:               pane.Text("harness"),
		CWD:                   pane.Text("cwd"),
		IncludeProjectContext: queueIncludesContext(queues, queueName),
		WatchMode:             pane.Text(data.FieldWatchMode),
		WatchQueues:           watch,
	}
	in, err := w.store.QueueDispatchInput(queueName, next, target, "")
	if err != nil {
		return "", err
	}
	sched := data.Record{
		Kind: data.KindSchedule,
		ID:   queueWatchScheduleID + pane.ID,
		Fields: map[string]any{
			"title":        fmt.Sprintf("queue watch (%s) for pane %s", target.Normalized().WatchMode, pane.ID),
			"scheduleKind": "agent_queue",
			"pane":         pane.Ref(),
		},
	}
	if projectID != "" {
		sched.Fields["task"] = core.Ref{Kind: data.KindTask, ID: projectID}
	}
	return w.dispatcher.DispatchQueue(ctx, sched, next, data.BuildQueueDispatchPrompt(in))
}

// queueIncludesContext reads the queue record's includeContextDefault; a
// queue without a record includes project context.
func queueIncludesContext(queues []data.Record, queueName string) bool {
	for _, q := range queues {
		if !strings.EqualFold(strings.TrimSpace(q.Text("title")), queueName) {
			continue
		}
		if _, set := q.Value("includeContextDefault"); set {
			return q.Bool("includeContextDefault")
		}
	}
	return true
}

// paneReadyForQueue reports whether the pane is live and not mid-turn.
func (s *Server) paneReadyForQueue(paneID string) bool {
	if s.host == nil {
		return false
	}
	if f, ok := s.host.Snapshot(paneID); !ok || f.Exited {
		return false
	}
	tel, _ := s.telemetry.snapshot(paneID)
	return isPaneAvailableForDispatch(tel[paneID], nil)
}
