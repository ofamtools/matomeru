package contextitems

import "slices"

// Origins is also the preference order for the primary origin: a
// conversational sighting wins, while Origins() keeps every known source.
var Origins = []string{"prompt", "response", "manual", "mcp", "thinking", "tool", "scrollback", "transcript", "command", "unknown"}

// Source origins are data, including the spellings written by older mtm.
var sourceOrigins = map[string]string{
	"prompt": "prompt", "response": "response", "thinking": "thinking", "tool": "tool",
	"scrollback": "scrollback", "screen": "scrollback",
	"transcript": "transcript", "history": "command", "command": "command", "mcp": "mcp",
	"create": "manual", "mtm": "manual", "manual": "manual",
}
var entryOrigins = map[[2]string]string{
	{"turn", "prompt-submitted"}: "prompt", {"turn", "response-completed"}: "response",
	{"transcript", "prompt-submitted"}: "prompt", {"transcript", "response-completed"}: "response",
	{"transcript", "thinking"}: "thinking", {"transcript", "reasoning"}: "thinking",
	{"transcript", "tool"}: "tool", {"transcript", "tool_call"}: "tool", {"transcript", "tool_use"}: "tool", {"transcript", "tool_result"}: "tool", {"transcript", "toolCall"}: "tool", {"transcript", "toolResult"}: "tool",
	{"history", "prompt"}: "prompt", {"history", "command"}: "command",
	{"transcript", "prompt"}: "prompt", {"transcript", "command"}: "command",
	{"activity", "prompt"}: "prompt", {"activity", "command"}: "command",
	{"", "prompt"}: "prompt", {"", "command"}: "command",
}

// Origin classifies stored provenance, never the item's text or kind.
func (s Source) Origin() string {
	if origin := entryOrigins[[2]string{s.Kind, s.EntryKind}]; origin != "" {
		return origin
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
