package mcpui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// ServerName is the serverInfo name of the MCP server.
const ServerName = "mtm"

// ProtocolVersions are the MCP protocol versions the server speaks, newest
// first. initialize echoes the client's version when it is one of these and
// answers with the newest otherwise.
var ProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Tools returns every tool the MCP server lists: the four Open UI tools,
// the lane tools, the data tools, the triage tools, pane search, task tools,
// then curated project context tools.
func Tools(router *Router, dataTools *DataTools) []ToolSpec {
	out := append(router.ToolSpecs(), NewLaneTools(nil).ToolSpecs()...)
	out = append(out, dataTools.ToolSpecs()...)
	out = append(out, NewTriageTools(nil).ToolSpecs()...)
	out = append(out, NewPaneSearchTools(nil).ToolSpecs()...)
	out = append(out, NewTaskTools(nil).ToolSpecs()...)
	return append(out, NewContextTools(nil).ToolSpecs()...)
}

// Toolbox is every tool over one store: the Open UI tools, the lane tools,
// the data tools, the triage tools, pane search, task tools and context tools,
// behind one Call. The MCP server and the CLI both run tools through it.
// Getenv reads the environment (nil is os.Getenv).
type Toolbox struct {
	Router     *Router
	Lanes      *LaneTools
	Data       *DataTools
	Triage     *TriageTools
	PaneSearch *PaneSearchTools
	Tasks      *TaskTools
	Context    *ContextTools
	Getenv     func(string) string
}

// NewToolbox returns the toolbox over store. A nil router gets a new one.
func NewToolbox(store *data.Store, router *Router) *Toolbox {
	if router == nil {
		router = New(store)
	}
	return &Toolbox{
		Router:     router,
		Lanes:      NewLaneTools(store),
		Data:       NewDataTools(store),
		Triage:     NewTriageTools(store),
		PaneSearch: NewPaneSearchTools(store),
		Tasks:      NewTaskTools(store),
		Context:    NewContextTools(store),
	}
}

// ToolSpecs lists every tool.
func (b *Toolbox) ToolSpecs() []ToolSpec {
	out := append(b.Router.ToolSpecs(), b.Lanes.ToolSpecs()...)
	out = append(out, b.Data.ToolSpecs()...)
	if b.Triage != nil {
		out = append(out, b.Triage.ToolSpecs()...)
	}
	if b.PaneSearch != nil {
		out = append(out, b.PaneSearch.ToolSpecs()...)
	}
	if b.Tasks != nil {
		out = append(out, b.Tasks.ToolSpecs()...)
	}
	if b.Context != nil {
		out = append(out, b.Context.ToolSpecs()...)
	}
	return out
}

// HasTool reports whether name is a tool of the toolbox.
func (b *Toolbox) HasTool(name string) bool {
	return slices.ContainsFunc(b.ToolSpecs(), func(t ToolSpec) bool { return t.Name == name })
}

type contextKey string

const (
	ctxPaneID contextKey = "mtm_pane_id"
	ctxTaskID contextKey = "mtm_task_id"
)

// WithPaneID binds an MTM_PANE_ID value into ctx so Toolbox.Call uses it as
// the default pane without mutating the shared Toolbox or process environment.
func WithPaneID(ctx context.Context, paneID string) context.Context {
	if paneID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxPaneID, paneID)
}

// PaneIDFromContext returns the MTM_PANE_ID bound into ctx, if any.
func PaneIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxPaneID).(string)
	return v
}

// WithTaskID binds an MTM_TASK_ID value into ctx so Toolbox.Call uses it as
// the default task without mutating the shared Toolbox or process environment.
func WithTaskID(ctx context.Context, taskID string) context.Context {
	if taskID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxTaskID, taskID)
}

// TaskIDFromContext returns the MTM_TASK_ID bound into ctx, if any.
func TaskIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxTaskID).(string)
	return v
}

func (b *Toolbox) envFunc(ctx context.Context) func(string) string {
	ctxPane := PaneIDFromContext(ctx)
	ctxTask := TaskIDFromContext(ctx)
	return func(name string) string {
		switch name {
		case EnvPaneID:
			if ctxPane != "" {
				return ctxPane
			}
		case EnvTaskID:
			if ctxTask != "" {
				return ctxTask
			}
		}
		if b.Getenv != nil {
			return b.Getenv(name)
		}
		return os.Getenv(name)
	}
}

// envDefaults fills the pane (and, for a new page or question, the task) a
// tool left out from the bound pane/task context or environment.
func (b *Toolbox) envDefaults(getenv func(string) string, name string, raw json.RawMessage) json.RawMessage {
	var keys []string
	switch name {
	case ToolCreatePage, ToolAsk, ToolSuggestCommand, ToolCreateDocument, ToolAddContext, ToolLogQA, ToolTaskNote:
		keys = []string{"pane", "task"}
	case ToolTaskFlagAgent, ToolTaskThread:
		keys = []string{"task"}
	case ToolUpdatePage, ToolRecordAgentInput:
		keys = []string{"pane"}
	default:
		return raw
	}
	pane, task := getenv(EnvPaneID), getenv(EnvTaskID)
	if pane == "" && task == "" {
		return raw
	}
	args := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return raw // the tool reports what is wrong with them
		}
	}
	for _, key := range keys {
		value := map[string]string{"pane": pane, "task": task}[key]
		if _, given := args[key]; given || value == "" {
			continue
		}
		args[key], _ = json.Marshal(value)
	}
	out, err := json.Marshal(args)
	if err != nil {
		return raw
	}
	return out
}

// Call runs one tool by name with JSON arguments.
func (b *Toolbox) Call(ctx context.Context, actor core.Actor, name string, raw json.RawMessage) (any, error) {
	getenv := b.envFunc(ctx)
	lanes := *b.Lanes
	if lanes.Getenv == nil {
		lanes.Getenv = getenv
	} else if PaneIDFromContext(ctx) != "" || TaskIDFromContext(ctx) != "" {
		orig := lanes.Getenv
		lanes.Getenv = func(k string) string {
			if k == EnvPaneID {
				if v := PaneIDFromContext(ctx); v != "" {
					return v
				}
			}
			if k == EnvTaskID {
				if v := TaskIDFromContext(ctx); v != "" {
					return v
				}
			}
			return orig(k)
		}
	}
	switch {
	case b.Data.HasTool(name):
		dataTools := *b.Data
		dataTools.Getenv = getenv
		return dataTools.Call(ctx, actor, name, b.envDefaults(getenv, name, raw))
	case lanes.HasTool(name):
		return lanes.Call(ctx, actor, name, raw)
	case b.Triage != nil && b.Triage.HasTool(name):
		return b.Triage.Call(ctx, actor, name, raw)
	case b.PaneSearch != nil && b.PaneSearch.HasTool(name):
		return b.PaneSearch.Call(ctx, actor, name, raw)
	case b.Tasks != nil && b.Tasks.HasTool(name):
		return b.Tasks.Call(ctx, actor, name, b.envDefaults(getenv, name, raw))
	case b.Context != nil && b.Context.HasTool(name):
		ctxTools := *b.Context
		ctxTools.Getenv = getenv
		return ctxTools.Call(ctx, actor, name, raw)
	}
	return b.Router.Call(ctx, actor, name, b.envDefaults(getenv, name, raw))
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

var nullID = json.RawMessage("null")

func errorReply(id json.RawMessage, code int, format string, a ...any) *rpcResponse {
	if len(id) == 0 {
		id = nullID
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: fmt.Sprintf(format, a...)}}
}

// server is one stdio session.
type server struct {
	box   *Toolbox
	tools []ToolSpec

	mu    sync.Mutex // guards actor
	actor core.Actor

	wmu  sync.Mutex // serializes writes to out
	out  io.Writer
	werr error
}

// ServeStdio serves MCP over newline-delimited JSON-RPC 2.0: one message per
// line on in, one reply per line on out. It lists and runs the Open UI tools
// of router, the lane tools and the data tools over store. A ui.* call that
// names no pane uses $MTM_PANE_ID (and $MTM_TASK_ID for its task). Writes made through it carry an
// agent actor (ID "mcp" unless a tool's "by" names another; the label is the
// client's name from initialize).
//
// Requests run concurrently and replies are written whole, one at a time.
// ServeStdio returns nil when in ends (after the replies in flight are
// written), a write error if out failed, and ctx's error when ctx is
// cancelled. On cancel it returns at once; a read still blocked on in ends
// when in is closed.
func ServeStdio(ctx context.Context, in io.Reader, out io.Writer, store *data.Store, router *Router) error {
	if router == nil {
		router = New(store)
	}
	s := &server{box: NewToolbox(store, router), out: out,
		actor: core.Actor{Kind: "agent", ID: DefaultAgentID, Label: ServerName}}
	s.tools = s.box.ToolSpecs()

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		r := bufio.NewReader(in)
		for {
			line, err := r.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				select {
				case lines <- line:
				case <-done:
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				readErr <- err
				return
			}
		}
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			wg.Wait()
			if err == nil {
				s.wmu.Lock()
				err = s.werr
				s.wmu.Unlock()
			}
			return err
		case line := <-lines:
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.handleLine(ctx, line)
			}()
		}
	}
}

// write sends one message as one line.
func (s *server) write(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		b, _ = json.Marshal(errorReply(nil, codeInternalError, "reply is not JSON: %v", err))
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.werr != nil {
		return
	}
	_, s.werr = s.out.Write(append(b, '\n'))
}

// handleLine answers one line: a request, a notification or a batch.
func (s *server) handleLine(ctx context.Context, line []byte) {
	line = bytes.TrimSpace(line)
	if line[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(line, &batch); err != nil {
			s.write(errorReply(nil, codeParseError, "parse error: %v", err))
			return
		}
		if len(batch) == 0 {
			s.write(errorReply(nil, codeInvalidRequest, "invalid request: empty batch"))
			return
		}
		var replies []*rpcResponse
		for _, msg := range batch {
			if reply := s.handle(ctx, msg); reply != nil {
				replies = append(replies, reply)
			}
		}
		if len(replies) > 0 {
			s.write(replies)
		}
		return
	}
	if !json.Valid(line) {
		s.write(errorReply(nil, codeParseError, "parse error: the line is not JSON"))
		return
	}
	if reply := s.handle(ctx, line); reply != nil {
		s.write(reply)
	}
}

// handle answers one message. A notification (no id) gets no reply.
func (s *server) handle(ctx context.Context, msg json.RawMessage) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return errorReply(nil, codeInvalidRequest, "invalid request: %v", err)
	}
	if req.JSONRPC != "2.0" || req.Method == nil {
		return errorReply(req.ID, codeInvalidRequest, `invalid request: needs "jsonrpc": "2.0" and a method`)
	}
	notification := len(req.ID) == 0
	result, rerr := s.dispatch(ctx, *req.Method, req.Params)
	if notification {
		return nil
	}
	if rerr != nil {
		return errorReply(req.ID, rerr.Code, "%s", rerr.Message)
	}
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func invalidParams(err error) *rpcError {
	return &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
}

// params decodes a method's params; absent params decode as {}.
func params(raw json.RawMessage, into any) *rpcError {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return invalidParams(err)
	}
	return nil
}

func (s *server) dispatch(ctx context.Context, method string, raw json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		if err := params(raw, &p); err != nil {
			return nil, err
		}
		version := ProtocolVersions[0]
		if slices.Contains(ProtocolVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		if p.ClientInfo.Name != "" {
			s.mu.Lock()
			s.actor.Label = p.ClientInfo.Name
			s.mu.Unlock()
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": ServerName, "version": "2"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := params(raw, &p); err != nil {
			return nil, err
		}
		if p.Name == "" {
			return nil, invalidParams(errors.New("name is required"))
		}
		return s.callTool(ctx, p.Name, p.Arguments)
	}
	if strings.HasPrefix(method, "notifications/") {
		return nil, nil // notifications/initialized and the rest need nothing
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + method}
}

// callTool runs a tool. An unknown tool is a protocol error; a tool that
// fails returns a result with isError and the error as text.
func (s *server) callTool(ctx context.Context, name string, args json.RawMessage) (any, *rpcError) {
	s.mu.Lock()
	actor := s.actor
	s.mu.Unlock()
	if !s.box.HasTool(name) {
		return nil, invalidParams(fmt.Errorf("unknown tool %q", name))
	}
	out, err := s.box.Call(ctx, actor, name, args)
	if err != nil {
		return toolError(err), nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return toolError(err), nil
	}
	var structured any
	if err := json.Unmarshal(b, &structured); err != nil {
		return toolError(err), nil
	}
	if _, isObject := structured.(map[string]any); !isObject {
		structured = map[string]any{"result": structured}
		b, _ = json.Marshal(structured)
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": structured,
	}, nil
}

func toolError(err error) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}
}
