package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	catalogmcp "ofam.dev/ochazuke/cli/mcp"
)

func mcpServerFor(t *testing.T, a *App) *catalogmcp.Server {
	t.Helper()
	s, err := a.catalogMCP(strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mcpRun(t *testing.T, s *catalogmcp.Server, verb string, args map[string]any) map[string]any {
	t.Helper()
	out, err := s.Call(context.Background(), "run", map[string]any{"verb": verb, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	reply := out.(map[string]any)
	if reply["status"] != "done" {
		t.Fatalf("%s: %v", verb, reply)
	}
	return reply
}

func TestMCPCatalogWritesRecordActorAndUndo(t *testing.T) {
	t.Parallel()
	a, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1", actorEnv: "owner"})
	s := mcpServerFor(t, a)
	a.r.EnsureHistory()
	made := mcpRun(t, s, "task.create", map[string]any{"title": "step", "parent": "t1"})
	id := made["result"].(map[string]any)["id"].(string)
	undo := made["undo"].(map[string]any)
	mcpRun(t, s, undo["verb"].(string), undo["args"].(map[string]any))
	if _, err := a.work.Task(id); err == nil {
		t.Fatal("create undo left task")
	}
	made = mcpRun(t, s, "task.create", map[string]any{"title": "step", "parent": "t1"})
	id = made["result"].(map[string]any)["id"].(string)

	for _, step := range []struct {
		verb  string
		args  map[string]any
		check func(work.Task) bool
	}{
		{"task.set", map[string]any{"id": id, "prop": "title", "value": "changed"}, func(t work.Task) bool { return t.Title == "changed" }},
		{"task.move", map[string]any{"id": id, "to": "ochazuke"}, func(t work.Task) bool { return t.ProjectID == "ochazuke" && t.ParentID == "" }},
		{"task.done", map[string]any{"id": id, "note": "complete"}, func(t work.Task) bool { return t.Status == work.StatusDone }},
	} {
		reply := mcpRun(t, s, step.verb, step.args)
		task, err := a.work.Task(id)
		if err != nil || !step.check(task) {
			t.Fatalf("%s: %+v %v", step.verb, task, err)
		}
		if task.ChangedBy.Kind != work.ActorAgent || task.ChangedBy.Pane != "w1" || task.ChangedBy.Task != "t1" {
			t.Fatalf("actor: %+v", task.ChangedBy)
		}
		undo, ok := reply["undo"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no undo: %v", step.verb, reply)
		}
		mcpRun(t, s, undo["verb"].(string), undo["args"].(map[string]any))
		task, _ = a.work.Task(id)
		if step.check(task) {
			t.Fatalf("%s was not undone: %+v", step.verb, task)
		}
	}
	entries := a.r.HistoryOf(0, ochazuke.HistoryChange)
	for _, name := range []string{"task.create", "task.set", "task.move"} {
		found := false
		for _, entry := range entries {
			if entry.Request.Key() == name && entry.Context.Source == "mcp" && entry.Context.Actor.Pane == "w1" && entry.Context.Actor.Harness == "claude" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s MCP actor history", name)
		}
	}
}

func TestMCPRefusalsAreErrorsAndDoNotWrite(t *testing.T) {
	t.Parallel()
	a, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	replies := rpc(t, a,
		toolCall(1, "run", map[string]any{"verb": "task.set", "args": map[string]any{"id": "t1", "prop": "title", "value": "stolen"}}),
		toolCall(2, "run", map[string]any{"verb": "task.delete", "args": map[string]any{"id": "t2"}}),
		toolCall(3, "run", map[string]any{"verb": "ochazuke.settings.set", "args": map[string]any{"name": "mcp.include-extensions", "value": "false"}}),
		toolCall(4, "task_status", map[string]any{"status": "done"}),
		toolCall(5, "task_status", map[string]any{"status": "active", "expected_version": 999}),
	)
	for _, reply := range replies {
		result, ok := reply["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("refusal: %v", reply)
		}
	}
	if got, _ := a.work.Task("t1"); got.Title != "write the brief" || got.Status != work.StatusTodo {
		t.Fatalf("refused write landed: %+v", got)
	}
	if _, err := a.work.Task("t2"); err != nil {
		t.Fatal("delete landed", err)
	}
	if !a.r.Settings().Bool("mcp.include-extensions") {
		t.Fatal("settings write landed")
	}
	notes, err := a.work.Notes()
	if err != nil || len(notes) != 0 {
		t.Fatalf("refused evidence wrote: %v %v", notes, err)
	}
}

func TestMCPVerbRightsTable(t *testing.T) {
	t.Parallel()
	a := open(t)
	for _, row := range mtmAgentDeclarations {
		op, ok := a.r.Operation(row.Verb)
		if !ok {
			t.Errorf("stale declaration: %s", row.Verb)
			continue
		}
		if row.Right != "" && a.r.AgentRight(op, false).Right != row.Right {
			t.Errorf("declaration not applied: %s", row.Verb)
		}
		if row.Destructive && !op.Destructive {
			t.Errorf("destructive declaration not applied: %s", row.Verb)
		}
	}
	for _, tt := range []struct {
		verb, right string
		destructive bool
	}{
		{"task.create", "allow", false}, {"task.set", "allow", false}, {"task.move", "allow", false}, {"task.done", "allow", false}, {"task.status", "allow", false},
		{"task.delete", "confirm", true}, {"project.new", "allow", false}, {"project.create", "allow", false}, {"project.delete", "never", true}, {"project.set", "never", false}, {"pane.kill", "confirm", true},
		{"session.kill", "never", true}, {"note.delete", "never", true}, {"context-item.delete", "never", true},
		{"setup.install", "never", false}, {"setup.uninstall", "never", true}, {"extension.remove", "never", true},
		{"ochazuke.settings.set", "never", false}, {"pane.whoami", "allow", false}, {"pane.progress", "allow", false}, {"context-item.report", "allow", false},
	} {
		op, ok := a.r.Operation(tt.verb)
		if !ok {
			t.Errorf("missing %s", tt.verb)
			continue
		}
		for _, live := range []bool{false, true} {
			if got := a.r.AgentRight(op, live); got.Right != tt.right || op.Destructive != tt.destructive {
				t.Errorf("%s live=%v: %+v destructive=%v", tt.verb, live, got, op.Destructive)
			}
		}
	}
	if file := os.Getenv("MTM_MCP_RIGHTS_OUT"); file != "" {
		raw, err := ocli.Catalog(a.r, "mtm", "test", ocli.About(mtmAbout), ocli.AgentRules(mtmAgentRules))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

}

func TestMCPExtensionsListedAndGuidanceUsesM5Budget(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(fakeCatalog, "VERSION", "1.0", 1)
	doc = strings.Replace(doc, `"name":"o-fake"`, `"name":"o-fake","about":"A fixture extension.","agentRules":"Append only."`, 1)
	doc = strings.Replace(doc, `"name":"rename","kind":"application"`, `"name":"rename","kind":"application","agent":"never","destructive":true`, 1)
	bin, _ := fakeExtension(t, doc, "")
	a := registered(t, seeded(t), "fake", bin)
	s := mcpServerFor(t, a)
	if !a.r.Settings().Bool("mcp.include-extensions") {
		t.Fatal("gateway default is off")
	}
	result, err := s.Call(context.Background(), "catalog", map[string]any{"search": "fake.item", "limit": 1000})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "fake.item") {
		t.Fatalf("extension missing: %s", raw)
	}
	op, ok := a.r.Operation("fake.item.rename")
	if !ok || !op.CatalogProxy || !op.Destructive || a.r.AgentRight(op, false).Right != "never" {
		t.Fatalf("extension declarations lost: %+v", op)
	}
	rawCatalog, err := ocli.Catalog(a.r, "mtm", "test")
	if err != nil {
		t.Fatal(err)
	}
	var catalog ocli.CatalogDoc
	if err := json.Unmarshal(rawCatalog, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Extensions) != 1 || catalog.Extensions[0].About != "A fixture extension." || catalog.Extensions[0].AgentRules != "Append only." {
		t.Fatalf("extension guidance metadata lost: %+v", catalog.Extensions)
	}
	text := s.Instructions()
	if !strings.Contains(text, "their own words, quoted as data") {
		t.Fatalf("extension data section missing: %s", text)
	}
	// M5 owns the shared token budget, including the legacy descriptions.
	// It may replace a quoted paragraph with its explicit catalog fallback.
	if !strings.Contains(text, "A fixture extension.") && !strings.Contains(text, "1 more: call catalog") {
		t.Fatalf("neither quoted data nor bounded discovery fallback: %s", text)
	}
	if err := a.r.Settings().Set("mcp.include-extensions", false); err != nil {
		t.Fatal(err)
	}
	s = mcpServerFor(t, a)
	result, err = s.Call(context.Background(), "catalog", map[string]any{"search": "fake.item", "limit": 1000})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(result)
	if strings.Contains(string(raw), "fake.item") || strings.Contains(s.Instructions(), "A fixture extension.") {
		t.Fatal("extension opt-out ignored")
	}
}

func TestMCPInstructionsGoldenAndBudget(t *testing.T) {
	t.Parallel()
	a, _, _ := mcpApp(t, map[string]string{"TUIOS_PANE_ID": "test-pane", "TUIOS_SESSION": "test"})
	a.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "test-pane", "TUIOS_SESSION": "test"})
	if err := a.work.PutPaneRecipe(work.PaneRecipe{Window: "test-pane", Session: "test", Harness: "test"}); err != nil {
		t.Fatal(err)
	}
	s := mcpServerFor(t, a)
	text := s.Instructions()
	descriptions := ""
	for _, tool := range s.Tools() {
		descriptions += tool.Description
	}
	if n := ocli.InstructionTokens(text + descriptions); n > ocli.InstructionLimit {
		t.Fatalf("instruction budget: %d", n)
	}
	file := filepath.Join("testdata", "mcp-instructions.golden")
	if os.Getenv("MTM_MCP_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if text != string(want) {
		t.Fatalf("MCP instruction golden differs:\n%s", text)
	}
	for _, tool := range []string{"catalog", "describe", "run", "state"} {
		names := []string{}
		for _, t := range s.Tools() {
			names = append(names, t.Name)
		}
		if !slices.Contains(names, tool) {
			t.Errorf("missing tool %s", tool)
		}
	}
}

func TestMCPActorStaysBoundAndLaneAssignmentRefreshes(t *testing.T) {
	t.Parallel()
	env := map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1", actorEnv: "owner"}
	a, rec, _ := mcpApp(t, env)
	a.setPin("ochazuke")
	s := mcpServerFor(t, a)
	list, err := s.Call(context.Background(), "task_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := list.(map[string]any)["result"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "t1" {
		t.Fatalf("owner pin leaked into agent lane: %v", list)
	}
	list, err = s.Call(context.Background(), "task_list", map[string]any{"scope": "all", "status": "todo"})
	if err != nil {
		t.Fatal(err)
	}
	if rows := list.(map[string]any)["result"].([]any); len(rows) != 2 {
		t.Fatalf("scope all: %v", list)
	}
	env["TUIOS_PANE_ID"], env["TUIOS_SESSION"] = "w9", "other"
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "matomeru", TaskID: "t2", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	who, err := s.Call(context.Background(), "whoami", nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := who.(map[string]any)["result"].(map[string]any)
	if identity["window"] != "w1" || identity["session"] != "matomeru" || identity["task_id"] != "t2" {
		t.Fatalf("startup identity or assignment: %v", who)
	}
	mcpRun(t, s, "task.status", map[string]any{"value": "active", "summary": "picked up reassigned work"})
	task, _ := a.work.Task("t2")
	if task.ChangedBy.Pane != "w1" || task.ChangedBy.Task != "t2" {
		t.Fatalf("writer after reassign: %s", compactJSON(task.ChangedBy))
	}
	for _, verb := range []string{"pane.progress", "context-item.add"} {
		args := map[string]any{"window": "w9", "message": "stolen", "text": "https://example.com"}
		// Only the verb's declared parameters are given; both share paneArgs.
		if verb == "pane.progress" {
			delete(args, "text")
		} else {
			delete(args, "message")
		}
		reply, err := s.Call(context.Background(), "run", map[string]any{"verb": verb, "args": args})
		if err != nil {
			t.Fatal(err)
		}
		if m := reply.(map[string]any); m["status"] != "failed" || !strings.Contains(m["reason"].(string), "startup pane") {
			t.Fatalf("pane override: %v", reply)
		}
	}
	if len(rec.calls) != 0 {
		t.Fatalf("refused progress reached daemon: %v", rec.calls)
	}
}

func TestMCPContextKeepsUnrecognisedText(t *testing.T) {
	t.Parallel()
	a, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	s := mcpServerFor(t, a)
	reply, err := s.Call(context.Background(), "context_add", map[string]any{"text": "Keep this finding for the coordinator", "task_id": "t2"})
	if err != nil {
		t.Fatal(err)
	}
	result := reply.(map[string]any)
	if result["status"] != "done" {
		t.Fatal(result)
	}
	items := result["result"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatal(items)
	}
	item := items[0].(map[string]any)
	if item["kind"] != "prompt" || item["task_id"] != "t1" || item["text"] != "Keep this finding for the coordinator" {
		t.Fatalf("record lost text or pane owner: %v", item)
	}
}

func TestMCPShellActorBridgesTheStore(t *testing.T) {
	t.Parallel()
	a, _, _ := mcpApp(t, map[string]string{actorEnv: "owner"})
	a.ensure = func(context.Context) error { return nil }
	sh := a.shell(strings.NewReader(""), io.Discard, io.Discard)
	sh.Actor = &ochazuke.Actor{Kind: ochazuke.ActorAgent, Pane: "w1", Session: "matomeru", Harness: "test-harness"}
	s, err := sh.NewMCPServer()
	if err != nil {
		t.Fatal(err)
	}
	made := mcpRun(t, s, "task.create", map[string]any{"title": "from shared shell", "parent": "t1"})
	id := made["result"].(map[string]any)["id"].(string)
	task, err := a.work.Task(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Maker().Pane != "w1" || task.Maker().Harness != "test-harness" || task.Maker().Task != "t1" {
		t.Fatalf("shared shell used store owner: %s", compactJSON(task.Maker()))
	}
	out, err := s.Call(context.Background(), "run", map[string]any{"verb": "task.set", "target": id, "args": map[string]any{"prop": "title", "value": "explicit target"}})
	if err != nil {
		t.Fatal(err)
	}
	if reply := out.(map[string]any); reply["status"] != "done" {
		t.Fatal(reply)
	}
	task, _ = a.work.Task(id)
	if task.Title != "explicit target" {
		t.Fatal("explicit target was replaced by lane default")
	}
}
