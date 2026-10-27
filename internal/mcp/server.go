package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Protocol versions this server speaks; a client's own is echoed when known.
var protocolVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

const defaultProtocol = "2025-06-18"

// JSON-RPC 2.0 error codes this server answers with.
const (
	codeParse          = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Serve speaks newline-delimited JSON-RPC over the given streams until the
// input closes or the context ends. The identity is fixed for the whole
// session: it comes from the environment, never from a request. Requests
// are answered one at a time, in order.
func (s *Server) Serve(ctx context.Context, id Identity, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	var mu sync.Mutex
	write := func(v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		_, err = out.Write(append(raw, '\n'))
		return err
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			if err := write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: "parse error: " + err.Error()}}); err != nil {
				return err
			}
			continue
		}
		if req.Method == "" {
			// A response to a server request; this server sends none.
			continue
		}
		result, rpcErr := s.handle(ctx, id, req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue // notification
		}
		resp := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		if err := write(resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handle(ctx context.Context, id Identity, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := defaultProtocol
		for _, known := range protocolVersions {
			if known == params.ProtocolVersion {
				version = known
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "mtm", "version": s.version},
			"instructions":    "mtm tools act as the pane this server was started in. Call whoami first to learn your task and project.",
		}, nil
	case "notifications/initialized", "notifications/cancelled", "notifications/roots/list_changed":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := s.Tools()
		listed := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			listed = append(listed, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema})
		}
		return map[string]any{"tools": listed}, nil
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
		}
		if params.Name == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "tool name is required"}
		}
		result, err := s.Call(ctx, id, params.Name, params.Arguments)
		if err != nil {
			code := "error"
			var structured *Error
			if errors.As(err, &structured) {
				code = structured.Code
				// An unknown tool and arguments that do not match the
				// tool's schema are protocol errors; everything else is
				// a tool result the model can read and act on.
				if code == CodeUnknownTool || code == CodeInvalidArgs {
					return nil, &rpcError{Code: codeInvalidParams, Message: err.Error(), Data: map[string]any{"code": code, "tool": params.Name}}
				}
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": code + ": " + err.Error()}}, "isError": true}, nil
		}
		raw, marshalErr := json.MarshalIndent(result, "", "  ")
		if marshalErr != nil {
			return nil, &rpcError{Code: codeInternal, Message: marshalErr.Error()}
		}
		// Results are typed values; hand clients plain JSON values.
		var generic any
		_ = json.Unmarshal(raw, &generic)
		reply := map[string]any{"content": []map[string]any{{"type": "text", "text": string(raw)}}, "isError": false}
		if object, isObject := generic.(map[string]any); isObject {
			reply["structuredContent"] = object
		} else if generic != nil {
			reply["structuredContent"] = map[string]any{"result": generic}
		}
		return reply, nil
	case "resources/list":
		return map[string]any{"resources": []any{}}, nil
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method %q not found", req.Method)}
}
