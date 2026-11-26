package data

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stepsText(c *SessionChecklist) string {
	if c == nil {
		return "<nil>"
	}
	parts := make([]string, 0, len(c.Steps))
	for _, s := range c.Steps {
		parts = append(parts, s.Title+"="+s.Status)
	}
	return c.Source + ": " + strings.Join(parts, ", ")
}

func TestParseClaudeTodoPayload(t *testing.T) {
	list, ok := ParseClaudeTodoPayload("TodoWrite", map[string]any{"todos": []any{
		map[string]any{"content": "Read brief", "status": "completed", "activeForm": "Reading brief"},
		map[string]any{"content": "Write code", "status": "in_progress"},
		map[string]any{"content": "Run tests", "status": "pending"},
	}})
	if !ok || stepsText(list) != "claude: Read brief=done, Write code=in_progress, Run tests=pending" {
		t.Fatalf("TodoWrite = %s (%v)", stepsText(list), ok)
	}
	if done, total := list.Counts(); done != 1 || total != 3 {
		t.Errorf("Counts = %d/%d, want 1/3", done, total)
	}
	if cur, ok := list.Current(); !ok || cur.Title != "Write code" {
		t.Errorf("Current = %+v", cur)
	}
	if _, ok := ParseClaudeTodoPayload("Bash", map[string]any{"command": "ls"}); ok {
		t.Error("a non-plan tool parsed as a checklist")
	}
	if _, ok := ParseClaudeTodoPayload("TodoWrite", map[string]any{"todos": "nope"}); ok {
		t.Error("malformed todos parsed as a checklist")
	}
	if list, ok := ParseClaudeTodoPayload("TodoWrite", map[string]any{"todos": []any{}}); !ok || len(list.Steps) != 0 {
		t.Error("an emptied todo list must parse as an empty checklist")
	}
}

func TestMergeClaudeChecklistTaskCreateAndUpdate(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	var list *SessionChecklist
	apply := func(tool string, input map[string]any) {
		t.Helper()
		next, ok := MergeClaudeChecklist(list, tool, input, now)
		if !ok {
			t.Fatalf("%s %v was not applied", tool, input)
		}
		list = next
	}
	apply("TaskCreate", map[string]any{"subject": "Plan"})
	apply("TaskCreate", map[string]any{"subject": "Build", "taskId": "2"})
	before := list
	apply("TaskUpdate", map[string]any{"taskId": "1", "status": "completed"})
	apply("TaskUpdate", map[string]any{"taskId": "2", "status": "in_progress", "subject": "Build it"})
	apply("TaskUpdate", map[string]any{"taskId": "9", "status": "deleted"})
	if got := stepsText(list); got != "claude: Plan=done, Build it=in_progress, Task 9=skipped" {
		t.Errorf("merged = %s", got)
	}
	if stepsText(before) != "claude: Plan=pending, Build=pending" {
		t.Errorf("MergeClaudeChecklist modified the previous checklist: %s", stepsText(before))
	}
	if !list.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v", list.UpdatedAt)
	}
	apply("TodoWrite", map[string]any{"todos": []any{map[string]any{"content": "Fresh", "status": "pending"}}})
	if got := stepsText(list); got != "claude: Fresh=pending" {
		t.Errorf("TodoWrite must replace the plan, got %s", got)
	}
	if same, ok := MergeClaudeChecklist(list, "Read", map[string]any{"file_path": "x"}, now); ok || same != list {
		t.Error("an unrelated tool changed the checklist")
	}
}

func TestParseTranscriptChecklist(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	codex := write("codex.jsonl", strings.Join([]string{
		`{"timestamp":"2026-11-26T09:00:00Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"Old\",\"status\":\"pending\"}]}"}}`,
		`{"timestamp":"2026-11-26T09:05:00Z","type":"response_item","payload":{"type":"message","content":"working on update_plan"}}`,
		`{"timestamp":"2026-11-26T09:06:00Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"explanation\":\"x\",\"plan\":[{\"step\":\"Explore\",\"status\":\"completed\"},{\"step\":\"Patch\",\"status\":\"in_progress\"}]}"}}`,
		`not json`,
		`{"type":"event_msg","payload":{"type":"token_count"}}`,
	}, "\n")+"\n")
	list, ok := ParseTranscriptChecklist(codex)
	if !ok || stepsText(list) != "codex: Explore=done, Patch=in_progress" {
		t.Fatalf("codex = %s (%v)", stepsText(list), ok)
	}
	if want := time.Date(2026, 11, 26, 9, 6, 0, 0, time.UTC); !list.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", list.UpdatedAt, want)
	}

	agy := write("agy.jsonl", `{"step":{"toolCalls":[{"name":"update_steps","args":{"steps":[{"title":"Scan","status":"done"},{"title":"Fix","status":"failed"},"Verify"]}}]}}`+"\n")
	list, ok = ParseTranscriptChecklist(agy)
	if !ok || stepsText(list) != "agy: Scan=done, Fix=failed, Verify=pending" {
		t.Fatalf("agy = %s (%v)", stepsText(list), ok)
	}
	if done, total := list.Counts(); done != 1 || total != 3 {
		t.Errorf("a failed step counted as finished: %d/%d", done, total)
	}

	// The newest plan sits past the tail window's cut: only whole lines count.
	big := write("big.jsonl", `{"name":"update_plan","arguments":{"plan":[{"step":"Stale","status":"pending"}]}}`+"\n"+
		strings.Repeat(`{"type":"noise","text":"`+strings.Repeat("x", 1000)+`"}`+"\n", transcriptTailBytes/1000+8)+
		`{"name":"update_plan","arguments":{"plan":[{"step":"Latest","status":"pending"}]}}`+"\n")
	if list, ok = ParseTranscriptChecklist(big); !ok || stepsText(list) != "codex: Latest=pending" {
		t.Fatalf("big = %s (%v)", stepsText(list), ok)
	}

	for _, path := range []string{"", filepath.Join(dir, "missing.jsonl"), dir, write("empty.jsonl", ""), write("noplan.jsonl", `{"type":"message"}`+"\n")} {
		if list, ok := ParseTranscriptChecklist(path); ok {
			t.Errorf("ParseTranscriptChecklist(%q) = %s, want none", path, stepsText(list))
		}
	}
}
