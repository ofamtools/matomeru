package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/history"
	"ofam.dev/matomeru/internal/work"
)

// Context from turns. When the journal takes a prompt or an answer (the
// daemon's activity, which the journal reader follows in the background,
// or a capture hook through the CLI), the same extractor runs over the
// turn's text and merges what it finds with the same de-duplication as a
// pane's output. Items belong to the turn's owner when its prompt went
// in (history.PaneTurn keeps it), not to the pane's owner now. The turn's
// prompt itself is not an item: the picker lists prompts from the journal
// (context_picker.go).
//
// Which text: a prompt that starts a turn gives the turn's prompt; a
// prompt that continues one (typed while the agent worked, a
// task-notification) gives its own text and the turn's owner; the
// daemon's summary of a captured prompt gives nothing (the capture holds
// the whole prompt); an answer gives its own text when the projection
// keeps it in a turn, nothing when a captured answer replaces it. The
// daemon cuts its texts (about 100 characters, then "..."); the cut word
// is left out.

// turnContextSource is the source kind of an item found in a turn.
const turnContextSource = "turn"

// turnContext extracts context items from the turns of journaled entries
// and returns how many were new. It reads the journal and writes the
// context table only, so it runs anywhere off the loop.
func turnContext(j *history.Journal, store *contextitems.Store, entries []history.Entry, origins []string) (int, error) {
	byWindow := map[string][]history.Entry{}
	for _, e := range entries {
		if e.Window == "" {
			continue
		}
		if e.Kind == "prompt-submitted" || e.Kind == "response-completed" {
			byWindow[e.Window] = append(byWindow[e.Window], e)
			continue
		}
		if kind := entryActivityKind(e); kind != "" && extractsFrom(origins, contextitems.Source{Kind: turnContextSource, EntryKind: kind}) {
			byWindow[e.Window] = append(byWindow[e.Window], e)
		}
	}
	windows := make([]string, 0, len(byWindow))
	for w := range byWindow {
		windows = append(windows, w)
	}
	slices.Sort(windows)
	added := 0
	var errs []error
	now := work.Now()
	mergeText := func(text string, src contextitems.Source, turnID string, owner contextitems.Owner) {
		if strings.TrimSpace(text) == "" || !extractsFrom(origins, src) {
			return
		}
		items := contextitems.Extract(text, 0, src)
		for i := range items {
			items[i].Provenance.TurnID = turnID
		}
		_, n, err := store.Merge(items, owner, now)
		if err != nil {
			errs = append(errs, err)
			return
		}
		added += n
	}
	for _, w := range windows {
		turns, err := j.Turns(w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, e := range byWindow[w] {
			if kind := entryActivityKind(e); kind != "" {
				t := activityTurn(turns, e)
				src := contextitems.Source{Kind: turnContextSource, EntryKind: kind, Session: t.Session, Window: t.Window, Pane: t.Pane, At: e.At, Cwd: e.Cwd}
				parts := []string{untruncated(cmp.Or(e.Detail, e.Summary))}
				if kind != "thinking" {
					if e.Target != "" {
						parts = append(parts, e.Target)
					}
					parts = append(parts, e.Files...)
				}
				mergeText(strings.Join(parts, "\n"), src, t.ID, contextitems.Owner{ProjectID: t.ProjectID, TaskID: t.TaskID})
				continue
			}
			t, text, ok := turnText(turns, e)
			if !ok {
				continue
			}
			if e.Turn == nil {
				text = untruncated(text)
			}
			owner := contextitems.Owner{ProjectID: t.ProjectID, TaskID: t.TaskID}
			src := contextitems.Source{Kind: turnContextSource, EntryKind: e.Kind, Session: t.Session, Window: t.Window, Pane: t.Pane, At: e.At, Cwd: e.Cwd}
			mergeText(text, src, t.ID, owner)
			if e.Kind == "response-completed" && e.Turn != nil {
				thinkingSrc, toolSrc := src, src
				thinkingSrc.EntryKind, toolSrc.EntryKind = "thinking", "tool"
				if extractsFrom(origins, thinkingSrc) || extractsFrom(origins, toolSrc) {
					thoughts, tools := turnNonProse(*e.Turn)
					mergeText(strings.Join(thoughts, "\n\n"), thinkingSrc, t.ID, owner)
					mergeText(strings.Join(tools, "\n\n"), toolSrc, t.ID, owner)
				}
			}
		}
	}
	return added, errors.Join(errs...)
}

func entryActivityKind(e history.Entry) string {
	switch e.Kind {
	case "thinking", "reasoning":
		return "thinking"
	case "tool", "tool_call", "tool_use", "tool_result", "toolCall", "toolResult":
		return e.Kind
	case "prompt-submitted", "response-completed", "session-start", "subagent-start", "subagent-stop":
		return ""
	}
	if e.Tool != "" {
		return "tool"
	}
	return ""
}

func activityTurn(turns []history.PaneTurn, e history.Entry) history.PaneTurn {
	out := history.PaneTurn{ID: e.Provenance.TurnID, Session: e.Session, Window: e.Window, Pane: e.Pane, ProjectID: e.ProjectID, TaskID: e.TaskID}
	for _, t := range turns {
		if t.StartedAt <= e.At {
			out = t
		}
	}
	return out
}

func turnNonProse(turn history.Turn) (thinking, tools []string) {
	if len(turn.ResponseMessage) > 0 {
		th, tl := messageNonProse(turn.ResponseMessage)
		thinking = append(thinking, th...)
		tools = append(tools, tl...)
	}
	if turn.TranscriptPath != "" {
		th, tl := transcriptFileNonProse(turn.TranscriptPath)
		thinking = append(thinking, th...)
		tools = append(tools, tl...)
	}
	return thinking, tools
}

type nonProseBlock struct {
	Type       string          `json:"type"`
	Kind       string          `json:"kind"`
	Text       string          `json:"text"`
	Thinking   string          `json:"thinking"`
	Reasoning  string          `json:"reasoning"`
	Tool       string          `json:"tool"`
	ToolID     string          `json:"tool_id"`
	ToolCallID string          `json:"toolCallId"`
	ToolUseID  string          `json:"tool_use_id"`
	Input      json.RawMessage `json:"input"`
	Arguments  json.RawMessage `json:"arguments"`
	Content    json.RawMessage `json:"content"`
	Output     string          `json:"output"`
}

func messageNonProse(raw json.RawMessage) (thinking, tools []string) {
	if len(raw) == 0 {
		return nil, nil
	}
	var msg struct {
		Role       string          `json:"role"`
		Thinking   string          `json:"thinking"`
		Reasoning  string          `json:"reasoning"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"toolCallId"`
		Output     string          `json:"output"`
	}
	content := raw
	if json.Unmarshal(raw, &msg) == nil && (msg.Role != "" || msg.Thinking != "" || msg.Reasoning != "" || len(msg.Content) > 0 || msg.ToolCallID != "" || msg.Output != "") {
		for _, s := range []string{msg.Thinking, msg.Reasoning} {
			if strings.TrimSpace(s) != "" {
				thinking = append(thinking, s)
			}
		}
		if msg.Role == "tool" || msg.Role == "toolResult" || msg.ToolCallID != "" {
			for _, s := range []string{jsonText(msg.Content), msg.Output} {
				if strings.TrimSpace(s) != "" {
					tools = append(tools, s)
				}
			}
			return thinking, tools
		}
		content = msg.Content
	}
	var blocks []nonProseBlock
	if json.Unmarshal(content, &blocks) != nil {
		var single nonProseBlock
		if json.Unmarshal(content, &single) == nil && (single.Type != "" || single.Kind != "") {
			blocks = []nonProseBlock{single}
		}
	}
	for _, b := range blocks {
		kind := cmp.Or(b.Type, b.Kind)
		switch {
		case kind == "thinking" || kind == "reasoning":
			for _, s := range []string{b.Thinking, b.Reasoning, b.Text} {
				if strings.TrimSpace(s) != "" {
					thinking = append(thinking, s)
				}
			}
		case kind == "tool" || kind == "tool_call" || kind == "tool_use" || kind == "tool_result" || kind == "toolCall" || kind == "toolResult" ||
			b.Tool != "" || b.ToolID != "" || b.ToolCallID != "" || b.ToolUseID != "":
			for _, s := range []string{b.Text, b.Output, jsonText(b.Input), jsonText(b.Arguments), jsonText(b.Content)} {
				if strings.TrimSpace(s) != "" {
					tools = append(tools, s)
				}
			}
		}
	}
	return thinking, tools
}

func transcriptFileNonProse(path string) (thinking, tools []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		var line struct {
			Type          string          `json:"type"`
			ToolUseResult json.RawMessage `json:"toolUseResult"`
			Message       json.RawMessage `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		var msg struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(line.Message, &msg)
		role := cmp.Or(msg.Role, line.Type)
		if role == "user" {
			var text string
			var blocks []nonProseBlock
			if json.Unmarshal(msg.Content, &text) == nil && text != "" {
				thinking, tools = nil, nil
				continue
			}
			if json.Unmarshal(msg.Content, &blocks) == nil && len(blocks) > 0 && cmp.Or(blocks[0].Type, blocks[0].Kind) == "text" {
				thinking, tools = nil, nil
				continue
			}
		}
		th, tl := messageNonProse(line.Message)
		thinking = append(thinking, th...)
		tools = append(tools, tl...)
		if s := jsonText(line.ToolUseResult); strings.TrimSpace(s) != "" {
			tools = append(tools, s)
		}
	}
	return thinking, tools
}

func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	var parts []string
	var walk func(any)
	walk = func(x any) {
		switch val := x.(type) {
		case string:
			if strings.TrimSpace(val) != "" {
				parts = append(parts, val)
			}
		case []any:
			for _, elem := range val {
				walk(elem)
			}
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(val)) {
				if k != "type" && k != "id" && k != "tool_use_id" && k != "toolCallId" && k != "role" {
					walk(val[k])
				}
			}
		}
	}
	walk(v)
	return strings.Join(parts, "\n")
}

// turnText is the turn an entry belongs to and the text of it to read.
func turnText(turns []history.PaneTurn, e history.Entry) (history.PaneTurn, string, bool) {
	if e.Kind == "response-completed" {
		for _, t := range turns {
			for _, r := range t.Responses {
				if r.ID == e.ID {
					return t, r.Text, true
				}
			}
		}
		return history.PaneTurn{}, "", false
	}
	if i := slices.IndexFunc(turns, func(t history.PaneTurn) bool { return t.ID == e.ID }); i >= 0 {
		return turns[i], turns[i].Prompt, true
	}
	var open *history.PaneTurn
	for i := range turns {
		t := &turns[i]
		// The daemon's summary of a captured prompt.
		if e.Turn == nil && t.Captured && abs64(t.StartedAt-e.At) <= 5_000 {
			return history.PaneTurn{}, "", false
		}
		if t.StartedAt <= e.At {
			open = t
		}
	}
	text := cmp.Or(e.Detail, e.Summary)
	if open == nil || text == "" {
		return history.PaneTurn{}, "", false
	}
	return *open, text, true
}

// untruncated is a daemon text without the word its cut (a trailing
// "..." or "…", about 100 characters in) left half: a half path or URL
// names nothing.
func untruncated(text string) string {
	trimmed := strings.TrimRightFunc(text, unicode.IsSpace)
	if !strings.HasSuffix(trimmed, "...") && !strings.HasSuffix(trimmed, "…") {
		return text
	}
	if cut := strings.LastIndexFunc(trimmed, unicode.IsSpace); cut >= 0 {
		return trimmed[:cut]
	}
	return ""
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// turnsExtracted is what context-item.turns wrote.
type turnsExtracted struct {
	Entries int `json:"entries"`
	New     int `json:"new"`
}

// extractTurns is context-item.turns: every journaled turn at once, off
// the loop, for the turns journaled before extraction from turns existed.
// Running it again adds nothing.
func (a *App) extractTurns(ctx ochazuke.Ctx) ochazuke.Result {
	j, store, origins := a.hist, a.contexts, a.contextExtraction()
	return ochazuke.Pending(func(context.Context) (any, error) {
		entries, err := j.All()
		if err != nil {
			return nil, err
		}
		n, err := turnContext(j, store, entries, origins)
		return turnsExtracted{Entries: len(entries), New: n}, err
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err != nil {
			return
		}
		if err := a.reloadContext(); err != nil {
			ctx.Error(err.Error())
		}
		a.contextChanged()
	})
}

// capturedContext is a capture verb's tail: the entry it journaled goes
// through turnContext off the loop, and the capture's own result is the
// command's. A failed extraction never fails the capture.
func (a *App) capturedContext(id string, result any) ochazuke.Result {
	j, store, origins := a.hist, a.contexts, a.contextExtraction()
	return ochazuke.Pending(func(context.Context) (any, error) {
		if e, err := j.Get(id); err == nil {
			_, _ = turnContext(j, store, []history.Entry{e}, origins)
		}
		return result, nil
	}).Then(func(ochazuke.Ctx, any, error) {
		if err := a.reloadContext(); err != nil {
			a.r.UI().Error(err.Error())
		}
		a.contextChanged()
	})
}

var transcriptConversationKinds = map[string]string{"user": "prompt-submitted", "assistant": "response-completed"}

type paneTranscript struct {
	Text    string
	Entries []daemon.TranscriptEntry
}

// transcriptContext returns the extraction for a transcript read of the
// pane at inst, safe off the loop: the owner is read now, on the loop.
// New items reach the views with the journal's next burst.
func (a *App) transcriptContext(inst *ochazuke.Instance) func(p pane, text paneTranscript) {
	var owner contextitems.Owner
	if p, err := a.paneAt(inst); err == nil {
		owner = a.contextOwner(p)
	}
	store, feed, origins := a.contexts, a.feed, a.contextExtraction()
	return func(p pane, text paneTranscript) {
		var items []contextitems.Item
		if len(text.Entries) == 0 {
			src := contextSource(p, "transcript")
			if extractsFrom(origins, src) {
				items = contextitems.Extract(text.Text, 0, src)
			}
		}
		for _, entry := range text.Entries {
			src := contextSource(p, "transcript")
			src.EntryKind, src.At = transcriptEntryKind(entry), entry.At
			if extractsFrom(origins, src) {
				items = append(items, contextitems.Extract(entry.Text, 0, src)...)
				if entry.Target != "" {
					items = append(items, contextitems.Extract(entry.Target, 0, src)...)
				}
			}
		}
		if written, _, err := store.Merge(items, owner, work.Now()); err == nil && len(written) > 0 && feed != nil {
			feed.contextAdded.Store(true)
			feed.signal()
		}
	}
}

func transcriptEntryKind(entry daemon.TranscriptEntry) string {
	switch {
	case entry.Tool != "" || entry.ToolID != "" || entry.Role == "tool":
		switch entry.Kind {
		case "tool", "tool_call", "tool_use", "tool_result", "toolCall", "toolResult":
			return entry.Kind
		}
		return "tool"
	case entry.Kind == "thinking" || entry.Kind == "reasoning" || entry.Role == "thinking":
		if entry.Kind == "reasoning" {
			return "reasoning"
		}
		return "thinking"
	case entry.Kind == "text":
		if kind := transcriptConversationKinds[entry.Role]; kind != "" {
			return kind
		}
	}
	return entry.Kind
}
