package daemon

import (
	"os"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
)

// transcriptStamp identifies one state of a transcript file.
type transcriptStamp struct {
	path    string
	modTime time.Time
	size    int64
}

// ingestHookChecklist folds a Claude plan tool call (TodoWrite, TaskCreate,
// TaskUpdate) carried by a hook event into the pane's checklist. Callers hold
// ts.mu.
func ingestHookChecklist(pt *PaneTelemetry, ev HookEvent, now time.Time) {
	tool := strings.TrimSpace(ev.Tool)
	if tool == "" {
		tool = payloadString(ev.Payload, "tool_name", "toolName")
	}
	input, _ := ev.Payload["tool_input"].(map[string]any)
	if tool == "" || input == nil {
		return
	}
	if tool == "TaskCreate" && payloadString(input, "taskId", "task_id", "id") == "" {
		// The created task's ID is in the tool's response, not its input.
		if id := createdTaskID(ev.Payload["tool_response"]); id != "" {
			input = cloneMap(input)
			input["taskId"] = id
		}
	}
	if next, ok := data.MergeClaudeChecklist(pt.Checklist, tool, input, now.UTC()); ok {
		pt.Checklist = next
	}
}

func createdTaskID(response any) string {
	m, ok := response.(map[string]any)
	if !ok {
		return ""
	}
	if task, ok := m["task"].(map[string]any); ok {
		m = task
	}
	return payloadString(m, "id", "taskId", "task_id")
}

// refreshTranscriptChecklist re-reads the pane's checklist from its session
// transcript when the file's mtime or size moved since the last read. It runs
// only on a hook event, costs one stat while the file is unchanged, and never
// replaces a checklist Claude's hooks already feed. The file is read without
// ts.mu held.
func (ts *telemetryStore) refreshTranscriptChecklist(paneID string) {
	if paneID == "" {
		return
	}
	ts.mu.Lock()
	pt := ts.panes[paneID]
	if pt == nil || pt.TranscriptPath == "" || hookFedChecklist(pt.Checklist) {
		ts.mu.Unlock()
		return
	}
	path, seen := pt.TranscriptPath, ts.transcripts[paneID]
	ts.mu.Unlock()

	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	stamp := transcriptStamp{path: path, modTime: info.ModTime(), size: info.Size()}
	if stamp == seen {
		return
	}
	list, found := data.ParseTranscriptChecklist(path)

	ts.mu.Lock()
	defer ts.mu.Unlock()
	pt = ts.panes[paneID]
	if pt == nil || pt.TranscriptPath != path {
		return
	}
	ts.transcripts[paneID] = stamp
	if found && !hookFedChecklist(pt.Checklist) {
		pt.Checklist = list
	}
}

// hookFedChecklist reports whether hooks keep the checklist current, in
// which case the transcript is the staler source.
func hookFedChecklist(list *data.SessionChecklist) bool {
	return list != nil && list.Source == data.ChecklistSourceClaude
}
