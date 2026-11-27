package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ofam.dev/matomeru/mcpui"
)

func TestAgentDoneEdgeTrigger(t *testing.T) {
	ts := newTelemetryStore()
	var got []AgentDoneEvent
	ts.onDone = func(ev AgentDoneEvent) {
		// Called without ts.mu held: re-entering the store must not deadlock.
		_, _ = ts.snapshot(ev.PaneID)
		got = append(got, ev)
	}
	now := time.Unix(1_800_000_000, 0)
	send := func(state string) {
		ts.recordEvent(HookEvent{PaneID: "p1", Harness: "claude", Kind: HookKindState, State: state, Summary: "s-" + state}, now)
	}

	send(StateDone) // no previous state: not an edge
	send(StateRunning)
	send(StateDone)     // running -> done: edge
	send(StateDone)     // done -> done: no duplicate
	send(StateIdle)     // done -> idle: not from an active state
	send(StateNeedsYou) // idle -> needs_you: agent.needs_input edge
	send(StateIdle)     // needs_you -> idle: edge

	if len(got) != 3 {
		t.Fatalf("events = %d, want 3: %+v", len(got), got)
	}
	if got[0].Event != EventAgentDone || got[0].PrevState != StateRunning || got[0].State != StateDone || got[0].Harness != "claude" || got[0].Summary != "s-done" {
		t.Fatalf("first event = %+v", got[0])
	}
	if got[1].Event != EventAgentNeedsInput || got[1].PrevState != StateIdle || got[1].State != StateNeedsYou {
		t.Fatalf("needs_input event = %+v", got[1])
	}
	if got[2].Event != EventAgentDone || got[2].PrevState != StateNeedsYou || got[2].State != StateIdle {
		t.Fatalf("third event = %+v", got[2])
	}
}

func writeHooks(t *testing.T, path string, rules ...HookRule) {
	t.Helper()
	if err := SaveHookRules(path, rules); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHooksFileCommandCallbackReceivesEventOnStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "event.json")
	hooks := filepath.Join(dir, HooksFileName)
	writeHooks(t, hooks,
		HookRule{ID: "cat", On: EventAgentDone, Match: HookMatch{Harness: "codex"}, Run: HookRun{Command: []string{"/bin/sh", "-c", `cat > "$OUT_FILE"`}}},
		HookRule{ID: "other-harness", Match: HookMatch{Harness: "claude"}, Run: HookRun{Command: []string{"/bin/sh", "-c", "exit 0"}}},
	)
	t.Setenv("OUT_FILE", out)
	_, client := startTestServer(t, Config{DisableMCP: true, HooksPath: hooks})
	ctx := context.Background()
	for _, st := range []string{StateRunning, StateDone} {
		if _, err := client.HookEvent(ctx, HookEvent{PaneID: "p-cmd", Harness: "codex", Kind: HookKindState, State: st, Response: "all green"}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 5*time.Second, "command delivery", func() bool {
		log, _ := client.HookLog(ctx, 0)
		return len(log) == 1
	})
	log, _ := client.HookLog(ctx, 0)
	if !log[0].OK || log[0].RuleID != "cat" || log[0].Attempts != 1 {
		t.Fatalf("delivery = %+v", log[0])
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var ev AgentDoneEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("stdin was not event JSON: %v: %s", err, raw)
	}
	if ev.PaneID != "p-cmd" || ev.State != StateDone || ev.PrevState != StateRunning || ev.Response != "all green" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestWebhookCallbackRetriesOnceOn500(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var body []byte
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		body, _ = io.ReadAll(r.Body)
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("HOOK_TEST_TOKEN", "abc")

	rule := HookRule{ID: "wh", Run: HookRun{Webhook: srv.URL, Headers: map[string]string{"Authorization": "Bearer ${HOOK_TEST_TOKEN}"}}}
	ev := AgentDoneEvent{Event: EventAgentDone, PaneID: "p-wh", State: StateDone}
	d := DeliverHook(context.Background(), srv.Client(), rule, ev)
	if !d.OK || d.Attempts != 2 || d.Status != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("delivery = %+v calls=%d", d, calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	var got AgentDoneEvent
	if err := json.Unmarshal(body, &got); err != nil || got.PaneID != "p-wh" {
		t.Fatalf("body = %s (%v)", body, err)
	}
	if auth != "Bearer abc" {
		t.Fatalf("Authorization = %q", auth)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failing.Close()
	d = DeliverHook(context.Background(), failing.Client(), HookRule{ID: "bad", Run: HookRun{Webhook: failing.URL}}, ev)
	if d.OK || d.Attempts != 2 || d.Status != http.StatusBadGateway || d.Error == "" {
		t.Fatalf("failing delivery = %+v", d)
	}
}

func TestOnceCallbacksRemoveThemselvesAfterFiring(t *testing.T) {
	hooks := filepath.Join(t.TempDir(), HooksFileName)
	ok := HookRun{Command: []string{"/bin/sh", "-c", "exit 0"}}
	writeHooks(t, hooks,
		HookRule{ID: "file-once", Once: true, Run: ok},
		HookRule{ID: "file-keep", Run: ok},
	)
	bus := newCallbackBus(hooks, nil)
	if err := bus.register(HookRule{ID: "mem-once", Once: true, Match: HookMatch{Pane: "p1"}, Run: ok}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	bus.start(ctx, &wg)
	defer func() { cancel(); wg.Wait() }()

	ev := AgentDoneEvent{Event: EventAgentDone, PaneID: "p1", State: StateDone}
	bus.publish(ev)
	waitFor(t, 5*time.Second, "first deliveries", func() bool { return len(bus.log(0)) == 3 })
	bus.publish(ev)
	waitFor(t, 5*time.Second, "second delivery", func() bool { return len(bus.log(0)) == 4 })
	time.Sleep(50 * time.Millisecond)
	if n := len(bus.log(0)); n != 4 {
		t.Fatalf("deliveries = %d, want 4", n)
	}
	if last := bus.log(1)[0]; last.RuleID != "file-keep" {
		t.Fatalf("second event fired %q, want only file-keep", last.RuleID)
	}
	rules, err := LoadHookRules(hooks)
	if err != nil || len(rules) != 1 || rules[0].ID != "file-keep" {
		t.Fatalf("hooks.json rules = %+v (%v)", rules, err)
	}
	if mem := bus.memoryRules(); len(mem) != 0 {
		t.Fatalf("memory rules = %+v", mem)
	}
}

func listenerCount(b *callbackBus) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.listeners)
}

func TestWaitAgentDoneUnblocksOnEvent(t *testing.T) {
	srv, client := startTestServer(t, Config{DisableMCP: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		ev  *AgentDoneEvent
		err error
	}
	done := make(chan result, 1)
	go func() {
		ev, err := client.WaitAgentDone(ctx, "p-wait", false)
		done <- result{ev, err}
	}()
	waitFor(t, 2*time.Second, "watch listener", func() bool { return listenerCount(srv.callbacks) == 1 })
	select {
	case r := <-done:
		t.Fatalf("returned before agent.done: %+v", r)
	default:
	}
	for _, st := range []string{StateRunning, StateDone} {
		if _, err := client.HookEvent(ctx, HookEvent{PaneID: "p-wait", Kind: HookKindState, State: st, Summary: "finished"}); err != nil {
			t.Fatal(err)
		}
	}
	r := <-done
	if r.err != nil || r.ev == nil || r.ev.PaneID != "p-wait" || r.ev.PrevState != StateRunning || r.ev.Summary != "finished" {
		t.Fatalf("wait = %+v, %v", r.ev, r.err)
	}

	// Already done: returns at once; with next it waits for a fresh edge.
	ev, err := client.WaitAgentDone(ctx, "p-wait", false)
	if err != nil || ev.State != StateDone || ev.PrevState != "" {
		t.Fatalf("already-done wait = %+v, %v", ev, err)
	}
	short, cancelShort := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancelShort()
	if _, err := client.WaitAgentDone(short, "p-wait", true); err == nil {
		t.Fatal("next wait returned without a new edge")
	}
	waitFor(t, 2*time.Second, "listener cleanup", func() bool { return listenerCount(srv.callbacks) == 0 })
}

func TestHookRPCRegisterListLogAndPaneClose(t *testing.T) {
	srv, client := startTestServer(t, Config{DisableMCP: true})
	ctx := context.Background()
	rule := OnDoneRule("p-x", mcpui.PaneOnDone{Command: []string{"true"}})
	if err := client.RegisterHook(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if err := client.RegisterHook(ctx, HookRule{ID: "bad"}); err == nil {
		t.Fatal("invalid rule accepted")
	}
	rules, err := client.HookRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].Source != HookSourceMemory || !rules[0].Once || rules[0].Match.Pane != "p-x" {
		t.Fatalf("rules = %+v (%v)", rules, err)
	}
	srv.handleRequest(Request{Op: OpClose, ID: "p-x"})
	if mem := srv.callbacks.memoryRules(); len(mem) != 0 {
		t.Fatalf("closing the pane kept its callbacks: %+v", mem)
	}
	if found, err := client.UnregisterHook(ctx, rule.ID); err != nil || found {
		t.Fatalf("unregister after close = %v, %v", found, err)
	}
}
