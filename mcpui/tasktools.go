package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Task queue and inline note thread MCP tool names.
const (
	ToolTaskQueue     = "task.queue"
	ToolTaskFlagAgent = "task.flag_agent"
	ToolTaskNote      = "task.note"
	ToolTaskThread    = "task.thread"
	ToolTaskDispatch  = "task.dispatch"
)

var (
	taskQueueStatuses = []string{
		data.AgentStatusQueued,
		data.AgentStatusWorking,
		data.AgentStatusNeedsYou,
		data.AgentStatusDone,
		"open",
		"all",
	}
	taskFlagStatuses = []string{
		data.AgentStatusQueued,
		data.AgentStatusWorking,
		data.AgentStatusNeedsYou,
		data.AgentStatusDone,
		data.AgentStatusNone,
	}
	taskNoteAuthorKinds = []string{"owner", "agent", "system"}
)

// TaskTools serves the task.* MCP tools over a store.
type TaskTools struct {
	store *data.Store
}

// NewTaskTools returns the task queue and note thread tools over store.
func NewTaskTools(store *data.Store) *TaskTools { return &TaskTools{store: store} }

// --- arguments and results ----------------------------------------------------

// TaskQueueArgs are the arguments of task.queue.
type TaskQueueArgs struct {
	Project               string   `json:"project,omitempty"`
	Status                string   `json:"status,omitempty"`
	IncludeNotes          *bool    `json:"includeNotes,omitempty"`
	IncludeContext        *bool    `json:"includeContext,omitempty"`
	IncludeProjectContext *bool    `json:"includeProjectContext,omitempty"`
	ContextIDs            []string `json:"contextIds,omitempty"`
}

// TaskQueueResult is what task.queue returns.
type TaskQueueResult struct {
	Items []data.AgentQueueItem `json:"items"`
	Count int                   `json:"count"`
}

// TaskFlagAgentArgs are the arguments of task.flag_agent.
type TaskFlagAgentArgs struct {
	Task        string `json:"task"`
	ForAgent    *bool  `json:"forAgent,omitempty"`
	Status      string `json:"status,omitempty"`
	AgentStatus string `json:"agentStatus,omitempty"`
	Assignee    string `json:"assignee,omitempty"`
	By          string `json:"by,omitempty"`
}

// TaskFlagAgentResult is what task.flag_agent returns.
type TaskFlagAgentResult struct {
	Task *data.Record `json:"task"`
}

// TaskNoteArgs are the arguments of task.note.
type TaskNoteArgs struct {
	Task        string            `json:"task"`
	Body        string            `json:"body,omitempty"`
	Title       string            `json:"title,omitempty"`
	AuthorKind  string            `json:"authorKind,omitempty"`
	AuthorLabel string            `json:"authorLabel,omitempty"`
	Pane        string            `json:"pane,omitempty"`
	Attachments []data.Attachment `json:"attachments,omitempty"`
	By          string            `json:"by,omitempty"`
}

// TaskNoteResult is what task.note returns.
type TaskNoteResult struct {
	Note *data.Record `json:"note"`
	Task *data.Record `json:"task"`
}

// TaskThreadArgs are the arguments of task.thread.
type TaskThreadArgs struct {
	Task string `json:"task"`
}

// TaskThreadResult is what task.thread returns.
type TaskThreadResult struct {
	Task        *data.Record      `json:"task"`
	Notes       []data.Record     `json:"notes"`
	Attachments []data.Attachment `json:"attachments"`
	Context     []data.Record     `json:"context"`
}

// TaskDispatchArgs are the arguments of task.dispatch.
type TaskDispatchArgs struct {
	Project               string   `json:"project,omitempty"`
	Status                string   `json:"status,omitempty"`
	Prompt                string   `json:"prompt,omitempty"`
	IncludeProjectContext *bool    `json:"includeProjectContext,omitempty"`
	ContextIDs            []string `json:"contextIds,omitempty"`
}

// TaskDispatchResult is what task.dispatch returns.
type TaskDispatchResult struct {
	Prompt string                `json:"prompt"`
	Count  int                   `json:"count"`
	Tasks  []data.AgentQueueItem `json:"tasks"`
}

func attachmentSchema() props {
	return props{
		"type":        "array",
		"description": "attached files, images, URLs, or context references",
		"items": props{
			"type":                 "object",
			"required":             []string{"kind", "path"},
			"additionalProperties": false,
			"properties": props{
				"kind":   oneOf("attachment kind", []string{"image", "file", "context", "url"}),
				"path":   text("absolute file path or URL"),
				"label":  text("display label"),
				"mime":   text("MIME type (e.g. image/png)"),
				"source": text("where the attachment came from (clipboard, picker, mention, agent)"),
				"ref":    text("optional linked record ref (e.g. context:<id>, document:<id>)"),
			},
		},
	}
}

// ToolSpecs returns the task.* tools with JSON schemas matching their real parameters.
func (t *TaskTools) ToolSpecs() []ToolSpec {
	by := text("who is writing: an agent ID recorded on the change set; default " + DefaultAgentID)
	return append([]ToolSpec{
		{
			Name:        ToolTaskQueue,
			Description: "List agent-flagged tasks ready for pickup or inspection, ordered by priority, sibling order, and creation time, with hydrated note threads, attachments, and linked context. Returns {items, count}.",
			InputSchema: object([]string{}, props{
				"project":               text("filter to this project/parent task ID and its descendants"),
				"status":                oneOf("agent status filter; default queued", taskQueueStatuses),
				"includeNotes":          flag("include chronological note threads on each item; default true"),
				"includeContext":        flag("include outgoing context records on each item; default true"),
				"includeProjectContext": flag("include official curated project context automatically; default true"),
				"contextIds":            props{"type": "array", "items": props{"type": "string"}, "description": "explicit context record IDs overriding the project's default official context bundle"},
			}),
		},
		{
			Name:        ToolTaskFlagAgent,
			Description: "Flag or unflag a task for agent queue pickup and update its agentStatus (queued, working, needs_you, done, none) and optional assignee. task defaults to $" + EnvTaskID + ". Returns {task}.",
			InputSchema: object([]string{"task"}, props{
				"task":        text("task ID; default $" + EnvTaskID),
				"forAgent":    flag("whether the task is flagged for agent pickup; defaults to true unless status is none"),
				"status":      oneOf("agent workflow status", taskFlagStatuses),
				"agentStatus": oneOf("alias for status", taskFlagStatuses),
				"assignee":    text("agent harness or worker assigned to this task"),
				"by":          by,
			}),
		},
		{
			Name:        ToolTaskNote,
			Description: "Append an inline note to a task's chronological note thread (creating a KindNote record linked via RelAbout) with optional attachments. pane defaults to $" + EnvPaneID + " and task defaults to $" + EnvTaskID + ". Returns {note, task}.",
			InputSchema: object([]string{"task"}, props{
				"task":        text("task ID; default $" + EnvTaskID),
				"body":        text("note body text (required unless title is given)"),
				"title":       text("optional note title; derived from the first body line when omitted"),
				"authorKind":  oneOf("who authored the note; default agent", taskNoteAuthorKinds),
				"authorLabel": text("display name of the note author"),
				"pane":        text("pane ID the note was written from; default $" + EnvPaneID),
				"attachments": attachmentSchema(),
				"by":          by,
			}),
		},
		{
			Name:        ToolTaskThread,
			Description: "Read a task together with its chronological note thread, deduplicated attachments (from the task and its notes), and linked context records. task defaults to $" + EnvTaskID + ". Returns {task, notes, attachments, context}.",
			InputSchema: object([]string{"task"}, props{
				"task": text("task ID; default $" + EnvTaskID),
			}),
		},
		{
			Name:        ToolTaskDispatch,
			Description: "Query the agent task queue and render a deterministic Markdown pickup prompt containing task metadata, official project context, note threads, attachments, linked context, and progress-reporting MCP commands. Returns {prompt, count, tasks}.",
			InputSchema: object([]string{}, props{
				"project":               text("filter to this project/parent task ID and its descendants"),
				"status":                oneOf("agent status filter; default queued", taskQueueStatuses),
				"prompt":                text("optional extra instructions prepended to the generated prompt"),
				"includeProjectContext": flag("include official curated project context automatically; default true"),
				"contextIds":            props{"type": "array", "items": props{"type": "string"}, "description": "explicit context record IDs overriding the project's default official context bundle"},
			}),
		},
	}, taskLineageToolSpecs(by)...)
}

// HasTool reports whether name is one of the task.* tools.
func (t *TaskTools) HasTool(name string) bool {
	return slices.Contains([]string{
		ToolTaskQueue,
		ToolTaskFlagAgent,
		ToolTaskNote,
		ToolTaskThread,
		ToolTaskDispatch,
		ToolTaskRewrite,
		ToolTaskSplit,
		ToolTaskJoin,
	}, name)
}

// Call runs one task.* tool with JSON arguments.
func (t *TaskTools) Call(ctx context.Context, actor core.Actor, toolName string, rawArgs json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		out any
		err error
	)
	switch toolName {
	case ToolTaskQueue:
		var args TaskQueueArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = t.Queue(args)
		}
	case ToolTaskFlagAgent:
		var args TaskFlagAgentArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = t.FlagAgent(agentActor(actor, args.By), args)
		}
	case ToolTaskNote:
		var args TaskNoteArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = t.Note(agentActor(actor, args.By), args)
		}
	case ToolTaskThread:
		var args TaskThreadArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = t.Thread(args)
		}
	case ToolTaskDispatch:
		var args TaskDispatchArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = t.Dispatch(args)
		}
	case ToolTaskRewrite, ToolTaskSplit, ToolTaskJoin:
		out, err = t.callTaskLineage(actor, toolName, rawArgs)
	default:
		return nil, fmt.Errorf("mcpui: unknown tool %q", toolName)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", toolName, err)
	}
	return out, nil
}

func normalizeQueueStatus(raw string) (string, error) {
	st := strings.TrimSpace(raw)
	if st == "" {
		return data.AgentStatusQueued, nil
	}
	if !slices.Contains(taskQueueStatuses, st) {
		return "", fmt.Errorf("invalid status %q (want %s)", st, strings.Join(taskQueueStatuses, ", "))
	}
	return st, nil
}

// Queue implements task.queue.
func (t *TaskTools) Queue(args TaskQueueArgs) (*TaskQueueResult, error) {
	if t == nil || t.store == nil {
		return nil, errors.New("store is nil")
	}
	status, err := normalizeQueueStatus(args.Status)
	if err != nil {
		return nil, err
	}
	includeNotes := true
	if args.IncludeNotes != nil {
		includeNotes = *args.IncludeNotes
	}
	includeContext := true
	if args.IncludeContext != nil {
		includeContext = *args.IncludeContext
	}
	projectID := strings.TrimSpace(args.Project)
	items, err := t.store.QueryAgentQueue(data.AgentQueueFilter{
		ProjectID:             projectID,
		Status:                status,
		IncludeSubtree:        projectID != "",
		IncludeNotes:          includeNotes,
		IncludeContext:        includeContext,
		IncludeProjectContext: args.IncludeProjectContext,
		ContextIDs:            args.ContextIDs,
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []data.AgentQueueItem{}
	}
	return &TaskQueueResult{Items: items, Count: len(items)}, nil
}

// FlagAgent implements task.flag_agent.
func (t *TaskTools) FlagAgent(actor core.Actor, args TaskFlagAgentArgs) (*TaskFlagAgentResult, error) {
	if t == nil || t.store == nil {
		return nil, errors.New("store is nil")
	}
	taskID := strings.TrimSpace(args.Task)
	if taskID == "" {
		return nil, errors.New("task is required")
	}
	status := strings.TrimSpace(args.Status)
	agentStatus := strings.TrimSpace(args.AgentStatus)
	if status != "" && agentStatus != "" && status != agentStatus {
		return nil, fmt.Errorf("conflicting status %q and agentStatus %q", status, agentStatus)
	}
	if status == "" {
		status = agentStatus
	}
	forAgent := status != data.AgentStatusNone
	if args.ForAgent != nil {
		forAgent = *args.ForAgent
	}
	rec, err := t.store.SetTaskAgentFlag(core.Ref{Kind: data.KindTask, ID: taskID}, forAgent, status, args.Assignee, actor)
	if err != nil {
		return nil, err
	}
	return &TaskFlagAgentResult{Task: &rec}, nil
}

// Note implements task.note.
func (t *TaskTools) Note(actor core.Actor, args TaskNoteArgs) (*TaskNoteResult, error) {
	if t == nil || t.store == nil {
		return nil, errors.New("store is nil")
	}
	taskID := strings.TrimSpace(args.Task)
	if taskID == "" {
		return nil, errors.New("task is required")
	}
	authorKind := strings.TrimSpace(args.AuthorKind)
	if authorKind == "" {
		authorKind = "agent"
	}
	note, err := t.store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID:      taskID,
		Title:       args.Title,
		Body:        args.Body,
		AuthorKind:  authorKind,
		AuthorLabel: args.AuthorLabel,
		PaneID:      args.Pane,
		Attachments: args.Attachments,
	}, actor)
	if err != nil {
		return nil, err
	}
	taskRec, err := t.store.Get(core.Ref{Kind: data.KindTask, ID: taskID})
	if err != nil {
		return nil, err
	}
	return &TaskNoteResult{Note: &note, Task: taskRec}, nil
}

// Thread implements task.thread.
func (t *TaskTools) Thread(args TaskThreadArgs) (*TaskThreadResult, error) {
	if t == nil || t.store == nil {
		return nil, errors.New("store is nil")
	}
	taskID := strings.TrimSpace(args.Task)
	if taskID == "" {
		return nil, errors.New("task is required")
	}
	taskRec, err := t.store.Get(core.Ref{Kind: data.KindTask, ID: taskID})
	if err != nil {
		return nil, err
	}
	notes, err := t.store.TaskNotes(taskID)
	if err != nil {
		return nil, err
	}
	if notes == nil {
		notes = []data.Record{}
	}
	ctxRecs, err := t.store.RelatedRecords(taskRec.Ref(), data.RelContext, data.Outgoing)
	if err != nil {
		return nil, err
	}
	activeCtx := make([]data.Record, 0, len(ctxRecs))
	for _, cr := range ctxRecs {
		if !cr.IsArchived() {
			activeCtx = append(activeCtx, cr)
		}
	}
	attachments := collectThreadAttachments(*taskRec, notes)
	return &TaskThreadResult{
		Task:        taskRec,
		Notes:       notes,
		Attachments: attachments,
		Context:     activeCtx,
	}, nil
}

func collectThreadAttachments(task data.Record, notes []data.Record) []data.Attachment {
	type attKey struct {
		kind string
		path string
		ref  string
	}
	seen := map[attKey]bool{}
	out := []data.Attachment{}
	add := func(v any) {
		for _, a := range data.DecodeAttachments(v) {
			k := attKey{kind: a.Kind, path: a.Path, ref: a.Ref}
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, a)
		}
	}
	if v, ok := task.Value("attachments"); ok {
		add(v)
	}
	for _, n := range notes {
		if v, ok := n.Value("attachments"); ok {
			add(v)
		}
	}
	return out
}

// Dispatch implements task.dispatch.
func (t *TaskTools) Dispatch(args TaskDispatchArgs) (*TaskDispatchResult, error) {
	if t == nil || t.store == nil {
		return nil, errors.New("store is nil")
	}
	status, err := normalizeQueueStatus(args.Status)
	if err != nil {
		return nil, err
	}
	projectID := strings.TrimSpace(args.Project)
	items, err := t.store.QueryAgentQueue(data.AgentQueueFilter{
		ProjectID:             projectID,
		Status:                status,
		IncludeSubtree:        projectID != "",
		IncludeNotes:          true,
		IncludeContext:        true,
		IncludeProjectContext: args.IncludeProjectContext,
		ContextIDs:            args.ContextIDs,
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []data.AgentQueueItem{}
	}
	prompt := data.FormatAgentQueuePrompt(items, args.Prompt)
	return &TaskDispatchResult{
		Prompt: prompt,
		Count:  len(items),
		Tasks:  items,
	}, nil
}
