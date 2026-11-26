package mcpui

import (
	"encoding/json"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Task lineage MCP tool names.
const (
	ToolTaskRewrite = "task." + data.TaskTypeRewrite
	ToolTaskSplit   = "task." + data.TaskTypeSplit
	ToolTaskJoin    = "task." + data.TaskTypeJoin
)

type taskRewriteArgs struct {
	SourceID string `json:"sourceID"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	By       string `json:"by,omitempty"`
}

type taskSplitArgs struct {
	SourceID string               `json:"sourceID"`
	Parts    []data.TaskSplitPart `json:"parts"`
	By       string               `json:"by,omitempty"`
}

type taskJoinArgs struct {
	SourceIDs []string `json:"sourceIDs"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	By        string   `json:"by,omitempty"`
}

type taskLineageResult struct {
	Task  *data.Record  `json:"task,omitempty"`
	Tasks []data.Record `json:"tasks,omitempty"`
}

func taskLineageToolSpecs(by props) []ToolSpec {
	title := text("title of the new task")
	body := text("Markdown body of the new task")
	return []ToolSpec{
		{
			Name:        ToolTaskRewrite,
			Description: "Create a rewrite of a task and link it to its preserved source. Returns {task}.",
			InputSchema: object([]string{"sourceID", "title"}, props{
				"sourceID": text("source task ID"), "title": title, "body": body, "by": by,
			}),
		},
		{
			Name:        ToolTaskSplit,
			Description: "Create tasks from a list of parts and link each to its preserved source. Returns {tasks}.",
			InputSchema: object([]string{"sourceID", "parts"}, props{
				"sourceID": text("source task ID"), "by": by,
				"parts": props{"type": "array", "minItems": 1, "items": object([]string{"title"}, props{"title": title, "body": body})},
			}),
		},
		{
			Name:        ToolTaskJoin,
			Description: "Create a joined task and link it to all preserved sources. Returns {task}.",
			InputSchema: object([]string{"sourceIDs", "title"}, props{
				"sourceIDs": props{"type": "array", "minItems": 2, "items": text("source task ID")},
				"title":     title, "body": body, "by": by,
			}),
		},
	}
}

func (t *TaskTools) callTaskLineage(actor core.Actor, name string, raw json.RawMessage) (taskLineageResult, error) {
	var result taskLineageResult
	var err error
	switch name {
	case ToolTaskRewrite:
		var args taskRewriteArgs
		if err = decodeArgs(raw, &args); err == nil {
			result.Task, err = t.store.RewriteTask(args.SourceID, args.Title, args.Body, agentActor(actor, args.By))
		}
	case ToolTaskSplit:
		var args taskSplitArgs
		if err = decodeArgs(raw, &args); err == nil {
			result.Tasks, err = t.store.SplitTask(args.SourceID, args.Parts, agentActor(actor, args.By))
		}
	case ToolTaskJoin:
		var args taskJoinArgs
		if err = decodeArgs(raw, &args); err == nil {
			result.Task, err = t.store.JoinTasks(args.SourceIDs, args.Title, args.Body, agentActor(actor, args.By))
		}
	}
	return result, err
}
