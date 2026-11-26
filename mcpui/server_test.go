package mcpui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// client drives ServeStdio over pipes like an MCP client would.
type client struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	outR *io.PipeReader
	errc chan error
	next int
}

func serve(t *testing.T, ctx context.Context, store *data.Store) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &client{t: t, in: inW, out: bufio.NewReader(outR), outR: outR, errc: make(chan error, 1)}
	go func() {
		err := ServeStdio(ctx, inR, outW, store, New(store))
		outW.Close()
		c.errc <- err
	}()
	return c
}

func (c *client) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		c.t.Fatalf("send: %v", err)
	}
}

// reply reads one reply line.
func (c *client) reply() map[string]any {
	c.t.Helper()
	line, err := c.out.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read reply: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		c.t.Fatalf("reply %q is not JSON: %v", line, err)
	}
	return msg
}

// request sends a request and returns its reply, checking the id matches.
func (c *client) request(method string, params any) map[string]any {
	c.t.Helper()
	c.next++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method, "params": params})
	c.send(string(b))
	msg := c.reply()
	if msg["jsonrpc"] != "2.0" || msg["id"] != float64(c.next) {
		c.t.Fatalf("%s: reply %v does not answer id %d", method, msg, c.next)
	}
	return msg
}

func (c *client) result(method string, params any) map[string]any {
	c.t.Helper()
	msg := c.request(method, params)
	res, ok := msg["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("%s: no result in %v", method, msg)
	}
	return res
}

// tool calls a tool and returns its structured content, failing on isError.
func (c *client) tool(name string, args any) map[string]any {
	c.t.Helper()
	res := c.result("tools/call", map[string]any{"name": name, "arguments": args})
	if res["isError"] == true {
		c.t.Fatalf("%s failed: %v", name, res["content"])
	}
	content := res["content"].([]any)[0].(map[string]any)
	var fromText map[string]any
	if content["type"] != "text" || json.Unmarshal([]byte(content["text"].(string)), &fromText) != nil {
		c.t.Fatalf("%s: content is not JSON text: %v", name, content)
	}
	structured := res["structuredContent"].(map[string]any)
	if !reflect.DeepEqual(fromText, structured) {
		c.t.Fatalf("%s: text %v and structuredContent %v differ", name, fromText, structured)
	}
	return structured
}

func errorCode(msg map[string]any) float64 {
	e, _ := msg["error"].(map[string]any)
	code, _ := e["code"].(float64)
	return code
}

// waitGoroutines fails unless the goroutine count falls back to base.
func waitGoroutines(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines, want %d: a goroutine leaked", runtime.NumGoroutine(), base)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServeStdioSession(t *testing.T) {
	f := setup(t)
	sub, _, err := f.store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "MCP", "parent": f.task.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	prompted, _, err := f.store.RecordPaneInput(f.pane.Ref(), owner)
	if err != nil {
		t.Fatal(err)
	}
	base := runtime.NumGoroutine()
	c := serve(t, context.Background(), f.store)

	hello := c.result("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "claude", "version": "1"}})
	if hello["protocolVersion"] != "2025-03-26" || hello["serverInfo"].(map[string]any)["name"] != ServerName {
		t.Fatalf("initialize: %v", hello)
	}
	if _, ok := hello["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatalf("initialize does not offer tools: %v", hello)
	}
	// A notification gets no reply: the next line answers the ping.
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	c.result("ping", nil)

	var names []string
	for _, tool := range c.result("tools/list", nil)["tools"].([]any) {
		spec := tool.(map[string]any)
		if spec["description"] == "" || spec["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("tool %v lacks a description or object schema", spec["name"])
		}
		names = append(names, spec["name"].(string))
	}
	want := []string{ToolCreatePage, ToolUpdatePage, ToolAsk, ToolListPages, ToolLaneStart, ToolLaneUpdate, ToolLaneLog, ToolLaneAttach, ToolSchemas, ToolQuery, ToolGet, ToolRelated,
		ToolTaskTree, ToolHistory, ToolCreateNote, ToolRecordAgentInput, ToolSuggestCommand, ToolCreateDocument, ToolAddContext, ToolLogQA, ToolCreate, ToolUpdate, ToolRelate, ToolUnrelate,
		ToolTriageIngest, ToolTriageEvaluate, ToolPaneSearch, ToolPaneSpawn, ToolPaneSnapshot, ToolPaneSend, ToolPaneStatus, ToolPaneClose,
		ToolTaskQueue, ToolTaskFlagAgent, ToolTaskNote, ToolTaskThread, ToolTaskDispatch,
		ToolTaskRewrite, ToolTaskSplit, ToolTaskJoin,
		ToolContextOfficial, ToolContextProject, ToolContextAddOfficial}
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}

	// The Open UI tools, through the router.
	asked := c.tool(ToolAsk, map[string]any{"pane": "w12", "title": "Ship it?", "choices": []map[string]any{{"id": "yes"}, {"id": "no"}}})
	if asked["cardId"] == "" || asked["slug"] != QuestionsSlug {
		t.Fatalf("ui.ask: %v", asked)
	}
	if q := c.tool(ToolQuery, map[string]any{"kind": data.KindPage, "query": "questions", "filterField": "slug"}); q["total"] != float64(1) {
		t.Fatalf("data.query: %v", q)
	}
	pages := c.tool(ToolListPages, map[string]any{"pane": "w12"})["pages"].([]any)
	if len(pages) != 1 || len(pages[0].(map[string]any)["questions"].([]any)) != 1 {
		t.Fatalf("ui.list_pages: %v", pages)
	}

	// Reads.
	if got := c.tool(ToolGet, map[string]any{"kind": data.KindPane, "id": "w12"}); got["fields"].(map[string]any)["title"] != "claude" {
		t.Fatalf("data.get: %v", got)
	}
	tree := c.tool(ToolTaskTree, map[string]any{})["tasks"].([]any)
	if len(tree) != 1 || tree[0].(map[string]any)["children"].([]any)[0].(map[string]any)["record"].(map[string]any)["id"] != sub.ID {
		t.Fatalf("data.task_tree: %v", tree)
	}
	schemas := c.tool(ToolSchemas, map[string]any{"kind": data.KindNote})["schemas"].([]any)
	if len(schemas) != 1 || !strings.Contains(toJSON(schemas[0]), `"name":"archiveStatus"`) || !strings.Contains(toJSON(schemas[0]), `"kind":"about"`) {
		t.Fatalf("data.schemas: %v", schemas)
	}

	// data.create_note: a note, about the pane, written by the named agent.
	made := c.tool(ToolCreateNote, map[string]any{"title": "seen", "body": "the agent saw this", "about": map[string]any{"kind": data.KindPane, "id": "w12"}, "by": "w7"})
	noteID := made["note"].(map[string]any)["id"].(string)
	note, err := f.store.Get(core.Ref{Kind: data.KindNote, ID: noteID})
	if err != nil || note.Text("body") != "the agent saw this" {
		t.Fatalf("note not stored: %v, %v", note, err)
	}
	rels, err := f.store.Related(note.Ref(), data.RelAbout, data.Outgoing)
	if err != nil || len(rels) != 1 || rels[0].To != f.pane.Ref() {
		t.Fatalf("about relation: %v, %v", rels, err)
	}
	if cs, _ := f.store.ChangeSet(note.ChangeSetNumber); cs.Actor != (core.Actor{Kind: "agent", ID: "w7", Label: "claude"}) {
		t.Fatalf("note written by %+v", cs.Actor)
	}
	related := c.tool(ToolRelated, map[string]any{"kind": data.KindPane, "id": "w12", "relation": data.RelAbout, "direction": "incoming"})
	if recs := related["records"].([]any); len(recs) != 1 || recs[0].(map[string]any)["id"] != noteID {
		t.Fatalf("data.related: %v", related)
	}

	// data.record_agent_input never moves the owner's promptedAt.
	input := c.tool(ToolRecordAgentInput, map[string]any{"pane": "w12"})
	fields := input["fields"].(map[string]any)
	if fields["promptedAt"] != prompted.Text("promptedAt") || fields["lastAgentBy"] != DefaultAgentID || fields["lastAgentInputAt"] == nil {
		t.Fatalf("data.record_agent_input: %v", fields)
	}

	latest, _ := f.store.LatestChangeSetNumber()
	hist := c.tool(ToolHistory, map[string]any{"kind": data.KindPane, "id": "w12", "limit": 2})
	if sets := hist["changeSets"].([]any); len(sets) != 2 || sets[0].(map[string]any)["number"] != float64(latest) || hist["latest"] != float64(latest) {
		t.Fatalf("data.history: %v", hist)
	}

	// A failing tool is a result with isError; protocol mistakes are errors.
	if res := c.result("tools/call", map[string]any{"name": ToolGet, "arguments": map[string]any{"kind": "task", "id": "nope"}}); res["isError"] != true {
		t.Fatalf("missing record: %v", res)
	}
	if code := errorCode(c.request("tools/call", map[string]any{"name": "data.nope"})); code != codeInvalidParams {
		t.Fatalf("unknown tool: code %v", code)
	}
	if code := errorCode(c.request("resources/list", nil)); code != codeMethodNotFound {
		t.Fatalf("unknown method: code %v", code)
	}
	c.send(`{"jsonrpc":"2.0","id":99,"method":`)
	if msg := c.reply(); errorCode(msg) != codeParseError || msg["id"] != nil {
		t.Fatalf("malformed JSON: %v", msg)
	}
	c.send(`{"id":100,"method":"ping"}`)
	if msg := c.reply(); errorCode(msg) != codeInvalidRequest || msg["id"] != float64(100) {
		t.Fatalf("no jsonrpc version: %v", msg)
	}

	// EOF ends the session cleanly.
	c.in.Close()
	if err := <-c.errc; err != nil {
		t.Fatalf("ServeStdio after EOF: %v", err)
	}
	waitGoroutines(t, base)
}

func TestServeStdioCancel(t *testing.T) {
	f := setup(t)
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	c := serve(t, ctx, f.store)
	c.result("ping", nil)
	cancel()
	select {
	case err := <-c.errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ServeStdio after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeStdio did not return on cancel")
	}
	// The read blocked on in ends when in is closed.
	c.in.Close()
	c.outR.Close()
	waitGoroutines(t, base)
}

func TestDataToolSpecsMatchRealParams(t *testing.T) {
	args := map[string]any{
		ToolSchemas: SchemasArgs{}, ToolQuery: QueryArgs{}, ToolGet: GetArgs{}, ToolRelated: RelatedArgs{},
		ToolTaskTree: TaskTreeArgs{}, ToolHistory: HistoryArgs{}, ToolCreateNote: CreateNoteArgs{}, ToolRecordAgentInput: RecordAgentInputArgs{},
		ToolSuggestCommand: SuggestCommandArgs{}, ToolCreateDocument: CreateDocumentArgs{}, ToolAddContext: AddContextArgs{},
		ToolLogQA: LogQAArgs{}, ToolCreate: CreateRecordArgs{}, ToolUpdate: UpdateRecordArgs{},
		ToolRelate: RelateArgs{}, ToolUnrelate: RelateArgs{},
	}
	specs := NewDataTools(nil).ToolSpecs()
	if len(specs) != len(args) {
		t.Fatalf("%d data tools, want %d", len(specs), len(args))
	}
	for _, spec := range specs {
		rt := reflect.TypeOf(args[spec.Name])
		if rt == nil {
			t.Fatalf("unexpected tool %q", spec.Name)
		}
		var fields, declared []string
		for i := range rt.NumField() {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			fields = append(fields, name)
		}
		for name := range spec.InputSchema["properties"].(map[string]any) {
			declared = append(declared, name)
		}
		slices.Sort(fields)
		slices.Sort(declared)
		if !slices.Equal(fields, declared) {
			t.Errorf("%s: schema properties %v, real params %v", spec.Name, declared, fields)
		}
		if spec.InputSchema["additionalProperties"] != false {
			t.Errorf("%s: schema accepts unknown keys", spec.Name)
		}
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
