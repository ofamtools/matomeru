package mcpui

import (
	"context"
	"encoding/json"
	"errors"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/triage"
	"ofam.dev/ochazuke/core"
)

// Triage tool names. An agent (or a hook) hands Matomeru what happened
// elsewhere as signals; the owner's triage rules sort them.
const (
	ToolTriageIngest   = "triage.ingest"
	ToolTriageEvaluate = "triage.evaluate"
)

// TriageTools serves the triage.* tools over a store.
type TriageTools struct {
	store *data.Store
}

// NewTriageTools returns the triage tools over store.
func NewTriageTools(store *data.Store) *TriageTools { return &TriageTools{store: store} }

// TriageIngestArgs are the arguments of triage.ingest: the fields of the
// signal to record.
type TriageIngestArgs struct {
	Title      string `json:"title,omitempty"`
	Source     string `json:"source,omitempty"`
	EventKind  string `json:"eventKind,omitempty"`
	Author     string `json:"author,omitempty"`
	Container  string `json:"container,omitempty"`
	Body       string `json:"body,omitempty"`
	URL        string `json:"url,omitempty"`
	OccurredAt string `json:"occurredAt,omitempty"`
	Task       string `json:"task,omitempty"`
	By         string `json:"by,omitempty"`
}

// TriageEvaluateArgs are the arguments of triage.evaluate. With a signal,
// that one is evaluated; without, every signal still unreviewed is.
type TriageEvaluateArgs struct {
	Signal string `json:"signal,omitempty"`
	By     string `json:"by,omitempty"`
}

// TriageResult is what both tools return.
type TriageResult struct {
	Signal *data.Record `json:"signal,omitempty"`
	triage.Result
}

// ToolSpecs returns the triage tools with JSON schemas of their real
// parameters.
func (t *TriageTools) ToolSpecs() []ToolSpec {
	by := text("who is writing: an agent ID recorded on the change set; default " + DefaultAgentID)
	return []ToolSpec{
		{
			Name:        ToolTriageIngest,
			Description: "Record a signal (something that happened elsewhere: a mention, a review request, a failed build) and run the owner's triage rules on it at once. Returns the signal as the rules left it and what they did.",
			InputSchema: object([]string{}, props{
				"title": text("what happened; default: the source and the event kind"), "source": text("where it came from: github, slack, ci, mail"),
				"eventKind": text("what kind of event it is: mention, review_request, failure"), "author": text("who or what caused it"),
				"container": text("where it happened: a repository, a channel, a thread"), "body": text("the text of the event"),
				"url": text("link to the event"), "occurredAt": text("when it happened (RFC 3339); default now"),
				"task": text("task ID the signal belongs to"), "by": by,
			}),
		},
		{
			Name:        ToolTriageEvaluate,
			Description: "Run the enabled triage rules: on one signal, or, with no signal, on every signal still unreviewed. Returns how many were evaluated and matched and every action applied.",
			InputSchema: object([]string{}, props{"signal": text("signal ID; empty evaluates every unreviewed signal"), "by": by}),
		},
	}
}

// HasTool reports whether name is one of the triage tools.
func (t *TriageTools) HasTool(name string) bool {
	return name == ToolTriageIngest || name == ToolTriageEvaluate
}

// Call runs one triage tool with JSON arguments.
func (t *TriageTools) Call(ctx context.Context, actor core.Actor, toolName string, rawArgs json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch toolName {
	case ToolTriageIngest:
		var args TriageIngestArgs
		if err := decodeArgs(rawArgs, &args); err != nil {
			return nil, err
		}
		return t.Ingest(agentActor(actor, args.By), args)
	case ToolTriageEvaluate:
		var args TriageEvaluateArgs
		if err := decodeArgs(rawArgs, &args); err != nil {
			return nil, err
		}
		return t.Evaluate(agentActor(actor, args.By), args)
	}
	return nil, errors.New("unknown tool " + toolName)
}

// Ingest records a signal and evaluates it.
func (t *TriageTools) Ingest(actor core.Actor, args TriageIngestArgs) (*TriageResult, error) {
	fields := map[string]any{
		"title": args.Title, "source": args.Source, "eventKind": args.EventKind, "author": args.Author,
		"container": args.Container, "body": args.Body, "url": args.URL, "occurredAt": args.OccurredAt,
	}
	if args.Task != "" {
		fields["task"] = core.Ref{Kind: data.KindTask, ID: args.Task}
	}
	rec, res, err := triage.Ingest(t.store, fields, actor)
	if err != nil {
		return nil, err
	}
	return &TriageResult{Signal: rec, Result: res}, nil
}

// Evaluate runs the rules on one signal or on every unreviewed one.
func (t *TriageTools) Evaluate(actor core.Actor, args TriageEvaluateArgs) (*TriageResult, error) {
	if args.Signal == "" {
		res, err := triage.ApplyAll(t.store, actor)
		if err != nil {
			return nil, err
		}
		return &TriageResult{Result: res}, nil
	}
	rec, err := t.store.Get(core.Ref{Kind: data.KindSignal, ID: args.Signal})
	if err != nil {
		return nil, err
	}
	res, err := triage.Evaluate(t.store, *rec, actor)
	if err != nil {
		return nil, err
	}
	if fresh, gerr := t.store.Get(rec.Ref()); gerr == nil {
		rec = fresh
	}
	return &TriageResult{Signal: rec, Result: res}, nil
}
