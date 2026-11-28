package mcpui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

const (
	// HeaderPaneID binds MTM_PANE_ID on HTTP MCP calls when not given in the URL path.
	HeaderPaneID = "X-MTM-Pane-ID"
	// HeaderTaskID optionally binds MTM_TASK_ID on HTTP MCP calls.
	HeaderTaskID = "X-MTM-Task-ID"
	// HeaderAgentID optionally overrides the actor ID on HTTP MCP calls.
	HeaderAgentID = "X-MTM-Agent-ID"

	maxHTTPBodyBytes = 16 << 20 // 16 MiB
)

// HTTPHandler serves MCP over HTTP JSON-RPC 2.0 on POST /mcp and
// POST /mcp/{pane_id}, accepts DELETE on both for client session teardown
// (204), plus GET /health returning {"ok":true}.
//
// When {pane_id} is present in the URL path or the X-MTM-Pane-ID header is set,
// HTTPHandler binds MTM_PANE_ID into the Toolbox call context so agents never
// need to pass pane_id manually. Agent access is open (no destructive
// permission blocks); mutating tool descriptions guide agents to prefer archive
// over delete.
type HTTPHandler struct {
	srv *server
}

// NewHTTPHandler creates an HTTP MCP handler over store and router.
func NewHTTPHandler(store *data.Store, router *Router) *HTTPHandler {
	return NewHTTPHandlerWithToolbox(NewToolbox(store, router))
}

// NewHTTPHandlerWithToolbox creates an HTTP MCP handler backed by box.
func NewHTTPHandlerWithToolbox(box *Toolbox) *HTTPHandler {
	srv := &server{
		box:   box,
		tools: box.ToolSpecs(),
		actor: core.Actor{Kind: "agent", ID: DefaultAgentID, Label: ServerName},
	}
	return &HTTPHandler{srv: srv}
}

// ServeHTTP implements http.Handler.
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/health" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
		}
		return
	}

	paneID, ok := parseMCPPath(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		// Streamable HTTP clients send DELETE to end a session; the server is
		// stateless, so there is nothing to tear down.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if paneID == "" {
		paneID = strings.TrimSpace(r.Header.Get(HeaderPaneID))
	}
	taskID := strings.TrimSpace(r.Header.Get(HeaderTaskID))

	ctx := r.Context()
	if paneID != "" {
		ctx = WithPaneID(ctx, paneID)
	}
	if taskID != "" {
		ctx = WithTaskID(ctx, taskID)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBodyBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorReply(nil, codeParseError, "read error: %v", err))
		return
	}
	if len(body) > maxHTTPBodyBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorReply(nil, codeInvalidRequest, "request body too large"))
		return
	}

	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		writeJSON(w, http.StatusOK, errorReply(nil, codeParseError, "parse error: empty body"))
		return
	}

	// If an explicit agent header is provided, run with a per-request actor override.
	srv := h.srv
	if agentID := strings.TrimSpace(r.Header.Get(HeaderAgentID)); agentID != "" {
		h.srv.mu.Lock()
		actor := h.srv.actor
		h.srv.mu.Unlock()
		actor.ID = agentID
		srv = &server{box: h.srv.box, tools: h.srv.tools, actor: actor}
	}

	if body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			writeJSON(w, http.StatusOK, errorReply(nil, codeParseError, "parse error: %v", err))
			return
		}
		if len(batch) == 0 {
			writeJSON(w, http.StatusOK, errorReply(nil, codeInvalidRequest, "invalid request: empty batch"))
			return
		}
		var replies []*rpcResponse
		for _, msg := range batch {
			if reply := srv.handle(ctx, msg); reply != nil {
				replies = append(replies, reply)
			}
		}
		if len(replies) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, http.StatusOK, replies)
		return
	}

	if !json.Valid(body) {
		writeJSON(w, http.StatusOK, errorReply(nil, codeParseError, "parse error: body is not valid JSON"))
		return
	}
	reply := srv.handle(ctx, body)
	if reply == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func parseMCPPath(path string) (paneID string, ok bool) {
	if path == "/mcp" || path == "/mcp/" {
		return "", true
	}
	rest, found := strings.CutPrefix(path, "/mcp/")
	if !found || rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	decoded, err := url.PathUnescape(rest)
	if err != nil {
		return rest, true
	}
	return decoded, true
}

func writeJSON(w http.ResponseWriter, status int, msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		b, _ = json.Marshal(errorReply(nil, codeInternalError, "reply is not JSON: %v", err))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
