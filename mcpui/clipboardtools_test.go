package mcpui

import (
	"context"
	"encoding/json"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestClipboardPutListGet(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	proj, _, err := store.Create(data.CreateInput{Kind: data.KindTask, ID: "proj", Fields: map[string]any{"title": "Proj"}}, seed)
	if err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.Create(data.CreateInput{Kind: data.KindTask, ID: "sub", Fields: map[string]any{"title": "Sub", "parent": proj.Ref()}}, seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create(data.CreateInput{Kind: data.KindPane, ID: "w1", Fields: map[string]any{"title": "worker", "task": sub.Ref()}}, seed); err != nil {
		t.Fatal(err)
	}
	box := NewToolbox(store, nil)
	box.Getenv = func(string) string { return "" }
	worker := WithPaneID(context.Background(), "w1")

	// An agent's put defaults to authorKind agent, its own pane and the
	// root project of the pane's task.
	var put ClipboardClipResult
	callMail(t, box, worker, ToolClipboardPut, map[string]any{"content": "/tmp/report.md"}, &put)
	clip := put.Clip
	from, _ := clip.RefField("fromPane")
	project, _ := clip.RefField("project")
	if clip.Text("authorKind") != data.ClipAuthorAgent || clip.Text("clipKind") != data.ClipKindPath || from.ID != "w1" || project.ID != "proj" {
		t.Fatalf("put = %+v", clip.Fields)
	}

	// The owner's push is visible to agents through clipboard.list.
	if _, err := store.PutClip(data.ClipInput{Content: "use branch feat/x", Project: "other"}, core.Actor{Kind: "user", ID: "owner"}); err != nil {
		t.Fatal(err)
	}
	var list ClipboardListResult
	callMail(t, box, worker, ToolClipboardList, map[string]any{}, &list)
	if len(list.Clips) != 2 || list.Clips[0].Text("content") != "use branch feat/x" || list.Clips[1].ID != clip.ID {
		t.Fatalf("list = %+v", list.Clips)
	}
	callMail(t, box, worker, ToolClipboardList, map[string]any{"authorKind": "owner", "limit": 5}, &list)
	if len(list.Clips) != 1 || list.Clips[0].Text("authorKind") != data.ClipAuthorOwner {
		t.Fatalf("owner list = %+v", list.Clips)
	}
	callMail(t, box, worker, ToolClipboardList, map[string]any{"project": "sub"}, &list)
	if len(list.Clips) != 1 || list.Clips[0].ID != clip.ID {
		t.Fatalf("project list = %+v", list.Clips)
	}

	var got ClipboardClipResult
	callMail(t, box, worker, ToolClipboardGet, map[string]any{"id": clip.ID}, &got)
	if got.Clip.Text("content") != "/tmp/report.md" {
		t.Fatalf("get = %+v", got.Clip.Fields)
	}

	raw, _ := json.Marshal(map[string]any{"content": " "})
	if _, err := box.Call(worker, core.Actor{Kind: "agent", ID: "test"}, ToolClipboardPut, raw); err == nil {
		t.Fatal("clipboard.put with empty content = nil error")
	}
}
