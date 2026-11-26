package data

import (
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func progressTask(id, status string) AgentQueueItem {
	return AgentQueueItem{Task: Record{Kind: KindTask, ID: id, Fields: map[string]any{"title": id, "forAgent": true, "agentStatus": status}}}
}

func progressLane(id, status, parent, task string, at int) Record {
	fields := map[string]any{"title": id, "laneStatus": status}
	if parent != "" {
		fields["parentLane"] = core.Ref{Kind: KindLane, ID: parent}
	}
	if task != "" {
		fields["task"] = core.Ref{Kind: KindTask, ID: task}
	}
	return Record{Kind: KindLane, ID: id, Fields: fields, CreatedAt: time.Unix(int64(at), 0)}
}

func checklist(statuses ...string) *SessionChecklist {
	list := &SessionChecklist{Source: ChecklistSourceClaude}
	for _, st := range statuses {
		list.Steps = append(list.Steps, ChecklistStep{Title: st, Status: st})
	}
	return list
}

func TestComputeQueueProgressRollsUpLanesSessionsAndSteps(t *testing.T) {
	got := ComputeQueueProgress(QueueProgressInput{
		QueueID: "q1", Name: "review", ProjectID: "proj",
		Items: []AgentQueueItem{
			progressTask("t1", AgentStatusWorking), // worked by lane root
			progressTask("t2", AgentStatusQueued),  // nobody on it yet
			progressTask("t3", AgentStatusDone),    // finished without a lane
			progressTask("t4", AgentStatusNeedsYou),
			progressTask("t5", AgentStatusWorking), // worked by a lane-less session
		},
		Lanes: []Record{
			progressLane("root", "running", "", "t1", 1),
			progressLane("child-done", "merged", "root", "", 2),
			progressLane("child-open", "running", "root", "", 3),
			progressLane("child-cancelled", "cancelled", "root", "", 4),
			progressLane("orphan", "planned", "gone", "", 5),
		},
		Sessions: []QueueSessionInput{
			{PaneID: "p1", Title: "claude", State: "running", LaneID: "child-open", Checklist: checklist(StepDone, StepSkipped, StepInProgress, StepFailed)},
			{PaneID: "p2", Title: "codex", State: "done", LaneID: "root"},
			{PaneID: "p3", Title: "loose", State: "running", TaskID: "t5", Checklist: checklist(StepDone, StepPending)},
		},
	})

	if got.Name != "review" || got.QueueID != "q1" || got.ProjectID != "proj" {
		t.Errorf("identity = %+v", got)
	}
	if got.Queued != 1 || got.Working != 2 || got.NeedsYou != 1 || got.Finished != 1 {
		t.Errorf("item counts = queued %d working %d needsYou %d finished %d", got.Queued, got.Working, got.NeedsYou, got.Finished)
	}
	if len(got.Lanes) != 2 || got.Lanes[0].LaneID != "root" || got.Lanes[1].LaneID != "orphan" {
		t.Fatalf("root lanes = %+v", got.Lanes)
	}
	root := got.Lanes[0]
	if len(root.Children) != 2 {
		t.Fatalf("root children = %+v (a cancelled lane must be left out)", root.Children)
	}
	// child-done 1/1, child-open = p1's 2/4, p2 (no checklist, done) 1/1.
	if root.Done != 4 || root.Total != 6 || root.TaskID != "t1" {
		t.Errorf("root = %d/%d task %q, want 4/6 on t1", root.Done, root.Total, root.TaskID)
	}
	open := root.Children[1]
	if open.Done != 2 || open.Total != 4 || len(open.Sessions) != 1 || open.Sessions[0].Current != StepInProgress {
		t.Errorf("child-open = %+v", open)
	}
	if got.Lanes[1].Done != 0 || got.Lanes[1].Total != 1 {
		t.Errorf("a lane with nothing under it is one unit, got %+v", got.Lanes[1])
	}
	if len(got.Sessions) != 1 || got.Sessions[0].PaneID != "p3" || got.Sessions[0].Ratio != 0.5 {
		t.Errorf("loose sessions = %+v", got.Sessions)
	}
	// root 4/6 + orphan 0/1 + p3 1/2 + uncovered items t2 0/1, t3 1/1, t4 0/1.
	if got.Done != 6 || got.Total != 12 || got.Ratio != 0.5 {
		t.Errorf("queue = %d/%d (%.2f), want 6/12", got.Done, got.Total, got.Ratio)
	}
}

func TestComputeQueueProgressEdges(t *testing.T) {
	empty := ComputeQueueProgress(QueueProgressInput{})
	if empty.Name != DefaultQueueName || empty.Total != 0 || empty.Ratio != 0 {
		t.Errorf("empty queue = %+v", empty)
	}

	// A finished lane is complete whatever its session's stale checklist says.
	done := ComputeQueueProgress(QueueProgressInput{
		Lanes:    []Record{progressLane("l", "done", "", "", 1)},
		Sessions: []QueueSessionInput{{PaneID: "p", LaneID: "l", Checklist: checklist(StepDone, StepPending, StepPending)}},
	})
	if done.Done != 3 || done.Total != 3 || done.Ratio != 1 {
		t.Errorf("finished lane = %d/%d", done.Done, done.Total)
	}

	// Lanes that name each other as parent must not loop.
	loop := ComputeQueueProgress(QueueProgressInput{Lanes: []Record{
		progressLane("a", "running", "b", "", 1), progressLane("b", "running", "a", "", 2),
	}})
	if loop.Total != 0 {
		t.Errorf("a parent cycle has no root, got %+v", loop)
	}
}

func TestQueueLanesSelectsByTaskNameAndDescent(t *testing.T) {
	named := progressLane("named", "running", "", "", 1)
	named.Fields[FieldQueueName] = "review"
	byRef := progressLane("by-ref", "running", "", "", 2)
	byRef.Fields[FieldQueue] = core.Ref{Kind: KindQueue, ID: "q1"}
	lanes := []Record{
		named, byRef,
		progressLane("on-task", "running", "", "t1", 3),
		progressLane("grandchild", "planned", "kid", "", 5),
		progressLane("kid", "planned", "on-task", "", 4),
		progressLane("unrelated", "running", "", "t9", 6),
	}
	got := QueueLanes(lanes, "review", map[string]bool{"t1": true}, map[string]string{"q1": "review"})
	var ids []string
	for _, lane := range got {
		ids = append(ids, lane.ID)
	}
	want := []string{"named", "by-ref", "on-task", "kid", "grandchild"}
	if len(ids) != len(want) {
		t.Fatalf("QueueLanes = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("QueueLanes = %v, want %v", ids, want)
		}
	}
	// A project scope keeps its own tasks' lanes and task-less lanes of its queues.
	scoped := LanesInProject(lanes, map[string]bool{"t1": true}, map[string]string{"q1": "review"})
	ids = ids[:0]
	for _, lane := range scoped {
		ids = append(ids, lane.ID)
	}
	if len(ids) != 2 || ids[0] != "by-ref" || ids[1] != "on-task" {
		t.Errorf("LanesInProject = %v, want [by-ref on-task]", ids)
	}

	// An unnamed lane is not swept into the default queue.
	if def := QueueLanes(lanes, "", nil, map[string]string{"q1": "review"}); len(def) != 0 {
		t.Errorf("default queue picked up %d unnamed lanes", len(def))
	}
}

func TestQueueSessionsFollowDispatchedPanes(t *testing.T) {
	dispatched := func(id, status, pane string) AgentQueueItem {
		item := progressTask(id, status)
		item.Task.Fields["agentDispatchedPane"] = core.Ref{Kind: KindPane, ID: pane}
		return item
	}
	items := []AgentQueueItem{
		dispatched("t1", AgentStatusWorking, "p1"),
		dispatched("t2", AgentStatusQueued, "p1"), // one pane is one session
		dispatched("t3", AgentStatusDone, "p2"),   // finished work has no live session
		dispatched("t4", AgentStatusWorking, "gone"),
		progressTask("t5", AgentStatusQueued),
	}
	lanes := []QueueLaneHandoff{{LaneID: "l1"}}
	list := checklist(StepDone)
	got := QueueSessions(items, lanes, []QueuePane{
		{PaneID: "p1", Title: "claude", Harness: "claude", State: "running", Checklist: list},
		{PaneID: "p2", Title: "codex"},
		{PaneID: "idle", Title: "bystander"},
	})
	if len(got) != 1 {
		t.Fatalf("sessions = %+v, want only p1", got)
	}
	if s := got[0]; s.PaneID != "p1" || s.TaskID != "t1" || s.LaneID != "l1" || s.Checklist != list || s.State != "running" {
		t.Errorf("session = %+v", s)
	}
}
