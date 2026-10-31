package mcp

import catalogmcp "ofam.dev/ochazuke/cli/mcp"

func schema(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// taskStatuses are the statuses task.status (the standard set of status)
// accepts.
var taskStatuses = []string{"todo", "active", "blocked", "done"}

// Aliases preserves the eight legacy tools and their argument schemas over run.
func Aliases() []catalogmcp.Alias {
	tools := []catalogmcp.Tool{
		{Name: "whoami", Description: "Who am I: the pane, its task and project, cwd and harness. Call it first.", InputSchema: schema(map[string]any{})},
		{Name: "task_list", Description: "List tasks: scope pin (the pane's task subtree, else its project's tasks; the default) or all; optional status filter.", InputSchema: schema(map[string]any{
			"scope":  map[string]any{"type": "string", "enum": []string{"pin", "all"}, "description": "pin: tasks under the pane's task or project; all: every task"},
			"status": str("Only tasks with this status"),
		})},
		{Name: "task_done", Description: "Mark a task done; the pane's task when task_id is empty. An optional note is attached to the task.", InputSchema: schema(map[string]any{
			"task_id": str("Task id; defaults to the pane's task"),
			"note":    str("Closing note saved on the task"),
		})},
		{Name: "task_status", Description: "Change a task's status with an optimistic version check; done requires a summary. Evidence (summary, verification, commit, blocker) is saved as a note.", InputSchema: schema(map[string]any{
			"task_id":          str("Task id; defaults to the pane's task"),
			"expected_version": map[string]any{"type": "integer", "description": "The task's updated stamp you last read; 0 skips the check"},
			"status":           map[string]any{"type": "string", "enum": taskStatuses, "description": "New status, one of mtm's task statuses"},
			"summary":          str("What was done; required for done"),
			"verification":     str("How it was verified"),
			"commit":           str("Commit, branch or PR that carries the work"),
			"blocker":          str("Why it is blocked"),
		}, "status")},
		{Name: "task_note_add", Description: "Add a note to a task or project; the pane's task when owner is empty.", InputSchema: schema(map[string]any{
			"owner": map[string]any{"type": "object", "description": "{kind: task|project, id}", "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"task", "project"}, "description": "task or project"}, "id": str("Owner id")}, "additionalProperties": false},
			"text":  str("Note text"),
		}, "text")},
		{Name: "context_add", Description: "Save a link, file path, PR, command or prompt as a context item of the pane's task.", InputSchema: schema(map[string]any{
			"kind":    str("link, file, dir, pr, branch, commit, prompt or command; inferred from the text when empty"),
			"text":    str("The item: a URL, path, PR link or command line"),
			"task_id": str("Ignored: items belong to the pane's task"),
		}, "text")},
		{Name: "document_report", Description: "Report a markdown or HTML document you wrote: it is saved as a file context item of the pane's task with a note naming it.", InputSchema: schema(map[string]any{
			"path":    str("Path to the .md or .html file; relative paths resolve against the pane's cwd"),
			"title":   str("Title shown in the note"),
			"kind":    map[string]any{"type": "string", "enum": []string{"report", "plan", "review"}, "description": "What the document is"},
			"summary": str("One paragraph on what it says"),
		}, "path")},
		{Name: "report_progress", Description: "Put a short message on the pane's row: the pane's agent state becomes working with this message.", InputSchema: schema(map[string]any{
			"message": str("What you are doing right now"),
		}, "message")},
	}
	verbs := []string{"pane.whoami", "task.list", "task.done", "task.status", "note.new", "context-item.add", "context-item.report", "pane.progress"}
	mappings := map[string]map[string]string{"task_done": {"task_id": "id"}, "task_status": {"task_id": "id", "status": "value"}}
	aliases := make([]catalogmcp.Alias, len(tools))
	for i, tool := range tools {
		aliases[i] = catalogmcp.Alias{Tool: tool, Verb: verbs[i], Args: mappings[tool.Name]}
	}
	return aliases
}
