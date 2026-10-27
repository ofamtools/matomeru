package turns

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic records in the shapes Claude Code writes (claude.go); no real
// session's content.

func rec(fields map[string]any) string {
	b, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func userPrompt(uuid, ts, text, source string) string {
	r := map[string]any{"type": "user", "uuid": uuid, "timestamp": ts, "sessionId": "s1",
		"message": map[string]any{"role": "user", "content": text}}
	if source != "" {
		r["promptSource"] = source
	}
	return rec(r)
}

func assistant(uuid, ts, stop string, blocks ...map[string]any) string {
	return rec(map[string]any{"type": "assistant", "uuid": uuid, "timestamp": ts, "sessionId": "s1",
		"message": map[string]any{"role": "assistant", "content": blocks, "stop_reason": stop}})
}

func text(s string) map[string]any     { return map[string]any{"type": "text", "text": s} }
func thinking(s string) map[string]any { return map[string]any{"type": "thinking", "thinking": s, "signature": "x"} }
func toolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func toolResult(uuid, ts, id, content string, isError bool) string {
	return rec(map[string]any{"type": "user", "uuid": uuid, "timestamp": ts, "sessionId": "s1",
		"message": map[string]any{"role": "user", "content": []map[string]any{
			{"type": "tool_result", "tool_use_id": id, "content": []map[string]any{{"type": "text", "text": content}}, "is_error": isError},
		}}})
}

func queued(uuid, ts, prompt string) string {
	return rec(map[string]any{"type": "attachment", "uuid": uuid, "timestamp": ts, "sessionId": "s1",
		"attachment": map[string]any{"type": "queued_command", "prompt": prompt, "commandMode": "prompt"}})
}

func duration(uuid, ts string, ms int64) string {
	return rec(map[string]any{"type": "system", "subtype": "turn_duration", "uuid": uuid, "timestamp": ts, "durationMs": ms})
}

func parse(lines ...string) []Turn {
	var p ClaudeParser
	for _, l := range lines {
		p.Feed([]byte(l))
	}
	return p.Turns()
}

func TestClaudeTurnWithThinkingToolsAndFinal(t *testing.T) {
	got := parse(
		rec(map[string]any{"type": "queue-operation", "operation": "enqueue"}),
		userPrompt("u1", "2026-10-27T10:00:00Z", "fix the tests", "typed"),
		rec(map[string]any{"type": "user", "uuid": "m1", "isMeta": true, "message": map[string]any{"content": "<system-reminder>x</system-reminder>"}}),
		assistant("a1", "2026-10-27T10:00:01Z", "tool_use", thinking("look at the failures")),
		assistant("a2", "2026-10-27T10:00:02Z", "tool_use", text("Running them.")),
		assistant("a3", "2026-10-27T10:00:03Z", "tool_use", toolUse("t1", "Bash", map[string]any{"command": "go test ./..."})),
		toolResult("r1", "2026-10-27T10:00:09Z", "t1", "FAIL x", true),
		assistant("a4", "2026-10-27T10:00:10Z", "end_turn", text("Fixed: **two** tests.")),
		duration("d1", "2026-10-27T10:00:11Z", 11000),
	)
	if len(got) != 1 {
		t.Fatalf("turns %+v", got)
	}
	tu := got[0]
	if tu.Prompt != "fix the tests" || tu.State != StateDone || tu.Final != "Fixed: **two** tests." || tu.Duration.Seconds() != 11 || tu.ID != "u1" {
		t.Fatalf("turn %+v", tu)
	}
	kinds := []StepKind{}
	for _, s := range tu.Steps {
		kinds = append(kinds, s.Kind)
	}
	if strings.Join(stepNames(kinds), ",") != "thinking,text,tool,text" {
		t.Fatalf("steps %v", kinds)
	}
	tool := tu.Steps[2]
	if tool.Tool != "Bash" || tool.Summary != "go test ./..." || !tool.HasResult || tool.Result != "FAIL x" || !tool.IsError {
		t.Fatalf("tool %+v", tool)
	}
}

func stepNames(k []StepKind) []string {
	out := make([]string, len(k))
	for i, s := range k {
		out[i] = string(s)
	}
	return out
}

// A message sent while the turn runs is a steer folded into it, whether
// it came as a queued_command attachment or a user record mid-loop; a
// queued follow-up after the turn ended is a turn of its own.
func TestClaudeSteersFoldAndFollowUpsStartTurns(t *testing.T) {
	got := parse(
		userPrompt("u1", "2026-10-27T10:00:00Z", "build it", "typed"),
		assistant("a1", "2026-10-27T10:00:01Z", "tool_use", toolUse("t1", "Read", map[string]any{"file_path": "/x/main.go"})),
		queued("q1", "2026-10-27T10:00:02Z", "use the v2 api"),
		toolResult("r1", "2026-10-27T10:00:03Z", "t1", "package main", false),
		userPrompt("u2", "2026-10-27T10:00:04Z", "and add a test", "typed"),
		assistant("a2", "2026-10-27T10:00:05Z", "end_turn", text("Done.")),
		duration("d1", "2026-10-27T10:00:06Z", 6000),
		userPrompt("u3", "2026-10-27T10:00:07Z", "now the docs", "queued"),
		assistant("a3", "2026-10-27T10:00:08Z", "end_turn", text("Docs done.")),
	)
	if len(got) != 2 {
		t.Fatalf("turns %d: %+v", len(got), got)
	}
	if len(got[0].Steers) != 2 || got[0].Steers[0].Text != "use the v2 api" || got[0].Steers[1].Text != "and add a test" {
		t.Fatalf("steers %+v", got[0].Steers)
	}
	if got[1].Prompt != "now the docs" || got[1].State != StateRunning || len(got[1].Steers) != 0 {
		t.Fatalf("follow-up %+v", got[1])
	}
	// A prompt after end_turn but before turn_duration is a new turn too.
	got = parse(
		userPrompt("u1", "2026-10-27T10:00:00Z", "one", "typed"),
		assistant("a1", "2026-10-27T10:00:01Z", "end_turn", text("1")),
		userPrompt("u2", "2026-10-27T10:00:02Z", "two", "typed"),
	)
	if len(got) != 2 || got[0].State != StateDone || got[1].Prompt != "two" {
		t.Fatalf("after end_turn %+v", got)
	}
}

func TestClaudeInterruptEndsTheTurnAsAnError(t *testing.T) {
	got := parse(
		userPrompt("u1", "2026-10-27T10:00:00Z", "long job", "typed"),
		assistant("a1", "2026-10-27T10:00:01Z", "tool_use", toolUse("t1", "Bash", map[string]any{"command": "sleep 100"})),
		rec(map[string]any{"type": "user", "uuid": "i1", "timestamp": "2026-10-27T10:00:05Z",
			"message": map[string]any{"content": []map[string]any{{"type": "text", "text": "[Request interrupted by user for tool use]"}}}}),
		userPrompt("u2", "2026-10-27T10:00:06Z", "something else", "typed"),
	)
	if len(got) != 2 || got[0].State != StateError || len(got[0].Steers) != 0 || got[1].Prompt != "something else" {
		t.Fatalf("turns %+v", got)
	}
}

func TestClaudeSkipsSidechainsSlashCommandsAndGarbage(t *testing.T) {
	side := rec(map[string]any{"type": "assistant", "uuid": "x1", "isSidechain": true, "message": map[string]any{"content": []map[string]any{text("sub")}, "stop_reason": "end_turn"}})
	got := parse(
		"not json",
		userPrompt("c1", "2026-10-27T10:00:00Z", "<command-name>/model</command-name>", ""),
		userPrompt("c2", "2026-10-27T10:00:00Z", "<local-command-stdout>ok</local-command-stdout>", ""),
		userPrompt("u1", "2026-10-27T10:00:01Z", "hi", "typed"),
		side,
		`{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hel`,
		assistant("a2", "2026-10-27T10:00:02Z", "end_turn", text("hello")),
		assistant("a2", "2026-10-27T10:00:02Z", "end_turn", text("hello")), // the same record again
	)
	if len(got) != 1 || got[0].Prompt != "hi" || got[0].Final != "hello" || len(got[0].Steps) != 1 {
		t.Fatalf("turns %+v", got)
	}
}

// The log reader keeps a partial last line until its newline comes, and
// reads only what was appended.
func TestClaudeLogReadsIncrementallyAndWaitsForAPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	first := userPrompt("u1", "2026-10-27T10:00:00Z", "hi", "typed") + "\n"
	answer := assistant("a1", "2026-10-27T10:00:01Z", "end_turn", text("hello"))
	if err := os.WriteFile(path, []byte(first+answer[:20]), 0o644); err != nil {
		t.Fatal(err)
	}
	log := NewClaudeLog(path, "s1")
	if changed, err := log.Read(); err != nil || !changed {
		t.Fatalf("first read %v %v", changed, err)
	}
	if got := log.Turns(); len(got) != 1 || got[0].Final != "" || got[0].State != StateRunning {
		t.Fatalf("after the partial line %+v", got)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(answer[20:] + "\n")
	f.Close()
	if changed, err := log.Read(); err != nil || !changed {
		t.Fatalf("second read %v %v", changed, err)
	}
	if got := log.Turns(); len(got) != 1 || got[0].Final != "hello" {
		t.Fatalf("after the rest %+v", got)
	}
	if changed, _ := log.Read(); changed {
		t.Fatal("nothing appended reads as unchanged")
	}
}

// /clear starts a new session log; the chain reads both as one list with
// the new session's first turn marked.
func TestClaudeChainAcrossClear(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects")
	os.MkdirAll(filepath.Join(projects, ClaudeProjectDir("/w/proj")), 0o755)
	os.MkdirAll(filepath.Join(projects, "-elsewhere"), 0o755)
	a := filepath.Join(projects, ClaudeProjectDir("/w/proj"), "s1.jsonl")
	b := filepath.Join(projects, "-elsewhere", "s2.jsonl")
	os.WriteFile(a, []byte(userPrompt("u1", "2026-10-27T10:00:00Z", "one", "typed")+"\n"+assistant("a1", "2026-10-27T10:00:01Z", "end_turn", text("1"))+"\n"+duration("d1", "2026-10-27T10:00:02Z", 2000)+"\n"), 0o644)
	os.WriteFile(b, []byte(userPrompt("u2", "2026-10-27T11:00:00Z", "two", "typed")+"\n"), 0o644)
	if got, err := FindClaudeLog(projects, "/w/proj", "s1"); err != nil || got != a {
		t.Fatalf("find s1 %q %v", got, err)
	}
	if got, err := FindClaudeLog(projects, "/w/proj", "s2"); err != nil || got != b {
		t.Fatalf("find s2 anywhere %q %v", got, err)
	}
	if _, err := FindClaudeLog(projects, "/w/proj", "../s1"); err == nil {
		t.Fatal("a session id with a path is refused")
	}
	var c ClaudeChain
	c.Add("s1", a)
	c.Add("s2", b)
	c.Add("s1", a)
	if _, err := c.Read(); err != nil {
		t.Fatal(err)
	}
	got := c.Turns()
	if len(got) != 2 || got[0].NewSession || !got[1].NewSession || got[0].Session != "s1" || got[1].Session != "s2" {
		t.Fatalf("chain %+v", got)
	}
}

func TestClaudeProjectDir(t *testing.T) {
	if got := ClaudeProjectDir("/Users/me/Library/Mobile Documents/iCloud~md~obsidian"); got != "-Users-me-Library-Mobile-Documents-iCloud-md-obsidian" {
		t.Fatal(got)
	}
}
