package data

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// NamedQueues returns the active queue records visible to projectID: the
// project's own and the shared ones (no project). An empty projectID returns
// every queue.
func (s *Store) NamedQueues(projectID string) ([]Record, error) {
	res, err := s.Query(Query{Kind: KindQueue})
	if err != nil {
		return nil, err
	}
	projectID = strings.TrimSpace(projectID)
	out := make([]Record, 0, len(res.Items))
	for _, q := range res.Items {
		if projectID != "" {
			if ref, ok := q.RefField("project"); ok && ref.ID != "" && ref.ID != projectID {
				continue
			}
		}
		out = append(out, q)
	}
	slices.SortStableFunc(out, func(a, b Record) int {
		if c := strings.Compare(strings.ToLower(a.Text("title")), strings.ToLower(b.Text("title"))); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// RootTaskID walks a task's parents to its root (the project). It returns
// taskID itself for a root or an unknown task.
func (s *Store) RootTaskID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	seen := map[string]bool{}
	for cur := taskID; cur != "" && !seen[cur]; {
		seen[cur] = true
		rec, err := s.Get(core.Ref{Kind: KindTask, ID: cur})
		if err != nil || rec == nil {
			break
		}
		parent, ok := rec.RefField("parent")
		if !ok || parent.Kind != KindTask || parent.ID == "" {
			return cur
		}
		cur = parent.ID
	}
	return taskID
}

// QueryNamedQueue returns the queued agent tasks of queueName (empty or "any"
// for every queue) inside projectID (empty for every project), hydrated with
// notes and context and ordered like QueryAgentQueue.
func (s *Store) QueryNamedQueue(queueName, projectID string) ([]AgentQueueItem, error) {
	return s.QueryNamedQueueFiltered(queueName, AgentQueueFilter{
		ProjectID:      strings.TrimSpace(projectID),
		Status:         AgentStatusQueued,
		IncludeSubtree: true,
		IncludeNotes:   true,
		IncludeContext: true,
	})
}

// QueryNamedQueueFiltered is QueryAgentQueue narrowed to one named queue.
func (s *Store) QueryNamedQueueFiltered(queueName string, filter AgentQueueFilter) ([]AgentQueueItem, error) {
	items, err := s.QueryAgentQueue(filter)
	if err != nil {
		return nil, err
	}
	queueName = strings.TrimSpace(queueName)
	if queueName == "" || strings.EqualFold(queueName, QueueNameAny) {
		return items, nil
	}
	queues, err := s.NamedQueues("")
	if err != nil {
		return nil, err
	}
	return FilterQueueItems(items, queueName, queueTitlesByID(queues)), nil
}

// EnsureNamedQueue returns the queue record named name in projectID, creating
// it when there is none. DefaultQueueName needs no record and is never created.
func (s *Store) EnsureNamedQueue(name, projectID string, fields map[string]any, actor core.Actor) (*Record, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("data: queue name is required")
	}
	projectID = strings.TrimSpace(projectID)
	queues, err := s.NamedQueues(projectID)
	if err != nil {
		return nil, err
	}
	for i := range queues {
		if strings.EqualFold(strings.TrimSpace(queues[i].Text("title")), name) {
			return &queues[i], nil
		}
	}
	create := map[string]any{}
	for k, v := range fields {
		create[k] = v
	}
	create["title"] = name
	if projectID != "" {
		create["project"] = core.Ref{Kind: KindTask, ID: projectID}
	}
	rec, _, err := s.Create(CreateInput{Kind: KindQueue, Fields: create}, actor)
	return rec, err
}

// AddTaskToQueue puts a task in a named queue and flags it queued for agent
// pickup, in one change set. queue may be nil for a queue without a record.
func (s *Store) AddTaskToQueue(taskID, queueName string, queue *Record, actor core.Actor) (*Record, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("data: task ID is required")
	}
	if err := s.guardAgentFlag(core.Ref{Kind: KindTask, ID: taskID}); err != nil {
		return nil, err
	}
	fields := map[string]any{
		FieldQueueName:        NormalizeQueueName(queueName),
		FieldQueue:            nil,
		"forAgent":            true,
		"agentStatus":         AgentStatusQueued,
		"agentDispatchedAt":   nil,
		"agentDispatchedPane": nil,
	}
	if queue != nil && queue.ID != "" {
		fields[FieldQueue] = queue.Ref()
		fields[FieldQueueName] = NormalizeQueueName(queue.Text("title"))
	}
	rec, _, err := s.Patch(PatchInput{
		Ref:     core.Ref{Kind: KindTask, ID: taskID},
		Fields:  fields,
		Summary: "queue task:" + taskID + " in " + Text(fields[FieldQueueName]),
	}, actor)
	return rec, err
}

// EnqueueTask adds req.TaskID to req.QueueName and returns the patched task
// record together with its 1-based position within that queue.
func (s *Store) EnqueueTask(req EnqueueRequest, queue *Record, actor core.Actor) (*Record, int, error) {
	queueName := NormalizeQueueName(req.QueueName)
	if queue != nil && queue.ID != "" {
		queueName = NormalizeQueueName(queue.Text("title"))
	}
	rec, err := s.AddTaskToQueue(req.TaskID, queueName, queue, actor)
	if err != nil {
		return nil, 0, err
	}
	open, err := s.QueryNamedQueueFiltered(queueName, AgentQueueFilter{
		ProjectID:      strings.TrimSpace(req.ProjectID),
		Status:         "open",
		IncludeSubtree: true,
	})
	if err != nil {
		return rec, 1, nil
	}
	for i, item := range open {
		if item.Task.ID == rec.ID {
			return rec, i + 1, nil
		}
	}
	return rec, len(open), nil
}

// ReorderQueueTask shifts taskID up (delta < 0) or down (delta > 0) within
// queueName by persisting sequential order values on the queue's open tasks.
func (s *Store) ReorderQueueTask(taskID, queueName, projectID string, delta int, actor core.Actor) (int, bool, error) {
	queueName = NormalizeQueueName(queueName)
	open, err := s.QueryNamedQueueFiltered(queueName, AgentQueueFilter{
		ProjectID:      strings.TrimSpace(projectID),
		Status:         "open",
		IncludeSubtree: true,
	})
	if err != nil {
		return 0, false, err
	}
	entries := make([]QueueEntry, len(open))
	for i, item := range open {
		entries[i] = QueueEntry{TaskID: item.Task.ID, QueueName: queueName, Position: i + 1, Order: int64(i + 1)}
	}
	reordered, pos, moved := ApplyReorder(entries, taskID, delta)
	if !moved {
		return pos, false, nil
	}
	patches := make([]PatchInput, len(reordered))
	for i, e := range reordered {
		patches[i] = PatchInput{
			Ref:    core.Ref{Kind: KindTask, ID: e.TaskID},
			Fields: map[string]any{"order": e.Order},
		}
	}
	if _, err := s.PatchMany(patches, "queue.item.reorder", fmt.Sprintf("reorder %s in %s to #%d", taskID, queueName, pos), actor); err != nil {
		return 0, false, err
	}
	return pos, true, nil
}

// QueueDispatchInput assembles the deterministic ID handoff for items of
// queueName: the queue record, the project, and the open lane (with its
// branch and worktree) working the first item.
func (s *Store) QueueDispatchInput(queueName string, items []AgentQueueItem, target DispatchTargetSpec, extraPrompt string) (QueueDispatchPromptInput, error) {
	in := QueueDispatchPromptInput{
		QueueName:   NormalizeQueueName(queueName),
		Items:       items,
		Target:      target,
		ExtraPrompt: extraPrompt,
	}
	if len(items) > 0 {
		in.ProjectID, in.ProjectTitle = items[0].ProjectID, items[0].ProjectTitle
	}
	queues, err := s.NamedQueues(in.ProjectID)
	if err != nil {
		return in, err
	}
	if rec := QueueRecordByName(queues, in.QueueName); rec != nil {
		in.QueueID = rec.ID
	}
	lanes, err := s.Query(Query{Kind: KindLane})
	if err != nil {
		return in, err
	}
	in.Lanes = QueueItemLanes(items, lanes.Items)
	return in, nil
}

// LinkDispatchedPane ties a pane to the work it was just handed, so every
// record leads to the others: the pane takes the first item's task when it
// has none, each open lane working an item is claimed by the pane, and the
// queue record is worked by it.
func (s *Store) LinkDispatchedPane(pane core.Ref, in QueueDispatchPromptInput, actor core.Actor) error {
	if pane.Kind != KindPane || strings.TrimSpace(pane.ID) == "" || len(in.Items) == 0 {
		return nil
	}
	if err := s.bindDispatchedPaneTask(pane, in.Items[0].Task.Ref(), actor); err != nil {
		return err
	}
	if err := s.claimDispatchedLanes(pane, in.Lanes, actor); err != nil {
		return err
	}
	if in.QueueID == "" {
		return nil
	}
	_, _, err := s.Relate(RelateInput{From: core.Ref{Kind: KindQueue, ID: in.QueueID}, Kind: RelClaim, To: pane}, actor)
	return err
}

func (s *Store) bindDispatchedPaneTask(pane, task core.Ref, actor core.Actor) error {
	rec, err := s.Get(pane)
	if err != nil {
		return err
	}
	if _, bound := rec.RefField("task"); bound {
		return nil
	}
	_, _, err = s.Patch(PatchInput{
		Ref:     pane,
		Fields:  map[string]any{"task": task},
		Summary: "bind pane to dispatched task",
	}, actor)
	return err
}

func (s *Store) claimDispatchedLanes(pane core.Ref, lanes []QueueLaneHandoff, actor core.Actor) error {
	seen := map[string]bool{}
	for _, lane := range lanes {
		if lane.LaneID == "" || seen[lane.LaneID] {
			continue
		}
		seen[lane.LaneID] = true
		if _, _, err := s.Relate(RelateInput{From: core.Ref{Kind: KindLane, ID: lane.LaneID}, Kind: RelClaim, To: pane}, actor); err != nil {
			return err
		}
	}
	return nil
}
