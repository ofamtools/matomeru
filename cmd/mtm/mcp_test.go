package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

// stateRecorder is the fake daemon's set-agent-state: the pane daemon of
// every app a tool call opens.
type stateRecorder struct {
	*fakePanes
	mu    sync.Mutex
	calls []map[string]any
}

func (s *stateRecorder) Call(_ context.Context, verb string, params, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := map[string]any{"verb": verb}
	for k, v := range params.(map[string]any) {
		call[k] = v
	}
	s.calls = append(s.calls, call)
	if reply, ok := out.(*map[string]any); ok {
		*reply = map[string]any{"applied": true}
	}
	return nil
}

// mcpApp is the serve app on a seeded database, the pane w1 of session
// matomeru assigned to task t1 and launched from a recipe; env is the
// pane's environment, and every per-call app shares the fake daemon.
func mcpApp(t *testing.T, env map[string]string) (*App, *stateRecorder, string) {
	t.Helper()
	path := seeded(t)
	cwd := t.TempDir()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetPaneOwner(work.Assignment{Window: "w1", Session: "matomeru", ProjectID: "matomeru", TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.PutPaneRecipe(work.PaneRecipe{Window: "w1", Session: "matomeru", Cwd: cwd, Harness: "claude", Model: "opus", TaskID: "t1", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	// t1, the pane's lane task, stays the owner's: task_status may change
	// its status (agents.owner-task-props). t2, outside the lane, is the
	// pane's own (the agent in w1 made it), so task_done may change it;
	// actor_test.go covers the refusals.
	for _, id := range []string{"t2"} {
		task, err := w.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		task.CreatedBy = work.Actor{Kind: work.ActorAgent, Harness: "claude", Pane: "w1", Session: "matomeru"}
		if err := w.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	app := openWith(t, Options{Data: path, Socket: filepath.Join(t.TempDir(), "none.sock")})
	getenv := func(k string) string { return env[k] }
	app.getenv = getenv
	rec := &stateRecorder{fakePanes: newFakePanes()}
	previous := mcpPrepare
	mcpPrepare = func(a *App) {
		a.panesd, a.renamer = rec, &fakeRenamer{panes: rec.fakePanes}
		a.ensure = func(context.Context) error { return nil }
		a.sleep = func(context.Context, time.Duration) error { return nil }
		a.getenv, a.journalSrc, a.pv.after = getenv, nil, nil
		a.home = func() (string, error) { return "/home/test", nil }
	}
	t.Cleanup(func() { mcpPrepare = previous })
	return app, rec, cwd
}

// rpc speaks JSON-RPC to mtm mcp serve over pipes: one request per line
// in, one response per request with an id out.
func rpc(t *testing.T, app *App, requests ...any) []map[string]any {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var errOut bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- app.commands([]string{"mcp", "serve"}, inR, outW, &errOut, func() error { return nil }).Run(context.Background())
		outW.Close()
	}()
	go func() {
		for _, r := range requests {
			raw, _ := json.Marshal(r)
			inW.Write(append(raw, '\n'))
		}
		inW.Close()
	}()
	var out []map[string]any
	sc := bufio.NewScanner(outR)
	sc.Buffer(nil, 4<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("stdout carries a non-JSON-RPC line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	if err := <-done; err != nil {
		t.Fatalf("serve: %v\n%s", err, errOut.String())
	}
	return out
}

func toolCall(id int, name string, args map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}
}

// structured is a successful tool result's value.
func structured(t *testing.T, resp map[string]any) any {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok || res["isError"] != false {
		t.Fatalf("tool call failed: %v", resp)
	}
	sc := res["structuredContent"].(map[string]any)
	if v, ok := sc["result"]; ok && len(sc) == 1 {
		return v
	}
	return sc
}

func TestMCPServeRunsEveryToolAgainstTheDatabase(t *testing.T) {
	app, rec, cwd := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	doc := filepath.Join(cwd, "report.md")
	if err := os.WriteFile(doc, []byte("# done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := rpc(t, app,
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}},
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
		toolCall(3, "whoami", nil),
		toolCall(4, "task_list", nil),
		toolCall(5, "task_note_add", map[string]any{"text": "first finding"}),
		toolCall(6, "task_status", map[string]any{"status": "active", "verification": "go test"}),
		toolCall(7, "context_add", map[string]any{"text": "see https://github.com/unixsuperhero/matomeru/pull/7"}),
		toolCall(8, "document_report", map[string]any{"path": "report.md", "title": "Report", "summary": "all green"}),
		toolCall(9, "report_progress", map[string]any{"message": "running the gate"}),
		toolCall(10, "task_done", map[string]any{"task_id": "t2", "note": "lifted"}),
		toolCall(11, "frob", nil),
		toolCall(12, "task_status", map[string]any{"status": "finished"}),
	)
	if len(out) != 12 {
		t.Fatalf("want 12 responses, got %d: %v", len(out), out)
	}
	if v := out[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-06-18" {
		t.Fatalf("initialize: %v", out[0])
	}
	var names []string
	for _, tool := range out[1]["result"].(map[string]any)["tools"].([]any) {
		m := tool.(map[string]any)
		names = append(names, m["name"].(string))
		if m["name"] == "task_status" {
			props := m["inputSchema"].(map[string]any)["properties"].(map[string]any)
			if !reflect.DeepEqual(m["inputSchema"].(map[string]any)["required"], []any{"status"}) || props["expected_version"].(map[string]any)["type"] != "integer" {
				t.Fatalf("task_status schema: %v", m["inputSchema"])
			}
		}
	}
	if want := []string{"context_add", "document_report", "report_progress", "task_done", "task_list", "task_note_add", "task_status", "whoami"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("tools %v, want %v", names, want)
	}
	who := structured(t, out[2]).(map[string]any)
	for k, want := range map[string]any{"session": "matomeru", "window": "w1", "task_id": "t1", "project_id": "matomeru", "cwd": cwd, "harness": "claude", "model": "opus", "task_title": "write the brief", "task_status": "todo", "project_name": "matomeru", "bound": true} {
		if who[k] != want {
			t.Errorf("whoami %s = %v, want %v", k, who[k], want)
		}
	}
	var listed []string
	for _, row := range structured(t, out[3]).([]any) {
		listed = append(listed, row.(map[string]any)["id"].(string))
	}
	if !reflect.DeepEqual(listed, []string{"t1"}) {
		t.Fatalf("task_list pins to the pane's task: %v", listed)
	}
	if note := structured(t, out[4]).(map[string]any); note["task_id"] != "t1" || note["text"] != "first finding" {
		t.Fatalf("task_note_add: %v", note)
	}
	note := structured(t, out[4]).(map[string]any)
	recorded, err := app.work.Note(note["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	wantMaker := work.Actor{Kind: work.ActorAgent, Harness: "claude", Pane: "w1", Session: "matomeru", Task: "t1", Model: "opus"}
	if recorded.Maker() != wantMaker {
		t.Fatalf("task_note_add maker: %+v", recorded.Maker())
	}

	if task := structured(t, out[5]).(map[string]any); task["id"] != "t1" || task["status"] != "active" {
		t.Fatalf("task_status: %v", task)
	}
	items := structured(t, out[6]).([]any)
	if len(items) != 1 || items[0].(map[string]any)["kind"] != "pr" || items[0].(map[string]any)["task_id"] != "t1" {
		t.Fatalf("context_add: %v", items)
	}
	resolved, _ := filepath.EvalSymlinks(doc)
	if rep := structured(t, out[7]).(map[string]any); rep["path"] != resolved || rep["note_id"] == "" || rep["context_item_id"] == "" {
		t.Fatalf("document_report: %v", rep)
	}
	if prog := structured(t, out[8]).(map[string]any); prog["window"] != "w1" || prog["state"] != "working" {
		t.Fatalf("report_progress: %v", prog)
	}
	if task := structured(t, out[9]).(map[string]any); task["id"] != "t2" || task["status"] != "done" {
		t.Fatalf("task_done: %v", task)
	}
	for _, resp := range out[10:] {
		if e, ok := resp["error"].(map[string]any); !ok || e["code"] != float64(-32602) {
			t.Fatalf("want a -32602 error: %v", resp)
		}
	}
	if want := []map[string]any{{"verb": "set-agent-state", "session": "matomeru", "window": "w1", "state": "working", "message": "running the gate", "source": "report"}}; !reflect.DeepEqual(rec.calls, want) {
		t.Fatalf("daemon calls %v, want %v", rec.calls, want)
	}

	// What the tools wrote is in the database, as mtm's commands write it.
	db, err := store.OpenReadOnly(app.dataPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for id, status := range map[string]string{"t1": "active", "t2": "done"} {
		if task, err := w.Task(id); err != nil || task.Status != status {
			t.Fatalf("task %s: %+v %v, want %s", id, task, err, status)
		}
	}
	notes, err := w.Notes()
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, n := range notes {
		texts = append(texts, n.TaskID+": "+n.Text)
	}
	slices.Sort(texts)
	want := []string{"t1: first finding", "t1: report: Report\n" + resolved + "\n\nall green", "t1: status: todo → active\nverification: go test", "t2: lifted"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("notes:\n%q\nwant\n%q", texts, want)
	}
	cx, err := contextitems.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	all, err := cx.All()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range all {
		kinds = append(kinds, it.Kind+" "+it.TaskID+" "+it.Text)
	}
	slices.Sort(kinds)
	if want := []string{"file t1 " + resolved, "pr t1 https://github.com/unixsuperhero/matomeru/pull/7"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("context items %q, want %q", kinds, want)
	}
}

func TestMCPWhoamiReadsTheTUIOSEnvironment(t *testing.T) {
	// The session owns the project; the pane has no assignment of its own.
	app, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "work", "TUIOS_PANE_ID": "w7"})
	if err := app.work.SetSessionOwner(work.SessionOwner{ID: "sid-1", Name: "work", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := app.commands([]string{"mcp", "whoami"}, strings.NewReader(""), &out, &errOut, nil).Run(context.Background()); err != nil {
		t.Fatalf("mcp whoami: %v\n%s", err, errOut.String())
	}
	var who map[string]any
	if err := json.Unmarshal(out.Bytes(), &who); err != nil {
		t.Fatal(err)
	}
	if who["session"] != "work" || who["window"] != "w7" || who["project_id"] != "ochazuke" || who["project_name"] != "ochazuke" || who["task_id"] != "" || who["bound"] != true {
		t.Fatalf("whoami from the session owner: %v", who)
	}

	// No TUIOS pane: whoami says so and pane tools refuse.
	app.getenv = func(string) string { return "" }
	resp := rpc(t, app, toolCall(1, "whoami", nil), toolCall(2, "report_progress", map[string]any{"message": "x"}))
	if who := structured(t, resp[0]).(map[string]any); who["bound"] != false {
		t.Fatalf("whoami outside a pane: %v", who)
	}
	if res := resp[1]["result"].(map[string]any); res["isError"] != true {
		t.Fatalf("report_progress outside a pane: %v", resp[1])
	}
}

func TestMCPConfigPointsAtThisBinary(t *testing.T) {
	app, _, _ := mcpApp(t, nil)
	var out, errOut bytes.Buffer
	if err := app.commands([]string{"mcp", "config", "claude"}, strings.NewReader(""), &out, &errOut, nil).Run(context.Background()); err != nil {
		t.Fatalf("mcp config claude: %v\n%s", err, errOut.String())
	}
	var config struct {
		Harness string   `json:"harness"`
		File    string   `json:"file"`
		Args    []string `json:"args"`
		Entry   struct {
			MCPServers map[string]struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(out.Bytes(), &config); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	entry := config.Entry.MCPServers["mtm"]
	data, _ := filepath.Abs(app.dataPath)
	if config.Harness != "claude" || config.File != "/home/test/.claude.json" || entry.Type != "stdio" || entry.Command != exe ||
		!reflect.DeepEqual(entry.Args, []string{"--data", data, "--socket", app.opts.Socket, "mcp", "serve"}) {
		t.Fatalf("claude config:\n%s", out.String())
	}
	if len(config.Args) != 2 || config.Args[0] != "--mcp-config" || !strings.Contains(config.Args[1], `"mcpServers"`) {
		t.Fatalf("claude args: %v", config.Args)
	}
	out.Reset()
	if err := app.commands([]string{"mcp", "config"}, strings.NewReader(""), &out, &errOut, nil).Run(context.Background()); err != nil || !strings.Contains(out.String(), `"harnesses"`) {
		t.Fatalf("mcp config: %v\n%s", err, out.String())
	}
	if err := app.commands([]string{"mcp", "config", "vim"}, strings.NewReader(""), &out, &errOut, nil).Run(context.Background()); err == nil {
		t.Fatal("an unknown harness was configured")
	}
}

func TestMCPConfigNamesTheServerWithoutASocket(t *testing.T) {
	app := openWith(t, Options{Data: seeded(t), Server: "mtm-daemon"})
	cmd := app.mcpServerCommand(io.Discard)
	if !slices.Equal(cmd.Args[2:], []string{"--server", "mtm-daemon", "mcp", "serve"}) {
		t.Fatalf("args: %v", cmd.Args)
	}
}

func TestContextItemAddKeepsAKindOrTheWholeText(t *testing.T) {
	app, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	out := mustCLI(t, app, "context-item", "add", "--text", "make gate", "--kind", "command")
	var got extracted
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.New != 1 || got.Items[0].Kind != "command" || got.Items[0].Text != "make gate" || got.Items[0].TaskID != "t1" {
		t.Fatalf("command item: %+v", got)
	}
	out = mustCLI(t, app, "context-item", "add", "--text", "nothing to see", "--window", "w9")
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Found != 0 || got.Items == nil {
		t.Fatalf("no items: %s %v", out, err)
	}
}
