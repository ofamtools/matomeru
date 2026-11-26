package agenthook_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ofam.dev/matomeru/agenthook"
)

func TestTranslateHarnesses(t *testing.T) {
	tests := []struct {
		name       string
		harness    string
		event      string
		payload    string
		wantSkip   bool
		wantKind   string
		wantState  string
		wantTool   string
		wantSubID  string
		wantSubAct string
		wantSumSub string
	}{
		// Claude Code
		{
			name:       "claude UserPromptSubmit",
			harness:    "claude-code",
			payload:    `{"hook_event_name":"UserPromptSubmit","prompt":"fix auth with API_KEY=secret123\nmore details","cwd":"/tmp/x","usage":{"input_tokens":500}}`,
			wantKind:   agenthook.KindState,
			wantState:  agenthook.StateRunning,
			wantSumSub: "fix auth with API_KEY=***",
		},
		{
			name:       "claude PreToolUse",
			harness:    "claude",
			payload:    `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`,
			wantKind:   agenthook.KindActivity,
			wantState:  agenthook.StateRunning,
			wantTool:   "Bash",
			wantSumSub: "Bash: go test ./...",
		},
		{
			name:       "claude PostToolUse",
			harness:    "claude",
			payload:    `{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"app/model.go"}}`,
			wantKind:   agenthook.KindActivity,
			wantState:  agenthook.StateRunning,
			wantTool:   "Edit",
			wantSumSub: "Edit: app/model.go",
		},
		{
			name:       "claude PostToolUseFailure",
			harness:    "claude",
			payload:    `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"exit status 1\ntrace"}`,
			wantKind:   agenthook.KindActivity,
			wantState:  agenthook.StateRunning,
			wantTool:   "Bash",
			wantSumSub: "Bash failed: exit status 1",
		},
		{
			name:       "claude PermissionRequest",
			harness:    "claude-code",
			payload:    `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"git push origin main"}}`,
			wantKind:   agenthook.KindApproval,
			wantState:  agenthook.StateNeedsYou,
			wantTool:   "Bash",
			wantSumSub: "approve Bash: git push origin main",
		},
		{
			name:       "claude Notification permission_prompt",
			harness:    "claude",
			payload:    `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`,
			wantKind:   agenthook.KindState,
			wantState:  agenthook.StateNeedsYou,
			wantSumSub: "Claude needs your permission",
		},
		{
			name:     "claude Notification auth_success skipped",
			harness:  "claude",
			payload:  `{"hook_event_name":"Notification","notification_type":"auth_success","message":"Signed in"}`,
			wantSkip: true,
		},
		{
			name:       "claude Stop",
			harness:    "claude",
			payload:    `{"hook_event_name":"Stop","last_assistant_message":"All checks passed.\nExtra details."}`,
			wantKind:   agenthook.KindState,
			wantState:  agenthook.StateDone,
			wantSumSub: "All checks passed.",
		},
		{
			name:       "claude StopFailure",
			harness:    "claude",
			payload:    `{"hook_event_name":"StopFailure","error_type":"rate_limit"}`,
			wantKind:   agenthook.KindState,
			wantState:  agenthook.StateDone,
			wantSumSub: "stopped on rate_limit",
		},
		{
			name:      "claude SessionEnd",
			harness:   "claude",
			payload:   `{"hook_event_name":"SessionEnd","reason":"exit"}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateIdle,
		},
		{
			name:       "claude SubagentStart",
			harness:    "claude",
			payload:    `{"hook_event_name":"SubagentStart","agent_id":"sub-42","agent_type":"Explore"}`,
			wantKind:   agenthook.KindSubagent,
			wantSubID:  "sub-42",
			wantSubAct: agenthook.SubagentStart,
			wantSumSub: "Explore",
		},
		{
			name:       "claude SubagentStop",
			harness:    "claude",
			payload:    `{"hook_event_name":"SubagentStop","agent_id":"sub-42","agent_type":"Explore"}`,
			wantKind:   agenthook.KindSubagent,
			wantSubID:  "sub-42",
			wantSubAct: agenthook.SubagentStop,
		},
		{
			name:     "claude invalid SubagentStart skipped",
			harness:  "claude",
			payload:  `{"hook_event_name":"SubagentStart","agent_id":"bad; rm -rf /"}`,
			wantSkip: true,
		},
		// Codex
		{
			name:       "codex PreToolUse",
			harness:    "codex",
			payload:    `{"hook_event_name":"PreToolUse","tool_name":"shell","tool_input":{"command":"ls -la"}}`,
			wantKind:   agenthook.KindActivity,
			wantState:  agenthook.StateRunning,
			wantTool:   "shell",
			wantSumSub: "shell: ls -la",
		},
		{
			name:       "codex legacy agent-turn-complete",
			harness:    "codex",
			payload:    `{"type":"agent-turn-complete","thread-id":"th-1","last-assistant-message":"Done building."}`,
			wantKind:   agenthook.KindState,
			wantState:  agenthook.StateDone,
			wantSumSub: "Done building.",
		},
		// OMP
		{
			name:      "omp agent_start",
			harness:   "omp",
			payload:   `{"hook_event_name":"agent_start","session_id":"omp-1"}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateRunning,
		},
		{
			name:      "omp agent_end",
			harness:   "omp",
			payload:   `{"hook_event_name":"agent_end","session_id":"omp-1"}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateDone,
		},
		{
			name:       "omp tool_approval_requested",
			harness:    "omp",
			payload:    `{"hook_event_name":"tool_approval_requested","tool_name":"bash","reason":"Run deploy?"}`,
			wantKind:   agenthook.KindApproval,
			wantState:  agenthook.StateNeedsYou,
			wantTool:   "bash",
			wantSumSub: "Run deploy?",
		},
		// Gemini / Antigravity
		{
			name:      "gemini BeforeAgent",
			harness:   "gemini",
			payload:   `{"hook_event_name":"BeforeAgent","prompt":"summarize repo"}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateRunning,
		},
		{
			name:      "gemini BeforeTool",
			harness:   "gemini",
			payload:   `{"hook_event_name":"BeforeTool","tool_name":"run_shell_command"}`,
			wantKind:  agenthook.KindActivity,
			wantState: agenthook.StateRunning,
			wantTool:  "run_shell_command",
		},
		{
			name:       "gemini Notification ToolPermission",
			harness:    "gemini",
			payload:    `{"hook_event_name":"Notification","notification_type":"ToolPermission","message":"Allow npm test?","details":{"tool":"run_shell_command"}}`,
			wantKind:   agenthook.KindState,
			wantState:  agenthook.StateNeedsYou,
			wantTool:   "run_shell_command",
			wantSumSub: "Allow npm test?",
		},
		{
			name:      "gemini AfterAgent",
			harness:   "gemini",
			payload:   `{"hook_event_name":"AfterAgent","prompt_response":"Completed."}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateDone,
		},
		{
			name:      "antigravity PreInvocation",
			harness:   "antigravity",
			payload:   `{"hook_event_name":"PreInvocation","conversationId":"ag-1"}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateRunning,
		},
		// Generic fallback
		{
			name:      "generic event fallback",
			harness:   "generic",
			payload:   `{"event":"Stop","summary":"finished"}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateDone,
		},
		{
			name:      "cli event override with empty json",
			harness:   "claude",
			event:     "Stop",
			payload:   `{}`,
			wantKind:  agenthook.KindState,
			wantState: agenthook.StateDone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := agenthook.Translate(tc.harness, tc.event, []byte(tc.payload))
			if tc.wantSkip {
				if res.SkipReason == "" {
					t.Fatalf("expected skip, got event %+v", res.Event)
				}
				return
			}
			if res.SkipReason != "" {
				t.Fatalf("unexpected skip: %s", res.SkipReason)
			}
			if res.Event.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", res.Event.Kind, tc.wantKind)
			}
			if res.Event.State != tc.wantState {
				t.Errorf("state = %q, want %q", res.Event.State, tc.wantState)
			}
			if tc.wantTool != "" && res.Event.Tool != tc.wantTool {
				t.Errorf("tool = %q, want %q", res.Event.Tool, tc.wantTool)
			}
			if tc.wantSubID != "" && res.Event.SubagentID != tc.wantSubID {
				t.Errorf("subagentId = %q, want %q", res.Event.SubagentID, tc.wantSubID)
			}
			if tc.wantSubAct != "" && res.Event.SubagentAction != tc.wantSubAct {
				t.Errorf("subagentAction = %q, want %q", res.Event.SubagentAction, tc.wantSubAct)
			}
			if tc.wantSumSub != "" && !strings.Contains(res.Event.Summary, tc.wantSumSub) {
				t.Errorf("summary = %q, want substring %q", res.Event.Summary, tc.wantSumSub)
			}
			// Verify cwd and usage/statusline keys are stripped from forwarded payload.
			if _, ok := res.Event.Payload["cwd"]; ok {
				t.Errorf("payload retained cwd override: %+v", res.Event.Payload)
			}
			if _, ok := res.Event.Payload["usage"]; ok {
				t.Errorf("payload retained token usage: %+v", res.Event.Payload)
			}
		})
	}
}

func startFakeDaemon(t *testing.T, handler func(agenthook.HookEvent) agenthook.DaemonResponse) (string, func() []agenthook.HookEvent) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtmhook-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "mtm.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var got []agenthook.HookEvent

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				line, err := br.ReadBytes('\n')
				if err != nil {
					return
				}
				var ev agenthook.HookEvent
				if err := json.Unmarshal(line, &ev); err != nil {
					return
				}
				mu.Lock()
				got = append(got, ev)
				mu.Unlock()
				resp := handler(ev)
				b, _ := json.Marshal(resp)
				_, _ = c.Write(append(b, '\n'))
			}(conn)
		}
	}()

	return sock, func() []agenthook.HookEvent {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]agenthook.HookEvent, len(got))
		copy(cp, got)
		return cp
	}
}

func TestRunSendsFinalResponse(t *testing.T) {
	sock, events := startFakeDaemon(t, func(agenthook.HookEvent) agenthook.DaemonResponse {
		return agenthook.DaemonResponse{OK: true}
	})
	env := func(key string) string {
		if key == "MTM_SOCKET" {
			return sock
		}
		return ""
	}
	const answer = "Updated app/final.go"
	in := strings.NewReader(`{"hook_event_name":"Stop","last_assistant_message":"Updated app/final.go"}`)
	if code := agenthook.RunWithEnv([]string{"claude"}, env, in, nil, nil); code != 0 {
		t.Fatalf("hook exit = %d", code)
	}
	if got := events(); len(got) != 1 || got[0].FinalResponse != answer {
		t.Fatalf("wire events = %+v, want final answer %q", got, answer)
	}
}

func TestRunBlockingApprovalAllowAndDeny(t *testing.T) {
	t.Run("claude allow", func(t *testing.T) {
		sock, events := startFakeDaemon(t, func(ev agenthook.HookEvent) agenthook.DaemonResponse {
			if ev.Kind != agenthook.KindApproval || ev.RequestID == "" {
				t.Errorf("expected approval with requestId, got %+v", ev)
			}
			return agenthook.DaemonResponse{OK: true, Decision: agenthook.DecisionAllow}
		})

		env := func(k string) string {
			switch k {
			case "MTM_SOCKET":
				return sock
			case "MTM_PANE_ID":
				return "pane-7"
			}
			return ""
		}
		var stdout, stderr bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
		code := agenthook.RunWithEnv([]string{"claude", "--timeout", "5s"}, env, in, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
		}

		var out struct {
			HookSpecificOutput struct {
				HookEventName string `json:"hookEventName"`
				Decision      struct {
					Behavior string `json:"behavior"`
				} `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatalf("invalid stdout json %q: %v", stdout.String(), err)
		}
		if out.HookSpecificOutput.HookEventName != "PermissionRequest" || out.HookSpecificOutput.Decision.Behavior != "allow" {
			t.Fatalf("unexpected stdout decision: %s", stdout.String())
		}
		got := events()
		if len(got) != 1 || got[0].PaneID != "pane-7" || got[0].Op != "hook_event" {
			t.Fatalf("unexpected daemon events: %+v", got)
		}
	})

	t.Run("claude deny with reason", func(t *testing.T) {
		sock, _ := startFakeDaemon(t, func(ev agenthook.HookEvent) agenthook.DaemonResponse {
			return agenthook.DaemonResponse{OK: true, Decision: agenthook.DecisionDeny, Reason: "not on main"}
		})
		env := func(k string) string {
			if k == "MTM_SOCKET" {
				return sock
			}
			return "pane-9"
		}
		var stdout, stderr bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"git push"}}`)
		code := agenthook.RunWithEnv([]string{"claude"}, env, in, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d", code)
		}
		if !strings.Contains(stdout.String(), `"behavior":"deny"`) || !strings.Contains(stdout.String(), `"message":"not on main"`) {
			t.Fatalf("unexpected deny output: %s", stdout.String())
		}
	})

	t.Run("approval timeout outputs nothing and exits 0", func(t *testing.T) {
		dir, err := os.MkdirTemp("/tmp", "mtmhook-to-*")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		sock := filepath.Join(dir, "mtm.sock")
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			time.Sleep(500 * time.Millisecond)
		}()

		env := func(k string) string {
			if k == "MTM_SOCKET" {
				return sock
			}
			return "pane-t"
		}
		var stdout, stderr bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
		code := agenthook.RunWithEnv([]string{"claude", "--timeout", "100ms", "--explain"}, env, in, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if stdout.Len() != 0 {
			t.Fatalf("expected empty stdout on timeout, got %q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "skipped:") {
			t.Fatalf("expected --explain output on stderr, got %q", stderr.String())
		}
	})
}

func TestRunSilentSkipOutsideMatomeru(t *testing.T) {
	// 1. MTM_SOCKET unset
	var stdout, stderr bytes.Buffer
	code := agenthook.RunWithEnv([]string{"claude", "--explain"}, func(string) string { return "" }, strings.NewReader(`{"hook_event_name":"Stop"}`), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "MTM_SOCKET is unset") {
		t.Fatalf("expected explain message, got %q", stderr.String())
	}

	// 2. Unreachable socket without --explain stays completely silent
	stdout.Reset()
	stderr.Reset()
	env := func(k string) string {
		if k == "MTM_SOCKET" {
			return "/tmp/does-not-exist-mtm-hook-test.sock"
		}
		return ""
	}
	code = agenthook.RunWithEnv([]string{"claude"}, env, strings.NewReader(`{"hook_event_name":"Stop"}`), &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("expected silent exit 0, got code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunManualStateCommand(t *testing.T) {
	sock, events := startFakeDaemon(t, func(ev agenthook.HookEvent) agenthook.DaemonResponse {
		return agenthook.DaemonResponse{OK: true}
	})
	env := func(k string) string {
		switch k {
		case "MTM_SOCKET":
			return sock
		case "MTM_PANE_ID":
			return "pane-manual"
		}
		return ""
	}

	for _, st := range []string{"running", "needs_you", "idle", "done"} {
		var stdout, stderr bytes.Buffer
		code := agenthook.RunWithEnv([]string{"state", st, "--summary", "manual update"}, env, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("state %s failed: code=%d stderr=%s", st, code, stderr.String())
		}
	}

	got := events()
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4", len(got))
	}
	if got[0].State != "running" || got[1].State != "needs_you" || got[2].State != "idle" || got[3].State != "done" {
		t.Fatalf("unexpected states: %+v", got)
	}
	if got[0].PaneID != "pane-manual" || got[0].Summary != "manual update" {
		t.Fatalf("unexpected event fields: %+v", got[0])
	}

	// Invalid state returns exit code 2.
	var stderr bytes.Buffer
	if code := agenthook.RunWithEnv([]string{"state", "bogus"}, env, nil, nil, &stderr); code != 2 {
		t.Fatalf("expected exit 2 for invalid state, got %d", code)
	}
}

func TestTranslateStructuredFieldsExtraction(t *testing.T) {
	// PostToolUse Write extracts Files.
	res := agenthook.Translate("claude", "", []byte(`{
		"hook_event_name": "PostToolUse",
		"tool_name": "Write",
		"tool_input": {"file_path": "/proj/matomeru/docs/design.md", "content": "# Design"}
	}`))
	if res.SkipReason != "" || len(res.Event.Files) != 1 || res.Event.Files[0] != "/proj/matomeru/docs/design.md" {
		t.Fatalf("PostToolUse Write files = %+v (skip=%q)", res.Event.Files, res.SkipReason)
	}

	// PostToolUse write_file (Gemini/OMP) extracts path.
	res = agenthook.Translate("gemini", "", []byte(`{
		"hook_event_name": "AfterTool",
		"tool_name": "write_file",
		"tool_input": {"path": "/proj/matomeru/notes.md"}
	}`))
	if res.SkipReason != "" || len(res.Event.Files) != 1 || res.Event.Files[0] != "/proj/matomeru/notes.md" {
		t.Fatalf("AfterTool write_file files = %+v (skip=%q)", res.Event.Files, res.SkipReason)
	}

	// PostToolUse non-write tool (Bash/Read) does not populate Files.
	res = agenthook.Translate("claude", "", []byte(`{
		"hook_event_name": "PostToolUse",
		"tool_name": "Read",
		"tool_input": {"file_path": "/proj/matomeru/README.md"}
	}`))
	if len(res.Event.Files) != 0 {
		t.Fatalf("Read should not populate Files: %+v", res.Event.Files)
	}

	// UserPromptSubmit extracts full Prompt (redacting secrets) while preserving URLs/PR refs.
	res = agenthook.Translate("claude", "", []byte(`{
		"hook_event_name": "UserPromptSubmit",
		"prompt": "Review https://github.com/ofam/matomeru/pull/42 with TOKEN=supersecret\nand check ofam/ochazuke#9"
	}`))
	if !strings.Contains(res.Event.Prompt, "https://github.com/ofam/matomeru/pull/42") ||
		!strings.Contains(res.Event.Prompt, "ofam/ochazuke#9") ||
		strings.Contains(res.Event.Prompt, "supersecret") {
		t.Fatalf("UserPromptSubmit prompt = %q", res.Event.Prompt)
	}

	// Stop extracts full Response across harnesses.
	res = agenthook.Translate("claude", "", []byte(`{
		"hook_event_name": "Stop",
		"last_assistant_message": "Opened PR https://github.com/ofam/matomeru/pull/99\nAll tests pass."
	}`))
	if !strings.Contains(res.Event.Response, "https://github.com/ofam/matomeru/pull/99") {
		t.Fatalf("Stop response = %q", res.Event.Response)
	}
	res = agenthook.Translate("codex", "", []byte(`{
		"type": "agent-turn-complete",
		"thread-id": "th-2",
		"last-assistant-message": "See ofam/matomeru#101 for details."
	}`))
	if !strings.Contains(res.Event.Response, "ofam/matomeru#101") {
		t.Fatalf("codex response = %q", res.Event.Response)
	}
}
