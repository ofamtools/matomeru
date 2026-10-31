// Package mcp is a shell over mtm's operations, like the CLI: it speaks the
// Model Context Protocol (JSON-RPC 2.0 over stdio) so an agent in a TUIOS
// pane can drive mtm. Each tool runs one or more mtm NOUN VERB operations
// through a Backend, which in mtm is the CLI's own in-process execution
// path (cmd/mtm/mcp.go), so a tool does what the matching command does.
//
// Identity is bound at start from the pane's environment (IdentityFromEnv),
// never taken from tool arguments, so an agent only ever acts as its own
// pane; what the pane is assigned to is resolved again on every call, so a
// reassignment applies without a restart.
//
// The package is ported from matomeru-old's internal/mcp. The protocol is
// unchanged; the old command bus is replaced by Backend. Tools whose
// features the new mtm does not have are not ported:
//
//	old tool                 needs in the new mtm
//	project_home_add         a project home page: answers and artifacts per
//	project_home_search      project (primary, support, byproduct levels) and
//	                         a search over them, context and tasks
//	action_item_create       action items (B-15): a table, a page and verbs
//	queue_add, queue_list,   a work queue (B-39): items, batches by repo,
//	queue_claim,             claim/finish/release owned by a pane, the
//	queue_finish,            queue.batch_size setting
//	queue_release
//	todo_list, todo_claim,   todo items (B-13) as rows with notes and claims
//	todo_note_add            (mtm's todos noun only opens the outline)
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// Identity is who is talking: the pane and what it is assigned to.
type Identity struct {
	Session   string `json:"session"`
	Window    string `json:"window"`
	TaskID    string `json:"task_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Harness   string `json:"harness,omitempty"`
	Model     string `json:"model,omitempty"`
	Bound     bool   `json:"bound"` // a pane is known; tools that need one refuse otherwise

	TaskTitle   string `json:"task_title,omitempty"`
	TaskStatus  string `json:"task_status,omitempty"`
	TaskVersion int64  `json:"task_version,omitempty"` // the task's updated stamp, for task_status's expected_version
	ProjectName string `json:"project_name,omitempty"`
	ProjectRepo string `json:"project_repo,omitempty"`
}

// Backend is mtm as the tools see it.
type Backend interface {
	// Identify resolves what the pane is assigned to now: its task and
	// project (the pane's assignment, else its session's owner), and the
	// cwd, harness and model of the recipe it was launched with.
	Identify(ctx context.Context, id Identity) (Identity, error)
	// Run runs one mtm operation, NOUN VERB with its arguments, the way
	// mtm NOUN VERB --json ARGS does, and returns the command's JSON result.
	Run(ctx context.Context, noun, verb string, args map[string]any) (json.RawMessage, error)
}

// Tool is one MCP tool: a thin wrapper over mtm operations. Operations
// names them, for mtm mcp tools and the docs.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Operations  []string       `json:"operations"`
	NeedsPane   bool           `json:"needs_pane"`
	call        func(context.Context, *Server, Identity, map[string]any) (any, error)
}

// Server is the tool table over a backend.
type Server struct {
	backend Backend
	version string
	tools   []Tool
}

// New is a server over b; version is reported by initialize.
func New(b Backend, version string) (*Server, error) {
	if b == nil {
		return nil, errors.New("mcp: a backend is required")
	}
	return &Server{backend: b, version: version, tools: defaultTools()}, nil
}

// IdentityFromEnv reads the pane the server was started in from getenv
// (os.Getenv when nil). TUIOS sets TUIOS_SESSION and TUIOS_PANE_ID on every
// pane, and a harness passes them on to the MCP servers it starts;
// MTM_SESSION and MTM_WINDOW override them for tests and explicit bridges,
// as in matomeru-old.
func IdentityFromEnv(getenv func(string) string) Identity {
	if getenv == nil {
		getenv = os.Getenv
	}
	session := getenv("MTM_SESSION")
	if session == "" {
		session = getenv("TUIOS_SESSION")
	}
	window := getenv("MTM_WINDOW")
	if window == "" {
		window = getenv("TUIOS_PANE_ID")
	}
	return Identity{Session: session, Window: window, Bound: window != ""}
}

// Identity resolves the pane's current assignment; an unbound identity is
// returned as it is.
func (s *Server) Identity(ctx context.Context, base Identity) Identity {
	base.Bound = base.Window != ""
	if !base.Bound {
		return base
	}
	if resolved, err := s.backend.Identify(ctx, base); err == nil {
		resolved.Session, resolved.Window, resolved.Bound = base.Session, base.Window, true
		return resolved
	}
	return base
}

// Tools lists the tools, in name order.
func (s *Server) Tools() []Tool {
	tools := slices.Clone(s.tools)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// Call runs one tool as the given identity.
func (s *Server) Call(ctx context.Context, base Identity, name string, args map[string]any) (any, error) {
	var tool *Tool
	for i := range s.tools {
		if s.tools[i].Name == name {
			tool = &s.tools[i]
			break
		}
	}
	if tool == nil {
		return nil, &Error{Code: CodeUnknownTool, Message: fmt.Sprintf("unknown tool %q", name)}
	}
	if args == nil {
		args = map[string]any{}
	}
	if err := validate(tool.InputSchema, args); err != nil {
		return nil, invalid(name + ": " + err.Error())
	}
	id := s.Identity(ctx, base)
	if tool.NeedsPane && !id.Bound {
		return nil, &Error{Code: "no_pane", Message: "this tool needs a TUIOS pane: start the agent in one, or set MTM_SESSION and MTM_WINDOW"}
	}
	return tool.call(WithIdentity(ctx, id), s, id, args)
}

type identityKey struct{}

// WithIdentity is ctx carrying the caller's identity, which Call gives
// every Backend.Run of a tool: mtm runs the operation as that pane's agent
// (cmd/mtm/actor.go).
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom is the identity WithIdentity put in ctx.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// Error is a tool failure with a stable code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Error codes the server maps to JSON-RPC errors rather than tool results.
const (
	CodeUnknownTool = "unknown_tool"
	CodeInvalidArgs = "invalid_args"
)

func invalid(message string) error { return &Error{Code: CodeInvalidArgs, Message: message} }

// validate checks args against the subset of JSON Schema the tool table
// uses: an object's required properties, additionalProperties false, and
// each property's type and enum (nested objects included).
func validate(schema map[string]any, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]string)
	for _, name := range required {
		if _, ok := args[name]; !ok {
			return fmt.Errorf("%s is required", name)
		}
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prop, ok := props[name].(map[string]any)
		if !ok {
			if extra, _ := schema["additionalProperties"].(bool); !extra {
				return fmt.Errorf("unknown argument %q", name)
			}
			continue
		}
		if err := validateValue(prop, args[name]); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func validateValue(prop map[string]any, value any) error {
	if value == nil {
		return nil // an explicit null is an omitted argument
	}
	want, _ := prop["type"].(string)
	switch want {
	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a string, not %s", jsonType(value))
		}
		if enum, ok := prop["enum"].([]string); ok && s != "" && !slices.Contains(enum, s) {
			return fmt.Errorf("must be one of %s", strings.Join(enum, ", "))
		}
	case "integer":
		switch n := value.(type) {
		case int, int64:
		case float64:
			if n != float64(int64(n)) {
				return fmt.Errorf("must be an integer, not %v", n)
			}
		default:
			return fmt.Errorf("must be an integer, not %s", jsonType(value))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("must be a boolean, not %s", jsonType(value))
		}
	case "object":
		m, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("must be an object, not %s", jsonType(value))
		}
		return validate(prop, m)
	}
	return nil
}

func jsonType(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	}
	return fmt.Sprintf("%T", v)
}

// run is one operation through the backend, decoded into dst when dst is
// not nil.
func (s *Server) run(ctx context.Context, noun, verb string, args map[string]any, dst any) (json.RawMessage, error) {
	raw, err := s.backend.Run(ctx, noun, verb, args)
	if err != nil {
		return nil, err
	}
	if dst != nil {
		if err := json.Unmarshal(raw, dst); err != nil {
			return raw, fmt.Errorf("%s.%s answered %q: %w", noun, verb, truncate(string(raw), 200), err)
		}
	}
	return raw, nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func text(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func integer(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func object(args map[string]any, key string) map[string]any {
	m, _ := args[key].(map[string]any)
	return m
}
