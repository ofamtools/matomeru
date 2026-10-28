package turns

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic subagent records in the shapes Claude Code writes (agent.go).
// The notification is a real one's shape (2026-10-28) with its text
// replaced.

func agentCall(uuid, ts, id, desc string, background bool) string {
	return assistant(uuid, ts, "tool_use", toolUse(id, "Agent", map[string]any{
		"description": desc, "subagent_type": "general-purpose", "model": "opus",
		"run_in_background": background, "prompt": "BRIEF: " + desc,
	}))
}

func withToolUseResult(line string, tr map[string]any) string {
	var r map[string]any
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		panic(err)
	}
	r["toolUseResult"] = tr
	return rec(r)
}

func launched(uuid, ts, id, agent string) string {
	return withToolUseResult(toolResult(uuid, ts, id, "Async agent launched successfully.\nagentId: "+agent+" (internal ID)\nThe agent is working in the background.", false),
		map[string]any{"isAsync": true, "status": "async_launched", "agentId": agent, "resolvedModel": "claude-opus-5-5"})
}

// RealNotification is a task notification in the shape Claude Code
// writes, the text replaced.
func RealNotification(task, call, status, summary, result string) string {
	return "<task-notification>\n<task-id>" + task + "</task-id>\n<tool-use-id>" + call + "</tool-use-id>\n" +
		"<output-file>/private/tmp/claude-501/-w-proj/s1/tasks/" + task + ".output</output-file>\n" +
		"<status>" + status + "</status>\n<summary>" + summary + "</summary>\n" +
		"<note>A task-notification fires each time this agent stops with no live background children of its own.</note>\n" +
		"<result>" + result + "</result>\n" +
		"<usage><subagent_tokens>204501</subagent_tokens><tool_uses>94</tool_uses><duration_ms>1243026</duration_ms></usage>\n</task-notification>"
}

func notify(uuid, ts, body string) string {
	return rec(map[string]any{"type": "user", "uuid": uuid, "timestamp": ts, "sessionId": "s1", "promptSource": "system",
		"origin":  map[string]any{"kind": "task-notification"},
		"message": map[string]any{"role": "user", "content": body}})
}

func agentOf(t *testing.T, turn Turn) *Agent {
	t.Helper()
	for _, s := range turn.Steps {
		if s.Agent != nil {
			return s.Agent
		}
	}
	t.Fatalf("turn %s has no agent step: %+v", turn.ID, turn.Steps)
	return nil
}

func TestForegroundAgent(t *testing.T) {
	got := parse(
		userPrompt("u1", "2026-10-28T10:00:00Z", "audit it", "typed"),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_f", "Audit the rail", false),
		withToolUseResult(toolResult("r1", "2026-10-28T10:02:01Z", "toolu_f", "The rail is fine.\nagentId: af1 (use SendMessage to continue)\n<usage>total_tokens: 1200\ntool_uses: 3\nduration_ms: 120000</usage>", false),
			map[string]any{"status": "completed", "agentId": "af1", "totalDurationMs": 120000, "totalTokens": 1200, "totalToolUseCount": 3}),
		assistant("a2", "2026-10-28T10:02:02Z", "end_turn", text("Done.")),
	)
	if len(got) != 1 {
		t.Fatalf("turns %+v", got)
	}
	a := agentOf(t, got[0])
	if a.State != AgentDone || a.Background || a.ID != "af1" || a.Result != "The rail is fine." || a.Tokens != 1200 || a.ToolUses != 3 || a.Duration != 2*time.Minute {
		t.Fatalf("agent %+v", a)
	}
	if a.Description != "Audit the rail" || a.Type != "general-purpose" || a.Model != "opus" || a.Brief != "BRIEF: Audit the rail" {
		t.Fatalf("call %+v", a)
	}
	if s := got[0].Steps[0]; s.Summary != "Audit the rail" {
		t.Fatalf("summary %q", s.Summary)
	}
}

// A background agent started in turn 1 ends two turns later: its
// notification opens a turn of its own (an event, its prompt the summary,
// never the XML) that links back to the call, whose agent turns done.
func TestBackgroundAgentFinishesTwoTurnsLater(t *testing.T) {
	got := parse(
		userPrompt("u1", "2026-10-28T10:00:00Z", "start it", "typed"),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_b", "One-line bindings export format", true),
		launched("r1", "2026-10-28T10:00:02Z", "toolu_b", "ab1"),
		assistant("a2", "2026-10-28T10:00:03Z", "end_turn", text("Started.")),
		duration("d1", "2026-10-28T10:00:04Z", 4000),
		userPrompt("u2", "2026-10-28T10:01:00Z", "something else", "typed"),
		assistant("a3", "2026-10-28T10:01:01Z", "end_turn", text("Ok.")),
		duration("d2", "2026-10-28T10:01:02Z", 2000),
		notify("n1", "2026-10-28T10:20:00Z", RealNotification("ab1", "toolu_b", "completed", `Agent "One-line bindings export format" finished`, "# Report\n\nAll done.")),
		assistant("a4", "2026-10-28T10:20:01Z", "end_turn", text("The agent finished.")),
		duration("d3", "2026-10-28T10:20:02Z", 2000),
	)
	if len(got) != 3 {
		t.Fatalf("%d turns: %+v", len(got), got)
	}
	a := agentOf(t, got[0])
	if a.State != AgentDone || !a.Background || a.Result != "# Report\n\nAll done." || a.Tokens != 204501 || a.ToolUses != 94 || a.Notice != "n1" {
		t.Fatalf("agent %+v", a)
	}
	n := got[2].Notice
	if n == nil || n.Turn != "u1" || n.ToolUseID != "toolu_b" || n.Title != "One-line bindings export format" || n.State != "finished" || n.Duration != 1243026*time.Millisecond {
		t.Fatalf("notice %+v", n)
	}
	if strings.Contains(got[2].Prompt, "<") || got[2].Prompt != `Agent "One-line bindings export format" finished` {
		t.Fatalf("prompt %q", got[2].Prompt)
	}
	if got[1].Notice != nil || got[1].Prompt != "something else" {
		t.Fatalf("turn 2 %+v", got[1])
	}
}

func TestFailedAgent(t *testing.T) {
	got := parse(
		userPrompt("u1", "2026-10-28T10:00:00Z", "start it", "typed"),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_x", "L4 on fake server", true),
		launched("r1", "2026-10-28T10:00:02Z", "toolu_x", "ax1"),
		assistant("a2", "2026-10-28T10:00:03Z", "end_turn", text("Started.")),
		duration("d1", "2026-10-28T10:00:04Z", 4000),
		notify("n1", "2026-10-28T10:05:00Z", RealNotification("ax1", "toolu_x", "failed", `Agent "L4 on fake server" failed: Agent terminated early due to an API error`, "")),
	)
	a := agentOf(t, got[0])
	if a.State != AgentFailed || !strings.Contains(a.Summary, "API error") {
		t.Fatalf("agent %+v", a)
	}
	if n := got[1].Notice; n == nil || n.State != "failed: Agent terminated early due to an API error" || n.Title != "L4 on fake server" {
		t.Fatalf("notice %+v", n)
	}
	// A foreground call whose result is an error failed too.
	got = parse(
		userPrompt("u1", "2026-10-28T10:00:00Z", "go", "typed"),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_e", "Broken", false),
		toolResult("r1", "2026-10-28T10:00:02Z", "toolu_e", "agent crashed", true),
	)
	if a := agentOf(t, got[0]); a.State != AgentFailed || a.Result != "agent crashed" {
		t.Fatalf("foreground failure %+v", a)
	}
}

// An interim notification (waiting on its own background work) and the
// final one fold into one card with the latest state; the second answer's
// turn keeps RepeatOf.
func TestRepeatedNotificationsFoldIntoOneCard(t *testing.T) {
	got := parse(
		userPrompt("u1", "2026-10-28T10:00:00Z", "start it", "typed"),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_r", "Lead", true),
		launched("r1", "2026-10-28T10:00:02Z", "toolu_r", "ar1"),
		assistant("a2", "2026-10-28T10:00:03Z", "end_turn", text("Started.")),
		duration("d1", "2026-10-28T10:00:04Z", 4000),
		notify("n1", "2026-10-28T10:05:00Z", RealNotification("ar1", "toolu_r", "completed", `Agent "Lead" is waiting on its own background work`, "waiting")),
		assistant("a3", "2026-10-28T10:05:01Z", "end_turn", text("Still going.")),
		duration("d2", "2026-10-28T10:05:02Z", 2000),
		notify("n2", "2026-10-28T10:09:00Z", RealNotification("ar1", "toolu_r", "completed", `Agent "Lead" finished`, "final report")),
		assistant("a4", "2026-10-28T10:09:01Z", "end_turn", text("Finished.")),
		duration("d3", "2026-10-28T10:09:02Z", 2000),
	)
	if len(got) != 3 {
		t.Fatalf("%d turns", len(got))
	}
	first, repeat := got[1].Notice, got[2].Notice
	if first == nil || first.State != "finished" || first.Result != "final report" || first.Repeats != 1 {
		t.Fatalf("first card %+v", first)
	}
	if repeat == nil || repeat.RepeatOf != "n1" {
		t.Fatalf("repeat %+v", repeat)
	}
	if a := agentOf(t, got[0]); a.State != AgentDone || a.Result != "final report" {
		t.Fatalf("agent %+v", a)
	}
}

// A notification delivered while a turn runs is an event of it, not a
// steer; one in the same record as the person's message leaves the
// message the real prompt.
func TestNotificationBesideThePersonsMessage(t *testing.T) {
	body := RealNotification("ak1", "toolu_k", "completed", `Agent "Side job" finished`, "side done") + "\n\nnow fix the docs"
	got := parse(
		userPrompt("u1", "2026-10-28T10:00:00Z", "start it", "typed"),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_k", "Side job", true),
		launched("r1", "2026-10-28T10:00:02Z", "toolu_k", "ak1"),
		assistant("a2", "2026-10-28T10:00:03Z", "end_turn", text("Started.")),
		duration("d1", "2026-10-28T10:00:04Z", 4000),
		userPrompt("u2", "2026-10-28T10:01:00Z", body, "typed"),
		assistant("a3", "2026-10-28T10:01:01Z", "tool_use", text("Working.")),
		queued("q1", "2026-10-28T10:01:02Z", RealNotification("bq1", "toolu_none", "completed", `Background command "go test" completed (exit code 0)`, "")),
		assistant("a4", "2026-10-28T10:01:03Z", "end_turn", text("Docs fixed.")),
	)
	if len(got) != 2 {
		t.Fatalf("%d turns", len(got))
	}
	if got[1].Prompt != "now fix the docs" || got[1].Notice != nil || len(got[1].Steers) != 0 {
		t.Fatalf("turn 2 %+v", got[1])
	}
	if len(got[1].Events) != 2 || got[1].Events[0].Title != "Side job" || got[1].Events[1].Title != "go test" || got[1].Events[1].State != "completed (exit code 0)" {
		t.Fatalf("events %+v", got[1].Events)
	}
	if a := agentOf(t, got[0]); a.State != AgentDone || a.Notice != "u2" {
		t.Fatalf("agent %+v", a)
	}
	// The person's message while a notification's turn runs is a turn.
	got = parse(
		notify("n1", "2026-10-28T10:00:00Z", RealNotification("ab", "toolu_q", "completed", `Agent "Q" finished`, "q")),
		assistant("a1", "2026-10-28T10:00:01Z", "tool_use", text("Reading.")),
		userPrompt("u1", "2026-10-28T10:00:02Z", "and this", "typed"),
	)
	if len(got) != 2 || got[0].Notice == nil || got[1].Prompt != "and this" {
		t.Fatalf("turns %+v", got)
	}
}

// An older log's subagent records (isSidechain) are the agent's turns; a
// sidechain's end_turn never ends the parent's turn (tuios's rule).
func TestSidechainIsTheAgentsAndNeverEndsTheTurn(t *testing.T) {
	side := func(uuid, ts, typ string, m map[string]any) string {
		return rec(map[string]any{"type": typ, "uuid": uuid, "timestamp": ts, "sessionId": "s1", "isSidechain": true, "message": m})
	}
	var p ClaudeParser
	for _, l := range []string{
		userPrompt("u1", "2026-10-28T10:00:00Z", "go", "typed"),
		rec(map[string]any{"type": "assistant", "uuid": "a1", "timestamp": "2026-10-28T10:00:01Z", "sessionId": "s1",
			"message": map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{toolUse("toolu_t", "Task", map[string]any{"description": "Old task", "prompt": "look"})}}}),
		side("s1", "2026-10-28T10:00:02Z", "user", map[string]any{"role": "user", "content": "look"}),
		side("s2", "2026-10-28T10:00:03Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{toolUse("st1", "Grep", map[string]any{"pattern": "x"})}}),
		side("s3", "2026-10-28T10:00:04Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{text("SECRET-SUBAGENT")}}),
	} {
		p.Feed([]byte(l))
	}
	got := p.Turns()
	if len(got) != 1 || got[0].State != StateRunning || got[0].Final != "" {
		t.Fatalf("the sidechain ended or wrote the turn: %+v", got)
	}
	a := agentOf(t, got[0])
	if len(a.Inline) != 1 || a.Inline[0].Prompt != "look" || a.Inline[0].Final != "SECRET-SUBAGENT" || len(a.Inline[0].Steps) != 2 {
		t.Fatalf("inline %+v", a.Inline)
	}
	AgentMetas{}.Enrich(got)
	if agentOf(t, got[0]).Missing {
		t.Fatal("an inline transcript is not missing")
	}
}

// sessionDir writes a session log and its subagents directory: metas
// (with or without their logs) and the agents' own logs.
type fixtureAgent struct {
	id, call, desc string
	depth          int
	parent         string
	log            []string // nil: no transcript on disk
}

func sessionDir(t *testing.T, main []string, agents ...fixtureAgent) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(log, []byte(strings.Join(main, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := SubagentDir(log)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		meta := map[string]any{"agentType": "general-purpose", "description": a.desc, "toolUseId": a.call, "spawnDepth": a.depth, "requestShape": "background", "model": "opus", "finished": true}
		if a.parent != "" {
			meta["parentAgentId"] = a.parent
		}
		if err := os.WriteFile(filepath.Join(sub, "agent-"+a.id+".meta.json"), []byte(rec(meta)), 0o644); err != nil {
			t.Fatal(err)
		}
		if a.log != nil {
			if err := os.WriteFile(filepath.Join(sub, "agent-"+a.id+".jsonl"), []byte(strings.Join(a.log, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return log
}

func sideRec(agent, uuid, ts, typ string, m map[string]any) string {
	return rec(map[string]any{"type": typ, "uuid": uuid, "timestamp": ts, "sessionId": "s1", "isSidechain": true, "agentId": agent, "message": m})
}

// A depth-2 agent: the session's agent starts one of its own; its call is
// in the first agent's log, its meta (parentAgentId) in the same flat
// directory. A meta without its log is a missing transcript.
func TestNestedAgentAndMissingTranscript(t *testing.T) {
	log := sessionDir(t, []string{
		userPrompt("u1", "2026-10-28T10:00:00Z", "go", "typed"),
		agentCall("a1", "2026-10-28T10:00:01Z", "toolu_1", "Lead", true),
		launched("r1", "2026-10-28T10:00:02Z", "toolu_1", "a1x"),
		agentCall("a2", "2026-10-28T10:00:03Z", "toolu_m", "Gone", true),
		launched("r2", "2026-10-28T10:00:04Z", "toolu_m", "amx"),
		agentCall("a3", "2026-10-28T10:00:05Z", "toolu_n", "No meta", true),
	},
		fixtureAgent{id: "a1x", call: "toolu_1", desc: "Lead", depth: 1, log: []string{
			sideRec("a1x", "s1", "2026-10-28T10:00:02Z", "user", map[string]any{"role": "user", "content": "BRIEF: Lead"}),
			sideRec("a1x", "s2", "2026-10-28T10:00:03Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{toolUse("toolu_2", "Agent", map[string]any{"description": "Child", "subagent_type": "Explore", "prompt": "BRIEF: Child"})}}),
			sideRec("a1x", "s3", "2026-10-28T10:00:09Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{text("lead done")}}),
		}},
		fixtureAgent{id: "a2x", call: "toolu_2", desc: "Child", depth: 2, parent: "a1x", log: []string{
			sideRec("a2x", "c1", "2026-10-28T10:00:04Z", "user", map[string]any{"role": "user", "content": "BRIEF: Child"}),
		}},
		fixtureAgent{id: "amx", call: "toolu_m", desc: "Gone", depth: 1},
	)
	l := NewClaudeLog(log, "s1")
	if _, err := l.Read(); err != nil {
		t.Fatal(err)
	}
	got := l.Turns()
	var lead, gone, nometa *Agent
	for _, s := range got[0].Steps {
		switch s.ToolID {
		case "toolu_1":
			lead = s.Agent
		case "toolu_m":
			gone = s.Agent
		case "toolu_n":
			nometa = s.Agent
		}
	}
	if lead == nil || lead.Missing || lead.Transcript == "" || lead.State != AgentDone {
		t.Fatalf("lead %+v", lead)
	}
	if gone == nil || !gone.Missing || gone.Transcript != "" {
		t.Fatalf("gone %+v", gone)
	}
	if nometa == nil || !nometa.Missing || nometa.State != AgentRunning {
		t.Fatalf("no meta %+v", nometa)
	}
	var logs AgentLogs
	tr, err := logs.Read(lead.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 1 || tr.Turns[0].Prompt != "BRIEF: Lead" || tr.Turns[0].Final != "lead done" {
		t.Fatalf("lead's turns %+v", tr.Turns)
	}
	child := agentOf(t, tr.Turns[0])
	if child.Depth != 2 || child.Parent != "a1x" || child.Type != "Explore" || child.Transcript == "" || child.Missing {
		t.Fatalf("child %+v", child)
	}
	// Read again unchanged: from the cache.
	if _, err := logs.Read(lead.Transcript); err != nil || logs.Reads() != 1 {
		t.Fatalf("reads %d err %v", logs.Reads(), err)
	}
}

// A large subagent log is read from its end: the answer shows, earlier
// steps are cut and the transcript says so; what is appended is read
// from where the last read stopped.
func TestLargeAgentLogIsTailRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s1", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-big.jsonl")
	var b strings.Builder
	b.WriteString(sideRec("big", "p", "2026-10-28T10:00:00Z", "user", map[string]any{"role": "user", "content": "BRIEF"}) + "\n")
	pad := strings.Repeat("x", 4000)
	for i := 0; b.Len() < AgentTailBytes*2; i++ {
		b.WriteString(sideRec("big", fmt.Sprintf("t%d", i), "2026-10-28T10:00:01Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{toolUse(fmt.Sprintf("b%d", i), "Bash", map[string]any{"command": pad})}}) + "\n")
	}
	b.WriteString(sideRec("big", "end", "2026-10-28T10:00:02Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{text("big answer")}}) + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var logs AgentLogs
	tr, err := logs.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Truncated || len(tr.Turns) != 1 || tr.Turns[0].Final != "big answer" || tr.Turns[0].Prompt != "" {
		t.Fatalf("tail read: truncated %v, %d turns", tr.Truncated, len(tr.Turns))
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(sideRec("big", "more", "2026-10-28T10:00:03Z", "user", map[string]any{"role": "user", "content": "again"}) + "\n")
	f.Close()
	tr, err = logs.Read(path)
	if err != nil || len(tr.Turns) != 2 || tr.Turns[1].Prompt != "again" || logs.Reads() != 2 {
		t.Fatalf("append: %d turns, reads %d, err %v", len(tr.Turns), logs.Reads(), err)
	}
}
