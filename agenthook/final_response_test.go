package agenthook_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/agenthook"
)

func TestCodexFinalResponseUsesOnlyCurrentTerminalMessage(t *testing.T) {
	const previous = `{"type":"turn_context","payload":{"turn_id":"previous"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Previous app/previous.go"}]}}
`
	const current = `{"type":"event_msg","payload":{"type":"task_started","turn_id":"current"}}
{"type":"turn_context","payload":{"turn_id":"current"}}
{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"Thinking app/reasoning.go"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Working on app/progress.go"}]}}
{"type":"response_item","payload":{"type":"function_call","arguments":"app/tool.go"}}
{"type":"response_item","payload":{"type":"function_call_output","output":"app/result.go"}}
`
	const final = `{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"Updated app/final.go"}]}}
`
	const commentary = `{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Working on app/progress.go"}]}}
`
	const legacy = `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Updated app/legacy.go"}]}}
`
	const tool = `{"type":"response_item","payload":{"type":"custom_tool_call","input":"app/tool.go"}}
`
	for _, tc := range []struct {
		name       string
		transcript string
		candidate  string
		want       string
	}{
		{"final phase", previous + current + final, "Working on app/progress.go", "Updated app/final.go"},
		{"commentary", previous + current + commentary, "Working on app/progress.go", ""},
		{"no final answer", previous + current, "Previous app/previous.go", ""},
		{"only metadata", previous + `{"type":"event_msg","payload":{"type":"task_started","turn_id":"current"}}`, "Previous app/previous.go", ""},
		{"legacy terminal text", previous + current + legacy, "Updated app/legacy.go", "Updated app/legacy.go"},
		{"legacy pre-tool text", previous + current + legacy + tool, "Updated app/legacy.go", ""},
		{"wrong turn", previous + final, "Updated app/final.go", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := translateWithTranscript(t, "codex", tc.transcript, map[string]any{
				"hook_event_name":        "Stop",
				"last_assistant_message": tc.candidate,
				"turn_id":                "current",
			})
			if res.SkipReason != "" || res.Event.FinalResponse != tc.want || res.Event.Response != tc.candidate {
				t.Fatalf("translated result = %+v, want final %q and telemetry %q", res, tc.want, tc.candidate)
			}
		})
	}
}

func TestClaudeFinalResponseExcludesThinkingAndToolTurnText(t *testing.T) {
	const toolTurn = `{"type":"user","message":{"content":"Please implement the change."}}
{"type":"assistant","message":{"id":"tool","stop_reason":"tool_use","content":[{"type":"text","text":"Editing app/working.go"},{"type":"thinking","thinking":"Inspect app/thinking.go"},{"type":"tool_use","input":{"file_path":"app/tool.go"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"app/result.go"}]}}
`
	const final = `{"type":"assistant","message":{"id":"final","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"Inspect app/other-thinking.go"}]}}
{"type":"assistant","message":{"id":"final","stop_reason":"end_turn","content":[{"type":"text","text":"Edited app/final.go"}]}}
`
	for _, tc := range []struct {
		name       string
		transcript string
		candidate  string
		want       string
	}{
		{"final", toolTurn + final, "Edited app/final.go", "Edited app/final.go"},
		{"tool turn only", toolTurn, "Editing app/working.go", ""},
		{"stop without answer", toolTurn + final, "", ""},
		{"no new answer", toolTurn + final + `{"type":"user","message":{"content":"Please continue."}}`, "Edited app/final.go", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := translateWithTranscript(t, "claude", tc.transcript, map[string]any{
				"hook_event_name":        "Stop",
				"last_assistant_message": tc.candidate,
			})
			if res.SkipReason != "" || res.Event.FinalResponse != tc.want {
				t.Fatalf("translated result = %+v, want final %q", res, tc.want)
			}
		})
	}
}

func TestOnlySuccessfulCompletionHooksAcceptAnswerFields(t *testing.T) {
	const answer = "See app/final.go"
	for _, tc := range []struct {
		harness string
		event   string
		payload string
		want    string
	}{
		{"claude", "Notification", `{"notification_type":"idle_prompt","response":"See app/final.go"}`, ""},
		{"claude", "Stop", `{"message":"See app/final.go"}`, ""},
		{"claude", "Stop", `{"last_assistant_message":"See app/final.go"}`, answer},
		{"codex", "Stop", `{"last_assistant_message":"See app/final.go"}`, ""},
		{"gemini", "AfterAgent", `{"prompt_response":"See app/final.go"}`, answer},
		{"gemini", "AfterTool", `{"prompt_response":"See app/final.go"}`, ""},
		{"gemini", "StopFailure", `{"prompt_response":"See app/final.go"}`, ""},
		{"antigravity", "PostInvocation", `{"response":"See app/final.go"}`, ""},
		{"antigravity", "Stop", `{"response":"See app/final.go"}`, answer},
		{"omp", "agent_end", `{"response":"See app/final.go"}`, answer},
		{"omp", "agent_end", `{"summary":"See app/final.go"}`, ""},
		{"omp", "agent_end", `{"response":"See app/final.go","willContinue":true}`, ""},
		{"generic", "Stop", `{"summary":"See app/final.go"}`, ""},
	} {
		t.Run(tc.harness+"/"+tc.event+"/"+tc.payload, func(t *testing.T) {
			res := agenthook.Translate(tc.harness, tc.event, []byte(tc.payload))
			if res.Event.FinalResponse != tc.want {
				t.Fatalf("final response = %q, want %q", res.Event.FinalResponse, tc.want)
			}
		})
	}
}

func translateWithTranscript(t *testing.T, harnessName, transcript string, payload map[string]any) agenthook.Result {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	payload["transcript_path"] = path
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return agenthook.Translate(harnessName, "", raw)
}
