package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

// historyLogs lists the daemon's logs by id, straight from the daemon.
func historyLogs(t *testing.T, ctx context.Context, client *daemon.Client) map[string]map[string]any {
	t.Helper()
	var res struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := client.Call(ctx, "list-history-logs", map[string]any{}, &res); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, l := range res.Logs {
		out[l["log"].(string)] = l
	}
	return out
}

// serial: starts a real daemon (this test binary behind daemonWord) and sets process environment.
func TestHistoryLogVerbsRunAgainstASelfDaemon(t *testing.T) {
	make, client := selfDaemonApp(t, paneScopeAll)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second*raceSlowdown)
	defer cancel()
	app := make()

	var made struct {
		Window string `json:"window_id"`
	}
	if err := client.Call(ctx, "new-session", map[string]any{"name": "hl", "cwd": "/tmp", "command": []string{"sh"}}, &made); err != nil {
		t.Fatal(err)
	}
	id := "hl/" + made.Window
	// A flood past the in-memory ring: the rows it lets go go to the log.
	const rows = 40000
	script := fmt.Sprintf("i=0; while [ $i -lt %d ]; do seq $((i+1)) $((i+5000)); i=$((i+5000)); sleep 0.25; done; echo done-%d\n", rows, rows)
	if err := client.Call(ctx, "send-text", map[string]any{"session": "hl", "window": made.Window, "text": script}, nil); err != nil {
		t.Fatal(err)
	}
	var live map[string]any
	for end := time.Now().Add(90 * time.Second * raceSlowdown); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		p := payload(rpc(t, app, runVerb(1, "history-log.list", map[string]any{"state": "live"}))[0])
		if p["status"] != "done" {
			t.Fatalf("history-log.list: %v", p)
		}
		res, _ := p["result"].(map[string]any)
		for _, l := range res["logs"].([]any) {
			if m := l.(map[string]any); m["log"] == id && m["rows"].(float64) >= 20000 {
				live = m
			}
		}
		if live != nil {
			break
		}
	}
	if live == nil {
		t.Fatalf("the pane's log never held the rows past the ring: %v", historyLogs(t, ctx, client))
	}
	if live["state"] != "live" || live["label"] == "" || live["label"] == nil {
		t.Fatalf("a live log row lacks its state or label: %v", live)
	}
	shown := resultMap(t, rpc(t, app, runVerb(2, "history-log.show", map[string]any{"log": id}))[0])
	if shown["window"] != made.Window {
		t.Fatalf("show: %v", shown)
	}

	// A live log cannot be kept.
	if p := payload(rpc(t, app, runVerb(3, "history-log.keep", map[string]any{"log": id}))[0]); p["status"] == "done" {
		t.Fatalf("kept a live log: %v", p)
	}

	if err := client.Call(ctx, "close-window", map[string]any{"session": "hl", "window": made.Window}, nil); err != nil {
		t.Fatal(err)
	}
	orphan := historyLogs(t, ctx, client)[id]
	if orphan == nil || orphan["state"] != "orphaned" {
		t.Fatalf("after the close: %v", orphan)
	}

	// Keep and unkeep, through mtm.
	if kept := resultMap(t, rpc(t, app, runVerb(4, "history-log.keep", map[string]any{"log": id}))[0]); kept["keep"] != true {
		t.Fatalf("keep: %v", kept)
	}
	if un := resultMap(t, rpc(t, app, runVerb(5, "history-log.unkeep", map[string]any{"log": id}))[0]); un["keep"] == true {
		t.Fatalf("unkeep: %v", un)
	}

	// An agent's purge is always a dry run, even when it asks for the real one.
	dry := resultMap(t, rpc(t, app, runVerb(6, "history-log.purge", map[string]any{"all_orphaned": true, "dry_run": false}))[0])
	if dry["dry_run"] != true || dry["confirm"] == "" || historyLogs(t, ctx, client)[id] == nil {
		t.Fatalf("an agent's purge was not a dry run, or deleted: %v", dry)
	}
	// Even with the token a dry run printed.
	tok, _ := dry["confirm"].(string)
	again := resultMap(t, rpc(t, app, runVerb(7, "history-log.purge", map[string]any{"all_orphaned": true, "confirm": tok}))[0])
	if again["dry_run"] != true || historyLogs(t, ctx, client)[id] == nil {
		t.Fatalf("an agent purged with the token: %v", again)
	}

	for verb, want := range map[string]string{"history-log.adopt": ochazuke.AgentConfirm, "pane.adopt-history": ochazuke.AgentConfirm, "history-log.list": ochazuke.AgentAllow} {
		if op, ok := app.r.Operation(verb); !ok || op.Agent != want {
			t.Errorf("%s: agent right %q, want %q", verb, op.Agent, want)
		}
	}
	// Adopting moves a log onto a pane: the owner approves it from an agent,
	// from either noun, and nothing moved.
	for i, c := range []struct {
		verb string
		args map[string]any
	}{
		{"history-log.adopt", map[string]any{"log": id, "window": "nope"}},
		{"pane.adopt-history", map[string]any{"log": id, "window": "nope"}},
	} {
		p := payload(rpc(t, app, runVerb(8+i, c.verb, c.args))[0])
		if p["status"] == "done" || historyLogs(t, ctx, client)[id] == nil {
			t.Fatalf("%s ran without the owner: %v", c.verb, p)
		}
	}

	// The owner (not an agent) deletes by the token, through the daemon.
	var res map[string]any
	if err := client.Call(ctx, "purge-history-logs", map[string]any{"all_orphaned": true, "confirm": tok}, &res); err != nil {
		t.Fatal(err)
	}
	if historyLogs(t, ctx, client)[id] != nil {
		t.Fatalf("the owner's purge left the log: %v", res)
	}
	if !strings.Contains(fmt.Sprint(res["purged"]), id) {
		t.Fatalf("purged: %v", res)
	}
}
