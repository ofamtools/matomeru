package agenthook

import (
	"encoding/json"
	"strings"
)

func translateAntigravity(evName string, payload map[string]any) Result {
	switch evName {
	case "SessionStart":
		return okResult(HookEvent{
			Harness: "antigravity",
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(strField(payload, "source")),
			Payload: sanitizePayload(payload),
		})

	case "PreInvocation", "BeforeAgent", "UserPromptSubmit":
		prompt := firstNonEmpty(strField(payload, "prompt"), strField(payload, "lastUserInput"))
		return okResult(HookEvent{
			Harness: "antigravity",
			Kind:    KindState,
			State:   StateRunning,
			Summary: cleanSummary(prompt),
			Prompt:  sanitizeHookText(prompt),
			Payload: sanitizePayload(payload),
		})

	case "PreToolUse", "BeforeTool":
		tool := antigravityToolName(payload)
		args := antigravityToolArgs(payload)
		return okResult(HookEvent{
			Harness: "antigravity",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: antigravityToolSummary(tool, args),
			Payload: sanitizePayload(payload),
		})

	case "PostToolUse", "AfterTool":
		tool := antigravityToolName(payload)
		args := antigravityToolArgs(payload)
		errMsg := strings.TrimSpace(strField(payload, "error"))
		ev := HookEvent{
			Harness: "antigravity",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Payload: sanitizePayload(payload),
		}
		if errMsg != "" {
			ev.Summary = failureSummary(tool, args, errMsg)
		} else {
			ev.Summary = antigravityToolSummary(tool, args)
			ev.Files = antigravityWrittenFiles(tool, args)
		}
		return okResult(ev)

	case "PostToolUseFailure", "AfterToolFailure":
		tool := antigravityToolName(payload)
		args := antigravityToolArgs(payload)
		return okResult(HookEvent{
			Harness: "antigravity",
			Kind:    KindActivity,
			State:   StateRunning,
			Tool:    tool,
			Summary: failureSummary(tool, args, strField(payload, "error")),
			Payload: sanitizePayload(payload),
		})

	case "PostInvocation":
		return okResult(HookEvent{
			Harness: "antigravity",
			Kind:    KindActivity,
			State:   StateRunning,
			Summary: cleanSummary(firstNonEmpty(strField(payload, "error"), strField(payload, "summary"), strField(payload, "message"))),
			Payload: sanitizePayload(payload),
		})

	case "PermissionRequest":
		tool := antigravityToolName(payload)
		args := antigravityToolArgs(payload)
		return okResult(HookEvent{
			Harness: "antigravity",
			Kind:    KindApproval,
			State:   StateNeedsYou,
			Tool:    tool,
			Summary: approvalSummary(tool, args, strField(payload, "message")),
			Payload: sanitizePayload(payload),
		})

	case "Notification":
		return translateNotification("antigravity", payload)

	case "Stop", "StopFailure", "AfterAgent":
		resp := firstNonEmpty(
			strField(payload, "prompt_response"),
			strField(payload, "last_assistant_message"),
			strField(payload, "response"),
		)
		sum := firstNonEmpty(
			strField(payload, "error"),
			strField(payload, "terminationReason"),
			resp,
		)
		return okResult(HookEvent{
			Harness:  "antigravity",
			Kind:     KindState,
			State:    StateDone,
			Summary:  cleanSummary(sum),
			Response: sanitizeHookText(resp),
			Payload:  sanitizePayload(payload),
		})

	case "SessionEnd":
		return okResult(HookEvent{
			Harness: "antigravity",
			Kind:    KindState,
			State:   StateIdle,
			Summary: cleanSummary(firstNonEmpty(strField(payload, "reason"), strField(payload, "terminationReason"))),
			Payload: sanitizePayload(payload),
		})

	case "SubagentStart":
		id := validSubagentID(firstNonEmpty(strField(payload, "agent_id"), strField(payload, "subagent_id")))
		if id == "" {
			return Result{SkipReason: "missing or invalid agent_id"}
		}
		return okResult(HookEvent{
			Harness:        "antigravity",
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
			Harness:        "antigravity",
			Kind:           KindSubagent,
			SubagentID:     id,
			SubagentAction: SubagentStop,
			Summary:        cleanSummary(strField(payload, "agent_type")),
			Payload:        sanitizePayload(payload),
		})

	default:
		return Result{SkipReason: "unmapped antigravity event " + evName}
	}
}

func antigravityToolName(payload map[string]any) string {
	return firstNonEmpty(
		strField(payload, "tool_name"),
		strField(mapField(payload, "toolCall"), "name"),
	)
}

func antigravityToolArgs(payload map[string]any) map[string]any {
	if m := mapField(payload, "tool_input"); m != nil {
		return m
	}
	return mapField(mapField(payload, "toolCall"), "args")
}

func antigravityToolSummary(tool string, args map[string]any) string {
	target := firstNonEmpty(
		toolTarget(args),
		strField(args, "CommandLine"),
		strField(args, "Path"),
		strField(args, "TargetFile"),
		strField(args, "AbsolutePath"),
		strField(args, "Url"),
		strField(args, "Query"),
	)
	switch {
	case tool != "" && target != "":
		return cleanSummary(tool + ": " + target)
	case tool != "":
		return cleanSummary(tool)
	default:
		return cleanSummary(target)
	}
}

func antigravityWrittenFiles(tool string, args map[string]any) []string {
	files := extractWrittenFiles(tool, args)
	if len(files) > 0 || !isWriteTool(tool) || len(args) == 0 {
		return files
	}
	for _, k := range []string{"Path", "AbsolutePath", "FilePath"} {
		if p := strings.TrimSpace(strField(args, k)); p != "" {
			return []string{p}
		}
	}
	return nil
}

// isAntigravityRaw reports whether raw stdin JSON identifies an Antigravity
// hook payload (camelCase hookEventName or conversationId), used to guarantee
// the "{}\n" stdout JSON contract even when Translate skips an event.
func isAntigravityRaw(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil || payload == nil {
		return false
	}
	return strField(payload, "conversationId") != "" || strField(payload, "hookEventName") != ""
}
