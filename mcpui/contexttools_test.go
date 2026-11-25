package mcpui

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestContextToolSpecsMatchRealParams(t *testing.T) {
	args := map[string]any{
		ToolContextOfficial:    ContextOfficialArgs{},
		ToolContextProject:     ContextProjectArgs{},
		ToolContextAddOfficial: ContextAddOfficialArgs{},
	}
	specs := NewContextTools(nil).ToolSpecs()
	if len(specs) != len(args) {
		t.Fatalf("%d context tools, want %d", len(specs), len(args))
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

func TestContextToolsAndTaskDispatchIntegration(t *testing.T) {
	f := setup(t)
	childTask, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":      "Child Work",
			"parent":     f.task.Ref(),
			"defaultDir": "/repo/matomeru",
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetTaskAgentFlag(childTask.Ref(), true, data.AgentStatusQueued, "", owner); err != nil {
		t.Fatal(err)
	}

	box := NewToolbox(f.store, f.r)
	box.Getenv = func(k string) string {
		switch k {
		case EnvPaneID:
			return f.pane.ID
		case EnvTaskID:
			return childTask.ID
		default:
			return ""
		}
	}

	call := func(ctx context.Context, name string, args any) map[string]any {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		res, err := box.Call(ctx, agent, name, raw)
		if err != nil {
			t.Fatalf("%s(%s): %v", name, raw, err)
		}
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// 1. context.add_official resolves root project via $MTM_TASK_ID (childTask -> f.task).
	added := call(context.Background(), ToolContextAddOfficial, map[string]any{
		"value": "https://docs.google.com/document/d/spec1/edit",
		"title": "Architecture PRD",
		"notes": "Read before editing schema.",
	})
	if added["projectId"] != f.task.ID {
		t.Fatalf("projectId = %v, want %s", added["projectId"], f.task.ID)
	}
	ctxObj := added["context"].(map[string]any)
	ctxID := ctxObj["id"].(string)
	ctxFields := ctxObj["fields"].(map[string]any)
	if ctxFields["official"] != true || ctxFields["pinned"] != true || ctxFields["contextKind"] != "gdoc" || ctxFields["source"] != "curated" {
		t.Fatalf("added context fields = %v", ctxFields)
	}

	// 2. data.add_context accepts gdoc/sheet/curated too.
	addedSheet := call(context.Background(), ToolAddContext, map[string]any{
		"value":       "https://docs.google.com/spreadsheets/d/kpi1/edit",
		"title":       "KPI Sheet",
		"contextKind": "sheet",
		"source":      "curated",
		"task":        f.task.ID,
	})
	sheetID := addedSheet["context"].(map[string]any)["id"].(string)

	// 3. context.project resolves via $MTM_PANE_ID when $MTM_TASK_ID is empty.
	boxPaneOnly := NewToolbox(f.store, f.r)
	boxPaneOnly.Getenv = func(k string) string {
		if k == EnvPaneID {
			return f.pane.ID
		}
		return ""
	}
	rawProj, err := boxPaneOnly.Call(context.Background(), agent, ToolContextProject, json.RawMessage(`{"includeAll":true}`))
	if err != nil {
		t.Fatalf("context.project via pane env: %v", err)
	}
	projRes := rawProj.(*ContextProjectResult)
	if projRes.ProjectID != f.task.ID || len(projRes.Official) != 1 || len(projRes.All) != 2 || len(projRes.Worktrees) != 1 {
		t.Fatalf("context.project result = %+v", projRes)
	}
	if !strings.Contains(projRes.Markdown, "## Official Project Context") || !strings.Contains(projRes.Markdown, "Architecture PRD") {
		t.Fatalf("context.project markdown = %q", projRes.Markdown)
	}

	// 4. context.official promotes sheetID and demotes ctxID.
	promoted := call(context.Background(), ToolContextOfficial, map[string]any{
		"id":    sheetID,
		"notes": "Track rollout metrics.",
	})
	if promoted["context"].(map[string]any)["fields"].(map[string]any)["official"] != true {
		t.Fatalf("promoted sheet = %v", promoted)
	}
	demoted := call(context.Background(), ToolContextOfficial, map[string]any{
		"id":       ctxID,
		"official": false,
	})
	if demoted["context"].(map[string]any)["fields"].(map[string]any)["official"] != false {
		t.Fatalf("demoted ctx = %v", demoted)
	}

	// 5. task.dispatch auto-injects official context (KPI Sheet), supports opt-out and explicit contextIds override.
	dispDefault := call(context.Background(), ToolTaskDispatch, map[string]any{"project": f.task.ID})
	promptDefault := dispDefault["prompt"].(string)
	if !strings.Contains(promptDefault, "## Official Project Context") || !strings.Contains(promptDefault, "KPI Sheet") || strings.Contains(promptDefault, "Architecture PRD") {
		t.Fatalf("dispatch default prompt:\n%s", promptDefault)
	}

	dispOptOut := call(context.Background(), ToolTaskDispatch, map[string]any{
		"project":               f.task.ID,
		"includeProjectContext": false,
	})
	if strings.Contains(dispOptOut["prompt"].(string), "## Official Project Context") {
		t.Fatalf("opt-out prompt should not include official context:\n%s", dispOptOut["prompt"])
	}

	dispOverride := call(context.Background(), ToolTaskDispatch, map[string]any{
		"project":    f.task.ID,
		"contextIds": []string{ctxID},
	})
	promptOverride := dispOverride["prompt"].(string)
	if !strings.Contains(promptOverride, "Architecture PRD") || strings.Contains(promptOverride, "KPI Sheet") {
		t.Fatalf("override prompt:\n%s", promptOverride)
	}

	// task.queue supports includeProjectContext and contextIds.
	qOptOut := call(context.Background(), ToolTaskQueue, map[string]any{
		"project":               f.task.ID,
		"includeProjectContext": false,
	})
	qItem := qOptOut["items"].([]any)[0].(map[string]any)
	if qItem["projectContext"] != nil {
		t.Fatalf("expected omitted projectContext on opt-out, got %v", qItem["projectContext"])
	}

	// Verify context.official returns error on missing ID.
	if _, err := box.Call(context.Background(), core.Actor{Kind: "agent", ID: "test"}, ToolContextOfficial, json.RawMessage(`{"id":"missing"}`)); err == nil {
		t.Fatal("expected error for missing context ID")
	}
}
