package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	callbackEventBuffer  = 256
	callbackJobBuffer    = 256
	callbackWorkers      = 4
	callbackHistoryLimit = 200
	callbackListenerBuf  = 16
	callbackOutputBytes  = 4 * 1024
)

// HookDelivery records one callback execution (both attempts) in the daemon's
// callback history ring.
type HookDelivery struct {
	RuleID     string    `json:"ruleId"`
	Source     string    `json:"source,omitempty"`
	Event      string    `json:"event"`
	PaneID     string    `json:"paneId,omitempty"`
	Target     string    `json:"target"`
	Attempts   int       `json:"attempts"`
	OK         bool      `json:"ok"`
	Status     int       `json:"status,omitempty"`
	Error      string    `json:"error,omitempty"`
	Output     string    `json:"output,omitempty"`
	At         time.Time `json:"at"`
	DurationMs int64     `json:"durationMs"`
}

type callbackJob struct {
	rule HookRule
	ev   AgentDoneEvent
}

type eventListener struct {
	pane string
	ch   chan AgentDoneEvent
}

// callbackBus fans bus events (agent.done, agent.needs_input, agent.blocked,
// mail.received) out to RPC listeners and to matching
// hooks.json / in-memory callback rules, delivering on a bounded worker pool.
type callbackBus struct {
	events chan AgentDoneEvent
	jobs   chan callbackJob
	file   *hookFile
	client *http.Client
	clock  func() time.Time
	// enrich fills store-derived fields (task, lane, project) off the hot path.
	enrich func(*AgentDoneEvent)
	// notify delivers a rule's run.notifyPane (mailbox message + idle wake).
	notify func(ctx context.Context, target string, ev AgentDoneEvent) error

	mu        sync.Mutex
	memory    []HookRule
	listeners map[*eventListener]struct{}
	history   []HookDelivery
	dropped   atomic.Uint64
	started   atomic.Bool
}

func newCallbackBus(hooksPath string, clock func() time.Time) *callbackBus {
	if clock == nil {
		clock = time.Now
	}
	return &callbackBus{
		events:    make(chan AgentDoneEvent, callbackEventBuffer),
		jobs:      make(chan callbackJob, callbackJobBuffer),
		file:      &hookFile{path: hooksPath},
		client:    &http.Client{},
		clock:     clock,
		listeners: map[*eventListener]struct{}{},
	}
}

// publish enqueues ev without blocking; a full queue drops the event.
func (b *callbackBus) publish(ev AgentDoneEvent) {
	select {
	case b.events <- ev:
	default:
		b.dropped.Add(1)
	}
}

// start runs the dispatcher and delivery workers until ctx is cancelled.
func (b *callbackBus) start(ctx context.Context, wg *sync.WaitGroup) {
	if !b.started.CompareAndSwap(false, true) {
		return
	}
	wg.Add(1 + callbackWorkers)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-b.events:
				b.dispatch(ev)
			}
		}
	}()
	for range callbackWorkers {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-b.jobs:
					b.record(b.deliver(ctx, job))
				}
			}
		}()
	}
}

func (b *callbackBus) dispatch(ev AgentDoneEvent) {
	if b.enrich != nil {
		b.enrich(&ev)
	}
	b.fanOut(ev)

	fileRules := b.file.load()
	var fileOnce []string
	matched := make([]HookRule, 0, 4)
	for _, r := range fileRules {
		if r.Validate() == nil && r.Matches(ev) {
			matched = append(matched, r)
			if r.Once {
				fileOnce = append(fileOnce, r.ID)
			}
		}
	}
	b.file.consume(fileOnce)

	b.mu.Lock()
	kept := b.memory[:0]
	for _, r := range b.memory {
		if r.Matches(ev) {
			matched = append(matched, r)
			if r.Once {
				continue
			}
		}
		kept = append(kept, r)
	}
	b.memory = kept
	b.mu.Unlock()

	for _, r := range matched {
		select {
		case b.jobs <- callbackJob{rule: r, ev: ev}:
		default:
			b.record(HookDelivery{
				RuleID: r.ID, Source: r.Source, Event: ev.Event, PaneID: ev.PaneID,
				Target: hookTarget(r), Error: "callback queue full; dropped", At: b.clock(),
			})
		}
	}
}

func (b *callbackBus) fanOut(ev AgentDoneEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for l := range b.listeners {
		if l.pane != "" && l.pane != ev.PaneID {
			continue
		}
		select {
		case l.ch <- ev:
		default:
		}
	}
}

// listen registers an event listener filtered to pane (empty = all panes).
func (b *callbackBus) listen(pane string) (*eventListener, func()) {
	l := &eventListener{pane: pane, ch: make(chan AgentDoneEvent, callbackListenerBuf)}
	b.mu.Lock()
	b.listeners[l] = struct{}{}
	b.mu.Unlock()
	return l, func() {
		b.mu.Lock()
		delete(b.listeners, l)
		b.mu.Unlock()
	}
}

// register upserts an in-memory callback rule.
func (b *callbackBus) register(rule HookRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	rule.Source = HookSourceMemory
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.memory {
		if b.memory[i].ID == rule.ID {
			b.memory[i] = rule
			return nil
		}
	}
	b.memory = append(b.memory, rule)
	return nil
}

// unregister removes in-memory rule id, reporting whether it existed.
func (b *callbackBus) unregister(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.memory)
	b.memory = slices.DeleteFunc(b.memory, func(r HookRule) bool { return r.ID == id })
	return len(b.memory) != n
}

// forgetPane drops in-memory rules bound to a closed pane.
func (b *callbackBus) forgetPane(paneID string) {
	if paneID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.memory = slices.DeleteFunc(b.memory, func(r HookRule) bool { return r.Match.Pane == paneID })
}

// rules returns file rules followed by in-memory rules.
func (b *callbackBus) rules() []HookRule {
	out := b.file.load()
	b.mu.Lock()
	out = append(out, b.memory...)
	b.mu.Unlock()
	return out
}

func (b *callbackBus) memoryRules() []HookRule {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.memory)
}

func (b *callbackBus) restore(rules []HookRule) {
	for _, r := range rules {
		_ = b.register(r)
	}
}

func (b *callbackBus) record(d HookDelivery) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.history = append(b.history, d)
	if len(b.history) > callbackHistoryLimit {
		b.history = slices.Clone(b.history[len(b.history)-callbackHistoryLimit:])
	}
}

// log returns the most recent deliveries, oldest first (limit <= 0 = all).
func (b *callbackBus) log(limit int) []HookDelivery {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.history
	if limit > 0 && len(h) > limit {
		h = h[len(h)-limit:]
	}
	return slices.Clone(h)
}

// deliver runs job's command/webhook and then its notifyPane delivery.
func (b *callbackBus) deliver(ctx context.Context, job callbackJob) HookDelivery {
	d := DeliverHook(ctx, b.client, job.rule, job.ev)
	target := strings.TrimSpace(job.rule.Run.NotifyPane)
	if target == "" {
		return d
	}
	d.Attempts++
	err := errors.New("notifyPane: daemon mailbox not wired")
	if b.notify != nil {
		err = b.notify(ctx, target, job.ev)
	}
	if err != nil {
		d.OK = false
		d.Error = strings.TrimPrefix(d.Error+"; "+err.Error(), "; ")
	}
	return d
}

func hookTarget(r HookRule) string {
	switch {
	case len(r.Run.Command) > 0:
		return "command:" + r.Run.Command[0]
	case strings.TrimSpace(r.Run.Webhook) != "":
		return "webhook:" + r.Run.Webhook
	}
	return "notify:" + r.Run.NotifyPane
}

// DeliverHook runs rule for ev: the command (event JSON on stdin) and/or the
// webhook (event JSON POST body), each with one retry on error or non-2xx.
func DeliverHook(ctx context.Context, client *http.Client, rule HookRule, ev AgentDoneEvent) HookDelivery {
	start := time.Now()
	d := HookDelivery{
		RuleID: rule.ID, Source: rule.Source, Event: ev.Event, PaneID: ev.PaneID,
		Target: hookTarget(rule), At: start,
	}
	body, err := json.Marshal(ev)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	if client == nil {
		client = http.DefaultClient
	}
	var errs []string
	ok := true
	if len(rule.Run.Command) > 0 {
		n, out, err := withRetry(ctx, func() (string, error) { return runHookCommand(ctx, rule, ev, body) })
		d.Attempts += n
		d.Output = out
		if err != nil {
			ok = false
			errs = append(errs, err.Error())
		}
	}
	if hook := strings.TrimSpace(rule.Run.Webhook); hook != "" {
		n, out, err := withRetry(ctx, func() (string, error) {
			status, err := postHookWebhook(ctx, client, rule, body)
			d.Status = status
			return "", err
		})
		d.Attempts += n
		if d.Output == "" {
			d.Output = out
		}
		if err != nil {
			ok = false
			errs = append(errs, err.Error())
		}
	}
	d.OK = ok
	d.Error = strings.Join(errs, "; ")
	d.DurationMs = time.Since(start).Milliseconds()
	return d
}

func withRetry(ctx context.Context, attempt func() (string, error)) (int, string, error) {
	out, err := attempt()
	if err == nil || ctx.Err() != nil {
		return 1, out, err
	}
	out, err = attempt()
	return 2, out, err
}

func runHookCommand(ctx context.Context, rule HookRule, ev AgentDoneEvent, body []byte) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, rule.Timeout())
	defer cancel()
	cmd := exec.CommandContext(cctx, rule.Run.Command[0], rule.Run.Command[1:]...)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = append(os.Environ(),
		"MTM_EVENT="+ev.Event,
		"MTM_EVENT_PANE_ID="+ev.PaneID,
		"MTM_EVENT_STATE="+ev.State,
		"MTM_HOOK_ID="+rule.ID,
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	out := buf.String()
	if len(out) > callbackOutputBytes {
		out = out[:callbackOutputBytes]
	}
	if cctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("command timed out after %s", rule.Timeout())
	}
	if err != nil {
		return out, fmt.Errorf("command: %w", err)
	}
	return out, nil
}

func postHookWebhook(ctx context.Context, client *http.Client, rule HookRule, body []byte) (int, error) {
	cctx, cancel := context.WithTimeout(ctx, rule.Timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimSpace(rule.Run.Webhook), bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("webhook: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mtm-hooks")
	for k, v := range rule.Run.Headers {
		// ${VAR} expansion keeps secrets in the environment, not in hooks.json.
		req.Header.Set(k, os.ExpandEnv(v))
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, callbackOutputBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("webhook: HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// isActiveState reports states an agent works in; leaving them for done/idle
// is the agent.done edge.
func isActiveState(state string) bool {
	return state == StateRunning || state == StateNeedsYou
}

func isRestState(state string) bool {
	return state == StateDone || state == StateIdle
}
