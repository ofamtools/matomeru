package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
)

func checklistText(pt *PaneTelemetry) string {
	if pt == nil || pt.Checklist == nil {
		return "<none>"
	}
	parts := make([]string, 0, len(pt.Checklist.Steps))
	for _, s := range pt.Checklist.Steps {
		parts = append(parts, s.Title+"="+s.Status)
	}
	return pt.Checklist.Source + ": " + strings.Join(parts, ", ")
}

func TestTelemetryIngestsClaudePlanHooks(t *testing.T) {
	srv, err := New(Config{DisableMCP: true, SchedulerInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	hook := func(tool string, input, response map[string]any) {
		t.Helper()
		payload := map[string]any{"hook_event_name": "PostToolUse", "tool_name": tool, "tool_input": input}
		if response != nil {
			payload["tool_response"] = response
		}
		resp, _ := srv.handleRequest(Request{
			Op: OpHookEvent, PaneID: "p1", Harness: "claude", Kind: HookKindActivity, State: StateRunning,
			Tool: tool, Payload: payload,
		})
		if !resp.OK {
			t.Fatalf("hook_event %s failed: %+v", tool, resp)
		}
	}
	current := func() *PaneTelemetry {
		tel, _ := srv.telemetry.snapshot("p1")
		return tel["p1"]
	}

	hook("TaskCreate", map[string]any{"subject": "Plan"}, map[string]any{"task": map[string]any{"id": "7", "subject": "Plan"}})
	hook("TaskCreate", map[string]any{"subject": "Build"}, map[string]any{"task": map[string]any{"id": "8"}})
	hook("Bash", map[string]any{"command": "go test"}, nil)
	hook("TaskUpdate", map[string]any{"taskId": "7", "status": "completed"}, nil)
	if got := checklistText(current()); got != "claude: Plan=done, Build=pending" {
		t.Fatalf("after TaskCreate/TaskUpdate: %s", got)
	}

	snap := current()
	snap.Checklist.Steps[0].Title = "mutated"
	if got := checklistText(current()); !strings.HasPrefix(got, "claude: Plan=done") {
		t.Fatalf("snapshot shares the checklist with the store: %s", got)
	}

	hook("TodoWrite", map[string]any{"todos": []any{
		map[string]any{"content": "Ship", "status": "in_progress"},
	}}, nil)
	if got := checklistText(current()); got != "claude: Ship=in_progress" {
		t.Fatalf("after TodoWrite: %s", got)
	}

	raw, err := json.Marshal(current())
	if err != nil || !strings.Contains(string(raw), `"checklist":{"source":"claude"`) {
		t.Fatalf("telemetry snapshot does not serialize the checklist: %s (%v)", raw, err)
	}

	srv.telemetry.clearPane("p1")
	if tel, _ := srv.telemetry.snapshot("p1"); tel["p1"] != nil {
		t.Fatal("clearPane kept the pane's telemetry")
	}
}

func TestTelemetryRefreshesChecklistFromTranscriptOnlyWhenItChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	plan := func(step, status string) string {
		return `{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"` + step + `\",\"status\":\"` + status + `\"}]}"}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(plan("Explore", "in_progress")), 0o600); err != nil {
		t.Fatal(err)
	}

	ts := newTelemetryStore()
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	event := func() {
		ts.recordEvent(HookEvent{PaneID: "p1", Harness: "codex", Kind: HookKindActivity, State: StateRunning,
			Payload: map[string]any{"transcript_path": path}}, now)
	}
	current := func() *PaneTelemetry {
		tel, _ := ts.snapshot("p1")
		return tel["p1"]
	}

	event()
	if got := checklistText(current()); got != "codex: Explore=in_progress" {
		t.Fatalf("first read: %s", got)
	}
	stamp := ts.transcripts["p1"]

	// Unchanged file: the cached stamp stands and the checklist is not re-read.
	ts.mu.Lock()
	ts.panes["p1"].Checklist = &data.SessionChecklist{Source: data.ChecklistSourceManual}
	ts.mu.Unlock()
	event()
	if got := checklistText(current()); got != "manual: " {
		t.Fatalf("an unchanged transcript was read again: %s", got)
	}
	if ts.transcripts["p1"] != stamp {
		t.Fatal("stamp moved although the transcript did not")
	}

	// Grown file: read again.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(plan("Explore", "completed")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	event()
	if got := checklistText(current()); got != "codex: Explore=done" {
		t.Fatalf("after the transcript grew: %s", got)
	}

	// A hook-fed Claude checklist is never replaced by the transcript.
	ts.mu.Lock()
	ts.panes["p1"].Checklist = &data.SessionChecklist{Source: data.ChecklistSourceClaude, Steps: []data.ChecklistStep{{Title: "Hooked", Status: data.StepPending}}}
	ts.mu.Unlock()
	if err := os.WriteFile(path, []byte(plan("Other", "pending")+plan("Other", "pending")+plan("Other", "pending")), 0o600); err != nil {
		t.Fatal(err)
	}
	event()
	if got := checklistText(current()); got != "claude: Hooked=pending" {
		t.Fatalf("transcript replaced a hook-fed checklist: %s", got)
	}

	ts.reset("test")
	if len(ts.transcripts) != 0 {
		t.Fatal("reset kept transcript stamps")
	}
}
