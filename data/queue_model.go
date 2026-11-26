package data

import (
	"errors"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// Named-queue vocabulary shared by tasks, lanes, panes and queue records.
const (
	// DefaultQueueName is the queue a task or lane is in when it names none.
	DefaultQueueName = "default"
	// QueueNameAny matches every named queue in a watch list.
	QueueNameAny = "any"

	FieldQueueName        = "queueName"
	FieldQueue            = "queue"
	FieldWatchMode        = "watchMode"
	FieldWatchQueues      = "watchQueues"
	FieldWatchIntervalSec = "watchIntervalSec"

	// Watch cadences of an agent pane (and the default cadence of a queue).
	WatchModeOnDone   = "on_done"
	WatchModeSchedule = "schedule"
	WatchModeManual   = "manual"

	// DefaultWatchIntervalSec is the polling interval of a "schedule" watcher
	// that names none.
	DefaultWatchIntervalSec = 300
)

// WatchModes lists the watch cadences in display order.
var WatchModes = []string{WatchModeOnDone, WatchModeSchedule, WatchModeManual}

func queueSchema() Schema {
	return Schema{
		// A queue is a named line of agent work: tasks and lanes join it by
		// queueName (or the queue ref), and agent panes watch it.
		Kind: KindQueue, Title: "Queue", Plural: "Queues", SummaryField: "title",
		Fields: []FieldSpec{
			{Name: "title", Type: core.ParamString, Required: true, Description: "queue name; tasks and lanes join it by this name"},
			strFmt("description", "what the queue is for", ShowFormatMarkdown),
			refTo("project", KindTask, "project (root task) the queue belongs to; empty is shared by every project"),
			str("defaultHarness", "agent harness a new agent started for this queue runs"),
			{Name: "defaultCwd", Type: core.ParamPath, Description: "working directory a new agent started for this queue opens in"},
			{Name: FieldWatchMode, Type: core.ParamString, Enum: WatchModes, Default: WatchModeManual, Description: "default cadence for agents watching the queue: on_done, schedule, or manual"},
			{Name: FieldWatchIntervalSec, Type: core.ParamInt, Default: DefaultWatchIntervalSec, Description: "seconds between polls for schedule watchers"},
			{Name: "orchestratorDefault", Type: core.ParamBool, Default: false, Description: "new agents for this queue start as orchestrators by default"},
			{Name: "includeContextDefault", Type: core.ParamBool, Default: true, Description: "dispatch prompts include the project's default and official context by default"},
		},
		DefaultColumns: []string{"title", "project", FieldWatchMode, "defaultHarness", "defaultCwd"},
		Relations: universalRelations(
			RelationSpec{Kind: RelClaim, Title: "Worked by", TargetKinds: []string{KindPane}, Direction: DirectionOutgoing},
		),
	}
}

// queueNameField and queueRefField put a task or a lane in a named queue.
func queueNameField() FieldSpec {
	// No schema default: a record that names no queue is in DefaultQueueName
	// on read (NormalizeQueueName), so only records that were put in a queue
	// carry the field.
	return FieldSpec{Name: FieldQueueName, Type: core.ParamString, Description: `named agent queue this belongs to; empty is "default"`}
}

func queueRefField() FieldSpec {
	return refTo(FieldQueue, KindQueue, "named queue record this belongs to; wins over queueName")
}

// NormalizeQueueName trims name and maps empty to DefaultQueueName.
func NormalizeQueueName(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return DefaultQueueName
}

// NormalizeWatchMode returns a valid watch cadence; unknown and empty values
// are manual.
func NormalizeWatchMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if slices.Contains(WatchModes, mode) {
		return mode
	}
	return WatchModeManual
}

// ParseWatchQueues splits a watch list ("any" or comma-separated names) into
// trimmed, de-duplicated names. Empty and any list containing "any" yield
// {"any"}.
func ParseWatchQueues(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if strings.EqualFold(name, QueueNameAny) {
			return []string{QueueNameAny}
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return []string{QueueNameAny}
	}
	return out
}

// WatchesQueue reports whether a watch list covers queueName.
func WatchesQueue(watchQueues, queueName string) bool {
	names := ParseWatchQueues(watchQueues)
	return names[0] == QueueNameAny || slices.Contains(names, NormalizeQueueName(queueName))
}

// QueueNameOf resolves the named queue of a task or lane: its queue ref (by
// the titles in queueTitles, keyed by queue ID) wins over queueName, and a
// record that names neither is in DefaultQueueName.
func QueueNameOf(rec Record, queueTitles map[string]string) string {
	if ref, ok := rec.RefField(FieldQueue); ok && ref.Kind == KindQueue {
		if title := strings.TrimSpace(queueTitles[ref.ID]); title != "" {
			return title
		}
	}
	return NormalizeQueueName(rec.Text(FieldQueueName))
}

// FilterQueueItems keeps the items whose task is in queueName. An empty name
// or "any" keeps every item.
func FilterQueueItems(items []AgentQueueItem, queueName string, queueTitles map[string]string) []AgentQueueItem {
	queueName = strings.TrimSpace(queueName)
	if queueName == "" || strings.EqualFold(queueName, QueueNameAny) {
		return items
	}
	out := make([]AgentQueueItem, 0, len(items))
	for _, item := range items {
		if QueueNameOf(item.Task, queueTitles) == queueName {
			out = append(out, item)
		}
	}
	return out
}

// UndispatchedQueueItems keeps the queued items no agent pane has been handed
// yet, so a watcher never receives the same item twice.
func UndispatchedQueueItems(items []AgentQueueItem) []AgentQueueItem {
	out := make([]AgentQueueItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Task.Text("agentDispatchedAt")) == "" {
			out = append(out, item)
		}
	}
	return out
}

// QueueNames lists the named queues in display order: DefaultQueueName, then
// the queue records, then any name only tasks carry.
func QueueNames(queues []Record, items []AgentQueueItem) []string {
	titles := queueTitlesByID(queues)
	names := []string{DefaultQueueName}
	add := func(name string) {
		if name = NormalizeQueueName(name); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	for _, q := range queues {
		add(q.Text("title"))
	}
	for _, item := range items {
		add(QueueNameOf(item.Task, titles))
	}
	return names
}

func queueTitlesByID(queues []Record) map[string]string {
	titles := make(map[string]string, len(queues))
	for _, q := range queues {
		titles[q.ID] = strings.TrimSpace(q.Text("title"))
	}
	return titles
}

// QueueTitlesByID maps queue record IDs to their names.
func QueueTitlesByID(queues []Record) map[string]string { return queueTitlesByID(queues) }

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

// WatchInterval returns the polling interval of a schedule watcher, in
// seconds: the record's watchIntervalSec, or DefaultWatchIntervalSec.
func WatchInterval(rec Record) int {
	if v, ok := rec.Value(FieldWatchIntervalSec); ok {
		if n, isNum := number(v); isNum && n >= 1 {
			return int(n)
		}
	}
	return DefaultWatchIntervalSec
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
	for _, q := range queues {
		if strings.EqualFold(strings.TrimSpace(q.Text("title")), in.QueueName) {
			in.QueueID = q.ID
			break
		}
	}
	lanes, err := s.Query(Query{Kind: KindLane})
	if err != nil {
		return in, err
	}
	in.Lanes = QueueItemLanes(items, lanes.Items)
	return in, nil
}

// QueueItemLanes returns, per item in order, the open lane working its task
// (zero value when there is none).
func QueueItemLanes(items []AgentQueueItem, lanes []Record) []QueueLaneHandoff {
	byTask := map[string]Record{}
	for _, lane := range lanes {
		if lane.IsArchived() || !LaneProgressOf(lane).IsOpen() {
			continue
		}
		ref, ok := lane.RefField("task")
		if !ok || ref.ID == "" {
			continue
		}
		if cur, seen := byTask[ref.ID]; !seen || lane.CreatedAt.After(cur.CreatedAt) {
			byTask[ref.ID] = lane
		}
	}
	out := make([]QueueLaneHandoff, len(items))
	for i, item := range items {
		lane, ok := byTask[item.Task.ID]
		if !ok {
			continue
		}
		out[i] = QueueLaneHandoff{
			LaneID:   lane.ID,
			Branch:   strings.TrimSpace(lane.Text("branch")),
			Worktree: strings.TrimSpace(lane.Text("worktree")),
		}
	}
	return out
}

// LinkDispatchedPane ties a pane to the work it was just handed, so every
// record leads to the others: the pane takes the first item's task when it
// has none, each open lane working an item is claimed by the pane, and the
// queue record is worked by it.
func (s *Store) LinkDispatchedPane(pane core.Ref, in QueueDispatchPromptInput, actor core.Actor) error {
	if pane.Kind != KindPane || strings.TrimSpace(pane.ID) == "" || len(in.Items) == 0 {
		return nil
	}
	rec, err := s.Get(pane)
	if err != nil {
		return err
	}
	if _, bound := rec.RefField("task"); !bound {
		if _, _, err := s.Patch(PatchInput{
			Ref:     pane,
			Fields:  map[string]any{"task": in.Items[0].Task.Ref()},
			Summary: "bind pane to dispatched task",
		}, actor); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, lane := range in.Lanes {
		if lane.LaneID == "" || seen[lane.LaneID] {
			continue
		}
		seen[lane.LaneID] = true
		if _, _, err := s.Relate(RelateInput{From: core.Ref{Kind: KindLane, ID: lane.LaneID}, Kind: RelClaim, To: pane}, actor); err != nil {
			return err
		}
	}
	if in.QueueID != "" {
		if _, _, err := s.Relate(RelateInput{From: core.Ref{Kind: KindQueue, ID: in.QueueID}, Kind: RelClaim, To: pane}, actor); err != nil {
			return err
		}
	}
	return nil
}
