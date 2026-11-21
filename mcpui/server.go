package mcpui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// then the data tools.
func Tools(router *Router, dataTools *DataTools) []ToolSpec {
	return append(router.ToolSpecs(), dataTools.ToolSpecs()...)
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
	router *Router
	data   *DataTools
	tools  []ToolSpec

	mu    sync.Mutex // guards actor
	actor core.Actor

	wmu  sync.Mutex // serializes writes to out
	out  io.Writer
	werr error
}

// ServeStdio serves MCP over newline-delimited JSON-RPC 2.0: one message per
// line on in, one reply per line on out. It lists and runs the Open UI tools
// of router and the data tools over store. Writes made through it carry an
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
	s := &server{router: router, data: NewDataTools(store), out: out,
		actor: core.Actor{Kind: "agent", ID: DefaultAgentID, Label: ServerName}}
	s.tools = Tools(s.router, s.data)

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
	var (
		out any
		err error
	)
	switch {
	case s.data.HasTool(name):
		out, err = s.data.Call(ctx, actor, name, args)
	case slices.ContainsFunc(s.router.ToolSpecs(), func(t ToolSpec) bool { return t.Name == name }):
		out, err = s.router.Call(ctx, actor, name, args)
	default:
		return nil, invalidParams(fmt.Errorf("unknown tool %q", name))
	}
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
