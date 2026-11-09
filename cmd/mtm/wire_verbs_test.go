//go:build !nodaemon

package main

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

// tuiosMCPTools are the tools of the tuios MCP server at v0.12.1
// (internal/mcp/tools.go) and the mtm verb each one is reached by through
// `mtm mcp serve`. Mail is dropped on purpose: tuios_read_agent_messages and
// tuios_send_agent_message have no mtm verb.
var tuiosMCPTools = map[string]string{
	"tuios_list_windows":    "session.list-panes",
	"tuios_capture_pane":    "pane.capture",
	"tuios_send_text":       "pane.send-text",
	"tuios_send_keys":       "pane.send-keys",
	"tuios_wait_for":        "pane.wait",
	"tuios_events":          "session.events",
	"tuios_list_agents":     "agent.scan",
	"tuios_get_agent_state": "agent.status",
	"tuios_set_agent_state": "agent.set-status",
	"tuios_set_agent_meta":  "agent.set-meta",
	"tuios_ask_agent":       "agent.ask",
	"tuios_fan":             "agent.fan",
	"tuios_peek_prompt":     "agent.peek-prompt",
	"tuios_respond":         "agent.respond",
}

// tuiosMailTools are the tuios MCP tools mtm does not carry.
var tuiosMailTools = []string{"tuios_read_agent_messages", "tuios_send_agent_message"}

func TestEveryTuiosMCPToolHasAGatewayVerb(t *testing.T) {
	t.Parallel()
	if len(tuiosMCPTools)+len(tuiosMailTools) != 16 {
		t.Fatalf("the tuios server has 16 tools; the table maps %d and drops %d", len(tuiosMCPTools), len(tuiosMailTools))
	}
	app, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	var reqs []any
	var verbs []string
	for tool, verb := range tuiosMCPTools {
		if _, ok := app.r.Operation(verb); !ok {
			t.Errorf("%s -> %s: no such verb", tool, verb)
			continue
		}
		verbs = append(verbs, verb)
		reqs = append(reqs, toolCall(len(reqs)+1, "describe", map[string]any{"verb": verb}))
	}
	slices.Sort(verbs)
	resps := rpc(t, app, reqs...)
	for i, resp := range resps {
		res, _ := resp["result"].(map[string]any)
		if res == nil || res["isError"] == true {
			t.Errorf("the gateway cannot describe %v: %v", reqs[i].(map[string]any)["params"], resp)
		}
	}
	for _, name := range tuiosMailTools {
		if _, ok := tuiosMCPTools[name]; ok {
			t.Errorf("%s is mail and must stay dropped", name)
		}
	}
	for _, w := range wireVerbs {
		if !w.NoTuios && !slices.Contains(verbs, w.key()) && !slices.Contains(slices.Collect(maps.Values(tuiosCLICommands)), w.key()) {
			t.Errorf("wire verb %s maps no tuios tool or CLI command", w.key())
		}
	}
	// Reads are allowed, never left to confirmation.
	for _, w := range wireVerbs {
		if op, _ := app.r.Operation(w.key()); w.Fetch && op.Agent != ochazuke.AgentAllow {
			t.Errorf("%s reads and must be allowed, is %q", w.key(), op.Agent)
		}
	}
}

// selfDaemonApp starts a self daemon in a temp HOME and returns a function
// that makes the serve app for it, plus a client of the daemon.
func selfDaemonApp(t *testing.T, scope string) (func() *App, *daemon.Client) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "mtmwire")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, d := range []string{"home", "run", "config", "state", "data"} {
		os.MkdirAll(filepath.Join(root, d), 0o700)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("MTM_DAEMON_RUNNER", runnerSelf)
	server := "wire" + scope
	for _, k := range []string{"TUIOS_SOCKET", "TUIOS_SERVER"} {
		// Both names: the daemon exports MTM_ beside TUIOS_, and mtm reads MTM_ first.
		for _, k := range []string{k, "MTM_" + strings.TrimPrefix(k, "TUIOS_")} {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	t.Setenv("MTM_SERVER", server)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self := daemon.SelfStart{Word: daemonWord, PinDir: filepath.Join(root, "state", "daemon-bin"), Label: "test", InUse: func(string) bool { return true }}
	client := daemon.ForServer(server)
	client.Start, client.Wait = self.Start, 60*time.Second*raceSlowdown
	client.Ready = client.HelloReady
	_ = exe
	make := func() *App {
		app := openWith(t, Options{Data: filepath.Join(root, "mtm.db"), Server: server})
		app.getenv = func(string) string { return "" }
		app.mcpPrepare = func(a *App) {
			a.daemon = client
			a.panesd = tuios{Client: client}
			a.ensure = client.Ensure
			a.getenv = func(string) string { return "" }
		}
		if err := app.r.Settings().Set(settingAgentsPaneScope, scope); err != nil {
			t.Fatal(err)
		}
		return app
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second*raceSlowdown)
	defer cancel()
	if err := client.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	h, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(h.PID, syscall.SIGTERM) })
	return make, client
}

// runVerb is one gateway run call's response.
func runVerb(id int, verb string, args map[string]any) map[string]any {
	return toolCall(id, "run", map[string]any{"verb": verb, "args": args})
}

// payload is the run envelope of a response.
func payload(resp map[string]any) map[string]any {
	res, _ := resp["result"].(map[string]any)
	sc, _ := res["structuredContent"].(map[string]any)
	if sc == nil {
		if content, ok := res["content"].([]any); ok && len(content) > 0 {
			_ = json.Unmarshal([]byte(content[len(content)-1].(map[string]any)["text"].(string)), &sc)
		}
	}
	return sc
}

func resultMap(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	p := payload(resp)
	if p["status"] != "done" {
		t.Fatalf("not done: %v", resp)
	}
	m, _ := p["result"].(map[string]any)
	if m == nil {
		t.Fatalf("no result map: %v", p)
	}
	return m
}

// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestWireVerbsRunAgainstASelfDaemonThroughMCPServe(t *testing.T) {
	make, client := selfDaemonApp(t, paneScopeAll)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second*raceSlowdown)
	defer cancel()

	// The table agrees with the daemon: every verb exists, every parameter
	// is one the verb takes, and what the daemon requires the table requires.
	var listed struct {
		Verbs []struct {
			Verb   string `json:"verb"`
			Params []struct {
				Name     string `json:"name"`
				Required bool   `json:"required"`
			} `json:"params"`
		} `json:"verbs"`
	}
	if err := client.Call(ctx, "list-verbs", nil, &listed); err != nil {
		t.Fatal(err)
	}
	hidden := map[string]bool{"human_nonce": true, "from_host": true, "scrollback": true, "ansi": true, "resolved": true, "palette": true}
	for _, w := range wireVerbs {
		found := false
		for _, v := range listed.Verbs {
			if v.Verb != w.Wire {
				continue
			}
			found = true
			have := map[string]bool{}
			for _, p := range v.Params {
				have[p.Name] = true
				for _, mine := range w.Params {
					if mine.Name == p.Name && mine.Required != p.Required {
						t.Errorf("%s.%s: required is %v, daemon says %v", w.key(), p.Name, mine.Required, p.Required)
					}
				}
				if p.Required && !slices.ContainsFunc(w.Params, func(m wireParam) bool { return m.Name == p.Name }) && !hidden[p.Name] {
					t.Errorf("%s: daemon requires %s, the table lacks it", w.key(), p.Name)
				}
			}
			for _, mine := range w.Params {
				if !have[mine.Name] {
					t.Errorf("%s: the daemon's %s takes no %s", w.key(), w.Wire, mine.Name)
				}
			}
		}
		if !found {
			t.Errorf("%s: the daemon has no verb %s", w.key(), w.Wire)
		}
	}

	// A pane running cat to type into.
	var made struct {
		Window string `json:"window_id"`
	}
	if err := client.Call(ctx, "new-session", map[string]any{"name": "wt", "cwd": "/tmp", "command": []string{"cat"}}, &made); err != nil || made.Window == "" {
		t.Fatalf("new-session: %v %+v", err, made)
	}
	win, base := made.Window, map[string]any{"session": "wt"}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	// First events call: from now, nothing yet.
	first := resultMap(t, rpc(t, make(), runVerb(1, "session.events", map[string]any{"wait_ms": 300}))[0])
	last, boot := first["last_seq"], first["boot_id"]
	if boot == "" || boot == nil {
		t.Fatalf("events returned no boot_id: %v", first)
	}

	app := make()
	resps := rpc(t, app,
		runVerb(1, "session.list-panes", base),
		runVerb(2, "pane.send-text", with(map[string]any{"window": win, "text": "hello-wire", "submit": true})),
		runVerb(3, "pane.wait", with(map[string]any{"window": win, "condition": "window-output", "pattern": "hello-wire", "timeout": 5000})),
		runVerb(4, "pane.capture", with(map[string]any{"window": win})),
		runVerb(5, "pane.send-keys", with(map[string]any{"window": win, "keys": "Enter"})),
		runVerb(6, "agent.set-status", with(map[string]any{"window": win, "state": "working", "message": "wired"})),
		runVerb(7, "agent.status", with(map[string]any{"window": win})),
		runVerb(8, "agent.scan", with(map[string]any{"all": true})),
		runVerb(9, "agent.set-meta", with(map[string]any{"window": win, "ttl_ms": 1000, "tokens": map[string]any{"total": "5"}})),
	)
	if got := resultMap(t, resps[0]); !strings.Contains(toJSON(got), win) {
		t.Errorf("list-panes lacks %s: %v", win, got)
	}
	_ = resultMap(t, resps[1])
	_ = resultMap(t, resps[2])
	if c := resultMap(t, resps[3]); !strings.Contains(toJSON(c), "hello-wire") || c["untrusted"] != true {
		t.Errorf("capture: %v", c)
	}
	_ = resultMap(t, resps[4])
	_ = resultMap(t, resps[5])
	if s := resultMap(t, resps[6]); s["state"] != "working" || s["message"] != "wired" {
		t.Errorf("status: %v", s)
	}
	if l := resultMap(t, resps[7]); !strings.Contains(toJSON(l), win) {
		t.Errorf("agent list lacks the pane: %v", l)
	}
	if p := payload(resps[8]); p["status"] != "done" {
		t.Errorf("set-meta: %v", resps[8])
	}

	// The events after the first call include the state change.
	ev := resultMap(t, rpc(t, make(), runVerb(1, "session.events", map[string]any{"after_seq": last, "boot_id": boot, "types": []any{"agent-state"}, "wait_ms": 2000}))[0])
	if !strings.Contains(toJSON(ev["events"]), "agent-state") {
		t.Errorf("events after %v lack agent-state: %v", last, ev)
	}

	// No agent runs in the pane: these reach the daemon and are refused by
	// it, by the verb's own name.
	for i, call := range []struct {
		verb string
		args map[string]any
	}{
		{"agent.peek-prompt", with(map[string]any{"window": win})},
		{"agent.respond", with(map[string]any{"window": win, "action": "approve"})},
		{"agent.ask", with(map[string]any{"window": win, "text": "hi", "ready_timeout": 200, "timeout": 500})},
		{"agent.fan", map[string]any{"count": 1, "agent": "nope", "prompt": "x", "ready_timeout": 200}},
	} {
		p := payload(rpc(t, make(), runVerb(i+1, call.verb, call.args))[0])
		w, _ := lookupWire(call.verb)
		if p["status"] == "done" {
			continue
		}
		if msg := toJSON(p); !strings.Contains(msg, w.Wire+":") {
			t.Errorf("%s did not reach the daemon: %v", call.verb, p)
		}
	}
}

func lookupWire(key string) (wireVerb, bool) {
	for _, w := range wireVerbs {
		if w.key() == key {
			return w, true
		}
	}
	return wireVerb{}, false
}

func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestWireVerbsOfAnAgentAreHeldToItsScope(t *testing.T) {
	make, client := selfDaemonApp(t, paneScopeOwn)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second*raceSlowdown)
	defer cancel()
	var made struct {
		Window string `json:"window_id"`
	}
	if err := client.Call(ctx, "new-session", map[string]any{"name": "other", "cwd": "/tmp", "command": []string{"cat"}}, &made); err != nil {
		t.Fatal(err)
	}
	// This agent runs in no pane of the daemon, so scope own reaches no
	// session at all: the daemon refuses the call, and nothing is typed.
	p := payload(rpc(t, make(), runVerb(1, "pane.send-text", map[string]any{"session": "other", "window": made.Window, "text": "x"}))[0])
	if p["status"] == "done" || !strings.Contains(toJSON(p), "forbidden") {
		t.Fatalf("an agent outside the pane's scope typed into another session: %v", p)
	}
	// The owner is never limited.
	var capture map[string]any
	if err := client.Call(ctx, "capture-pane", map[string]any{"session": "other", "window": made.Window}, &capture); err != nil {
		t.Fatal(err)
	}
}

// A value mtm spells differently from the daemon is mapped by the row's Wire
// table, and every mapped value is one the parameter accepts.
func TestWireParamValuesMapToTheDaemonSpelling(t *testing.T) {
	t.Parallel()
	for _, w := range wireVerbs {
		for _, p := range w.Params {
			for from, to := range p.Wire {
				if len(p.Enum) > 0 && !slices.Contains(p.Enum, from) {
					t.Errorf("%s %s: Wire maps %q, which is not in its enum %v", w.key(), p.Name, from, p.Enum)
				}
				if got := p.wireValue(from); got != to {
					t.Errorf("%s %s: %q went to the daemon as %v, want %q", w.key(), p.Name, from, got, to)
				}
			}
			if got := p.wireValue(42); got != 42 {
				t.Errorf("%s %s: a non-string value changed: %v", w.key(), p.Name, got)
			}
		}
	}
	for _, w := range wireVerbs {
		if w.key() != "pane.switch" {
			continue
		}
		for _, p := range w.Params {
			if p.Name == "relative" && (p.wireValue("previous") != "prev" || p.wireValue("next") != "next" || p.wireValue("prev") != "prev" || !slices.Contains(p.Enum, "prev")) {
				t.Errorf("pane.switch relative: previous -> %v, next -> %v", p.wireValue("previous"), p.wireValue("next"))
			}
		}
	}
}
