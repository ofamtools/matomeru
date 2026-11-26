package mcpui

import (
	"context"
	"encoding/json"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestTaskLineageMCP(t *testing.T) {
	f := setup(t)
	box := NewToolbox(f.store, f.r)
	call := func(name string, args any) taskLineageResult {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		result, err := box.Call(context.Background(), agent, name, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return result.(taskLineageResult)
	}
	rewrite := call(ToolTaskRewrite, taskRewriteArgs{SourceID: f.task.ID, Title: "Rewrite", Body: "Markdown"}).Task
	if rewrite.Text(data.FieldTaskType) != data.TaskTypeRewrite || rewrite.Text("description") != "Markdown" {
		t.Fatalf("rewrite fields: %v", rewrite.Fields)
	}
	splits := call(ToolTaskSplit, taskSplitArgs{SourceID: f.task.ID, Parts: []data.TaskSplitPart{{Title: "First"}, {Title: "Second"}}}).Tasks
	if len(splits) != 2 || splits[1].Text(data.FieldTaskType) != data.TaskTypeSplit {
		t.Fatalf("split result: %v", splits)
	}
	join := call(ToolTaskJoin, taskJoinArgs{SourceIDs: []string{splits[0].ID, splits[1].ID}, Title: "Join", By: "lineage-agent"}).Task
	links, err := f.store.Related(join.Ref(), data.RelJoinedFrom, data.Outgoing)
	if err != nil || len(links) != 2 || join.Text(data.FieldTaskType) != data.TaskTypeJoin {
		t.Fatalf("join result: %v, relations %v, err %v", join, links, err)
	}
	if cs, err := f.store.ChangeSet(join.ChangeSetNumber); err != nil || cs.Actor.Kind != "agent" || cs.Actor.ID != "lineage-agent" {
		t.Fatalf("join audit actor: %+v, %v", cs, err)
	}
}
