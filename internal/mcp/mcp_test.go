package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeBackend answers task.list from tasks and records every other run.
type fakeBackend struct {
	id    Identity
	tasks []map[string]any
	runs  []string // "noun.verb {args}"
	reply map[string]string
}

func (f *fakeBackend) Identify(_ context.Context, id Identity) (Identity, error) {
	out := f.id
	out.Session, out.Window = id.Session, id.Window
	return out, nil
}

func (f *fakeBackend) Run(_ context.Context, noun, verb string, args map[string]any) (json.RawMessage, error) {
	raw, _ := json.Marshal(args)
	f.runs = append(f.runs, noun+"."+verb+" "+string(raw))
	if noun+"."+verb == "task.list" {
		return json.Marshal(f.tasks)
	}
	if r, ok := f.reply[noun+"."+verb]; ok {
		return json.RawMessage(r), nil
	}
	return json.RawMessage(`{"ok":true}`), nil
}

// session runs requests through Serve over pipes and returns one decoded
// response per request with an id.
func session(t *testing.T, s *Server, id Identity, lines ...string) []map[string]any {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), id, inR, outW); outW.Close() }()
	go func() {
		for _, l := range lines {
			io.WriteString(inW, l+"\n")
		}
		inW.Close()
	}()
	var out []map[string]any
	sc := bufio.NewScanner(outR)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("response %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return out
}

func call(id int, name string, args any) string {
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	return string(raw)
}

func newServer(t *testing.T, f *fakeBackend) *Server {
	t.Helper()
	s, err := New(f, "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestServeInitializeListAndNotifications(t *testing.T) {
	s := newServer(t, &fakeBackend{})
	out := session(t, s, Identity{},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`not json`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
	)
	if len(out) != 5 {
		t.Fatalf("want 5 responses (the notification gets none), got %d: %v", len(out), out)
	}
	init := out[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["serverInfo"].(map[string]any)["name"] != "mtm" {
		t.Fatalf("initialize: %v", init)
	}
	var names []string
	for _, tool := range out[1]["result"].(map[string]any)["tools"].([]any) {
		m := tool.(map[string]any)
		names = append(names, m["name"].(string))
		if m["inputSchema"].(map[string]any)["type"] != "object" {
			t.Fatalf("%s schema: %v", m["name"], m["inputSchema"])
		}
	}
	want := []string{"context_add", "document_report", "report_progress", "task_done", "task_list", "task_note_add", "task_status", "whoami"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tools: %v, want %v", names, want)
	}
	if code := out[3]["error"].(map[string]any)["code"]; code != float64(codeParse) {
		t.Fatalf("parse error: %v", out[3])
	}
	if code := out[4]["error"].(map[string]any)["code"]; code != float64(codeMethodNotFound) {
		t.Fatalf("unknown method: %v", out[4])
	}
}

func TestUnknownToolAndInvalidArgumentsAreJSONRPCErrors(t *testing.T) {
	f := &fakeBackend{}
	s := newServer(t, f)
	pane := Identity{Session: "work", Window: "w1"}
	out := session(t, s, pane,
		call(1, "frob", map[string]any{}),
		call(2, "task_status", map[string]any{}),                                  // status missing
		call(3, "task_status", map[string]any{"status": "finished"}),              // not in the enum
		call(4, "task_done", map[string]any{"task_id": 7}),                        // wrong type
		call(5, "report_progress", map[string]any{"message": "x", "extra": true}), // additionalProperties
		call(6, "task_note_add", map[string]any{"text": "x", "owner": map[string]any{"kind": "team"}}),
		call(7, "task_status", map[string]any{"status": "active", "expected_version": 1.5}),
	)
	for i, resp := range out {
		e, ok := resp["error"].(map[string]any)
		if !ok || e["code"] != float64(codeInvalidParams) {
			t.Fatalf("request %d: want a -32602 error, got %v", i+1, resp)
		}
	}
	if !strings.Contains(out[0]["error"].(map[string]any)["message"].(string), `unknown tool "frob"`) {
		t.Fatalf("unknown tool message: %v", out[0])
	}
	if len(f.runs) != 0 {
		t.Fatalf("invalid calls reached the backend: %v", f.runs)
	}
}

func TestPaneToolsRefuseWithoutAPane(t *testing.T) {
	s := newServer(t, &fakeBackend{})
	out := session(t, s, Identity{}, call(1, "task_done", map[string]any{}), call(2, "whoami", nil))
	res := out[0]["result"].(map[string]any)
	if res["isError"] != true || !strings.HasPrefix(res["content"].([]any)[0].(map[string]any)["text"].(string), "no_pane:") {
		t.Fatalf("task_done without a pane: %v", out[0])
	}
	who := out[1]["result"].(map[string]any)["structuredContent"].(map[string]any)
	if who["bound"] != false || who["origin"].(map[string]any)["kind"] != "none" {
		t.Fatalf("whoami without a pane: %v", who)
	}
}

func TestIdentityFromEnv(t *testing.T) {
	env := map[string]string{"TUIOS_SESSION": "work", "TUIOS_PANE_ID": "w1"}
	id := IdentityFromEnv(func(k string) string { return env[k] })
	if id != (Identity{Session: "work", Window: "w1", Bound: true}) {
		t.Fatalf("tuios env: %+v", id)
	}
	env["MTM_SESSION"], env["MTM_WINDOW"] = "s2", "w2"
	if id := IdentityFromEnv(func(k string) string { return env[k] }); id.Session != "s2" || id.Window != "w2" {
		t.Fatalf("MTM_ overrides: %+v", id)
	}
	if id := IdentityFromEnv(func(string) string { return "" }); id.Bound {
		t.Fatalf("no env is unbound: %+v", id)
	}
}

func TestTaskToolsDefaultToThePaneAndCallTheOperations(t *testing.T) {
	f := &fakeBackend{
		id: Identity{TaskID: "t1", ProjectID: "p1"},
		tasks: []map[string]any{
			{"id": "t1", "project_id": "p1", "status": "active", "updated": 5},
			{"id": "t2", "parent_id": "t1", "status": "todo", "updated": 1},
			{"id": "t3", "project_id": "p1", "status": "todo", "updated": 1},
			{"id": "t4", "project_id": "p2", "status": "todo", "updated": 1},
		},
	}
	s := newServer(t, f)
	ctx := context.Background()
	pane := Identity{Session: "work", Window: "w1"}
	ids := func(v any) []string {
		var out []string
		for _, raw := range v.([]json.RawMessage) {
			var row taskRow
			json.Unmarshal(raw, &row)
			out = append(out, row.ID)
		}
		return out
	}
	got, err := s.Call(ctx, pane, "task_list", nil)
	if err != nil || !reflect.DeepEqual(ids(got), []string{"t1", "t2"}) {
		t.Fatalf("task_list pin under the pane's task: %v %v", ids(got), err)
	}
	f.id.TaskID = ""
	if got, _ := s.Call(ctx, pane, "task_list", map[string]any{"status": "todo"}); !reflect.DeepEqual(ids(got), []string{"t2", "t3"}) {
		t.Fatalf("task_list pin to the pane's project, todo: %v", ids(got))
	}
	if got, _ := s.Call(ctx, pane, "task_list", map[string]any{"scope": "all"}); len(ids(got)) != 4 {
		t.Fatalf("task_list all: %v", ids(got))
	}
	f.id.TaskID = "t1"
	f.runs = nil
	if _, err := s.Call(ctx, pane, "task_done", map[string]any{"note": "shipped"}); err != nil {
		t.Fatal(err)
	}
	want := []string{`task.done {"id":"t1"}`, `note.new {"task":"t1","text":"shipped"}`}
	if !reflect.DeepEqual(f.runs, want) {
		t.Fatalf("task_done runs %v, want %v", f.runs, want)
	}
	if _, err := s.Call(ctx, pane, "task_status", map[string]any{"status": "done"}); err == nil || !strings.Contains(err.Error(), "summary is required") {
		t.Fatalf("done without summary: %v", err)
	}
	_, err = s.Call(ctx, pane, "task_status", map[string]any{"status": "blocked", "expected_version": 4})
	var e *Error
	if !asError(err, &e) || e.Code != "version_conflict" {
		t.Fatalf("stale version: %v", err)
	}
	f.runs = nil
	if _, err := s.Call(ctx, pane, "task_status", map[string]any{"status": "blocked", "expected_version": 5, "blocker": "waiting on review"}); err != nil {
		t.Fatal(err)
	}
	want = []string{`task.list {"all":true}`, `task.status {"id":"t1","status":"blocked"}`, `note.new {"task":"t1","text":"status: active → blocked\nblocker: waiting on review"}`}
	if !reflect.DeepEqual(f.runs, want) {
		t.Fatalf("task_status runs %v, want %v", f.runs, want)
	}
	f.runs = nil
	if _, err := s.Call(ctx, pane, "task_note_add", map[string]any{"text": "p note", "owner": map[string]any{"kind": "project", "id": "p9"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(ctx, pane, "task_note_add", map[string]any{"text": "t note"}); err != nil {
		t.Fatal(err)
	}
	want = []string{`note.new {"project":"p9","text":"p note"}`, `note.new {"task":"t1","text":"t note"}`}
	if !reflect.DeepEqual(f.runs, want) {
		t.Fatalf("task_note_add runs %v, want %v", f.runs, want)
	}
}

func TestContextDocumentAndProgressRunAsThePane(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(doc, []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeBackend{id: Identity{TaskID: "t1", Cwd: dir}, reply: map[string]string{
		"context-item.add": `{"found":1,"new":1,"items":[{"id":"c1","kind":"file"}]}`,
		"note.new":         `{"id":"n1"}`,
	}}
	s := newServer(t, f)
	ctx := context.Background()
	pane := Identity{Session: "work", Window: "w1"}
	if _, err := s.Call(ctx, pane, "context_add", map[string]any{"text": "https://example.com/x"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Call(ctx, pane, "document_report", map[string]any{"path": "plan.md", "kind": "plan", "summary": "the plan"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(doc)
	if m := got.(map[string]any); m["path"] != resolved || m["note_id"] != "n1" || m["context_item_id"] != "c1" {
		t.Fatalf("document_report: %v", m)
	}
	if _, err := s.Call(ctx, pane, "document_report", map[string]any{"path": "plan.txt"}); err == nil {
		t.Fatal("a .txt document was accepted")
	}
	if _, err := s.Call(ctx, pane, "report_progress", map[string]any{"message": "running tests"}); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(dir)
	file, _ := json.Marshal(resolved)
	want := []string{
		`context-item.add {"cwd":` + string(cwd) + `,"session":"work","text":"https://example.com/x","window":"w1"}`,
		`context-item.add {"cwd":` + string(cwd) + `,"kind":"file","session":"work","text":` + string(file) + `,"window":"w1"}`,
		`note.new {"task":"t1","text":` + jsonString("plan: plan.md\n"+resolved+"\n\nthe plan") + `}`,
		`pane.progress {"message":"running tests","session":"work","window":"w1"}`,
	}
	if !reflect.DeepEqual(f.runs, want) {
		t.Fatalf("runs:\n%s\nwant:\n%s", strings.Join(f.runs, "\n"), strings.Join(want, "\n"))
	}
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestConfigEntries(t *testing.T) {
	cmd := Command{Executable: "/bin/mtm", Args: []string{"--data", "/d/mtm.db", "--server", "mtm-daemon", "mcp", "serve"}}
	env := func(string) string { return "" }
	claude, err := Config(cmd, "claude", "/home/u", env)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(claude)
	want := `{"harness":"claude","file":"/home/u/.claude.json","args":["--mcp-config","{\"mcpServers\":{\"mtm\":{\"args\":[\"--data\",\"/d/mtm.db\",\"--server\",\"mtm-daemon\",\"mcp\",\"serve\"],\"command\":\"/bin/mtm\",\"type\":\"stdio\"}}}"],"entry":{"mcpServers":{"mtm":{"args":["--data","/d/mtm.db","--server","mtm-daemon","mcp","serve"],"command":"/bin/mtm","type":"stdio"}}}}`
	if string(raw) != want {
		t.Fatalf("claude config:\n%s\nwant\n%s", raw, want)
	}
	codex, _ := Config(cmd, "codex", "/home/u", env)
	if codex.File != "/home/u/.codex/config.toml" || codex.Args[1] != `mcp_servers.mtm.command="/bin/mtm"` {
		t.Fatalf("codex config: %+v", codex)
	}
	if omp, _ := Config(cmd, "omp", "/home/u", env); omp.File != "/home/u/.omp/agent/mcp.json" {
		t.Fatalf("omp config: %+v", omp)
	}
	if oc, _ := Config(cmd, "opencode", "/home/u", env); oc.File != "/home/u/.config/opencode/opencode.json" {
		t.Fatalf("opencode config: %+v", oc)
	}
	if _, err := Config(cmd, "vim", "/home/u", env); err == nil {
		t.Fatal("an unknown harness was configured")
	}
}
