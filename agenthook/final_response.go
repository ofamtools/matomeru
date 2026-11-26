package agenthook

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func completedTurn(harness string, payload map[string]any, summary, response, final string) Result {
	return okResult(HookEvent{
		Harness:       harness,
		Kind:          KindState,
		State:         StateDone,
		Summary:       cleanSummary(summary),
		Response:      sanitizeHookText(response),
		FinalResponse: sanitizeHookText(final),
		Payload:       sanitizePayload(payload),
	})
}

func terminalResponse(event string, payload map[string]any, keys ...string) string {
	switch strings.ToLower(event) {
	case "stop", "afteragent", "agent_end", "done":
	default:
		return ""
	}
	for _, key := range keys {
		if response := strField(payload, key); strings.TrimSpace(response) != "" {
			return response
		}
	}
	return ""
}

type transcriptContent []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (content transcriptContent) text(blockType string) string {
	var out strings.Builder
	for _, block := range content {
		if block.Type == blockType {
			out.WriteString(block.Text)
		}
	}
	return out.String()
}

func codexFinalResponse(payload map[string]any) string {
	turnID := firstNonEmpty(strField(payload, "turn_id"), strField(payload, "turn-id"))
	candidate := firstNonEmpty(strField(payload, "last_assistant_message"), strField(payload, "last-assistant-message"))
	return scanTranscript(payload, func(lines *bufio.Scanner) string {
		var answer, phase, activeTurn string
		for lines.Scan() {
			var row struct {
				Type    string `json:"type"`
				Payload struct {
					Type    string          `json:"type"`
					TurnID  string          `json:"turn_id"`
					Role    string          `json:"role"`
					Phase   string          `json:"phase"`
					Content json.RawMessage `json:"content"`
				} `json:"payload"`
			}
			if json.Unmarshal(lines.Bytes(), &row) != nil {
				continue
			}
			p := row.Payload
			switch {
			case row.Type == "turn_context" || row.Type == "event_msg" && p.Type == "task_started":
				if p.TurnID != activeTurn {
					activeTurn, answer, phase = p.TurnID, "", ""
				}
			case row.Type == "event_msg" && (p.Type == "turn_aborted" || p.Type == "error"):
				answer, phase = "", ""
			case row.Type == "response_item":
				if p.Type == "additional_tools" {
					continue
				}
				// A later reasoning, tool, or other model item invalidates earlier text.
				answer, phase = "", ""
				if p.Type == "message" && p.Role == "assistant" {
					var content transcriptContent
					if json.Unmarshal(p.Content, &content) == nil {
						answer, phase = content.text("output_text"), p.Phase
					}
				}
			}
		}
		if activeTurn == "" || turnID != "" && activeTurn != turnID {
			return ""
		}
		if phase == "final_answer" && (turnID != "" || sameAnswer(answer, candidate)) {
			return answer
		}
		// Older transcripts omit phase. A same-turn terminal hook must confirm
		// the last assistant item, with no subsequent model or tool activity.
		if phase == "" && turnID != "" && sameAnswer(answer, candidate) {
			return answer
		}
		return ""
	})
}

func claudeFinalResponse(payload map[string]any) string {
	candidate := strField(payload, "last_assistant_message")
	if transcriptPath(payload) == "" {
		return candidate
	}
	return scanTranscript(payload, func(lines *bufio.Scanner) string {
		var answer, messageID string
		hasUserTurn := false
		for lines.Scan() {
			var row struct {
				Type        string `json:"type"`
				IsSidechain bool   `json:"isSidechain"`
				Message     struct {
					ID      string          `json:"id"`
					Stop    string          `json:"stop_reason"`
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(lines.Bytes(), &row) != nil || row.IsSidechain {
				continue
			}
			var content transcriptContent
			_ = json.Unmarshal(row.Message.Content, &content)
			switch row.Type {
			case "user":
				if content.text("text") != "" || strings.HasPrefix(string(row.Message.Content), `"`) {
					hasUserTurn = true
				}
				answer, messageID = "", ""
			case "assistant":
				if row.Message.ID != messageID || row.Message.Stop != "end_turn" {
					answer, messageID = "", row.Message.ID
				}
				if row.Message.Stop == "end_turn" {
					answer += content.text("text")
				}
			}
		}
		if hasUserTurn && sameAnswer(answer, candidate) {
			return answer
		}
		return ""
	})
}

func sameAnswer(transcript, hook string) bool {
	return strings.TrimSpace(hook) != "" && sanitizeHookText(transcript) == sanitizeHookText(hook)
}

func transcriptPath(payload map[string]any) string {
	return firstNonEmpty(strField(payload, "transcript_path"), strField(payload, "transcriptPath"))
}

func scanTranscript(payload map[string]any, scan func(*bufio.Scanner) string) string {
	path := transcriptPath(payload)
	if !filepath.IsAbs(path) {
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ""
	}

	const maxBytes = 2 << 20
	start := max(opened.Size()-maxBytes, 0)
	lines := bufio.NewScanner(io.NewSectionReader(f, start, opened.Size()-start))
	lines.Buffer(make([]byte, 64<<10), maxBytes)
	if start > 0 {
		lines.Scan() // The bounded tail can begin in the middle of a JSONL record.
	}
	answer := scan(lines)
	if lines.Err() != nil {
		return ""
	}
	return answer
}
