package agenthook_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/matomeru/agenthook"
)

func TestTranslateAntigravityCamelCase(t *testing.T) {
	t.Run("SessionStart camelCase", func(t *testing.T) {
		raw := `{
			"hookEventName": "SessionStart",
			"conversationId": "conv-101",
			"transcriptPath": "/tmp/transcript.jsonl",
			"cwd": "/Users/dev/project"
		}`
		res := agenthook.Translate("antigravity", "", []byte(raw))
		if res.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", res.SkipReason)
		}
		if res.Event.Harness != "antigravity" || res.Event.Kind != agenthook.KindState || res.Event.State != agenthook.StateIdle {
			t.Fatalf("unexpected event: %+v", res.Event)
		}
		if _, hasCWD := res.Event.Payload["cwd"]; hasCWD {
			t.Errorf("expected cwd to be stripped from payload")
		}
	})

	t.Run("PreInvocation reads lastUserInput and auto-detects antigravity", func(t *testing.T) {
		raw := `{
			"hookEventName": "PreInvocation",
			"conversationId": "conv-102",
			"lastUserInput": "deploy service with API_KEY=topsecret\nsecond line",
			"transcriptPath": "/tmp/t.jsonl",
			"cwd": "/tmp/work"
		}`
		res := agenthook.Translate("", "", []byte(raw))
		if res.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", res.SkipReason)
		}
		if res.Event.Harness != "antigravity" || res.Event.Kind != agenthook.KindState || res.Event.State != agenthook.StateRunning {
			t.Fatalf("unexpected event: %+v", res.Event)
		}
		if !strings.Contains(res.Event.Summary, "API_KEY=***") || strings.Contains(res.Event.Summary, "topsecret") {
			t.Errorf("summary = %q, expected redacted lastUserInput", res.Event.Summary)
		}
		if !strings.Contains(res.Event.Prompt, "API_KEY=***") || strings.Contains(res.Event.Prompt, "topsecret") {
			t.Errorf("prompt = %q, expected redacted lastUserInput", res.Event.Prompt)
		}
	})

	t.Run("PreToolUse reads toolCall name and args", func(t *testing.T) {
		raw := `{
			"hookEventName": "PreToolUse",
			"conversationId": "conv-103",
			"toolCall": {
				"id": "tc-1",
				"name": "run_command",
				"args": {"CommandLine": "go test ./..."}
			}
		}`
		res := agenthook.Translate("antigravity", "", []byte(raw))
		if res.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", res.SkipReason)
		}
		if res.Event.Kind != agenthook.KindActivity || res.Event.State != agenthook.StateRunning {
			t.Fatalf("unexpected kind/state: %+v", res.Event)
		}
		if res.Event.Tool != "run_command" {
			t.Errorf("tool = %q, want run_command", res.Event.Tool)
		}
		if res.Event.Summary != "run_command: go test ./..." {
			t.Errorf("summary = %q, want %q", res.Event.Summary, "run_command: go test ./...")
		}
	})

	t.Run("PostToolUse with toolCall name and args extracts written files", func(t *testing.T) {
		raw := `{
			"hookEventName": "PostToolUse",
			"conversationId": "conv-104",
			"toolCall": {
				"id": "tc-2",
				"name": "write_file",
				"args": {"Path": "/proj/matomeru/setup/hooks.go"}
			}
		}`
		res := agenthook.Translate("", "", []byte(raw))
		if res.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", res.SkipReason)
		}
		if res.Event.Harness != "antigravity" || res.Event.Kind != agenthook.KindActivity || res.Event.State != agenthook.StateRunning {
			t.Fatalf("unexpected event: %+v", res.Event)
		}
		if res.Event.Tool != "write_file" {
			t.Errorf("tool = %q, want write_file", res.Event.Tool)
		}
		if res.Event.Summary != "write_file: /proj/matomeru/setup/hooks.go" {
			t.Errorf("summary = %q", res.Event.Summary)
		}
		if len(res.Event.Files) != 1 || res.Event.Files[0] != "/proj/matomeru/setup/hooks.go" {
			t.Errorf("files = %+v, want [/proj/matomeru/setup/hooks.go]", res.Event.Files)
		}
	})

	t.Run("PostToolUse with toolCall and error reports failure summary", func(t *testing.T) {
		raw := `{
			"hookEventName": "PostToolUse",
			"conversationId": "conv-105",
			"toolCall": {
				"id": "tc-3",
				"name": "edit_file",
				"args": {"path": "/proj/foo.go"}
			},
			"error": "target string not found"
		}`
		res := agenthook.Translate("antigravity", "", []byte(raw))
		if res.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", res.SkipReason)
		}
		if res.Event.Kind != agenthook.KindActivity || res.Event.State != agenthook.StateRunning {
			t.Fatalf("unexpected event: %+v", res.Event)
		}
		if res.Event.Tool != "edit_file" {
			t.Errorf("tool = %q, want edit_file", res.Event.Tool)
		}
		if res.Event.Summary != "edit_file failed: target string not found" {
			t.Errorf("summary = %q, want %q", res.Event.Summary, "edit_file failed: target string not found")
		}
		if len(res.Event.Files) != 0 {
			t.Errorf("expected no written files on error, got %+v", res.Event.Files)
		}
	})

	t.Run("PostInvocation is active turn telemetry", func(t *testing.T) {
		raw := `{
			"hookEventName": "PostInvocation",
			"conversationId": "conv-106",
			"transcriptPath": "/tmp/t.jsonl"
		}`
		res := agenthook.Translate("antigravity", "", []byte(raw))
		if res.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", res.SkipReason)
		}
		if res.Event.Harness != "antigravity" || res.Event.Kind != agenthook.KindActivity || res.Event.State != agenthook.StateRunning {
			t.Fatalf("unexpected PostInvocation event: %+v", res.Event)
		}
	})

	t.Run("Stop marks StateDone with terminationReason or error", func(t *testing.T) {
		res := agenthook.Translate("antigravity", "", []byte(`{
			"hookEventName": "Stop",
			"conversationId": "conv-107",
			"terminationReason": "completed"
		}`))
		if res.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", res.SkipReason)
		}
		if res.Event.Kind != agenthook.KindState || res.Event.State != agenthook.StateDone || res.Event.Summary != "completed" {
			t.Fatalf("unexpected Stop event: %+v", res.Event)
		}

		resErr := agenthook.Translate("", "", []byte(`{
			"hookEventName": "Stop",
			"error": "rate limit exceeded",
			"terminationReason": "error"
		}`))
		if resErr.SkipReason != "" {
			t.Fatalf("unexpected skip: %s", resErr.SkipReason)
		}
		if resErr.Event.Harness != "antigravity" || resErr.Event.State != agenthook.StateDone || resErr.Event.Summary != "rate limit exceeded" {
			t.Fatalf("unexpected Stop error event: %+v", resErr.Event)
		}
	})
}

func TestRunAntigravityStdoutJSONContract(t *testing.T) {
	sock, events := startFakeDaemon(t, func(ev agenthook.HookEvent) agenthook.DaemonResponse {
		if ev.Kind == agenthook.KindApproval {
			return agenthook.DaemonResponse{OK: true, Decision: agenthook.DecisionAllow}
		}
		return agenthook.DaemonResponse{OK: true}
	})

	envWithSock := func(k string) string {
		switch k {
		case "MTM_SOCKET":
			return sock
		case "MTM_PANE_ID":
			return "pane-agy"
		}
		return ""
	}

	// 1. All 5 telemetry events emit "{}\n" to stdout and forward to daemon.
	telemetryPayloads := []string{
		`{"hookEventName":"SessionStart","conversationId":"c1"}`,
		`{"hookEventName":"PreInvocation","conversationId":"c1","lastUserInput":"hello"}`,
		`{"hookEventName":"PostToolUse","conversationId":"c1","toolCall":{"id":"1","name":"read_file","args":{"path":"a.go"}}}`,
		`{"hookEventName":"PostInvocation","conversationId":"c1"}`,
		`{"hookEventName":"Stop","conversationId":"c1","terminationReason":"completed"}`,
	}
	for _, p := range telemetryPayloads {
		var stdout, stderr bytes.Buffer
		code := agenthook.RunWithEnv([]string{"antigravity"}, envWithSock, strings.NewReader(p), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d for %s (stderr=%s)", code, p, stderr.String())
		}
		if stdout.String() != "{}\n" {
			t.Fatalf("stdout = %q, want %q for payload %s", stdout.String(), "{}\n", p)
		}
	}
	got := events()
	if len(got) != len(telemetryPayloads) {
		t.Fatalf("daemon received %d events, want %d", len(got), len(telemetryPayloads))
	}
	if got[0].State != agenthook.StateIdle ||
		got[1].State != agenthook.StateRunning ||
		got[2].Kind != agenthook.KindActivity ||
		got[3].Kind != agenthook.KindActivity ||
		got[4].State != agenthook.StateDone {
		t.Fatalf("unexpected forwarded event sequence: %+v", got)
	}

	// 2. When MTM_SOCKET is unset, antigravity still exits 0 and emits "{}\n".
	var stdout, stderr bytes.Buffer
	code := agenthook.RunWithEnv([]string{"antigravity"}, func(string) string { return "" }, strings.NewReader(telemetryPayloads[0]), &stdout, &stderr)
	if code != 0 || stdout.String() != "{}\n" {
		t.Fatalf("MTM_SOCKET unset: code=%d stdout=%q want 0 and \"{}\\\\n\"", code, stdout.String())
	}

	// 3. When daemon socket is unreachable, antigravity still exits 0 and emits "{}\n".
	stdout.Reset()
	stderr.Reset()
	badEnv := func(k string) string {
		if k == "MTM_SOCKET" {
			return "/tmp/does-not-exist-antigravity-test.sock"
		}
		return ""
	}
	code = agenthook.RunWithEnv([]string{"antigravity"}, badEnv, strings.NewReader(telemetryPayloads[0]), &stdout, &stderr)
	if code != 0 || stdout.String() != "{}\n" {
		t.Fatalf("unreachable socket: code=%d stdout=%q want 0 and \"{}\\\\n\"", code, stdout.String())
	}

	// 4. When event is skipped (both explicit harness and auto-detected), still emits "{}\n".
	for _, args := range [][]string{{"antigravity"}, {}} {
		stdout.Reset()
		stderr.Reset()
		code = agenthook.RunWithEnv(args, envWithSock, strings.NewReader(`{"hookEventName":"UnmappedEvent","conversationId":"c2"}`), &stdout, &stderr)
		if code != 0 || stdout.String() != "{}\n" {
			t.Fatalf("skipped event (args=%v): code=%d stdout=%q want 0 and \"{}\\\\n\"", args, code, stdout.String())
		}
	}

	// 5. When an approval decision IS emitted, stdout contains the decision JSON (not "{}\n").
	stdout.Reset()
	stderr.Reset()
	code = agenthook.RunWithEnv([]string{"antigravity"}, envWithSock, strings.NewReader(`{"hookEventName":"PermissionRequest","conversationId":"c3","toolCall":{"name":"run_command","args":{"command":"rm -rf x"}}}`), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("approval exit code = %d", code)
	}
	var dec map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &dec); err != nil {
		t.Fatalf("invalid approval stdout JSON %q: %v", stdout.String(), err)
	}
	if dec["decision"] != "allow" {
		t.Fatalf("approval stdout = %q, want decision allow", stdout.String())
	}
}
