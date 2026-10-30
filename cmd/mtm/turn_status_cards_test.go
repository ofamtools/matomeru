package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// The status word of every Turns card goes through statusWords: a
// background command's "completed (exit code 0)" is done (green), as is
// a monitor's "stream ended"; a failed command and a killed agent are
// bad (red); the running turn is working (blue). Before, a notification
// whose summary worded its state its own way ("completed (exit code 0)")
// matched no kind and its word was drawn muted like the rest of the
// line, on the owner's cards most often (1444 of them in a survey of
// real logs).
func TestTurnsCardStatusWordsAreToned(t *testing.T) {
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "claude-code", "working", "s1", "/w/proj"
	dir := filepath.Join(app.claudeProjects, turns.ClaudeProjectDir("/w/proj"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(dir, "s1.jsonl"),
		logPrompt(t, "u1", "2026-10-28T10:00:00Z", "start the work"),
		logAgentCall(t, "a1", "2026-10-28T10:00:01Z", "toolu_k", "Killed agent", true),
		logLaunched(t, "r1", "2026-10-28T10:00:02Z", "toolu_k", "ak1"),
		logAssistant(t, "a2", "2026-10-28T10:00:03Z", "end_turn", map[string]any{"type": "text", "text": "Started."}),
		logDone(t, "d1", "2026-10-28T10:00:04Z"),
		logNotification(t, "n1", "2026-10-28T10:01:00Z", realNotification("bc1", "toolu_cmd", "completed", `Background command "go test ./..." completed (exit code 0)`, "")),
		logAssistant(t, "a3", "2026-10-28T10:01:01Z", "end_turn", map[string]any{"type": "text", "text": "Tests pass."}),
		logDone(t, "d2", "2026-10-28T10:01:02Z"),
		logNotification(t, "n2", "2026-10-28T10:02:00Z", realNotification("bc2", "toolu_fail", "failed", `Background command "make gate" failed with exit code 144`, "")),
		logAssistant(t, "a4", "2026-10-28T10:02:01Z", "end_turn", map[string]any{"type": "text", "text": "Gate failed."}),
		logDone(t, "d3", "2026-10-28T10:02:02Z"),
		logNotification(t, "n3", "2026-10-28T10:03:00Z", realNotification("ak1", "toolu_k", "killed", `Agent "Killed agent" was stopped by Claude`, "")),
		logAssistant(t, "a5", "2026-10-28T10:03:01Z", "end_turn", map[string]any{"type": "text", "text": "Stopped it."}),
		logDone(t, "d4", "2026-10-28T10:03:02Z"),
		logNotification(t, "n4", "2026-10-28T10:04:00Z", realNotification("mo1", "toolu_mon", "completed", `Monitor "tail" stream ended`, "")),
		logAssistant(t, "a6", "2026-10-28T10:04:01Z", "end_turn", map[string]any{"type": "text", "text": "Monitor ended."}),
		logDone(t, "d5", "2026-10-28T10:04:02Z"),
		logPrompt(t, "u2", "2026-10-28T10:05:00Z", "keep going"),
	)
	reload(t, app)
	settle(app, key(t, app, "["))
	fr := frameWH(app, 140, 60)
	want := map[string][2]string{ // header prefix → status word, tone
		"⤷ go test ./...": {"✓ completed (exit code 0)", ochazuke.ToneDone},
		"⤷ make gate":     {"✗ failed with exit code 144", ochazuke.ToneErrored},
		"⤷ Killed agent":  {"⊘ stopped", ochazuke.ToneErrored},
		"⤷ tail":          {"✓ stream ended", ochazuke.ToneDone},
		"keep going":      {"● working", ochazuke.ToneWorking},
		"start the work":  {"✓ done", ochazuke.ToneDone},
	}
	seen := 0
	for _, c := range app.pv.cards.Cards() {
		for head, w := range want {
			if !strings.HasPrefix(c.Header, head) {
				continue
			}
			seen++
			if len(c.MetaTones) != 1 || c.MetaTones[0].Tone != w[1] {
				t.Fatalf("%q: tones %+v, want %s\nmeta %q", head, c.MetaTones, w[1], c.Meta)
			}
			sp := c.MetaTones[0]
			if got := string([]rune(c.Meta)[sp.From:sp.To]); got != w[0] {
				t.Fatalf("%q: toned %q, want %q (meta %q)", head, got, w[0], c.Meta)
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d of %d cards:\n%s", seen, len(want), fr)
	}
	if os.Getenv("MTM_FRAMES") != "" {
		t.Logf("frame:\n%s", fr)
	}
}
