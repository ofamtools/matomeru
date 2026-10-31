package contextitems

import "slices"

// Origins is also the preference order for the primary origin: a
// conversational sighting wins, while Origins() keeps every known source.
var Origins = []string{"prompt", "response", "manual", "mcp", "thinking", "tool", "scrollback", "transcript", "command", "unknown"}

// Source origins are data, including the spellings written by older mtm.
var sourceOrigins = map[string]string{
	"prompt": "prompt", "response": "response",
	"thinking": "thinking", "reasoning": "thinking",
	"tool": "tool", "tool_call": "tool", "tool_use": "tool", "tool_result": "tool", "toolCall": "tool", "toolResult": "tool",
	"scrollback": "scrollback", "screen": "scrollback",
	"transcript": "transcript", "history": "command", "command": "command", "mcp": "mcp",
	"create": "manual", "mtm": "manual", "manual": "manual",
}
var entryOriginKinds = map[string]bool{
	"": true, "turn": true, "transcript": true, "history": true, "activity": true,
}
var entryKindOrigins = map[string]string{
	"prompt": "prompt", "prompt-submitted": "prompt",
	"response": "response", "response-completed": "response",
	"thinking": "thinking", "reasoning": "thinking",
	"tool": "tool", "tool_call": "tool", "tool_use": "tool", "tool_result": "tool", "toolCall": "tool", "toolResult": "tool",
	"command": "command",
}

// Origin classifies stored provenance, never the item's text or kind.
func (s Source) Origin() string {
	if entryOriginKinds[s.Kind] {
		if origin := entryKindOrigins[s.EntryKind]; origin != "" {
			return origin
		}
	}
	if origin := sourceOrigins[s.Kind]; origin != "" {
		return origin
	}
	return "unknown"
}

// Origins derives all recorded origins without rewriting legacy rows. A
// provenance turn id alone cannot distinguish prompt from response.
func (it Item) Origins() []string {
	out := []string{}
	for _, origin := range Origins {
		if slices.ContainsFunc(it.Sources, func(s Source) bool { return s.Origin() == origin }) {
			out = append(out, origin)
		}
	}
	if len(out) == 0 {
		return []string{"unknown"}
	}
	return out
}

// Origin is the most conversational origin; Origins keeps the rest.
func (it Item) Origin() string { return it.Origins()[0] }
