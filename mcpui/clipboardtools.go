package mcpui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Shared clipboard tool names.
const (
	ToolClipboardPut  = "clipboard.put"
	ToolClipboardList = "clipboard.list"
	ToolClipboardGet  = "clipboard.get"
)

// defaultClipboardListLimit bounds clipboard.list when no limit is given.
const defaultClipboardListLimit = 50

// ClipboardTools serves clipboard.put, clipboard.list and clipboard.get: the
// shared clipboard the owner and agents use to hand text, paths, URLs, code
// and references to each other.
type ClipboardTools struct {
	store  *data.Store
	Getenv func(string) string
}

// NewClipboardTools returns shared clipboard tools over store.
func NewClipboardTools(store *data.Store) *ClipboardTools {
	return &ClipboardTools{store: store}
}

// ClipboardPutArgs are the arguments of clipboard.put.
type ClipboardPutArgs struct {
	data.ClipInput
	By string `json:"by,omitempty"`
}

// ClipboardListArgs are the arguments of clipboard.list.
type ClipboardListArgs struct {
	Project    string `json:"project,omitempty"`
	AuthorKind string `json:"authorKind,omitempty"`
	ClipKind   string `json:"clipKind,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// ClipboardGetArgs are the arguments of clipboard.get.
type ClipboardGetArgs struct {
	ID string `json:"id"`
}

// ClipboardClipResult is returned by clipboard.put and clipboard.get.
type ClipboardClipResult struct {
	Clip data.Record `json:"clip"`
}

// ClipboardListResult is returned by clipboard.list.
type ClipboardListResult struct {
	Clips []data.Record `json:"clips"`
}

// ToolSpecs returns the MCP tool schemas of the shared clipboard.
func (c *ClipboardTools) ToolSpecs() []ToolSpec {
	clipKind := props{"type": "string", "enum": data.ClipKinds, "description": "what the content is"}
	authorKind := props{"type": "string", "enum": data.ClipAuthorKinds, "description": "who shared the clip"}
	return []ToolSpec{
		{
			Name:        ToolClipboardPut,
			Description: "Put text, a path, a URL, code or a kind:id reference on the shared clipboard so the owner can copy it from the Shared Clipboard picker (and other agents can read it). authorKind defaults to agent, fromPane to $" + EnvPaneID + " and project to the root project of $" + EnvTaskID + " or the pane's task. Returns {clip}.",
			InputSchema: object([]string{"content"}, props{
				"content":    text("the payload to share"),
				"title":      text("one-line label; derived from content when omitted"),
				"clipKind":   clipKind,
				"authorKind": authorKind,
				"fromPane":   text("pane the clip is shared from; default $" + EnvPaneID),
				"project":    text("project or task ID (walked to its root project); default $" + EnvTaskID + " or the pane's task"),
				"by":         text("optional agent identifier recorded on the change set"),
			}),
		},
		{
			Name:        ToolClipboardList,
			Description: "List the active clips of the shared clipboard, newest first: what the owner pushed for agents and what agents shared. Returns {clips}.",
			InputSchema: object(nil, props{
				"project":    text("only clips of this project (a project or task ID, walked to its root project)"),
				"authorKind": authorKind,
				"clipKind":   clipKind,
				"limit":      count(fmt.Sprintf("keep the newest N clips (default %d)", defaultClipboardListLimit)),
			}),
		},
		{
			Name:        ToolClipboardGet,
			Description: "Get one shared clipboard clip by ID with its full content. Returns {clip}.",
			InputSchema: object([]string{"id"}, props{"id": text("clip ID")}),
		},
	}
}

// HasTool reports whether name is a shared clipboard tool.
func (c *ClipboardTools) HasTool(name string) bool {
	switch name {
	case ToolClipboardPut, ToolClipboardList, ToolClipboardGet:
		return true
	}
	return false
}

// Call dispatches one shared clipboard tool call.
func (c *ClipboardTools) Call(actor core.Actor, name string, raw json.RawMessage) (any, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("clipboard: no store")
	}
	switch name {
	case ToolClipboardPut:
		var args ClipboardPutArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return c.Put(actor, args)
	case ToolClipboardList:
		var args ClipboardListArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return c.List(args)
	case ToolClipboardGet:
		var args ClipboardGetArgs
		if err := decodeArgs(raw, &args); err != nil {
			return nil, err
		}
		return c.Get(args)
	}
	return nil, fmt.Errorf("unknown clipboard tool %q", name)
}

func (c *ClipboardTools) env(name string) string {
	if c.Getenv != nil {
		return strings.TrimSpace(c.Getenv(name))
	}
	return strings.TrimSpace(os.Getenv(name))
}

// defaultProject is the task the clip belongs to when none was named: the
// environment's task, else the task of the sharing pane. Empty when neither
// is known: a clip without a project is still recorded.
func (c *ClipboardTools) defaultProject(paneID string) string {
	if task := c.env(EnvTaskID); task != "" {
		return task
	}
	if paneID == "" {
		return ""
	}
	pane, err := c.store.Get(paneRef(paneID))
	if err != nil || pane == nil {
		return ""
	}
	task, _ := pane.RefField("task")
	return task.ID
}

// Defaults fills the pane and project left out of in from the environment:
// fromPane from $MTM_PANE_ID, project from $MTM_TASK_ID or the pane's task.
func (c *ClipboardTools) Defaults(in data.ClipInput) data.ClipInput {
	if strings.TrimSpace(in.FromPane) == "" {
		in.FromPane = c.env(EnvPaneID)
	}
	if strings.TrimSpace(in.Project) == "" {
		in.Project = c.defaultProject(strings.TrimSpace(in.FromPane))
	}
	return in
}

// Put implements clipboard.put.
func (c *ClipboardTools) Put(actor core.Actor, args ClipboardPutArgs) (ClipboardClipResult, error) {
	in := c.Defaults(args.ClipInput)
	if strings.TrimSpace(in.AuthorKind) == "" {
		in.AuthorKind = data.ClipAuthorAgent
	}
	rec, err := c.store.PutClip(in, agentActor(actor, args.By))
	if err != nil {
		return ClipboardClipResult{}, fmt.Errorf("%s: %w", ToolClipboardPut, err)
	}
	return ClipboardClipResult{Clip: *rec}, nil
}

// List implements clipboard.list.
func (c *ClipboardTools) List(args ClipboardListArgs) (ClipboardListResult, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = defaultClipboardListLimit
	}
	clips, err := c.store.ListClips(data.ClipFilter{Project: args.Project, AuthorKind: args.AuthorKind, ClipKind: args.ClipKind, Limit: limit})
	if err != nil {
		return ClipboardListResult{}, fmt.Errorf("%s: %w", ToolClipboardList, err)
	}
	return ClipboardListResult{Clips: clips}, nil
}

// Get implements clipboard.get.
func (c *ClipboardTools) Get(args ClipboardGetArgs) (ClipboardClipResult, error) {
	rec, err := c.store.GetClip(args.ID)
	if err != nil {
		return ClipboardClipResult{}, fmt.Errorf("%s: %w", ToolClipboardGet, err)
	}
	return ClipboardClipResult{Clip: *rec}, nil
}
