package data

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Checklist step statuses.
const (
	StepPending    = "pending"
	StepInProgress = "in_progress"
	StepDone       = "done"
	StepSkipped    = "skipped"
	StepFailed     = "failed"
)

// Checklist sources: which harness (or the owner) wrote the steps.
const (
	ChecklistSourceClaude = "claude"
	ChecklistSourceCodex  = "codex"
	ChecklistSourceAgy    = "agy"
	ChecklistSourceManual = "manual"
)

// transcriptTailBytes bounds how much of a transcript ParseTranscriptChecklist
// reads: plans are rewritten whole on every update, so the newest one is near
// the end.
const transcriptTailBytes = 512 * 1024

// ChecklistStep is one step of an agent session's live plan.
type ChecklistStep struct {
	// ID is the harness's own step identifier when it has one (Claude task
	// IDs), so a later update can find the step it changes.
	ID     string `json:"id,omitempty"`
	Title  string `json:"title"`
	Status string `json:"status"` // "pending" | "in_progress" | "done" | "skipped" | "failed"
}

// SessionChecklist is the live plan of one AI session.
type SessionChecklist struct {
	Source    string          `json:"source"` // "claude" | "codex" | "agy" | "manual"
	UpdatedAt time.Time       `json:"updatedAt,omitempty"`
	Steps     []ChecklistStep `json:"steps,omitempty"`
}

// Clone returns a deep copy (nil for nil).
func (c *SessionChecklist) Clone() *SessionChecklist {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Steps = slices.Clone(c.Steps)
	return &cp
}

// Counts returns how many steps are finished and how many count at all.
// Skipped steps are finished; failed steps count but are not finished.
func (c *SessionChecklist) Counts() (done, total int) {
	if c == nil {
		return 0, 0
	}
	for _, step := range c.Steps {
		total++
		if step.Status == StepDone || step.Status == StepSkipped {
			done++
		}
	}
	return done, total
}

// Current returns the step in progress, or the first pending one.
func (c *SessionChecklist) Current() (ChecklistStep, bool) {
	if c == nil {
		return ChecklistStep{}, false
	}
	for _, want := range []string{StepInProgress, StepPending} {
		for _, step := range c.Steps {
			if step.Status == want {
				return step, true
			}
		}
	}
	return ChecklistStep{}, false
}

// NormalizeStepStatus maps a harness's status word onto the checklist
// vocabulary; unknown words are pending.
func NormalizeStepStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_progress", "in-progress", "inprogress", "active", "running", "working", "started":
		return StepInProgress
	case "done", "completed", "complete", "finished", "success", "succeeded":
		return StepDone
	case "skipped", "skip", "cancelled", "canceled", "deleted":
		return StepSkipped
	case "failed", "fail", "error", "errored", "blocked":
		return StepFailed
	default:
		return StepPending
	}
}

// ParseClaudeTodoPayload reads one Claude Code tool call (TodoWrite,
// TaskCreate or TaskUpdate) as a checklist. TodoWrite carries the whole plan;
// TaskCreate and TaskUpdate carry one step, which MergeClaudeChecklist folds
// into the plan so far.
func ParseClaudeTodoPayload(toolName string, input map[string]any) (*SessionChecklist, bool) {
	switch strings.TrimSpace(toolName) {
	case "TodoWrite":
		steps, ok := checklistSteps(input["todos"])
		if !ok {
			return nil, false
		}
		return &SessionChecklist{Source: ChecklistSourceClaude, Steps: steps}, true
	case "TaskCreate":
		title := firstText(input, "subject", "title", "content", "description")
		if title == "" {
			return nil, false
		}
		return &SessionChecklist{Source: ChecklistSourceClaude, Steps: []ChecklistStep{{
			ID: firstText(input, "taskId", "task_id", "id"), Title: title, Status: StepPending,
		}}}, true
	case "TaskUpdate":
		id := firstText(input, "taskId", "task_id", "id")
		title := firstText(input, "subject", "title")
		status := firstText(input, "status")
		if id == "" && title == "" {
			return nil, false
		}
		step := ChecklistStep{ID: id, Title: title}
		if status != "" {
			step.Status = NormalizeStepStatus(status)
		}
		return &SessionChecklist{Source: ChecklistSourceClaude, Steps: []ChecklistStep{step}}, true
	}
	return nil, false
}

// MergeClaudeChecklist applies one Claude Code tool call to the plan so far
// and returns the new plan: TodoWrite replaces it, TaskCreate appends a step,
// and TaskUpdate changes the step with the same ID (or, without IDs, the same
// title). prev is never modified.
func MergeClaudeChecklist(prev *SessionChecklist, toolName string, input map[string]any, now time.Time) (*SessionChecklist, bool) {
	delta, ok := ParseClaudeTodoPayload(toolName, input)
	if !ok {
		return prev, false
	}
	delta.UpdatedAt = now
	toolName = strings.TrimSpace(toolName)
	if toolName == "TodoWrite" || prev == nil || prev.Source != ChecklistSourceClaude {
		if toolName == "TaskUpdate" {
			step := &delta.Steps[0]
			if step.Title == "" {
				step.Title = "Task " + step.ID
			}
			if step.Status == "" {
				step.Status = StepPending
			}
		}
		if toolName == "TaskCreate" && delta.Steps[0].ID == "" {
			delta.Steps[0].ID = "1"
		}
		return delta, true
	}

	out := prev.Clone()
	out.UpdatedAt = now
	step := delta.Steps[0]
	if toolName == "TaskCreate" {
		if step.ID == "" {
			step.ID = strconv.Itoa(len(out.Steps) + 1)
		}
		if i := slices.IndexFunc(out.Steps, func(s ChecklistStep) bool { return s.ID == step.ID }); i >= 0 {
			out.Steps[i].Title = step.Title
			return out, true
		}
		out.Steps = append(out.Steps, step)
		return out, true
	}

	i := slices.IndexFunc(out.Steps, func(s ChecklistStep) bool {
		if step.ID != "" {
			return s.ID == step.ID
		}
		return s.Title == step.Title
	})
	if i < 0 {
		if step.Title == "" {
			step.Title = "Task " + step.ID
		}
		if step.Status == "" {
			step.Status = StepPending
		}
		out.Steps = append(out.Steps, step)
		return out, true
	}
	if step.Title != "" {
		out.Steps[i].Title = step.Title
	}
	if step.Status != "" {
		out.Steps[i].Status = step.Status
	}
	return out, true
}

// ParseTranscriptChecklist tail-reads a JSONL session transcript and returns
// the newest plan in it: a Codex update_plan call, an Antigravity update_steps
// call, or a Claude TodoWrite call. It reports false when the file cannot be
// read or holds no plan.
func ParseTranscriptChecklist(path string) (*SessionChecklist, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return nil, false
	}
	start := max(int64(0), info.Size()-transcriptTailBytes)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, false
	}
	buf, err := io.ReadAll(io.LimitReader(f, transcriptTailBytes))
	if err != nil {
		return nil, false
	}
	lines := bytes.Split(buf, []byte("\n"))
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line of a tail is cut mid-record
	}
	for _, line := range slices.Backward(lines) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' || !mentionsPlanTool(line) {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if list, ok := findPlanCall(entry, 0); ok {
			list.UpdatedAt = info.ModTime().UTC()
			if at, ok := entryTime(entry); ok {
				list.UpdatedAt = at
			}
			return list, true
		}
	}
	return nil, false
}

// planTools maps a plan-writing tool name to the harness that owns it and the
// argument that holds its steps.
var planTools = map[string]struct{ source, stepsKey string }{
	"update_plan":  {ChecklistSourceCodex, "plan"},
	"update_steps": {ChecklistSourceAgy, "steps"},
	"TodoWrite":    {ChecklistSourceClaude, "todos"},
}

func mentionsPlanTool(line []byte) bool {
	for name := range planTools {
		if bytes.Contains(line, []byte(name)) {
			return true
		}
	}
	return false
}

// findPlanCall walks one decoded transcript entry for the last plan-tool call
// in it, wherever the harness nests it.
func findPlanCall(v any, depth int) (*SessionChecklist, bool) {
	if depth > 12 {
		return nil, false
	}
	switch x := v.(type) {
	case []any:
		for _, child := range slices.Backward(x) {
			if list, ok := findPlanCall(child, depth+1); ok {
				return list, true
			}
		}
	case map[string]any:
		if tool, ok := planTools[firstText(x, "name", "tool_name", "toolName", "tool")]; ok {
			for _, key := range []string{"arguments", "args", "input", "tool_input", "parameters", "params"} {
				args := decodeArgs(x[key])
				if args == nil {
					continue
				}
				raw, has := args[tool.stepsKey]
				if !has {
					raw = firstValue(args, "steps", "plan", "todos", "items")
				}
				if steps, ok := checklistSteps(raw); ok {
					return &SessionChecklist{Source: tool.source, Steps: steps}, true
				}
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if list, ok := findPlanCall(x[k], depth+1); ok {
				return list, true
			}
		}
	}
	return nil, false
}

// decodeArgs reads tool arguments given as an object or as a JSON string.
func decodeArgs(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case string:
		var out map[string]any
		if json.Unmarshal([]byte(x), &out) == nil {
			return out
		}
	}
	return nil
}

func checklistSteps(v any) ([]ChecklistStep, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	steps := make([]ChecklistStep, 0, len(list))
	for _, raw := range list {
		switch x := raw.(type) {
		case string:
			if title := strings.TrimSpace(x); title != "" {
				steps = append(steps, ChecklistStep{Title: title, Status: StepPending})
			}
		case map[string]any:
			title := firstText(x, "step", "title", "content", "subject", "description", "text", "name")
			if title == "" {
				continue
			}
			steps = append(steps, ChecklistStep{
				ID:     firstText(x, "id", "taskId"),
				Title:  title,
				Status: NormalizeStepStatus(firstText(x, "status", "state")),
			})
		}
	}
	return steps, true
}

func entryTime(entry map[string]any) (time.Time, bool) {
	for _, key := range []string{"timestamp", "time", "created_at", "createdAt"} {
		if s, ok := entry[key].(string); ok {
			if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return at.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func firstValue(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func firstText(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch x := m[k].(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			return strconv.Itoa(x)
		}
	}
	return ""
}
