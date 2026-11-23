package agenthook

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// Result is the output of translating a harness hook payload.
// When SkipReason is non-empty, the caller should exit 0 without contacting the daemon.
type Result struct {
	Event      HookEvent
	SkipReason string
}

// CanonicalHarness normalizes harness aliases to their canonical identifier.
func CanonicalHarness(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return ""
	case "claude", "claude-code", "claudecode":
		return "claude"
	case "codex", "openai-codex":
		return "codex"
	case "omp", "oh-my-pi":
		return "omp"
	case "gemini", "gemini-cli":
		return "gemini"
	case "antigravity", "agy":
		return "antigravity"
	case "generic":
		return "generic"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// Translate parses a raw hook JSON payload for harness (with optional CLI
// eventOverride) and maps it to a daemon HookEvent. Statusline/token payloads,
// tuios mail notices, and agent_cwd overrides are intentionally ignored.
func Translate(harness, eventOverride string, raw []byte) Result {
	trimmed := strings.TrimSpace(string(raw))
	var payload map[string]any
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil || payload == nil {
			return Result{SkipReason: "stdin is not a JSON object"}
		}
	} else if strings.TrimSpace(eventOverride) == "" {
		return Result{SkipReason: "empty payload"}
	} else {
		payload = map[string]any{}
	}

	h := CanonicalHarness(harness)
	if h == "" {
		h = detectHarness(payload)
	}

	evName := strings.TrimSpace(eventOverride)
	if evName == "" {
		evName = firstNonEmpty(
			strField(payload, "hook_event_name"),
			strField(payload, "hookEventName"),
			strField(payload, "event"),
			strField(payload, "type"),
		)
	}
	if evName == "" {
		if h == "antigravity" && strField(payload, "conversationId") != "" {
			evName = "PreInvocation"
		} else {
			return Result{SkipReason: "missing hook event name"}
		}
	}

	switch h {
	case "claude":
		return translateClaude(evName, payload)
	case "codex":
		return translateCodex(evName, payload)
	case "omp":
		return translateOMP(evName, payload)
	case "gemini":
		return translateGemini(h, evName, payload)
	case "antigravity":
		return translateAntigravity(evName, payload)
	default:
		return translateGeneric(h, evName, payload)
	}
}

func detectHarness(payload map[string]any) string {
	if strField(payload, "conversationId") != "" || strField(payload, "hookEventName") != "" {
		return "antigravity"
	}
	if strField(payload, "type") == "agent-turn-complete" || strField(payload, "thread-id") != "" {
		return "codex"
	}
	switch strField(payload, "hook_event_name") {
	case "session_start", "agent_start", "agent_end", "tool_approval_requested", "tool_approval_resolved":
		return "omp"
	case "BeforeAgent", "AfterAgent", "BeforeTool", "AfterTool", "BeforeModel", "AfterModel":
		return "gemini"
	case "PreInvocation", "PostInvocation":
		return "antigravity"
	}
	if strField(payload, "hook_event_name") != "" {
		return "claude"
	}
	return "generic"
}

func translateClaude(evName string, payload map[string]any) Result {
	agentID := strField(payload, "agent_id")

	switch evName {
	case "SessionStart":
		if strField(payload, "source") == "compact" {
			return Result{SkipReason: "SessionStart compact ignored"}
		}
		return okResult(HookEvent{
			Harness: "claude",
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(strField(payload, "source")),
			Payload: sanitizePayload(payload),
		})

	case "UserPromptSubmit":
		prompt := strField(payload, "prompt")
		return okResult(HookEvent{
			Harness: "claude",
			Kind:    KindState,
			State:   StateRunning,
			Summary: cleanSummary(prompt),
			Prompt:  sanitizeHookText(prompt),
			Payload: sanitizePayload(payload),
		})

	case "PreToolUse":
		tool := strField(payload, "tool_name")
		ev := HookEvent{
			Harness:    "claude",
			Kind:       KindActivity,
			Tool:       tool,
			Summary:    toolSummary(tool, mapField(payload, "tool_input")),
			SubagentID: validSubagentID(agentID),
			Payload:    sanitizePayload(payload),
		}
		if agentID == "" {
			ev.State = StateRunning
		}
		return okResult(ev)

	case "PostToolUse":
		tool := strField(payload, "tool_name")
		input := mapField(payload, "tool_input")
		ev := HookEvent{
			Harness:    "claude",
			Kind:       KindActivity,
			Tool:       tool,
			Summary:    toolSummary(tool, input),
			Files:      extractWrittenFiles(tool, input),
			SubagentID: validSubagentID(agentID),
			Payload:    sanitizePayload(payload),
		}
		if agentID == "" {
			ev.State = StateRunning
		}
		return okResult(ev)

	case "PostToolUseFailure":
		tool := strField(payload, "tool_name")
		ev := HookEvent{
			Harness:    "claude",
			Kind:       KindActivity,
			Tool:       tool,
			Summary:    failureSummary(tool, mapField(payload, "tool_input"), strField(payload, "error")),
			SubagentID: validSubagentID(agentID),
			Payload:    sanitizePayload(payload),
		}
		if agentID == "" {
			ev.State = StateRunning
		}
		return okResult(ev)

	case "PermissionRequest":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: "claude",
			Kind:    KindApproval,
			State:   StateNeedsYou,
			Tool:    tool,
			Summary: approvalSummary(tool, mapField(payload, "tool_input"), ""),
			Payload: sanitizePayload(payload),
		})

	case "PermissionDenied", "ElicitationResult":
		return okResult(HookEvent{
			Harness: "claude",
			Kind:    KindState,
			State:   StateRunning,
			Tool:    strField(payload, "tool_name"),
			Payload: sanitizePayload(payload),
		})

	case "Notification":
		if agentID != "" {
			return Result{SkipReason: "subagent notification ignored"}
		}
		return translateNotification("claude", payload)

	case "Stop":
		resp := firstNonEmpty(strField(payload, "last_assistant_message"), strField(payload, "response"), strField(payload, "message"))
		return okResult(HookEvent{
			Harness:  "claude",
			Kind:     KindState,
			State:    StateDone,
			Summary:  cleanSummary(resp),
			Response: sanitizeHookText(resp),
			Payload:  sanitizePayload(payload),
		})

	case "StopFailure":
		errType := strField(payload, "error_type")
		sum := "stopped on error"
		if errType != "" {
			sum = "stopped on " + errType
		}
		return okResult(HookEvent{
			Harness: "claude",
			Kind:    KindState,
			State:   StateDone,
			Summary: cleanSummary(sum),
			Payload: sanitizePayload(payload),
		})

	case "SessionEnd":
		return okResult(HookEvent{
			Harness: "claude",
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(strField(payload, "reason")),
			Payload: sanitizePayload(payload),
		})

	case "SubagentStart":
		id := validSubagentID(agentID)
		if id == "" {
			return Result{SkipReason: "missing or invalid agent_id"}
		}
		return okResult(HookEvent{
			Harness:        "claude",
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStart,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	case "SubagentStop":
		id := validSubagentID(agentID)
		if id == "" {
			return Result{SkipReason: "missing or invalid agent_id"}
		}
		return okResult(HookEvent{
			Harness:        "claude",
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStop,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	default:
		return Result{SkipReason: "unmapped claude event " + evName}
	}
}

func translateCodex(evName string, payload map[string]any) Result {
	switch evName {
	case "agent-turn-complete":
		resp := firstNonEmpty(strField(payload, "last-assistant-message"), strField(payload, "last_assistant_message"), strField(payload, "response"))
		return okResult(HookEvent{
			Harness:  "codex",
			Kind:     KindState,
			State:    StateDone,
			Summary:  cleanSummary(resp),
			Response: sanitizeHookText(resp),
			Payload:  sanitizePayload(payload),
		})

	case "SessionStart":
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(strField(payload, "source")),
			Payload: sanitizePayload(payload),
		})

	case "UserPromptSubmit":
		prompt := strField(payload, "prompt")
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindState,
			State:   StateRunning,
			Summary: cleanSummary(prompt),
			Prompt:  sanitizeHookText(prompt),
			Payload: sanitizePayload(payload),
		})

	case "PreToolUse":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, mapField(payload, "tool_input")),
			Payload: sanitizePayload(payload),
		})

	case "PostToolUse":
		tool := strField(payload, "tool_name")
		input := mapField(payload, "tool_input")
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, input),
			Files:   extractWrittenFiles(tool, input),
			Payload: sanitizePayload(payload),
		})

	case "PostToolUseFailure":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: failureSummary(tool, mapField(payload, "tool_input"), strField(payload, "error")),
			Payload: sanitizePayload(payload),
		})

	case "PermissionRequest":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindApproval,
			State:   StateNeedsYou,
			Tool:    tool,
			Summary: approvalSummary(tool, mapField(payload, "tool_input"), ""),
			Payload: sanitizePayload(payload),
		})

	case "Notification":
		return translateNotification("codex", payload)

	case "Stop":
		resp := firstNonEmpty(strField(payload, "last_assistant_message"), strField(payload, "last-assistant-message"), strField(payload, "response"))
		return okResult(HookEvent{
			Harness:  "codex",
			Kind:     KindState,
			State:    StateDone,
			Summary:  cleanSummary(resp),
			Response: sanitizeHookText(resp),
			Payload:  sanitizePayload(payload),
		})

	case "StopFailure":
		errType := strField(payload, "error_type")
		sum := "stopped on error"
		if errType != "" {
			sum = "stopped on " + errType
		}
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindState,
			State:   StateDone,
			Summary: cleanSummary(sum),
			Payload: sanitizePayload(payload),
		})

	case "Interrupt", "SessionEnd":
		return okResult(HookEvent{
			Harness: "codex",
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(strField(payload, "reason")),
			Payload: sanitizePayload(payload),
		})

	case "SubagentStart":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "codex subagent start without id"}
		}
		return okResult(HookEvent{
			Harness:        "codex",
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStart,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	case "SubagentStop":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "codex subagent stop without id"}
		}
		return okResult(HookEvent{
			Harness:        "codex",
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStop,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	default:
		return Result{SkipReason: "unmapped codex event " + evName}
	}
}

func translateOMP(evName string, payload map[string]any) Result {
	switch evName {
	case "session_start", "SessionStart":
		st := StateIdle
		if b, ok := payload["busy"].(bool); ok && b {
			st = StateRunning
		}
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindState,
			State:   st,
			Payload: sanitizePayload(payload),
		})

	case "agent_start", "UserPromptSubmit":
		prompt := strField(payload, "prompt")
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindState,
			State:   StateRunning,
			Summary: cleanSummary(prompt),
			Prompt:  sanitizeHookText(prompt),
			Payload: sanitizePayload(payload),
		})

	case "agent_end", "Stop", "StopFailure":
		resp := firstNonEmpty(strField(payload, "last_assistant_message"), strField(payload, "response"), strField(payload, "summary"))
		return okResult(HookEvent{
			Harness:  "omp",
			Kind:     KindState,
			State:    StateDone,
			Summary:  cleanSummary(firstNonEmpty(strField(payload, "summary"), strField(payload, "last_assistant_message"), strField(payload, "response"))),
			Response: sanitizeHookText(resp),
			Payload:  sanitizePayload(payload),
		})

	case "session_end", "SessionEnd":
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindState,
			State:   StateIdle,
			Payload: sanitizePayload(payload),
		})

	case "tool_start", "PreToolUse":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, mapField(payload, "tool_input")),
			Payload: sanitizePayload(payload),
		})

	case "tool_end", "PostToolUse":
		tool := strField(payload, "tool_name")
		input := mapField(payload, "tool_input")
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, input),
			Files:   extractWrittenFiles(tool, input),
			Payload: sanitizePayload(payload),
		})

	case "tool_error", "PostToolUseFailure":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: failureSummary(tool, mapField(payload, "tool_input"), strField(payload, "error")),
			Payload: sanitizePayload(payload),
		})

	case "tool_approval_requested", "PermissionRequest":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindApproval,
			State:   StateNeedsYou,
			Tool:    tool,
			Summary: approvalSummary(tool, mapField(payload, "tool_input"), strField(payload, "reason")),
			Payload: sanitizePayload(payload),
		})

	case "tool_approval_resolved":
		return okResult(HookEvent{
			Harness: "omp",
			Kind:    KindState,
			State:   StateRunning,
			Payload: sanitizePayload(payload),
		})

	case "subagent_start", "SubagentStart":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "missing or invalid subagent id"}
		}
		return okResult(HookEvent{
			Harness:        "omp",
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStart,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	case "subagent_stop", "SubagentStop":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "missing or invalid subagent id"}
		}
		return okResult(HookEvent{
			Harness:        "omp",
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStop,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	default:
		return Result{SkipReason: "unmapped omp event " + evName}
	}
}

func translateGemini(harness, evName string, payload map[string]any) Result {
	switch evName {
	case "SessionStart":
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(strField(payload, "source")),
			Payload: sanitizePayload(payload),
		})

	case "BeforeAgent", "PreInvocation", "UserPromptSubmit":
		prompt := firstNonEmpty(strField(payload, "prompt"), strField(payload, "lastUserInput"))
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindState,
			State:   StateRunning,
			Summary: cleanSummary(prompt),
			Prompt:  sanitizeHookText(prompt),
			Payload: sanitizePayload(payload),
		})

	case "BeforeTool", "PreToolUse":
		tool, input := antigravityToolName(payload), antigravityToolArgs(payload)
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, input),
			Payload: sanitizePayload(payload),
		})

	case "AfterTool", "PostToolUse":
		tool, input := antigravityToolName(payload), antigravityToolArgs(payload)
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, input),
			Files:   extractWrittenFiles(tool, input),
			Payload: sanitizePayload(payload),
		})

	case "AfterToolFailure", "PostToolUseFailure":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: failureSummary(tool, mapField(payload, "tool_input"), strField(payload, "error")),
			Payload: sanitizePayload(payload),
		})

	case "PermissionRequest":
		tool := strField(payload, "tool_name")
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindApproval,
			State:   StateNeedsYou,
			Tool:    tool,
			Summary: approvalSummary(tool, mapField(payload, "tool_input"), strField(payload, "message")),
			Payload: sanitizePayload(payload),
		})

	case "Notification":
		ntype := strField(payload, "notification_type")
		if ntype == "ToolPermission" {
			tool := ""
			if details := mapField(payload, "details"); details != nil {
				tool = strField(details, "tool")
			}
			return okResult(HookEvent{
				Harness: harness,
				Kind:    KindState,
				State:   StateNeedsYou,
				Tool:    tool,
				Summary: cleanSummary(strField(payload, "message")),
				Payload: sanitizePayload(payload),
			})
		}
		return translateNotification(harness, payload)

	case "AfterAgent", "PostInvocation", "Stop", "StopFailure":
		resp := firstNonEmpty(strField(payload, "prompt_response"), strField(payload, "last_assistant_message"), strField(payload, "response"))
		return okResult(HookEvent{
			Harness:  harness,
			Kind:     KindState,
			State:    StateDone,
			Summary:  cleanSummary(resp),
			Response: sanitizeHookText(resp),
			Payload:  sanitizePayload(payload),
		})

	case "SessionEnd":
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(strField(payload, "reason")),
			Payload: sanitizePayload(payload),
		})

	case "SubagentStart":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "missing or invalid agent_id"}
		}
		return okResult(HookEvent{
			Harness:        harness,
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStart,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	case "SubagentStop":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "missing or invalid agent_id"}
		}
		return okResult(HookEvent{
			Harness:        harness,
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStop,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	default:
		return Result{SkipReason: "unmapped " + harness + " event " + evName}
	}
}

func translateGeneric(harness, evName string, payload map[string]any) Result {
	if harness == "" {
		harness = "generic"
	}
	norm := strings.ToLower(strings.TrimSpace(evName))
	switch norm {
	case "userpromptsubmit", "beforeagent", "agent_start", "running", "working", "preinvocation":
		prompt := strField(payload, "prompt")
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindState,
			State:   StateRunning,
			Summary: cleanSummary(firstNonEmpty(prompt, strField(payload, "summary"), strField(payload, "message"))),
			Prompt:  sanitizeHookText(prompt),
			Payload: sanitizePayload(payload),
		})

	case "pretooluse", "beforetool", "tool_start":
		tool := firstNonEmpty(strField(payload, "tool_name"), strField(payload, "tool"))
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, mapField(payload, "tool_input")),
			Payload: sanitizePayload(payload),
		})

	case "posttooluse", "aftertool", "tool_end":
		tool := firstNonEmpty(strField(payload, "tool_name"), strField(payload, "tool"))
		input := mapField(payload, "tool_input")
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: toolSummary(tool, input),
			Files:   extractWrittenFiles(tool, input),
			Payload: sanitizePayload(payload),
		})

	case "posttoolusefailure", "aftertoolfailure", "tool_error":
		tool := firstNonEmpty(strField(payload, "tool_name"), strField(payload, "tool"))
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: failureSummary(tool, mapField(payload, "tool_input"), strField(payload, "error")),
			Payload: sanitizePayload(payload),
		})

	case "permissionrequest", "tool_approval_requested", "approval":
		tool := firstNonEmpty(strField(payload, "tool_name"), strField(payload, "tool"))
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindApproval,
			State:   StateNeedsYou,
			Tool:    tool,
			Summary: approvalSummary(tool, mapField(payload, "tool_input"), firstNonEmpty(strField(payload, "reason"), strField(payload, "message"))),
			Payload: sanitizePayload(payload),
		})

	case "notification":
		return translateNotification(harness, payload)

	case "needs_you", "needs_input":
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindState,
			State:   StateNeedsYou,
			Summary: cleanSummary(firstNonEmpty(strField(payload, "message"), strField(payload, "summary"))),
			Payload: sanitizePayload(payload),
		})

	case "stop", "stopfailure", "afteragent", "postinvocation", "agent_end", "done":
		resp := firstNonEmpty(strField(payload, "last_assistant_message"), strField(payload, "prompt_response"), strField(payload, "response"), strField(payload, "summary"), strField(payload, "message"))
		return okResult(HookEvent{
			Harness:  harness,
			Kind:     KindState,
			State:    StateDone,
			Summary:  cleanSummary(resp),
			Response: sanitizeHookText(resp),
			Payload:  sanitizePayload(payload),
		})

	case "sessionstart", "session_start", "sessionend", "session_end", "interrupt", "idle":
		return okResult(HookEvent{
			Harness: harness,
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(firstNonEmpty(strField(payload, "reason"), strField(payload, "summary"))),
			Payload: sanitizePayload(payload),
		})

	case "subagentstart", "subagent_start":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "missing or invalid subagent id"}
		}
		return okResult(HookEvent{
			Harness:        harness,
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStart,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	case "subagentstop", "subagent_stop":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "missing or invalid subagent id"}
		}
		return okResult(HookEvent{
			Harness:        harness,
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStop,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	default:
		return Result{SkipReason: "unmapped event " + evName}
	}
}

func translateNotification(harness string, payload map[string]any) Result {
	ntype := strings.ToLower(strings.TrimSpace(strField(payload, "notification_type")))
	msg := strField(payload, "message")
	resp := sanitizeHookText(firstNonEmpty(strField(payload, "response"), strField(payload, "last_assistant_message"), msg))

	switch ntype {
	case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input", "toolpermission", "input":
		return okResult(HookEvent{
			Harness:  harness,
			Kind:     KindState,
			State:    StateNeedsYou,
			Summary:  cleanSummary(msg),
			Response: resp,
			Payload:  sanitizePayload(payload),
		})
	case "idle_prompt":
		return okResult(HookEvent{
			Harness:  harness,
			Kind:     KindState,
			State:    StateIdle,
			Summary:  cleanSummary(msg),
			Response: resp,
			Payload:  sanitizePayload(payload),
		})
	case "":
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "needs your permission"), strings.Contains(lower, "needs input"), strings.Contains(lower, "permission to"):
			return okResult(HookEvent{
				Harness:  harness,
				Kind:     KindState,
				State:    StateNeedsYou,
				Summary:  cleanSummary(msg),
				Response: resp,
				Payload:  sanitizePayload(payload),
			})
		case strings.Contains(lower, "waiting for your input"):
			return okResult(HookEvent{
				Harness:  harness,
				Kind:     KindState,
				State:    StateIdle,
				Summary:  cleanSummary(msg),
				Response: resp,
				Payload:  sanitizePayload(payload),
			})
		default:
			return Result{SkipReason: "untyped notification ignored"}
		}
	default:
		return Result{SkipReason: "notification type " + ntype + " ignored"}
	}
}

func okResult(ev HookEvent) Result {
	return Result{Event: ev}
}

// sanitizePayload copies payload fields while intentionally dropping
// statusline/token/usage metadata and cwd overrides.
func sanitizePayload(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch strings.ToLower(k) {
		case "cwd", "agent_cwd", "statusline", "status_line", "tokens", "usage", "token_usage", "cost", "cost_usd", "context_window":
			continue
		default:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toolSummary(tool string, input map[string]any) string {
	target := toolTarget(input)
	switch {
	case tool != "" && target != "":
		return cleanSummary(tool + ": " + target)
	case tool != "":
		return cleanSummary(tool)
	default:
		return cleanSummary(target)
	}
}

func approvalSummary(tool string, input map[string]any, reason string) string {
	if strings.TrimSpace(reason) != "" {
		return cleanSummary(reason)
	}
	target := toolTarget(input)
	switch {
	case tool != "" && target != "":
		return cleanSummary("approve " + tool + ": " + target)
	case tool != "":
		return cleanSummary("approve " + tool)
	default:
		return "approval requested"
	}
}

func failureSummary(tool string, input map[string]any, errMsg string) string {
	if strings.TrimSpace(errMsg) != "" {
		if tool != "" {
			return cleanSummary(tool + " failed: " + errMsg)
		}
		return cleanSummary(errMsg)
	}
	base := toolSummary(tool, input)
	if base == "" {
		return "tool failed"
	}
	return cleanSummary(base + " failed")
}

func toolTarget(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	return firstNonEmpty(
		strField(input, "command"),
		strField(input, "file_path"),
		strField(input, "notebook_path"),
		strField(input, "path"),
		strField(input, "url"),
		strField(input, "pattern"),
		strField(input, "query"),
	)
}

var secretAssignRe = regexp.MustCompile(`\b((?:[A-Z][A-Z0-9_]*)?(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|AUTH|CREDENTIAL)[A-Z0-9_]*)=([^\s'"]+)`)

const maxHookTextBytes = 16 * 1024

func sanitizeHookText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = secretAssignRe.ReplaceAllString(s, "${1}=***")
	if len(s) > maxHookTextBytes {
		s = strings.TrimSpace(s[:maxHookTextBytes])
	}
	return s
}

func isWriteTool(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "write", "edit", "multiedit", "notebookedit",
		"write_file", "edit_file", "write_to_file",
		"replace_file_content", "multi_replace_file_content":
		return true
	default:
		return false
	}
}

func extractWrittenFiles(tool string, input map[string]any) []string {
	if !isWriteTool(tool) || len(input) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, k := range []string{"file_path", "path", "notebook_path", "target_file", "TargetFile"} {
		add(strField(input, k))
	}
	if edits, ok := input["edits"].([]any); ok {
		for _, e := range edits {
			if em, ok := e.(map[string]any); ok {
				for _, k := range []string{"file_path", "path"} {
					add(strField(em, k))
				}
			}
		}
	}
	return out
}

func cleanSummary(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	s = secretAssignRe.ReplaceAllString(s, "${1}=***")
	runes := []rune(s)
	const maxRunes = 160
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-3]) + "..."
	}
	return s
}

func validSubagentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 {
		return ""
	}
	for _, r := range id {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ':') {
			return ""
		}
	}
	return id
}

func strField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func mapField(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	sub, _ := m[key].(map[string]any)
	return sub
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
