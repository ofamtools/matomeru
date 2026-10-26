package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/history"
)

// Turn capture through the CLI, for the hooks mtm setup installs (see
// setup.go): each runs in the agent's pane, so TUIOS_SESSION and
// TUIOS_PANE_ID name the pane, and writes the journal whether or not the
// TUI runs.
//
//   - history capture-turn takes a whole turn record (matomeru-old's: a
//     start without completed_at, then the completion with the response),
//     as --json '{...}' or, with --stdin, on standard input. The OMP
//     extension calls it.
//   - history capture-hook HARNESS reads one hook payload of Claude Code
//     or Codex on standard input: UserPromptSubmit journals the start
//     with the full prompt, Stop completes the conversation's open turn
//     with last_assistant_message (else the transcript's last answer).
//     Anything else, or a call from outside a TUIOS pane, does nothing.

// hookHarnesses are the harnesses capture-hook reads payloads of.
var hookHarnesses = []string{"claude-code", "codex"}

// defineCapture adds the capture verbs to the history noun.
func (a *App) defineCapture() {
	a.r.Define("history").
		Verb("capture-turn", a.captureTurn, "Journal a turn record (a start, or its completion with the response) from --json or --stdin").
		Verb("capture-hook", a.captureHook, "Journal a turn from one Claude Code or Codex hook payload on stdin (UserPromptSubmit, Stop)").Positional("harness")
	turn := map[string]ochazuke.Parameter{
		"stdin":            {Type: "boolean", Description: "Read the turn record as JSON from standard input"},
		"id":               {Type: "string", Description: "The turn's id, the same for its start and completion"},
		"session":          {Type: "string"},
		"window":           {Type: "string"},
		"pane":             {Type: "string"},
		"harness":          {Type: "string"},
		"conversation_id":  {Type: "string", Description: "The harness's session id"},
		"prompt":           {Type: "string", Description: "The whole request"},
		"response":         {Type: "string", Description: "The whole final answer (with completed_at)"},
		"started_at":       {Type: "integer", Description: "Unix milliseconds"},
		"completed_at":     {Type: "integer", Description: "Unix milliseconds; omit for the start"},
		"transcript_path":  {Type: "string"},
		"response_message": {Type: "object"},
	}
	a.parameters("history.capture-turn", turn)
	a.parameters("history.capture-hook", map[string]ochazuke.Parameter{"harness": {Type: "string", Required: true, Enum: hookHarnesses}})
}

// captureTurn is history.capture-turn.
func (a *App) captureTurn(ctx ochazuke.Ctx) ochazuke.Result {
	args := ochazuke.Arguments{}
	for k, v := range ctx.Args {
		args[k] = v
	}
	if stdin, _ := args["stdin"].(bool); stdin {
		delete(args, "stdin")
		var fromStdin map[string]any
		if err := json.NewDecoder(a.input()).Decode(&fromStdin); err != nil {
			return ochazuke.Failed(fmt.Errorf("read the turn from stdin: %w", err))
		}
		for k, v := range fromStdin {
			args[k] = v
		}
	}
	delete(args, "stdin")
	raw, err := json.Marshal(args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	var t history.Turn
	if err := json.Unmarshal(raw, &t); err != nil {
		return ochazuke.Failed(fmt.Errorf("turn record: %w", err))
	}
	id, err := a.hist.Capture(t)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(map[string]string{"id": id})
}

// hookPayload is the part of a Claude Code or Codex hook payload capture
// reads (both name these fields alike).
type hookPayload struct {
	Event          string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TurnID         string `json:"turn_id"` // Codex
	Prompt         string `json:"prompt"`
	LastMessage    string `json:"last_assistant_message"`
	TranscriptPath string `json:"transcript_path"`
}

// captureHook is history.capture-hook.
func (a *App) captureHook(ctx ochazuke.Ctx) ochazuke.Result {
	harness := ochazuke.StringArg(ctx.Args, "harness", "")
	session, window := a.getenv("TUIOS_SESSION"), a.getenv("TUIOS_PANE_ID")
	var p hookPayload
	if err := json.NewDecoder(a.input()).Decode(&p); err != nil {
		return ochazuke.Failed(fmt.Errorf("read the hook payload: %w", err))
	}
	if session == "" || window == "" {
		return ochazuke.Done(map[string]string{"skipped": "not in a TUIOS pane"})
	}
	conversation := p.SessionID
	if conversation == "" {
		conversation = window
	}
	switch p.Event {
	case "UserPromptSubmit":
		if strings.TrimSpace(p.Prompt) == "" {
			return ochazuke.Done(map[string]string{"skipped": "empty prompt"})
		}
		id := p.TurnID
		if id == "" {
			id = history.NewID()
		}
		turn := history.Turn{ID: id, Session: session, Window: window, Pane: a.getenv("TUIOS_WINDOW_NAME"), Harness: harness,
			ConversationID: conversation, Prompt: p.Prompt, StartedAt: history.Now(), TranscriptPath: p.TranscriptPath}
		entry, err := a.hist.Capture(turn)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(map[string]string{"id": entry})
	case "Stop":
		open, ok, err := a.hist.OpenCapture(session, window, harness, conversation)
		if err != nil {
			return ochazuke.Failed(err)
		}
		if !ok {
			return ochazuke.Done(map[string]string{"skipped": "no open turn"})
		}
		response := p.LastMessage
		if response == "" {
			response = lastAnswer(cmpPath(p.TranscriptPath, open.TranscriptPath))
		}
		entry, err := a.hist.Complete(open, response, history.Now())
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(map[string]string{"id": entry})
	}
	return ochazuke.Done(map[string]string{"skipped": "event " + p.Event})
}

func cmpPath(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// lastAnswer is the text of the assistant messages after the last user
// prompt in a Claude Code transcript (JSON Lines), "" when there is none
// or the file cannot be read.
func lastAnswer(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var parts []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		var line struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		var text string
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(line.Message.Content, &text) != nil {
			_ = json.Unmarshal(line.Message.Content, &blocks)
		}
		switch line.Type {
		case "user":
			// A prompt (text), not a tool result, starts a new answer.
			if text != "" || len(blocks) > 0 && blocks[0].Type == "text" {
				parts = nil
			}
		case "assistant":
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					parts = append(parts, b.Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// input is the CLI's standard input (empty outside the CLI).
func (a *App) input() io.Reader {
	if a.stdin == nil {
		return strings.NewReader("")
	}
	return a.stdin
}
