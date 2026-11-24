package daemon

import (
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestTelemetryStickyTranscriptAndRecentTurnsRing(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "claude worker", "harness": "claude"},
	}, core.Actor{Kind: "user", ID: "owner"})
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{Store: store, DisableMCP: true, SchedulerInterval: -1})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Initial prompt event carries transcript_path and session_id.
	resp, _ := srv.handleRequest(Request{
		Op:      OpHookEvent,
		PaneID:  pane.ID,
		Harness: "claude",
		Kind:    HookKindState,
		State:   StateRunning,
		Prompt:  "Fix auth with API_KEY=supersecret999",
		Payload: map[string]any{
			"transcript_path": "/tmp/claude-session-1.jsonl",
			"session_id":      "sess-001",
		},
	})
	if !resp.OK {
		t.Fatalf("hook_event 1 failed: %+v", resp)
	}

	// Verify SQLite KindPane was patched with transcriptPath and sessionId.
	patched, err := store.Get(pane.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if patched.Text("transcriptPath") != "/tmp/claude-session-1.jsonl" || patched.Text("sessionId") != "sess-001" {
		t.Fatalf("pane fields = transcriptPath:%q sessionId:%q", patched.Text("transcriptPath"), patched.Text("sessionId"))
	}
	revAfterFirst := patched.ChangeSetNumber

	// 2. Subsequent tool activity event has NO transcript_path/session_id — sticky metadata must survive!
	resp, _ = srv.handleRequest(Request{
		Op:      OpHookEvent,
		PaneID:  pane.ID,
		Harness: "claude",
		Kind:    HookKindActivity,
		State:   StateRunning,
		Tool:    "Read",
		Summary: "Read: main.go",
		Payload: map[string]any{"tool_name": "Read"},
	})
	if !resp.OK {
		t.Fatalf("hook_event 2 failed: %+v", resp)
	}

	// Same transcriptPath/sessionId again must NOT create a redundant SQLite write.
	resp, _ = srv.handleRequest(Request{
		Op:       OpHookEvent,
		PaneID:   pane.ID,
		Harness:  "claude",
		Kind:     HookKindState,
		State:    StateDone,
		Response: "Fixed auth safely",
		Payload: map[string]any{
			"transcript_path": "/tmp/claude-session-1.jsonl",
			"session_id":      "sess-001",
		},
	})
	if !resp.OK {
		t.Fatalf("hook_event 3 failed: %+v", resp)
	}
	afterSame, _ := store.Get(pane.Ref())
	if afterSame.ChangeSetNumber != revAfterFirst {
		t.Fatalf("redundant pane patch on unchanged metadata: rev %d -> %d", revAfterFirst, afterSame.ChangeSetNumber)
	}

	tel, _ := srv.telemetry.snapshot(pane.ID)
	pt := tel[pane.ID]
	if pt == nil {
		t.Fatal("missing PaneTelemetry")
	}
	if pt.TranscriptPath != "/tmp/claude-session-1.jsonl" || pt.SessionID != "sess-001" {
		t.Fatalf("sticky metadata wiped: transcriptPath=%q sessionId=%q", pt.TranscriptPath, pt.SessionID)
	}
	if len(pt.RecentTurns) != 2 {
		t.Fatalf("RecentTurns len = %d, want 2: %+v", len(pt.RecentTurns), pt.RecentTurns)
	}
	if pt.RecentTurns[0].Prompt != "Fix auth with API_KEY=***" {
		t.Fatalf("prompt not sanitized: %q", pt.RecentTurns[0].Prompt)
	}
	if pt.RecentTurns[1].Response != "Fixed auth safely" {
		t.Fatalf("response = %q", pt.RecentTurns[1].Response)
	}

	// 3. Bounded ring caps at maxRecentTurns (50) retaining newest turns.
	now := time.Now()
	for i := range 70 {
		srv.telemetry.recordEvent(HookEvent{
			PaneID: pane.ID,
			Kind:   HookKindState,
			Prompt: "turn-" + string(rune('A'+(i%26))) + "-" + time.Duration(i).String(),
		}, now.Add(time.Duration(i)*time.Second))
	}
	tel2, _ := srv.telemetry.snapshot(pane.ID)
	if got := len(tel2[pane.ID].RecentTurns); got != maxRecentTurns {
		t.Fatalf("bounded RecentTurns len = %d, want %d", got, maxRecentTurns)
	}
}
