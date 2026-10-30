package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// defaultTools is the tool table: matomeru-old's names and argument
// schemas, each over the mtm operations it lists.
func defaultTools() []Tool {
	return []Tool{
		{Name: "whoami", Description: "Who am I: the pane, its task and project, cwd and harness. Call it first.", InputSchema: schema(map[string]any{}), call: toolWhoami},
		{Name: "task_list", Description: "List tasks: scope pin (the pane's task subtree, else its project's tasks; the default) or all; optional status filter.", InputSchema: schema(map[string]any{
			"scope":  map[string]any{"type": "string", "enum": []string{"pin", "all"}, "description": "pin: tasks under the pane's task or project; all: every task"},
			"status": str("Only tasks with this status"),
		}), Operations: []string{"task.list"}, call: toolTaskList},
		{Name: "task_done", Description: "Mark a task done; the pane's task when task_id is empty. An optional note is attached to the task.", InputSchema: schema(map[string]any{
			"task_id": str("Task id; defaults to the pane's task"),
			"note":    str("Closing note saved on the task"),
		}), Operations: []string{"task.done", "note.new"}, NeedsPane: true, call: toolTaskDone},
		{Name: "task_status", Description: "Change a task's status with an optimistic version check; done requires a summary. Evidence (summary, verification, commit, blocker) is saved as a note.", InputSchema: schema(map[string]any{
			"task_id":          str("Task id; defaults to the pane's task"),
			"expected_version": map[string]any{"type": "integer", "description": "The task's updated stamp you last read; 0 skips the check"},
			"status":           map[string]any{"type": "string", "enum": taskStatuses, "description": "New status, one of mtm's task statuses"},
			"summary":          str("What was done; required for done"),
			"verification":     str("How it was verified"),
			"commit":           str("Commit, branch or PR that carries the work"),
			"blocker":          str("Why it is blocked"),
		}, "status"), Operations: []string{"task.list", "task.status", "note.new"}, NeedsPane: true, call: toolTaskStatus},
		{Name: "task_note_add", Description: "Add a note to a task or project; the pane's task when owner is empty.", InputSchema: schema(map[string]any{
			"owner": map[string]any{"type": "object", "description": "{kind: task|project, id}", "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"task", "project"}, "description": "task or project"}, "id": str("Owner id")}, "additionalProperties": false},
			"text":  str("Note text"),
		}, "text"), Operations: []string{"note.new"}, NeedsPane: true, call: toolNoteAdd},
		{Name: "context_add", Description: "Save a link, file path, PR, command or prompt as a context item of the pane's task.", InputSchema: schema(map[string]any{
			"kind":    str("link, file, dir, pr, branch, commit, prompt or command; inferred from the text when empty"),
			"text":    str("The item: a URL, path, PR link or command line"),
			"task_id": str("Ignored: items belong to the pane's task"),
		}, "text"), Operations: []string{"context-item.add"}, NeedsPane: true, call: toolContextAdd},
		{Name: "document_report", Description: "Report a markdown or HTML document you wrote: it is saved as a file context item of the pane's task with a note naming it.", InputSchema: schema(map[string]any{
			"path":    str("Path to the .md or .html file; relative paths resolve against the pane's cwd"),
			"title":   str("Title shown in the note"),
			"kind":    map[string]any{"type": "string", "enum": []string{"report", "plan", "review"}, "description": "What the document is"},
			"summary": str("One paragraph on what it says"),
		}, "path"), Operations: []string{"context-item.add", "note.new"}, NeedsPane: true, call: toolDocumentReport},
		{Name: "report_progress", Description: "Put a short message on the pane's row: the pane's agent state becomes working with this message.", InputSchema: schema(map[string]any{
			"message": str("What you are doing right now"),
		}, "message"), Operations: []string{"pane.progress"}, NeedsPane: true, call: toolReportProgress},
	}
}

func toolWhoami(_ context.Context, _ *Server, id Identity, _ map[string]any) (any, error) {
	origin := "pane"
	if !id.Bound {
		origin = "none"
	}
	result := map[string]any{"session": id.Session, "window": id.Window, "task_id": id.TaskID, "project_id": id.ProjectID, "cwd": id.Cwd, "harness": id.Harness, "bound": id.Bound, "origin": map[string]any{"kind": origin}}
	if id.Model != "" {
		result["model"] = id.Model
	}
	if id.TaskID != "" {
		result["task_title"], result["task_status"], result["task_version"] = id.TaskTitle, id.TaskStatus, id.TaskVersion
	}
	if id.ProjectID != "" {
		result["project_name"], result["project_repo"] = id.ProjectName, id.ProjectRepo
	}
	return result, nil
}

// taskRow is the part of a task.list row (a task's props) the tools read;
// the rows are returned as the operation printed them.
type taskRow struct {
	ID        string `json:"id"`
	ParentID  string `json:"parent"`
	ProjectID string `json:"project"`
	Status    string `json:"status"`
	Updated   int64  `json:"updated"`
}

// tasks is every task (task.list --all), each row raw and parsed.
func (s *Server) tasks(ctx context.Context) ([]json.RawMessage, []taskRow, error) {
	var raws []json.RawMessage
	if _, err := s.run(ctx, "task", "list", map[string]any{"all": true}, &raws); err != nil {
		return nil, nil, err
	}
	rows := make([]taskRow, len(raws))
	for i, raw := range raws {
		if err := json.Unmarshal(raw, &rows[i]); err != nil {
			return nil, nil, err
		}
	}
	return raws, rows, nil
}

func toolTaskList(ctx context.Context, s *Server, id Identity, args map[string]any) (any, error) {
	raws, rows, err := s.tasks(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]taskRow, len(rows))
	for _, t := range rows {
		byID[t.ID] = t
	}
	// in reports whether t is the pane's task or under it (scope task),
	// or belongs to the pane's project through its parent chain.
	in := func(t taskRow) bool {
		seen := map[string]bool{}
		for cur, ok := t, true; ok && !seen[cur.ID]; cur, ok = byID[cur.ParentID] {
			seen[cur.ID] = true
			if id.TaskID != "" {
				if cur.ID == id.TaskID {
					return true
				}
				continue
			}
			if cur.ProjectID != "" {
				return cur.ProjectID == id.ProjectID
			}
		}
		return false
	}
	pinned := text(args, "scope") != "all" && (id.TaskID != "" || id.ProjectID != "")
	status := text(args, "status")
	result := make([]json.RawMessage, 0, len(rows))
	for i, t := range rows {
		if status != "" && t.Status != status {
			continue
		}
		if pinned && !in(t) {
			continue
		}
		result = append(result, raws[i])
	}
	return result, nil
}

func ownTask(id Identity, args map[string]any) (string, error) {
	taskID := text(args, "task_id")
	if taskID == "" {
		taskID = id.TaskID
	}
	if taskID == "" {
		return "", invalid("task_id is required: the pane has no task")
	}
	return taskID, nil
}

func toolTaskDone(ctx context.Context, s *Server, id Identity, args map[string]any) (any, error) {
	taskID, err := ownTask(id, args)
	if err != nil {
		return nil, err
	}
	task, err := s.run(ctx, "task", "done", map[string]any{"id": taskID}, nil)
	if err != nil {
		return nil, err
	}
	if note := strings.TrimSpace(text(args, "note")); note != "" {
		if _, err := s.run(ctx, "note", "new", map[string]any{"text": note, "task": taskID}, nil); err != nil {
			return nil, err
		}
	}
	return task, nil
}

func toolTaskStatus(ctx context.Context, s *Server, id Identity, args map[string]any) (any, error) {
	taskID, err := ownTask(id, args)
	if err != nil {
		return nil, err
	}
	status := strings.TrimSpace(text(args, "status"))
	if status == "" {
		return nil, invalid("status is required")
	}
	summary, verification, commit, blocker := strings.TrimSpace(text(args, "summary")), strings.TrimSpace(text(args, "verification")), strings.TrimSpace(text(args, "commit")), strings.TrimSpace(text(args, "blocker"))
	if status == "done" && summary == "" {
		return nil, invalid("summary is required to mark a task done")
	}
	_, rows, err := s.tasks(ctx)
	if err != nil {
		return nil, err
	}
	var current *taskRow
	for i := range rows {
		if rows[i].ID == taskID {
			current = &rows[i]
		}
	}
	if current == nil {
		return nil, &Error{Code: "not_found", Message: fmt.Sprintf("task %q not found", taskID)}
	}
	if expected := integer(args["expected_version"]); expected != 0 && expected != current.Updated {
		return nil, &Error{Code: "version_conflict", Message: fmt.Sprintf("task %s is at version %d, not %d; re-read it", taskID, current.Updated, expected)}
	}
	task, err := s.run(ctx, "task", "status", map[string]any{"id": taskID, "value": status}, nil)
	if err != nil {
		return nil, err
	}
	lines := []string{"status: " + current.Status + " → " + status}
	for _, field := range []struct{ label, value string }{{"summary", summary}, {"verification", verification}, {"commit", commit}, {"blocker", blocker}} {
		if field.value != "" {
			lines = append(lines, field.label+": "+field.value)
		}
	}
	if len(lines) > 1 {
		if _, err := s.run(ctx, "note", "new", map[string]any{"text": strings.Join(lines, "\n"), "task": taskID}, nil); err != nil {
			return nil, err
		}
	}
	return task, nil
}

func toolNoteAdd(ctx context.Context, s *Server, id Identity, args map[string]any) (any, error) {
	note := strings.TrimSpace(text(args, "text"))
	if note == "" {
		return nil, invalid("text is required")
	}
	owner := object(args, "owner")
	payload := map[string]any{"text": note}
	switch kind := text(owner, "kind"); kind {
	case "project":
		if text(owner, "id") == "" {
			return nil, invalid("owner.id is required for a project note")
		}
		payload["project"] = text(owner, "id")
	case "", "task":
		taskID := text(owner, "id")
		if taskID == "" {
			taskID = id.TaskID
		}
		if taskID == "" {
			return nil, invalid("owner.id is required: the pane has no task")
		}
		payload["task"] = taskID
	default:
		return nil, invalid("owner.kind must be task or project")
	}
	return s.run(ctx, "note", "new", payload, nil)
}

// contextAdded is context-item.add's answer.
type contextAdded struct {
	Found int               `json:"found"`
	New   int               `json:"new"`
	Items []json.RawMessage `json:"items"`
}

// addContext runs context-item.add as the pane.
func (s *Server) addContext(ctx context.Context, id Identity, body, kind string) (contextAdded, error) {
	payload := map[string]any{"text": body, "session": id.Session, "window": id.Window}
	if kind != "" {
		payload["kind"] = kind
	}
	if id.Cwd != "" {
		payload["cwd"] = id.Cwd
	}
	var added contextAdded
	_, err := s.run(ctx, "context-item", "add", payload, &added)
	return added, err
}

func toolContextAdd(ctx context.Context, s *Server, id Identity, args map[string]any) (any, error) {
	body := strings.TrimSpace(text(args, "text"))
	if body == "" {
		return nil, invalid("text is required")
	}
	added, err := s.addContext(ctx, id, body, strings.TrimSpace(text(args, "kind")))
	if err != nil {
		return nil, err
	}
	if len(added.Items) == 0 {
		return nil, invalid("no link, path, PR, prompt or command recognised in text")
	}
	return added.Items, nil
}

func toolDocumentReport(ctx context.Context, s *Server, id Identity, args map[string]any) (any, error) {
	path := strings.TrimSpace(text(args, "path"))
	if path == "" {
		return nil, invalid("path is required")
	}
	if !filepath.IsAbs(path) {
		if id.Cwd == "" {
			return nil, invalid("path must be absolute: the pane has no cwd")
		}
		path = filepath.Join(id.Cwd, path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".html":
	default:
		return nil, invalid("path must name a .md or .html file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &Error{Code: "not_found", Message: "document does not exist: " + path}
		}
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, invalid("path must be a regular file")
	}
	title := strings.TrimSpace(text(args, "title"))
	if title == "" {
		title = filepath.Base(resolved)
	}
	kind := text(args, "kind")
	if kind == "" {
		kind = "report"
	}
	summary := strings.TrimSpace(text(args, "summary"))
	added, err := s.addContext(ctx, id, resolved, "file")
	if err != nil {
		return nil, err
	}
	note := map[string]any{"text": strings.TrimSpace(kind + ": " + title + "\n" + resolved + "\n\n" + summary)}
	switch {
	case id.TaskID != "":
		note["task"] = id.TaskID
	case id.ProjectID != "":
		note["project"] = id.ProjectID
	default:
		note["scratch"] = true
	}
	var saved struct {
		ID string `json:"id"`
	}
	if _, err := s.run(ctx, "note", "new", note, &saved); err != nil {
		return nil, err
	}
	result := map[string]any{"path": resolved, "title": title, "kind": kind, "summary": summary, "task_id": id.TaskID, "project_id": id.ProjectID, "size": info.Size(), "note_id": saved.ID}
	if len(added.Items) > 0 {
		var item struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(added.Items[0], &item)
		result["context_item_id"] = item.ID
	}
	return result, nil
}

func toolReportProgress(ctx context.Context, s *Server, id Identity, args map[string]any) (any, error) {
	message := strings.TrimSpace(text(args, "message"))
	if message == "" {
		return nil, invalid("message is required")
	}
	return s.run(ctx, "pane", "progress", map[string]any{"message": message, "session": id.Session, "window": id.Window}, nil)
}
