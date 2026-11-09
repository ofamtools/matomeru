//go:build !nodaemon

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/agenthook"
)

// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestWindowAndSessionVerbsRunAgainstASelfDaemon(t *testing.T) {
	make, client := selfDaemonApp(t, paneScopeAll)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second*raceSlowdown)
	defer cancel()
	_ = ctx

	made := resultMap(t, rpc(t, make(), runVerb(1, "session.new", map[string]any{"name": "wm", "cwd": "/tmp", "command": []any{"cat"}}))[0])
	if made["session"] != "wm" || made["window_id"] == nil {
		t.Fatalf("session.new: %v", made)
	}
	base := map[string]any{"session": "wm"}
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
	// A second session of the same name is refused by the daemon.
	if p := payload(rpc(t, make(), runVerb(1, "session.new", map[string]any{"name": "wm"}))[0]); p["status"] == "done" {
		t.Errorf("session.new reused a name: %v", p)
	}

	opened := resultMap(t, rpc(t, make(), runVerb(1, "pane.open", with(map[string]any{"name": "job", "command": []any{"cat"}, "focus": false})))[0])
	win, _ := opened["window_id"].(string)
	if win == "" {
		t.Fatalf("pane.open: %v", opened)
	}
	// A command that ends reports its exit code to a caller that waits.
	ended := resultMap(t, rpc(t, make(), runVerb(1, "pane.open", with(map[string]any{"name": "ends", "command": []any{"sh", "-c", "exit 3"}, "keep_open": true, "wait": true, "timeout": 5000})))[0])
	if ended["exit_code"] != float64(3) {
		t.Errorf("pane.open --wait: %v", ended)
	}

	resps := rpc(t, make(),
		runVerb(1, "pane.show", with(map[string]any{"window": win})),
		runVerb(2, "pane.resize", with(map[string]any{"window": win, "width": 100, "height": 30})),
		runVerb(3, "pane.switch", with(map[string]any{"window": win})),
		runVerb(4, "pane.send-text", with(map[string]any{"window": win, "text": "\x1b[31mred\x1b[0m", "submit": true})),
		runVerb(5, "pane.wait", with(map[string]any{"window": win, "condition": "window-output", "pattern": "red", "timeout": 5000})),
		runVerb(6, "pane.capture", with(map[string]any{"window": win, "ansi": true})),
	)
	if s := resultMap(t, resps[0]); !strings.Contains(toJSON(s), win) {
		t.Errorf("pane.show lacks %s: %v", win, s)
	}
	for i := 1; i <= 4; i++ {
		_ = resultMap(t, resps[i])
	}
	if c := resultMap(t, resps[5]); !strings.Contains(toJSON(c), "red") {
		t.Errorf("pane.capture ansi: %v", c)
	}

	// Relative moves: mtm's previous reaches the daemon as its prev.
	for i, rel := range []string{"next", "previous", "prev"} {
		r := payload(rpc(t, make(), runVerb(i+1, "pane.switch", with(map[string]any{"relative": rel})))[0])
		if r["status"] != "done" {
			t.Errorf("pane.switch --relative %s: %v", rel, r)
		}
	}

	// No agent runs there: queue-prompt reaches the daemon and is refused by it.
	q := payload(rpc(t, make(), runVerb(1, "agent.enqueue", with(map[string]any{"window": win, "text": "next"})))[0])
	if q["status"] != "done" && !strings.Contains(toJSON(q), "queue-prompt:") {
		t.Errorf("agent.enqueue did not reach the daemon: %v", q)
	}

	// Closing asks the owner: an agent's call is refused, and nothing closes.
	c := payload(rpc(t, make(), runVerb(1, "pane.close", with(map[string]any{"window": win})))[0])
	if c["status"] == "done" {
		t.Errorf("an agent closed a window unasked: %v", c)
	}
	still := resultMap(t, rpc(t, make(), runVerb(1, "pane.show", with(map[string]any{"window": win})))[0])
	if !strings.Contains(toJSON(still), win) {
		t.Errorf("the window went away: %v", still)
	}
	// The owner closes it, as the CLI would: the same daemon verb.
	if err := client.Call(ctx, "close-window", with(map[string]any{"window": win}), nil); err != nil {
		t.Fatal(err)
	}

	g := resultMap(t, rpc(t, make(), runVerb(1, "agent.guide", map[string]any{"topic": "panes"}))[0])
	if text, _ := g["text"].(string); !strings.Contains(text, "pane.open") {
		t.Errorf("agent.guide panes: %v", g)
	}
	if all := resultMap(t, rpc(t, make(), runVerb(1, "agent.guide", map[string]any{"all": true}))[0]); !strings.Contains(toJSON(all["shown"]), "events") {
		t.Errorf("agent.guide all: %v", all)
	}
}

// pane.show and agent.status name the transcript the pane's agent hook
// reported (the daemon holds the path in memory only and does not say it).
//
// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestPaneShowAndAgentStatusNameTheTranscript(t *testing.T) {
	make, client := selfDaemonApp(t, paneScopeAll)
	made := resultMap(t, rpc(t, make(), runVerb(1, "session.new", map[string]any{"name": "tr", "cwd": "/tmp", "command": []any{"cat"}}))[0])
	win, _ := made["window_id"].(string)
	if win == "" {
		t.Fatalf("session.new: %v", made)
	}
	const path = "/synthetic/projects/c1.jsonl"
	// What mtm agent hook leaves beside the daemon's socket.
	if err := os.WriteFile(agenthook.TranscriptStamp(filepath.Dir(client.Socket), win), []byte(path), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"pane.show", "agent.status"} {
		r := payload(rpc(t, make(), runVerb(1, verb, map[string]any{"session": "tr", "window": win}))[0])
		if !strings.Contains(toJSON(r), path) {
			t.Errorf("%s does not name the transcript: %v", verb, r)
		}
	}
}
