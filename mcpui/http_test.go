package mcpui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func httpPostJSON(t *testing.T, h http.Handler, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	switch v := body.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeRPCMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var msg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("status=%d body=%q is not JSON object: %v", rec.Code, rec.Body.String(), err)
	}
	return msg
}

func TestHTTPHealthAndRouting(t *testing.T) {
	f := setup(t)
	h := NewHTTPHandler(f.store, f.r)

	// GET /health -> {"ok":true}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200", rec.Code)
	}
	got := decodeRPCMap(t, rec)
	if got["ok"] != true {
		t.Fatalf("GET /health body = %v, want {ok:true}", got)
	}

	// Non-GET on /health -> 405
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health status = %d, want 405", rec.Code)
	}

	// Non-POST on /mcp -> 405
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status = %d, want 405", rec.Code)
	}

	// Unknown paths -> 404
	for _, p := range []string{"/", "/unknown", "/mcp/a/b"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{}`)))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404", p, rec.Code)
		}
	}
}

func TestHTTPPaneBindingAndJSONRPC(t *testing.T) {
	f := setup(t)
	// Add a second pane so we can verify independent auto-binding.
	_, _, err := f.store.Create(data.CreateInput{Kind: data.KindPane, ID: "w99",
		Fields: map[string]any{"title": "codex", "task": f.task.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHTTPHandler(f.store, f.r)

	// Initialize + notification + tools/list
	rec := httpPostJSON(t, h, "/mcp/w12", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18", "clientInfo": map[string]any{"name": "claude-http"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d", rec.Code)
	}
	initRes := decodeRPCMap(t, rec)["result"].(map[string]any)
	if initRes["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize result = %v", initRes)
	}

	// Notification returns 202 Accepted with empty body.
	rec = httpPostJSON(t, h, "/mcp/w12", nil, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", rec.Code)
	}

	// Call ui.ask on POST /mcp/w12 WITHOUT passing "pane" in arguments!
	rec = httpPostJSON(t, h, "/mcp/w12", nil, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": ToolAsk,
			"arguments": map[string]any{
				"title":   "Deploy now?",
				"choices": []map[string]any{{"id": "yes"}, {"id": "no"}},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ui.ask status = %d", rec.Code)
	}
	askResp := decodeRPCMap(t, rec)["result"].(map[string]any)
	if askResp["isError"] == true {
		t.Fatalf("ui.ask error: %v", askResp)
	}
	pageID := askResp["structuredContent"].(map[string]any)["id"].(string)
	pageRec, err := f.store.Get(core.Ref{Kind: data.KindPage, ID: pageID})
	if err != nil {
		t.Fatal(err)
	}
	if pane, ok := pageRec.RefField("pane"); !ok || pane.ID != "w12" {
		t.Fatalf("page pane ref = %v (%v), want w12 from URL path", pane, ok)
	}

	// Call lane.start on POST /mcp with X-MTM-Pane-ID header (no pane in args).
	rec = httpPostJSON(t, h, "/mcp", map[string]string{HeaderPaneID: "w99"}, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name":      ToolLaneStart,
			"arguments": map[string]any{"title": "HTTP MCP lane"},
		},
	})
	laneResp := decodeRPCMap(t, rec)["result"].(map[string]any)
	if laneResp["isError"] == true {
		t.Fatalf("lane.start error: %v", laneResp)
	}
	claim := laneResp["structuredContent"].(map[string]any)["claim"].(map[string]any)
	toRef := claim["to"].(map[string]any)
	if toRef["id"] != "w99" {
		t.Fatalf("lane claim pane = %v, want w99 from X-MTM-Pane-ID header", toRef)
	}

	// Call data.record_agent_input on POST /mcp/w99 without passing "pane".
	rec = httpPostJSON(t, h, "/mcp/w99", nil, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": ToolRecordAgentInput, "arguments": map[string]any{}},
	})
	inpResp := decodeRPCMap(t, rec)["result"].(map[string]any)
	if inpResp["isError"] == true {
		t.Fatalf("data.record_agent_input error: %v", inpResp)
	}
	if inpResp["structuredContent"].(map[string]any)["id"] != "w99" {
		t.Fatalf("record_agent_input returned %v", inpResp)
	}

	// Concurrent calls from two different panes must not bleed pane IDs across requests.
	var wg sync.WaitGroup
	for _, pid := range []string{"w12", "w99"} {
		wg.Add(1)
		go func(paneID string) {
			defer wg.Done()
			r := httpPostJSON(t, h, "/mcp/"+paneID, nil, map[string]any{
				"jsonrpc": "2.0", "id": 10, "method": "tools/call",
				"params": map[string]any{
					"name": ToolCreatePage,
					"arguments": map[string]any{
						"title": "Page for " + paneID,
						"root":  map[string]any{"kind": "text", "props": map[string]any{"text": paneID}},
					},
				},
			})
			res := decodeRPCMap(t, r)["result"].(map[string]any)
			if res["isError"] == true {
				t.Errorf("concurrent create_page %s error: %v", paneID, res)
				return
			}
			id := res["structuredContent"].(map[string]any)["id"].(string)
			pr, gerr := f.store.Get(core.Ref{Kind: data.KindPage, ID: id})
			if gerr != nil {
				t.Errorf("get page %s: %v", id, gerr)
				return
			}
			if ref, _ := pr.RefField("pane"); ref.ID != paneID {
				t.Errorf("concurrent page pane = %q, want %q", ref.ID, paneID)
			}
		}(pid)
	}
	wg.Wait()

	// Batch JSON-RPC request
	rec = httpPostJSON(t, h, "/mcp/w12", nil, []map[string]any{
		{"jsonrpc": "2.0", "id": 20, "method": "ping"},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 21, "method": "tools/list"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status = %d", rec.Code)
	}
	var batch []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil {
		t.Fatalf("batch decode: %v", err)
	}
	if len(batch) != 2 || batch[0]["id"] != float64(20) || batch[1]["id"] != float64(21) {
		t.Fatalf("batch replies = %v", batch)
	}

	// Malformed JSON returns JSON-RPC parse error.
	rec = httpPostJSON(t, h, "/mcp", nil, `{"jsonrpc":"2.0",`)
	if code := errorCode(decodeRPCMap(t, rec)); code != codeParseError {
		t.Fatalf("malformed JSON code = %v, want %d", code, codeParseError)
	}

	// Verify open access tool descriptions advise archive over delete for mutating tools.
	tools := batch[1]["result"].(map[string]any)["tools"].([]any)
	sawArchiveGuidance := false
	for _, item := range tools {
		spec := item.(map[string]any)
		desc := strings.ToLower(spec["description"].(string))
		if strings.Contains(desc, "archive") && strings.Contains(desc, "delete") {
			sawArchiveGuidance = true
		}
	}
	if !sawArchiveGuidance {
		t.Fatal("expected mutating tool descriptions to advise preferring archive over delete")
	}
}

func TestBatchedAskAndSubmitAll(t *testing.T) {
	f := setup(t)

	// Ask a batch of 3 questions in a single ui.ask call.
	var asked PageResult
	f.call(ToolAsk, map[string]any{
		"pane":  "w12",
		"id":    "arch-batch",
		"title": "Architecture Decisions",
		"questions": []any{
			map[string]any{
				"title": "Which storage engine?",
				"choices": []any{
					map[string]any{"id": "sqlite", "label": "SQLite"},
					map[string]any{"id": "json", "label": "JSON files"},
				},
			},
			map[string]any{
				"id":    "q-transport",
				"title": "Which transport?",
				"choices": []any{
					map[string]any{"id": "unix", "label": "Unix socket"},
					map[string]any{"id": "tcp", "label": "TCP"},
				},
			},
			map[string]any{
				"title": "Default timeout?",
				"form": map[string]any{
					"fields": []any{
						map[string]any{"name": "seconds", "type": "int", "required": true},
					},
				},
			},
		},
	}, &asked)

	if asked.Slug != QuestionsSlug || asked.BatchID != "arch-batch" {
		t.Fatalf("batch ask result = %+v", asked)
	}
	wantIDs := []string{"arch-batch-q1", "q-transport", "arch-batch-q3"}
	if !slices.Equal(asked.CardIDs, wantIDs) || asked.CardID != wantIDs[0] {
		t.Fatalf("cardIds = %v (cardId=%q), want %v", asked.CardIDs, asked.CardID, wantIDs)
	}

	page, root := f.page(asked.ID)
	if !page.IsUnread() || page.Text("pageKind") != "questions" {
		t.Fatalf("questions page = %+v", page.Fields)
	}
	// Cards appear in top-to-bottom order matching the questions array.
	if got := childIDs(root); !slices.Equal(got, wantIDs) {
		t.Fatalf("page child order = %v, want %v", got, wantIDs)
	}
	for i, id := range wantIDs {
		card := root.Find(id)
		if card == nil {
			t.Fatalf("missing card %q", id)
		}
		if card.Props["batchId"] != "arch-batch" || card.Props["batchTitle"] != "Architecture Decisions" {
			t.Fatalf("card %q missing batch metadata: %+v", id, card.Props)
		}
		if card.Facts["batchId"] != "arch-batch" || card.Facts["answered"] != false {
			t.Fatalf("card %q facts = %+v", id, card.Facts)
		}
		if gotIdx := int(card.Props["batchIndex"].(float64)); gotIdx != i {
			t.Fatalf("card %q batchIndex = %d, want %d", id, gotIdx, i)
		}
	}

	// Cannot mix questions array with top-level choices/form/card.
	if _, err := f.r.Call(context.Background(), agent, ToolAsk, json.RawMessage(`{
		"pane":"w12",
		"choices":[{"id":"a"}],
		"questions":[{"title":"Q1","choices":[{"id":"x"}]}]
	}`)); err == nil || !strings.Contains(err.Error(), "cannot specify both") {
		t.Fatalf("mixed batch/single ask error = %v", err)
	}

	// Duplicate ID inside batch is rejected cleanly.
	if _, err := f.r.Call(context.Background(), agent, ToolAsk, json.RawMessage(`{
		"pane":"w12",
		"questions":[
			{"id":"dup","title":"Q1","choices":[{"id":"x"}]},
			{"id":"dup","title":"Q2","choices":[{"id":"y"}]}
		]
	}`)); err == nil || !strings.Contains(err.Error(), "duplicate card id") {
		t.Fatalf("duplicate batch id error = %v", err)
	}

	// Submit All answers in one atomic write.
	answers := map[string]map[string]any{
		"arch-batch-q1": {"choice": "sqlite"},
		"q-transport":   {"choice": "unix"},
		"arch-batch-q3": {"seconds": float64(300)},
	}
	saved, err := f.r.SubmitAnswers(asked.ID, answers, owner)
	if err != nil {
		t.Fatalf("SubmitAnswers: %v", err)
	}
	if saved.IsUnread() {
		t.Fatal("SubmitAnswers should mark page viewed")
	}

	// ui.list_pages returns all 3 answered questions cleanly.
	var list ListPagesResult
	f.call(ToolListPages, map[string]any{"pane": "w12"}, &list)
	if len(list.Pages) != 1 || len(list.Pages[0].Questions) != 3 {
		t.Fatalf("list_pages = %+v", list)
	}
	for i, id := range wantIDs {
		q := list.Pages[0].Questions[i]
		if q.CardID != id || !q.Answered || !reflect.DeepEqual(q.Answer, answers[id]) {
			t.Fatalf("question[%d] = %+v, want answered with %v", i, q, answers[id])
		}
	}
}
