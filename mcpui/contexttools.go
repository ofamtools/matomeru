package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Curated project context MCP tool names.
const (
	ToolContextOfficial    = "context.official"
	ToolContextProject     = "context.project"
	ToolContextAddOfficial = "context.add_official"
)

var contextToolKinds = []string{"url", "pr", "file", "doc", "gdoc", "sheet"}

// ContextTools serves the context.* MCP tools over a store. Getenv reads the
// environment (nil falls back to os.Getenv).
type ContextTools struct {
	store  *data.Store
	Getenv func(string) string
}

// NewContextTools returns the project context tools over store.
func NewContextTools(store *data.Store) *ContextTools { return &ContextTools{store: store} }

func (c *ContextTools) env(name string) string {
	if c != nil && c.Getenv != nil {
		return c.Getenv(name)
	}
	return os.Getenv(name)
}

// --- arguments and results ----------------------------------------------------

// ContextOfficialArgs are the arguments of context.official.
type ContextOfficialArgs struct {
	ID       string  `json:"id"`
	Official *bool   `json:"official,omitempty"`
	Pinned   *bool   `json:"pinned,omitempty"`
	Notes    *string `json:"notes,omitempty"`
	By       string  `json:"by,omitempty"`
}

// ContextOfficialResult is what context.official returns.
type ContextOfficialResult struct {
	Context *data.Record `json:"context"`
}

// ContextProjectArgs are the arguments of context.project.
type ContextProjectArgs struct {
	Project         string `json:"project,omitempty"`
	Pane            string `json:"pane,omitempty"`
	IncludeAll      bool   `json:"includeAll,omitempty"`
	IncludeMarkdown *bool  `json:"includeMarkdown,omitempty"`
}

// ContextProjectResult is what context.project returns.
type ContextProjectResult struct {
	ProjectID string                 `json:"projectId"`
	Official  []data.Record          `json:"official"`
	All       []data.Record          `json:"all,omitempty"`
	Worktrees []data.ProjectWorktree `json:"worktrees"`
	Markdown  string                 `json:"markdown,omitempty"`
}

// ContextAddOfficialArgs are the arguments of context.add_official.
type ContextAddOfficialArgs struct {
	Project     string `json:"project,omitempty"`
	Pane        string `json:"pane,omitempty"`
	Value       string `json:"value"`
	Title       string `json:"title,omitempty"`
	ContextKind string `json:"contextKind,omitempty"`
	Notes       string `json:"notes,omitempty"`
	Pinned      *bool  `json:"pinned,omitempty"`
	By          string `json:"by,omitempty"`
}

// ContextAddOfficialResult is what context.add_official returns.
type ContextAddOfficialResult struct {
	ProjectID    string       `json:"projectId"`
	Context      *data.Record `json:"context"`
	Deduplicated bool         `json:"deduplicated,omitempty"`
}

// ToolSpecs returns the context.* tools with JSON schemas matching their real
// parameters.
func (c *ContextTools) ToolSpecs() []ToolSpec {
	by := text("who is writing: an agent ID recorded on the change set; default " + DefaultAgentID)
	return []ToolSpec{
		{
			Name:        ToolContextOfficial,
			Description: "Promote or demote a context record as official curated project context (automatically included for agents unless opted out), optionally updating pinned and notes. Returns {context}.",
			InputSchema: object([]string{"id"}, props{
				"id":       text("context record ID"),
				"official": flag("whether this context item is official project context; default true"),
				"pinned":   flag("whether this context item is pinned on the Project Page"),
				"notes":    text("why this context item matters to agents and humans"),
				"by":       by,
			}),
		},
		{
			Name:        ToolContextProject,
			Description: "List a project's official curated context items, optional full project context, deduplicated project worktrees, and formatted Markdown block. Auto-resolves project from $" + EnvTaskID + " or $" + EnvPaneID + " when omitted. Returns {projectId, official, all?, worktrees, markdown?}.",
			InputSchema: object([]string{}, props{
				"project":         text("project or task ID (walked to root project); defaults to $" + EnvTaskID + " or $" + EnvPaneID + "'s task"),
				"pane":            text("optional pane ID used to resolve its task's root project; default $" + EnvPaneID),
				"includeAll":      flag("also return all active project-scoped context items (official and unofficial)"),
				"includeMarkdown": flag("include formatted ## Official Project Context Markdown; default true"),
			}),
		},
		{
			Name:        ToolContextAddOfficial,
			Description: "Add or promote an official curated context item (URL, PR, file, doc, gdoc, sheet) on a project so agents automatically receive it. Auto-resolves project from $" + EnvTaskID + " or $" + EnvPaneID + " when omitted. Returns {projectId, context, deduplicated?}.",
			InputSchema: object([]string{"value"}, props{
				"project":     text("project or task ID (walked to root project); defaults to $" + EnvTaskID + " or $" + EnvPaneID + "'s task"),
				"pane":        text("optional pane ID used to resolve its task's root project; default $" + EnvPaneID),
				"value":       text("URL, Google Doc/Sheet URL, PR reference, or file path"),
				"title":       text("display title; derived from value when omitted"),
				"contextKind": oneOf("kind of context item; inferred from value when omitted", contextToolKinds),
				"notes":       text("why this context item matters to agents and humans"),
				"pinned":      flag("pin for quick access on the Project Page; default true"),
				"by":          by,
			}),
		},
	}
}

// HasTool reports whether name is one of the context.* tools.
func (c *ContextTools) HasTool(name string) bool {
	return slices.Contains([]string{
		ToolContextOfficial,
		ToolContextProject,
		ToolContextAddOfficial,
	}, name)
}

// Call runs one context.* tool with JSON arguments.
func (c *ContextTools) Call(ctx context.Context, actor core.Actor, toolName string, rawArgs json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		out any
		err error
	)
	switch toolName {
	case ToolContextOfficial:
		var args ContextOfficialArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = c.Official(agentActor(actor, args.By), args)
		}
	case ToolContextProject:
		var args ContextProjectArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = c.Project(args)
		}
	case ToolContextAddOfficial:
		var args ContextAddOfficialArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = c.AddOfficial(agentActor(actor, args.By), args)
		}
	default:
		return nil, fmt.Errorf("mcpui: unknown tool %q", toolName)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", toolName, err)
	}
	return out, nil
}

func (c *ContextTools) resolveProjectID(explicitProject, explicitPane string) (string, error) {
	if p := strings.TrimSpace(explicitProject); p != "" {
		return c.store.RootProjectID(p), nil
	}
	if envTask := strings.TrimSpace(c.env(EnvTaskID)); envTask != "" {
		return c.store.RootProjectID(envTask), nil
	}
	paneID := strings.TrimSpace(explicitPane)
	if paneID == "" {
		paneID = strings.TrimSpace(c.env(EnvPaneID))
	}
	if paneID != "" {
		if pane, err := c.store.Get(paneRef(paneID)); err == nil && pane != nil {
			if tRef, ok := pane.RefField("task"); ok && tRef.Kind == data.KindTask && strings.TrimSpace(tRef.ID) != "" {
				return c.store.RootProjectID(tRef.ID), nil
			}
		}
	}
	return "", fmt.Errorf("project is required (or set %s / %s)", EnvTaskID, EnvPaneID)
}

// Official implements context.official.
func (c *ContextTools) Official(actor core.Actor, args ContextOfficialArgs) (*ContextOfficialResult, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("store is nil")
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return nil, errors.New("id is required")
	}
	official := true
	if args.Official != nil {
		official = *args.Official
	}
	rec, err := c.store.SetContextOfficial(core.Ref{Kind: data.KindContext, ID: id}, official, args.Pinned, args.Notes, actor)
	if err != nil {
		return nil, err
	}
	return &ContextOfficialResult{Context: &rec}, nil
}

// Project implements context.project.
func (c *ContextTools) Project(args ContextProjectArgs) (*ContextProjectResult, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("store is nil")
	}
	projectID, err := c.resolveProjectID(args.Project, args.Pane)
	if err != nil {
		return nil, err
	}
	official, err := c.store.OfficialProjectContext(projectID)
	if err != nil {
		return nil, err
	}
	if official == nil {
		official = []data.Record{}
	}
	worktrees, err := c.store.ProjectWorktrees(projectID)
	if err != nil {
		return nil, err
	}
	if worktrees == nil {
		worktrees = []data.ProjectWorktree{}
	}
	res := &ContextProjectResult{
		ProjectID: projectID,
		Official:  official,
		Worktrees: worktrees,
	}
	if args.IncludeAll {
		all, err := c.store.ProjectContextAll(projectID)
		if err != nil {
			return nil, err
		}
		if all == nil {
			all = []data.Record{}
		}
		res.All = all
	}
	includeMD := true
	if args.IncludeMarkdown != nil {
		includeMD = *args.IncludeMarkdown
	}
	if includeMD {
		res.Markdown = data.FormatProjectContextMarkdown(official)
	}
	return res, nil
}

// AddOfficial implements context.add_official.
func (c *ContextTools) AddOfficial(actor core.Actor, args ContextAddOfficialArgs) (*ContextAddOfficialResult, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("store is nil")
	}
	projectID, err := c.resolveProjectID(args.Project, args.Pane)
	if err != nil {
		return nil, err
	}
	pinned := true
	if args.Pinned != nil {
		pinned = *args.Pinned
	}
	rec, deduped, err := c.store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID:   projectID,
		Title:       args.Title,
		Value:       args.Value,
		ContextKind: args.ContextKind,
		Notes:       args.Notes,
		Pinned:      pinned,
	}, actor)
	if err != nil {
		return nil, err
	}
	return &ContextAddOfficialResult{
		ProjectID:    projectID,
		Context:      &rec,
		Deduplicated: deduped,
	}, nil
}
