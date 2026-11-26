package mcpui

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestTaskToolSpecsMatchRealParams(t *testing.T) {
	args := map[string]any{
		ToolTaskQueue:     TaskQueueArgs{},
		ToolTaskFlagAgent: TaskFlagAgentArgs{},
		ToolTaskNote:      TaskNoteArgs{},
		ToolTaskThread:    TaskThreadArgs{},
		ToolTaskDispatch:  TaskDispatchArgs{},
		ToolTaskRewrite:   taskRewriteArgs{},
		ToolTaskSplit:     taskSplitArgs{},
		ToolTaskJoin:      taskJoinArgs{},
	}
	specs := NewTaskTools(nil).ToolSpecs()
	if len(specs) != len(args) {
		t.Fatalf("%d task tools, want %d", len(specs), len(args))
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

func TestTaskToolsEndToEndViaToolbox(t *testing.T) {
	f := setup(t)
	box := NewToolbox(f.store, f.r)
	box.Getenv = func(k string) string {
		switch k {
		case EnvPaneID:
			return f.pane.ID
		case EnvTaskID:
			return f.task.ID
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

	// Create child tasks under f.task and another root project.
	t1, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       "Fix queue dispatch",
			"description": "Deliver queued tasks cleanly",
			"priority":    "P1",
			"parent":      f.task.Ref(),
			"attachments": data.EncodeAttachments([]data.Attachment{
				{Kind: "file", Path: "/tmp/spec.md", Label: "spec.md", Source: "picker"},
			}),
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	otherProj, _, err := f.store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Other Project"},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	t2, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":    "Unrelated P0 task",
			"priority": "P0",
			"parent":   otherProj.Ref(),
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	// Attach outgoing RelContext to t1.
	ctxRec, _, err := f.store.Create(data.CreateInput{
		Kind: data.KindContext,
		Fields: map[string]any{
			"title": "Design doc",
			"value": "https://example.com/design",
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.Relate(data.RelateInput{From: t1.Ref(), Kind: data.RelContext, To: ctxRec.Ref()}, owner); err != nil {
		t.Fatal(err)
	}

	// 1. task.flag_agent flags t1 (defaults forAgent=true, status=queued) and t2 (working).
	flagged := call(context.Background(), ToolTaskFlagAgent, map[string]any{
		"task":     t1.ID,
		"assignee": "claude",
	})
	tf := flagged["task"].(map[string]any)["fields"].(map[string]any)
	if tf["forAgent"] != true || tf["agentStatus"] != data.AgentStatusQueued || tf["agentAssignee"] != "claude" {
		t.Fatalf("flag_agent t1 fields = %v", tf)
	}

	// Alias agentStatus (as emitted by FormatAgentQueuePrompt) also works.
	flagged2 := call(context.Background(), ToolTaskFlagAgent, map[string]any{
		"task":        t2.ID,
		"agentStatus": data.AgentStatusWorking,
	})
	tf2 := flagged2["task"].(map[string]any)["fields"].(map[string]any)
	if tf2["forAgent"] != true || tf2["agentStatus"] != data.AgentStatusWorking {
		t.Fatalf("flag_agent t2 fields = %v", tf2)
	}

	// Env default task inheritance on task.flag_agent and task.note (pane + task).
	envFlag := call(context.Background(), ToolTaskFlagAgent, map[string]any{"status": data.AgentStatusQueued})
	if envFlag["task"].(map[string]any)["id"] != f.task.ID {
		t.Fatalf("envDefault task not inherited on flag_agent: %v", envFlag)
	}
	// Unflag f.task via status="none" (defaults forAgent=false).
	unflagged := call(context.Background(), ToolTaskFlagAgent, map[string]any{"task": f.task.ID, "status": data.AgentStatusNone})
	uf := unflagged["task"].(map[string]any)["fields"].(map[string]any)
	if uf["forAgent"] != false || uf["agentStatus"] != data.AgentStatusNone {
		t.Fatalf("unflag f.task fields = %v", uf)
	}

	// 2. task.note appends notes with attachments and auto-inherits pane from $MTM_PANE_ID.
	noteRes1 := call(context.Background(), ToolTaskNote, map[string]any{
		"task":        t1.ID,
		"body":        "Picked up the task and checking tests",
		"authorLabel": "worker-a2",
		"attachments": []data.Attachment{
			{Kind: "image", Path: "/tmp/shot.png", Label: "shot.png", Mime: "image/png", Source: "agent"},
			{Kind: "file", Path: "/tmp/spec.md", Label: "spec.md", Source: "picker"}, // duplicate of task attachment
		},
		"by": "worker-a2",
	})
	n1 := noteRes1["note"].(map[string]any)
	n1Fields := n1["fields"].(map[string]any)
	if n1Fields["authorKind"] != "agent" || n1Fields["authorLabel"] != "worker-a2" {
		t.Fatalf("note1 fields = %v", n1Fields)
	}
	paneField, _ := n1Fields["pane"].(map[string]any)
	if paneField["kind"] != data.KindPane || paneField["id"] != f.pane.ID {
		t.Fatalf("pane not inherited from envDefaults: %v", n1Fields["pane"])
	}

	// Second note with explicit context pane override and owner authorKind.
	ctxOverride := WithPaneID(context.Background(), "pane-override")
	noteRes2 := call(ctxOverride, ToolTaskNote, map[string]any{
		"task":       t1.ID,
		"title":      "Follow-up",
		"body":       "Looks great",
		"authorKind": "owner",
	})
	n2Fields := noteRes2["note"].(map[string]any)["fields"].(map[string]any)
	if p, _ := n2Fields["pane"].(map[string]any); p["id"] != "pane-override" {
		t.Fatalf("context pane override failed: %v", n2Fields["pane"])
	}

	// 3. task.queue filtering: default status="queued" returns only t1.
	qQueued := call(context.Background(), ToolTaskQueue, map[string]any{})
	if qQueued["count"] != float64(1) {
		t.Fatalf("queued count = %v, want 1", qQueued["count"])
	}
	item0 := qQueued["items"].([]any)[0].(map[string]any)
	if item0["task"].(map[string]any)["id"] != t1.ID {
		t.Fatalf("queued item = %v", item0["task"])
	}
	if len(item0["notes"].([]any)) != 2 || len(item0["context"].([]any)) != 1 || len(item0["attachments"].([]any)) != 2 {
		t.Fatalf("hydrated queue item mismatch: notes=%d ctx=%d att=%d",
			len(item0["notes"].([]any)), len(item0["context"].([]any)), len(item0["attachments"].([]any)))
	}

	// status="open" returns t2 (P0) before t1 (P1); project filter restricts to f.task subtree.
	qOpen := call(context.Background(), ToolTaskQueue, map[string]any{"status": "open"})
	if qOpen["count"] != float64(2) {
		t.Fatalf("open count = %v, want 2", qOpen["count"])
	}
	openItems := qOpen["items"].([]any)
	if openItems[0].(map[string]any)["task"].(map[string]any)["id"] != t2.ID {
		t.Fatalf("expected P0 t2 first, got %v", openItems[0])
	}
	qProj := call(context.Background(), ToolTaskQueue, map[string]any{"project": f.task.ID, "status": "open", "includeNotes": false, "includeContext": false})
	if qProj["count"] != float64(1) {
		t.Fatalf("project open count = %v, want 1", qProj["count"])
	}
	projItem := qProj["items"].([]any)[0].(map[string]any)
	if projItem["notes"] != nil || projItem["context"] != nil {
		t.Fatalf("expected notes/context omitted when false: %v", projItem)
	}

	// 4. task.thread returns task, chronological notes, deduplicated attachments, and context.
	thread := call(context.Background(), ToolTaskThread, map[string]any{"task": t1.ID})
	if thread["task"].(map[string]any)["id"] != t1.ID {
		t.Fatalf("thread task = %v", thread["task"])
	}
	if len(thread["notes"].([]any)) != 2 {
		t.Fatalf("thread notes = %v", thread["notes"])
	}
	if len(thread["attachments"].([]any)) != 2 {
		t.Fatalf("thread deduplicated attachments = %v", thread["attachments"])
	}
	if len(thread["context"].([]any)) != 1 {
		t.Fatalf("thread context = %v", thread["context"])
	}

	// 5. task.dispatch returns deterministic formatted prompt, count, and tasks.
	disp := call(context.Background(), ToolTaskDispatch, map[string]any{
		"project": f.task.ID,
		"prompt":  "Focus on clean diffs.",
	})
	if disp["count"] != float64(1) {
		t.Fatalf("dispatch count = %v, want 1", disp["count"])
	}
	prompt, _ := disp["prompt"].(string)
	for _, want := range []string{
		"# Agent Task Queue",
		"Focus on clean diffs.",
		"Fix queue dispatch",
		"Picked up the task and checking tests",
		"/tmp/shot.png",
		"https://example.com/design",
		"task.note",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("dispatch prompt missing %q:\n%s", want, prompt)
		}
	}

	// Invalid status and missing task fail cleanly.
	if _, err := box.Call(context.Background(), agent, ToolTaskQueue, json.RawMessage(`{"status":"bogus"}`)); err == nil {
		t.Fatal("expected error for bogus status")
	}
	boxNoEnv := NewToolbox(f.store, f.r)
	boxNoEnv.Getenv = func(string) string { return "" }
	if _, err := boxNoEnv.Call(context.Background(), agent, ToolTaskThread, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error when task is omitted without env")
	}
}
