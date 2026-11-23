package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Lane tool names. An agent starts a lane for the work it does, keeps its
// status and summary current, logs what happened and attaches what it made.
const (
	ToolLaneStart  = "lane.start"
	ToolLaneUpdate = "lane.update"
	ToolLaneLog    = "lane.log"
	ToolLaneAttach = "lane.attach"
)

// The environment a pane's program runs in: which pane it is and which task
// the pane works on. The lane tools and the ui.* tools read them as defaults.
const (
	EnvPaneID = "MTM_PANE_ID"
	EnvTaskID = "MTM_TASK_ID"
)

// LogKinds are the kinds of a lane.log entry. Any other word is accepted too.
var LogKinds = []string{"milestone", "decision", "blocker", "verification", "note"}

// attachRelations maps what lane.attach attaches to the relation it writes.
var attachRelations = map[string]string{
	data.KindDocument: data.RelCreated,
	data.KindPage:     data.RelCreated,
	data.KindQuestion: data.RelCreated,
	data.KindCommand:  data.RelCreated,
	data.KindContext:  data.RelContext,
	data.KindRun:      data.RelRun,
	data.KindPane:     data.RelClaim,
}

// LaneTools serves the lane.* tools over a store. Getenv reads the
// environment (nil is os.Getenv), so tests set a pane and task without
// touching the process's own.
type LaneTools struct {
	store  *data.Store
	Getenv func(string) string
}

// NewLaneTools returns the lane tools over store.
func NewLaneTools(store *data.Store) *LaneTools { return &LaneTools{store: store} }

func (l *LaneTools) env(name string) string {
	if l.Getenv != nil {
		return l.Getenv(name)
	}
	return os.Getenv(name)
}

// LaneStartArgs are the arguments of lane.start.
type LaneStartArgs struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	OwnerWords  string `json:"ownerWords,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Task        string `json:"task,omitempty"`
	ParentLane  string `json:"parentLane,omitempty"`
	Pane        string `json:"pane,omitempty"`
	Repo        string `json:"repo,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Worktree    string `json:"worktree,omitempty"`
	BaseCommit  string `json:"baseCommit,omitempty"`
	HeadCommit  string `json:"headCommit,omitempty"`
	Harness     string `json:"harness,omitempty"`
	AgentModel  string `json:"agentModel,omitempty"`
	By          string `json:"by,omitempty"`
}

// LaneUpdateArgs are the arguments of lane.update. Only the fields given are
// written.
type LaneUpdateArgs struct {
	Lane        string  `json:"lane"`
	Title       *string `json:"title,omitempty"`
	OwnerWords  *string `json:"ownerWords,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	Summary     *string `json:"summary,omitempty"`
	Task        *string `json:"task,omitempty"`
	ParentLane  *string `json:"parentLane,omitempty"`
	Repo        *string `json:"repo,omitempty"`
	Branch      *string `json:"branch,omitempty"`
	Worktree    *string `json:"worktree,omitempty"`
	BaseCommit  *string `json:"baseCommit,omitempty"`
	HeadCommit  *string `json:"headCommit,omitempty"`
	Harness     *string `json:"harness,omitempty"`
	AgentModel  *string `json:"agentModel,omitempty"`
	By          string  `json:"by,omitempty"`
}

// LaneLogArgs are the arguments of lane.log.
type LaneLogArgs struct {
	Lane string `json:"lane"`
	Kind string `json:"kind,omitempty"`
	Text string `json:"text"`
	Body string `json:"body,omitempty"`
	By   string `json:"by,omitempty"`
}

// LaneAttachArgs are the arguments of lane.attach.
type LaneAttachArgs struct {
	Lane     string `json:"lane"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Relation string `json:"relation,omitempty"`
	By       string `json:"by,omitempty"`
}

// LaneResult is what lane.start and lane.update return.
type LaneResult struct {
	Lane  *data.Record   `json:"lane"`
	Claim *data.Relation `json:"claim,omitempty"`
}

// LaneLogResult is what lane.log returns.
type LaneLogResult struct {
	Note     *data.Record   `json:"note"`
	Relation *data.Relation `json:"relation"`
}

// LaneAttachResult is what lane.attach returns.
type LaneAttachResult struct {
	Relation *data.Relation `json:"relation"`
}

func laneRef(id string) core.Ref { return core.Ref{Kind: data.KindLane, ID: id} }

func (l *LaneTools) statuses() []string {
	if l.store != nil {
		if schema, ok := l.store.Registry().Get(data.KindLane); ok {
			if col, ok := schema.StatusColumn("laneStatus"); ok {
				return col.Names()
			}
		}
	}
	return []string{"planned", "running", "review", "merged", "done", "parked", "cancelled"}
}

// ToolSpecs returns the lane tools with JSON schemas of their real parameters.
func (l *LaneTools) ToolSpecs() []ToolSpec {
	by := text("who is writing: an agent ID recorded on the change set; default " + DefaultAgentID)
	status := oneOf("where the work stands", l.statuses())
	fields := func(extra props) props {
		out := props{
			"title": text("what the lane is for"), "ownerWords": text("the owner's own wording of what this lane is for and why it matters"),
			"description": text("detailed dossier context, scope, and motivation"),
			"status":      status, "summary": text("what happened so far"),
			"task": text("task ID the lane works on"), "parentLane": text("lane ID this lane is a part of"),
			"repo": text("repository"), "branch": text("branch the work is on"), "worktree": text("worktree directory"),
			"baseCommit": text("commit the work started from"), "headCommit": text("newest commit of the work"),
			"harness": text("agent harness doing the work"), "agentModel": text("model the agent runs"), "by": by,
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	return []ToolSpec{
		{
			Name: ToolLaneStart,
			Description: "Start a lane: a dossier record of one piece of work. Populate ownerWords (the owner's wording) and description (scope and motivation), not just a bare title. task defaults to $" + EnvTaskID + " and the lane is claimed by pane (default $" + EnvPaneID +
				"), so the pane shows the lane's progress. Returns {lane, claim}.",
			InputSchema: object([]string{"title"}, fields(props{
				"id":   text("lane ID; generated when omitted"),
				"pane": text("pane ID that claims the lane; default $" + EnvPaneID),
			})),
		},
		{
			Name:        ToolLaneUpdate,
			Description: "Update a lane dossier: its ownerWords, description, status, summary, head commit or any other field. Keep dossier context and owner wording filled out, not just a bare title. Only the fields given are written. Prefer archive over delete (or set status to parked/cancelled) when retiring lanes. Returns {lane}.",
			InputSchema: object([]string{"lane"}, fields(props{"lane": text("lane ID")})),
		},
		{
			Name:        ToolLaneLog,
			Description: "Log what happened on a lane: a timestamped note about it, shown in the lane's timeline in order. Returns {note, relation}.",
			InputSchema: object([]string{"lane", "text"}, props{
				"lane": text("lane ID"),
				"kind": text("what the entry is: " + strings.Join(LogKinds, ", ") + " (default note); any word is accepted"),
				"text": text("one line: what happened"),
				"body": text("details, when one line is not enough"),
				"by":   by,
			}),
		},
		{
			Name:        ToolLaneAttach,
			Description: "Attach a record to a lane: a document or ui.page it created, a run it started, a plan document, or a pane that claims it. Returns {relation}.",
			InputSchema: object([]string{"lane", "kind", "id"}, props{
				"lane":     text("lane ID"),
				"kind":     text("kind of the record to attach: document, ui.page, run, pane, or any other"),
				"id":       text("ID of the record to attach"),
				"relation": text("relation to write: created, plan, run or claim; default by kind (document and ui.page created, run run, pane claim)"),
				"by":       by,
			}),
		},
	}
}

// HasTool reports whether name is one of the lane tools.
func (l *LaneTools) HasTool(name string) bool {
	return slices.Contains([]string{ToolLaneStart, ToolLaneUpdate, ToolLaneLog, ToolLaneAttach}, name)
}

// Call runs one lane tool with JSON arguments.
func (l *LaneTools) Call(ctx context.Context, actor core.Actor, toolName string, rawArgs json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		out any
		err error
	)
	switch toolName {
	case ToolLaneStart:
		var args LaneStartArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = l.Start(agentActor(actor, args.By), args)
		}
	case ToolLaneUpdate:
		var args LaneUpdateArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = l.Update(agentActor(actor, args.By), args)
		}
	case ToolLaneLog:
		var args LaneLogArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = l.Log(agentActor(actor, args.By), args)
		}
	case ToolLaneAttach:
		var args LaneAttachArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = l.Attach(agentActor(actor, args.By), args)
		}
	default:
		return nil, fmt.Errorf("mcpui: unknown tool %q", toolName)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", toolName, err)
	}
	return out, nil
}

// Start is lane.start: it stores a lane and relates it to the pane that
// claims it. The task and the pane come from the environment when the
// arguments leave them out; a pane's own task is used when neither names one.
func (l *LaneTools) Start(actor core.Actor, args LaneStartArgs) (*LaneResult, error) {
	if strings.TrimSpace(args.Title) == "" {
		return nil, errors.New("title is required")
	}
	if args.Pane == "" {
		args.Pane = l.env(EnvPaneID)
	}
	if args.Task == "" {
		args.Task = l.env(EnvTaskID)
	}
	if args.Task == "" && args.Pane != "" {
		if pane, err := l.store.Get(paneRef(args.Pane)); err == nil {
			if task, ok := pane.RefField("task"); ok {
				args.Task = task.ID
			}
		}
	}
	fields := map[string]any{"title": args.Title}
	for name, v := range map[string]string{"ownerWords": args.OwnerWords, "description": args.Description,
		"laneStatus": args.Status, "summary": args.Summary, "repo": args.Repo, "branch": args.Branch,
		"worktree": args.Worktree, "baseCommit": args.BaseCommit, "headCommit": args.HeadCommit, "harness": args.Harness, "agentModel": args.AgentModel} {
		if v != "" {
			fields[name] = v
		}
	}
	if args.Task != "" {
		fields["task"] = taskRef(args.Task)
	}
	if args.ParentLane != "" {
		fields["parentLane"] = laneRef(args.ParentLane)
	}
	lane, _, err := l.store.Create(data.CreateInput{Kind: data.KindLane, ID: args.ID, Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	out := &LaneResult{Lane: lane}
	if args.Pane != "" {
		if out.Claim, _, err = l.store.Relate(data.RelateInput{From: lane.Ref(), Kind: data.RelClaim, To: paneRef(args.Pane)}, actor); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Update is lane.update: a patch of the fields given. An empty string
// removes the field.
func (l *LaneTools) Update(actor core.Actor, args LaneUpdateArgs) (*LaneResult, error) {
	if args.Lane == "" {
		return nil, errors.New("lane is required")
	}
	if _, err := l.store.Get(laneRef(args.Lane)); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	for name, v := range map[string]*string{"title": args.Title, "ownerWords": args.OwnerWords, "description": args.Description,
		"laneStatus": args.Status, "summary": args.Summary, "repo": args.Repo,
		"branch": args.Branch, "worktree": args.Worktree, "baseCommit": args.BaseCommit, "headCommit": args.HeadCommit,
		"harness": args.Harness, "agentModel": args.AgentModel} {
		switch {
		case v == nil:
		case *v == "":
			fields[name] = nil
		default:
			fields[name] = *v
		}
	}
	for name, ref := range map[string]struct {
		v    *string
		kind string
	}{"task": {args.Task, data.KindTask}, "parentLane": {args.ParentLane, data.KindLane}} {
		switch {
		case ref.v == nil:
		case *ref.v == "":
			fields[name] = nil
		default:
			fields[name] = core.Ref{Kind: ref.kind, ID: *ref.v}
		}
	}
	if len(fields) == 0 {
		return nil, errors.New("nothing to update: give status, summary or another field")
	}
	lane, _, err := l.store.Patch(data.PatchInput{Ref: laneRef(args.Lane), Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	return &LaneResult{Lane: lane}, nil
}

// LogTitle is the title of a lane.log note: "kind: text". The timeline reads
// the kind back from it.
func LogTitle(kind, text string) string {
	if kind == "" {
		kind = "note"
	}
	return kind + ": " + text
}

// Log is lane.log: a note about the lane, stamped with when it was logged.
func (l *LaneTools) Log(actor core.Actor, args LaneLogArgs) (*LaneLogResult, error) {
	if args.Lane == "" {
		return nil, errors.New("lane is required")
	}
	if strings.TrimSpace(args.Text) == "" {
		return nil, errors.New("text is required")
	}
	lane, err := l.store.Get(laneRef(args.Lane))
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(args.Kind))
	if kind == "" {
		kind = "note"
	}
	fields := map[string]any{"title": LogTitle(kind, args.Text), "logKind": kind, "loggedAt": time.Now().UTC().Format(data.TimeFormat)}
	if args.Body != "" {
		fields["body"] = args.Body
	}
	if task, ok := lane.RefField("task"); ok {
		fields["task"] = task
	}
	note, _, err := l.store.Create(data.CreateInput{Kind: data.KindNote, Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	rel, _, err := l.store.Relate(data.RelateInput{From: note.Ref(), Kind: data.RelAbout, To: lane.Ref()}, actor)
	if err != nil {
		return nil, err
	}
	return &LaneLogResult{Note: note, Relation: rel}, nil
}

// Attach is lane.attach: a relation from the lane to a record. The record
// does not have to be stored: the relation keeps the reference.
func (l *LaneTools) Attach(actor core.Actor, args LaneAttachArgs) (*LaneAttachResult, error) {
	if args.Lane == "" || args.Kind == "" || args.ID == "" {
		return nil, errors.New("lane, kind and id are required")
	}
	if _, err := l.store.Get(laneRef(args.Lane)); err != nil {
		return nil, err
	}
	relation := args.Relation
	if relation == "" {
		relation = attachRelations[args.Kind]
	}
	if relation == "" {
		relation = data.RelCreated
	}
	rel, _, err := l.store.Relate(data.RelateInput{From: laneRef(args.Lane), Kind: relation, To: core.Ref{Kind: args.Kind, ID: args.ID}}, actor)
	if err != nil {
		return nil, err
	}
	return &LaneAttachResult{Relation: rel}, nil
}
