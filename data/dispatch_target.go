package data

import (
	"fmt"
	"strings"
)

// DispatchTargetSpec says where queued work goes: a new agent pane (the first
// choice of every target picker) or an existing live one, and how that agent
// keeps taking work afterwards.
type DispatchTargetSpec struct {
	NewAgent              bool   `json:"newAgent"` // FIRST option in picker!
	ExistingPaneID        string `json:"existingPaneId,omitempty"`
	CWD                   string `json:"cwd,omitempty"`
	Harness               string `json:"harness,omitempty"`
	Orchestrator          bool   `json:"orchestrator,omitempty"`
	IncludeProjectContext bool   `json:"includeProjectContext,omitempty"`
	WatchMode             string `json:"watchMode,omitempty"`   // "on_done" | "schedule" | "manual"
	WatchQueues           string `json:"watchQueues,omitempty"` // "any" or comma-separated queue names
}

// Normalized returns the spec with trimmed fields, a valid watch mode, a
// canonical watch list, and exactly one of NewAgent / ExistingPaneID set: an
// existing pane wins, and a spec that names neither is a new agent.
func (t DispatchTargetSpec) Normalized() DispatchTargetSpec {
	t.ExistingPaneID = strings.TrimSpace(t.ExistingPaneID)
	t.CWD = strings.TrimSpace(t.CWD)
	t.Harness = strings.TrimSpace(t.Harness)
	t.WatchMode = NormalizeWatchMode(t.WatchMode)
	t.WatchQueues = strings.Join(ParseWatchQueues(t.WatchQueues), ",")
	t.NewAgent = t.ExistingPaneID == ""
	return t
}

// PaneWatchFields are the pane fields that persist the target's watch cadence.
func (t DispatchTargetSpec) PaneWatchFields() map[string]any {
	t = t.Normalized()
	return map[string]any{FieldWatchMode: t.WatchMode, FieldWatchQueues: t.WatchQueues}
}

// QueueLaneHandoff names the lane already working a queue item.
type QueueLaneHandoff struct {
	LaneID   string `json:"laneId,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`
}

// QueueDispatchPromptInput is everything BuildQueueDispatchPrompt embeds.
// Lanes is parallel to Items (missing entries mean no lane).
type QueueDispatchPromptInput struct {
	QueueID      string
	QueueName    string
	ProjectID    string
	ProjectTitle string
	Items        []AgentQueueItem
	Lanes        []QueueLaneHandoff
	Target       DispatchTargetSpec
	ExtraPrompt  string
}

// BuildQueueDispatchPrompt renders the deterministic prompt an agent receives
// for queue items: a handoff block with every associated ID (queue, items,
// project, lane, branch, worktree) and the commands to fetch full context and
// link results back, followed by the task queue itself. Project default and
// official context is embedded when the target asks for it and the items
// carry it.
func BuildQueueDispatchPrompt(in QueueDispatchPromptInput) string {
	target := in.Target.Normalized()
	var b strings.Builder
	b.WriteString("# Queue Handoff\n")
	row := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(&b, "- **%s:** `%s`\n", label, value)
		}
	}
	row("Queue Name", NormalizeQueueName(in.QueueName))
	row("Queue ID", in.QueueID)
	row("Project ID", in.ProjectID)
	if title := strings.TrimSpace(in.ProjectTitle); title != "" {
		fmt.Fprintf(&b, "- **Project:** %s\n", title)
	}
	if target.Orchestrator {
		b.WriteString("- **Role:** orchestrator (plan, delegate to subagents, review, report)\n")
	}
	if len(in.Items) > 0 {
		b.WriteString("\n## Queue Items\n")
	}
	firstTask, firstLane := "", ""
	for i, item := range in.Items {
		title := strings.TrimSpace(item.Task.Text("title"))
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "%d. `task:%s` %s\n", i+1, item.Task.ID, title)
		if firstTask == "" {
			firstTask = item.Task.ID
		}
		if i >= len(in.Lanes) {
			continue
		}
		lane := in.Lanes[i]
		if lane.LaneID != "" {
			fmt.Fprintf(&b, "   - **Lane ID:** `%s`\n", lane.LaneID)
			if firstLane == "" {
				firstLane = lane.LaneID
			}
		}
		if lane.Branch != "" {
			fmt.Fprintf(&b, "   - **Branch:** `%s`\n", lane.Branch)
		}
		if lane.Worktree != "" {
			fmt.Fprintf(&b, "   - **Worktree:** `%s`\n", lane.Worktree)
		}
	}

	if firstTask != "" {
		b.WriteString("\n## Fetch Context & Link Results\n")
		fmt.Fprintf(&b, "- Full task context: `mtm task show %s`\n", firstTask)
		if in.ProjectID != "" && in.ProjectID != firstTask {
			fmt.Fprintf(&b, "- Project context: `mtm task show %s`\n", in.ProjectID)
		}
		if in.QueueID != "" {
			fmt.Fprintf(&b, "- Queue: `mtm queue show %s`\n", in.QueueID)
		}
		if firstLane != "" {
			fmt.Fprintf(&b, "- Lane: `mtm lane show %s` (keep `laneStatus` and `summary` current)\n", firstLane)
		} else {
			fmt.Fprintf(&b, "- No lane yet: `mtm lane create --title \"...\" --field laneStatus=running --field task=task:%s --field queueName=%s`\n", firstTask, NormalizeQueueName(in.QueueName))
		}
		fmt.Fprintf(&b, "- Link PRs and artifacts back: `mtm mcp call data.add_context '{\"task\":\"%s\",\"value\":\"<url-or-path>\"}'`\n", firstTask)
	}

	items := in.Items
	if !target.IncludeProjectContext {
		items = make([]AgentQueueItem, len(in.Items))
		for i, item := range in.Items {
			item.ProjectContext = nil
			items[i] = item
		}
	}
	b.WriteString("\n")
	b.WriteString(FormatAgentQueuePrompt(items, in.ExtraPrompt))
	return b.String()
}
