package turns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic entries in the shapes omp writes (omp.go); no real session's
// content.

func ompEntryLine(typ, id, parent, ts string, extra map[string]any) string {
	r := map[string]any{"type": typ, "id": id, "timestamp": ts}
	if parent == "" {
		r["parentId"] = nil
	} else {
		r["parentId"] = parent
	}
	for k, v := range extra {
		r[k] = v
	}
	return rec(r)
}

func ompUser(id, parent, ts, text string, steering bool) string {
	m := map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": text}}, "attribution": "user"}
	if steering {
		m["steering"] = true
	}
	return ompEntryLine("message", id, parent, ts, map[string]any{"message": m})
}

func ompAssistant(id, parent, ts, stop string, blocks ...map[string]any) string {
	return ompEntryLine("message", id, parent, ts, map[string]any{"message": map[string]any{"role": "assistant", "content": blocks, "stopReason": stop}})
}

func ompToolCall(id, name string, args map[string]any) map[string]any {
	return map[string]any{"type": "toolCall", "id": id, "name": name, "arguments": args}
}

func ompResult(id, parent, ts, call, text string, isError bool) string {
	return ompEntryLine("message", id, parent, ts, map[string]any{"message": map[string]any{"role": "toolResult", "toolCallId": call, "toolName": "bash",
		"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}})
}

func ompHeader(id string) string {
	return rec(map[string]any{"type": "session", "version": 3, "id": id, "timestamp": "2026-10-27T10:00:00Z", "cwd": "/w/proj"})
}

func ompTitle() string {
	return rec(map[string]any{"type": "title", "v": 1, "title": "a title", "source": "auto", "pad": "      "})
}

func parseOmp(lines ...string) []Turn {
	p := OmpParser{}
	for _, l := range lines {
		p.Feed([]byte(l))
	}
	return p.Turns()
}

func TestOmpTurnWithThinkingToolsAndFinal(t *testing.T) {
	got := parseOmp(
		ompTitle(),
		ompHeader("s1"),
		ompEntryLine("model_change", "m0", "", "2026-10-27T10:00:00Z", map[string]any{"model": "openai-codex/gpt"}),
		ompEntryLine("thinking_level_change", "k0", "m0", "2026-10-27T10:00:00Z", map[string]any{"thinkingLevel": "medium"}),
		ompUser("u1", "k0", "2026-10-27T10:00:01Z", "fix the tests", false),
		ompAssistant("a1", "u1", "2026-10-27T10:00:02Z", "toolUse",
			map[string]any{"type": "thinking", "thinking": ""},
			map[string]any{"type": "thinking", "thinking": "look at the failures"},
			map[string]any{"type": "text", "text": "Running them."},
			ompToolCall("c1", "bash", map[string]any{"command": "go test ./..."})),
		ompEntryLine("custom", "x1", "a1", "2026-10-27T10:00:02Z", map[string]any{"customType": "tool_execution_start"}),
		ompResult("r1", "x1", "2026-10-27T10:00:05Z", "c1", "FAIL x", true),
		ompEntryLine("title_change", "t1", "r1", "2026-10-27T10:00:05Z", map[string]any{"title": "Tests"}),
		ompAssistant("a2", "t1", "2026-10-27T10:00:09Z", "stop", map[string]any{"type": "text", "text": "Fixed: **two** tests."}),
	)
	if len(got) != 1 {
		t.Fatalf("turns %+v", got)
	}
	tr := got[0]
	if tr.Source != SourceOmp || tr.Session != "s1" || tr.ID != "s1/u1" || tr.Prompt != "fix the tests" || tr.State != StateDone || tr.Final != "Fixed: **two** tests." {
		t.Fatalf("turn %+v", tr)
	}
	if tr.Duration.Seconds() != 8 {
		t.Fatalf("duration %v", tr.Duration)
	}
	kinds := []StepKind{}
	for _, s := range tr.Steps {
		kinds = append(kinds, s.Kind)
	}
	if strings.Join(stepKinds(kinds), ",") != "thinking,text,tool,text" {
		t.Fatalf("steps %v", kinds)
	}
	tool := tr.Steps[2]
	if tool.Tool != "bash" || tool.Summary != "go test ./..." || !tool.HasResult || !tool.IsError || tool.Result != "FAIL x" {
		t.Fatalf("tool %+v", tool)
	}
}

func stepKinds(k []StepKind) []string {
	out := make([]string, len(k))
	for i, s := range k {
		out[i] = string(s)
	}
	return out
}

// The file is a tree: a retry from an earlier entry abandons the branch
// after it, and only the branch the leaf is on is the pane's turns.
func TestOmpFollowsTheBranchFromTheLeaf(t *testing.T) {
	got := parseOmp(
		ompHeader("s1"),
		ompUser("u1", "", "2026-10-27T10:00:01Z", "first", false),
		ompAssistant("a1", "u1", "2026-10-27T10:00:02Z", "stop", map[string]any{"type": "text", "text": "one"}),
		ompUser("u2", "a1", "2026-10-27T10:00:03Z", "abandoned question", false),
		ompAssistant("a2", "u2", "2026-10-27T10:00:04Z", "stop", map[string]any{"type": "text", "text": "abandoned answer"}),
		// /tree back to a1, then a new question on that branch.
		ompUser("u3", "a1", "2026-10-27T10:00:05Z", "second", false),
		ompAssistant("a3", "u3", "2026-10-27T10:00:06Z", "stop", map[string]any{"type": "text", "text": "two"}),
	)
	if len(got) != 2 || got[0].Prompt != "first" || got[1].Prompt != "second" || got[1].Final != "two" {
		t.Fatalf("turns %+v", got)
	}
	// A late entry that names the leaf it interrupted keeps that leaf.
	got = parseOmp(
		ompHeader("s1"),
		ompUser("u1", "", "2026-10-27T10:00:01Z", "first", false),
		ompAssistant("a1", "u1", "2026-10-27T10:00:02Z", "stop", map[string]any{"type": "text", "text": "one"}),
		ompUser("u2", "a1", "2026-10-27T10:00:03Z", "second", false),
		ompEntryLine("model_usage", "z1", "u1", "2026-10-27T10:00:04Z", map[string]any{"activeLeafId": "u2"}),
	)
	if len(got) != 2 || got[1].Prompt != "second" || got[1].State != StateRunning {
		t.Fatalf("activeLeafId: %+v", got)
	}
}

// A message sent while the agent works carries steering and folds into the
// running turn (even past its agent's last word); a follow-up queued to
// run after the turn carries none and starts a turn of its own.
func TestOmpSteersFoldAndFollowUpsStartTurns(t *testing.T) {
	got := parseOmp(
		ompHeader("s1"),
		ompUser("u1", "", "2026-10-27T10:00:01Z", "refactor the parser", false),
		ompAssistant("a1", "u1", "2026-10-27T10:00:02Z", "toolUse", ompToolCall("c1", "read", map[string]any{"path": "p.go"})),
		ompUser("s2", "a1", "2026-10-27T10:00:03Z", "keep the API", true),
		ompResult("r1", "s2", "2026-10-27T10:00:04Z", "c1", "package p", false),
		ompAssistant("a2", "r1", "2026-10-27T10:00:05Z", "stop", map[string]any{"type": "text", "text": "done"}),
		ompUser("s3", "a2", "2026-10-27T10:00:06Z", "and the docs", true),
		ompAssistant("a3", "s3", "2026-10-27T10:00:07Z", "stop", map[string]any{"type": "text", "text": "docs too"}),
		ompUser("u4", "a3", "2026-10-27T10:00:08Z", "now commit", false),
		ompAssistant("a4", "u4", "2026-10-27T10:00:09Z", "toolUse", ompToolCall("c2", "bash", map[string]any{"command": "git commit"})),
	)
	if len(got) != 2 {
		t.Fatalf("turns %+v", got)
	}
	first := got[0]
	if len(first.Steers) != 2 || first.Steers[0].Text != "keep the API" || first.Steers[1].Text != "and the docs" || first.Final != "docs too" || first.State != StateDone {
		t.Fatalf("first %+v", first)
	}
	if !first.Steps[0].HasResult || first.Steps[0].Result != "package p" {
		t.Fatalf("tool pairing across a steer: %+v", first.Steps[0])
	}
	second := got[1]
	if second.Prompt != "now commit" || second.State != StateRunning || second.Steps[0].HasResult {
		t.Fatalf("second %+v", second)
	}
}

// An interrupt (aborted) and an API error end the turn as an error; a /clear
// in place (reset_boundary) marks the next turn as a new session; a
// compaction changes nothing.
func TestOmpInterruptResetAndCompaction(t *testing.T) {
	got := parseOmp(
		ompHeader("s1"),
		ompUser("u1", "", "2026-10-27T10:00:01Z", "long job", false),
		ompAssistant("a1", "u1", "2026-10-27T10:00:02Z", "aborted", map[string]any{"type": "text", "text": "partial"}),
		ompEntryLine("compaction", "k1", "a1", "2026-10-27T10:00:03Z", map[string]any{"summary": "…", "firstKeptEntryId": "u1"}),
		ompUser("u2", "k1", "2026-10-27T10:00:04Z", "again", false),
		ompAssistant("a2", "u2", "2026-10-27T10:00:05Z", "stop", map[string]any{"type": "text", "text": "ok"}),
		ompEntryLine("reset_boundary", "b1", "a2", "2026-10-27T10:00:06Z", nil),
		ompUser("u3", "b1", "2026-10-27T10:00:07Z", "fresh start", false),
		ompEntryLine("message", "x1", "u3", "2026-10-27T10:00:08Z", map[string]any{"message": map[string]any{"role": "bashExecution", "command": "ls", "output": "a\nb", "exitCode": 0}}),
	)
	if len(got) != 3 {
		t.Fatalf("turns %+v", got)
	}
	if got[0].State != StateError || got[1].State != StateDone || got[1].NewSession || !got[2].NewSession || got[2].Prompt != "fresh start" {
		t.Fatalf("turns %+v", got)
	}
	if len(got[2].Steps) != 1 || got[2].Steps[0].Summary != "ls" || got[2].Steps[0].Result != "a\nb" {
		t.Fatalf("bash step %+v", got[2].Steps)
	}
	// Chained, the reset's separator survives.
	chained := Chain(got)
	if !chained[2].NewSession || chained[0].NewSession {
		t.Fatalf("chain %+v", chained)
	}
	// A `!command` with no turn running is a turn of its own.
	got = parseOmp(ompHeader("s1"), ompEntryLine("message", "x1", "", "2026-10-27T10:00:08Z", map[string]any{"message": map[string]any{"role": "bashExecution", "command": "false", "output": "", "exitCode": 1}}))
	if len(got) != 1 || got[0].Prompt != "! false" || got[0].State != StateError {
		t.Fatalf("bash turn %+v", got)
	}
}

func TestOmpLogReadsIncrementallyAndWaitsForAPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-10-27T10-00-00-000Z_s1.jsonl")
	head := ompTitle() + "\n" + ompHeader("s1") + "\n" + ompUser("u1", "", "2026-10-27T10:00:01Z", "hello", false) + "\n"
	answer := ompAssistant("a1", "u1", "2026-10-27T10:00:02Z", "stop", map[string]any{"type": "text", "text": "hi"})
	if err := os.WriteFile(path, []byte(head+answer[:20]), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewOmpLog(path, "s1")
	if _, err := l.Read(); err != nil {
		t.Fatal(err)
	}
	if got := l.Turns(); len(got) != 1 || got[0].State != StateRunning {
		t.Fatalf("partial: %+v", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(answer[20:] + "\n")
	f.Close()
	if changed, err := l.Read(); err != nil || !changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	if got := l.Turns(); len(got) != 1 || got[0].State != StateDone || got[0].Final != "hi" {
		t.Fatalf("whole: %+v", got)
	}
	if changed, _ := l.Read(); changed {
		t.Fatal("a read with nothing new changed")
	}
}

func TestFindOmpLog(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "-proj-x")
	os.MkdirAll(ws, 0o755)
	want := filepath.Join(ws, "2026-10-27T10-00-00-000Z_01a1-ab.jsonl")
	os.WriteFile(want, []byte(ompHeader("01a1-ab")+"\n"), 0o644)
	if got, err := FindOmpLog(dir, "01a1-ab"); err != nil || got != want {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "../x", "a/b", "*"} {
		if _, err := FindOmpLog(dir, bad); !IsNoLog(err) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if _, err := FindOmpLog(dir, "missing"); !IsNoLog(err) {
		t.Fatalf("missing: %v", err)
	}
}
