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

// --- Pure queue board value objects & state transitions --------------------------

// EnqueueMode chooses how a task is queued when added via the rich add-to-queue flow.
type EnqueueMode string

const (
	EnqueueModeAdd       EnqueueMode = "add"
	EnqueueModeAddStart  EnqueueMode = "start"
	EnqueueModeAddPreset EnqueueMode = "preset"
)

// EnqueueRequest is the pure input for adding a task to a named queue.
type EnqueueRequest struct {
	TaskID    string      `json:"taskId"`
	Title     string      `json:"title,omitempty"`
	QueueName string      `json:"queueName"`
	QueueID   string      `json:"queueId,omitempty"`
	Mode      EnqueueMode `json:"mode,omitempty"`
	Priority  string      `json:"priority,omitempty"`
	Harness   string      `json:"harness,omitempty"`
	CWD       string      `json:"cwd,omitempty"`
	ProjectID string      `json:"projectId,omitempty"`
}

// QueueEntry is the pure domain representation of one open item in a named queue.
type QueueEntry struct {
	TaskID              string `json:"taskId"`
	Title               string `json:"title"`
	QueueName           string `json:"queueName"`
	QueueID             string `json:"queueId,omitempty"`
	Position            int    `json:"position"` // 1-based position within its queue
	Status              string `json:"status"`   // queued, working, needs_you, done
	Priority            string `json:"priority,omitempty"`
	Order               int64  `json:"order,omitempty"`
	ProjectID           string `json:"projectId,omitempty"`
	ProjectTitle        string `json:"projectTitle,omitempty"`
	LaneID              string `json:"laneId,omitempty"`
	LaneTitle           string `json:"laneTitle,omitempty"`
	AgentPaneID         string `json:"agentPaneId,omitempty"`
	AgentTitle          string `json:"agentTitle,omitempty"`
	AgentState          string `json:"agentState,omitempty"`
	Dispatched          bool   `json:"dispatched,omitempty"`
	NeedsAttention      bool   `json:"needsAttention,omitempty"`
	AttentionReason     string `json:"attentionReason,omitempty"` // "question", "needs_you", "status"
	AttentionPaneID     string `json:"attentionPaneId,omitempty"`
	AttentionPageID     string `json:"attentionPageId,omitempty"`
	AttentionQuestionID string `json:"attentionQuestionId,omitempty"`
}

// QueueAttentionTarget identifies the exact pane, question page, or record
// requiring owner attention for a queued item.
type QueueAttentionTarget struct {
	TaskID     string `json:"taskId"`
	TaskTitle  string `json:"taskTitle,omitempty"`
	QueueName  string `json:"queueName"`
	ProjectID  string `json:"projectId,omitempty"`
	PaneID     string `json:"paneId,omitempty"`
	PageID     string `json:"pageId,omitempty"`
	QuestionID string `json:"questionId,omitempty"`
	Reason     string `json:"reason"` // "question", "needs_you", "status"
}

// QueueWatcher is the pure projection of a live agent pane watching queues.
type QueueWatcher struct {
	PaneID      string `json:"paneId"`
	Title       string `json:"title"`
	Harness     string `json:"harness,omitempty"`
	State       string `json:"state,omitempty"`
	WatchMode   string `json:"watchMode"`
	WatchQueues string `json:"watchQueues,omitempty"`
	IntervalSec int    `json:"intervalSec,omitempty"`
	Current     string `json:"current,omitempty"`
	Done        int    `json:"done"`
	Total       int    `json:"total"`
}

// QueueBoardInput is the pure, store-free input to BuildQueueBoard.
type QueueBoardInput struct {
	ProjectID         string
	Queues            []Record
	Items             []AgentQueueItem
	Lanes             []Record
	Panes             []QueuePane
	Questions         []Record
	OpenQuestionPages map[string]string // paneID -> pageID for open interactive questions
}

// QueueBoard is the complete pure projection of every named queue in a scope.
type QueueBoard struct {
	ProjectID string
	Records   []Record
	Queues    []QueueProgress
	Entries   map[string][]QueueEntry
	Projects  map[string]string
	Watchers  []QueueWatcher
}

// QueueRecordByName returns the named queue record matching name (case-insensitive), or nil.
func QueueRecordByName(records []Record, name string) *Record {
	want := NormalizeQueueName(name)
	for i := range records {
		if strings.EqualFold(strings.TrimSpace(records[i].Text("title")), want) {
			return &records[i]
		}
	}
	return nil
}

// BuildQueueBoard projects named queues, progress segments, open queue entries,
// attention targets, and watchers from pure input data with zero store access.
func BuildQueueBoard(in QueueBoardInput) QueueBoard {
	board := QueueBoard{
		ProjectID: in.ProjectID,
		Records:   in.Queues,
		Entries:   map[string][]QueueEntry{},
		Projects:  map[string]string{},
		Watchers:  buildQueueWatchers(in.Panes),
	}
	titles := QueueTitlesByID(in.Queues)
	panesByID, paneByTask := indexQueuePanes(in.Panes)
	qByTask, qByPane := indexOpenQuestions(in.Questions)

	for _, name := range QueueNames(in.Queues, in.Items) {
		items := FilterQueueItems(in.Items, name, titles)
		taskIDs := collectQueueTaskIDs(items, board.Projects)
		lanes := QueueLanes(in.Lanes, name, taskIDs, titles)
		handoffs := QueueItemLanes(items, lanes)
		progressIn := QueueProgressInput{
			Name:      name,
			ProjectID: in.ProjectID,
			Items:     items,
			Lanes:     lanes,
			Sessions:  QueueSessions(items, handoffs, in.Panes),
		}
		if rec := QueueRecordByName(in.Queues, name); rec != nil {
			progressIn.QueueID = rec.ID
		}
		board.Queues = append(board.Queues, ComputeQueueProgress(progressIn))
		board.Entries[name] = buildQueueEntries(queueEntriesInput{
			queueName:         name,
			queueID:           progressIn.QueueID,
			items:             items,
			lanes:             lanes,
			handoffs:          handoffs,
			panesByID:         panesByID,
			paneByTask:        paneByTask,
			questionsByTask:   qByTask,
			questionsByPane:   qByPane,
			openQuestionPages: in.OpenQuestionPages,
		})
	}
	return board
}

type queueEntriesInput struct {
	queueName         string
	queueID           string
	items             []AgentQueueItem
	lanes             []Record
	handoffs          []QueueLaneHandoff
	panesByID         map[string]QueuePane
	paneByTask        map[string]QueuePane
	questionsByTask   map[string]Record
	questionsByPane   map[string]Record
	openQuestionPages map[string]string
}

func collectQueueTaskIDs(items []AgentQueueItem, projects map[string]string) map[string]bool {
	ids := make(map[string]bool, len(items))
	for _, item := range items {
		ids[item.Task.ID] = true
		projects[item.Task.ID] = item.ProjectID
	}
	return ids
}

func buildQueueWatchers(panes []QueuePane) []QueueWatcher {
	var watchers []QueueWatcher
	for _, qp := range panes {
		mode := NormalizeWatchMode(qp.WatchMode)
		if mode == WatchModeManual {
			continue
		}
		w := QueueWatcher{
			PaneID:      qp.PaneID,
			Title:       qp.Title,
			Harness:     qp.Harness,
			State:       qp.State,
			WatchMode:   mode,
			WatchQueues: qp.WatchQueues,
			IntervalSec: qp.IntervalSec,
		}
		w.Done, w.Total = qp.Checklist.Counts()
		if cur, ok := qp.Checklist.Current(); ok {
			w.Current = cur.Title
		}
		watchers = append(watchers, w)
	}
	return watchers
}

func indexQueuePanes(panes []QueuePane) (byID map[string]QueuePane, byTask map[string]QueuePane) {
	byID = make(map[string]QueuePane, len(panes))
	byTask = make(map[string]QueuePane, len(panes))
	for _, p := range panes {
		byID[p.PaneID] = p
		if p.TaskID != "" {
			if prev, exists := byTask[p.TaskID]; !exists || isNeedsAttentionState(p.State) && !isNeedsAttentionState(prev.State) {
				byTask[p.TaskID] = p
			}
		}
	}
	return byID, byTask
}

func indexOpenQuestions(questions []Record) (byTask map[string]Record, byPane map[string]Record) {
	byTask = map[string]Record{}
	byPane = map[string]Record{}
	for _, q := range questions {
		if !isOpenQuestionRecord(q) {
			continue
		}
		if ref, ok := q.RefField("task"); ok && ref.ID != "" {
			if _, exists := byTask[ref.ID]; !exists {
				byTask[ref.ID] = q
			}
		}
		if ref, ok := q.RefField("pane"); ok && ref.ID != "" {
			if _, exists := byPane[ref.ID]; !exists {
				byPane[ref.ID] = q
			}
		}
	}
	return byTask, byPane
}

func isOpenQuestionRecord(q Record) bool {
	if q.IsArchived() {
		return false
	}
	st := strings.ToLower(strings.TrimSpace(q.Text("status")))
	if st == "answered" || st == "dismissed" {
		return false
	}
	return strings.TrimSpace(q.Text("answer")) == ""
}

func isNeedsAttentionState(state string) bool {
	s := strings.ToLower(strings.TrimSpace(state))
	return s == AgentStatusNeedsYou || s == "waiting"
}

func buildQueueEntries(in queueEntriesInput) []QueueEntry {
	laneTitles := make(map[string]string, len(in.lanes))
	for _, lane := range in.lanes {
		laneTitles[lane.ID] = strings.TrimSpace(lane.Text("title"))
	}
	entries := make([]QueueEntry, 0, len(in.items))
	for i, item := range in.items {
		if item.Task.IsArchived() {
			continue
		}
		status := effectiveAgentStatus(item.Task)
		if status == AgentStatusDone || status == AgentStatusNone {
			continue
		}
		entry := buildSingleQueueEntry(in, i, item, status, laneTitles)
		entry.Position = len(entries) + 1
		entries = append(entries, entry)
	}
	return entries
}

func buildSingleQueueEntry(in queueEntriesInput, idx int, item AgentQueueItem, status string, laneTitles map[string]string) QueueEntry {
	projTitle := item.ProjectTitle
	if item.ProjectID == item.Task.ID {
		projTitle = ""
	}
	entry := QueueEntry{
		TaskID:       item.Task.ID,
		Title:        strings.TrimSpace(item.Task.Text("title")),
		QueueName:    in.queueName,
		QueueID:      in.queueID,
		Status:       status,
		Priority:     item.Task.Text("priority"),
		ProjectID:    item.ProjectID,
		ProjectTitle: projTitle,
		Dispatched:   strings.TrimSpace(item.Task.Text("agentDispatchedAt")) != "",
	}
	if v, ok := item.Task.Value("order"); ok {
		if n, isNum := number(v); isNum {
			entry.Order = int64(n)
		}
	}
	if idx < len(in.handoffs) && in.handoffs[idx].LaneID != "" {
		entry.LaneID = in.handoffs[idx].LaneID
		entry.LaneTitle = laneTitles[entry.LaneID]
	}
	attachQueueEntryPane(&entry, item.Task, in.panesByID, in.paneByTask)
	attachQueueEntryAttention(&entry, in)
	return entry
}

func attachQueueEntryPane(entry *QueueEntry, task Record, panesByID, paneByTask map[string]QueuePane) {
	if ref, ok := task.RefField("agentDispatchedPane"); ok && ref.ID != "" {
		entry.AgentPaneID = ref.ID
		if pane, live := panesByID[ref.ID]; live {
			entry.AgentTitle = pane.Title
			entry.AgentState = pane.State
		}
		return
	}
	if pane, ok := paneByTask[task.ID]; ok {
		entry.AgentPaneID = pane.PaneID
		entry.AgentTitle = pane.Title
		entry.AgentState = pane.State
	}
}

func attachQueueEntryAttention(entry *QueueEntry, in queueEntriesInput) {
	if q, ok := matchEntryOpenQuestion(entry, in.questionsByTask, in.questionsByPane); ok {
		entry.NeedsAttention = true
		entry.AttentionReason = "question"
		entry.AttentionQuestionID = q.ID
		if ref, hasPage := q.RefField("page"); hasPage {
			entry.AttentionPageID = ref.ID
		}
		if ref, hasPane := q.RefField("pane"); hasPane && ref.ID != "" {
			entry.AttentionPaneID = ref.ID
		} else {
			entry.AttentionPaneID = entry.AgentPaneID
		}
		return
	}
	if entry.AgentPaneID != "" && in.openQuestionPages[entry.AgentPaneID] != "" {
		entry.NeedsAttention = true
		entry.AttentionReason = "question"
		entry.AttentionPaneID = entry.AgentPaneID
		entry.AttentionPageID = in.openQuestionPages[entry.AgentPaneID]
		return
	}
	if isNeedsAttentionState(entry.AgentState) {
		entry.NeedsAttention = true
		entry.AttentionReason = AgentStatusNeedsYou
		entry.AttentionPaneID = entry.AgentPaneID
		return
	}
	if entry.Status == AgentStatusNeedsYou {
		entry.NeedsAttention = true
		entry.AttentionReason = "status"
		entry.AttentionPaneID = entry.AgentPaneID
	}
}

func matchEntryOpenQuestion(entry *QueueEntry, byTask, byPane map[string]Record) (Record, bool) {
	if q, ok := byTask[entry.TaskID]; ok {
		return q, true
	}
	if entry.AgentPaneID != "" {
		if q, ok := byPane[entry.AgentPaneID]; ok {
			return q, true
		}
	}
	return Record{}, false
}

// ResolveQueueAttention finds the attention target in entries: the focused item
// when it needs attention, otherwise the next item after selectedTaskID
// (wrapping around) that needs attention.
func ResolveQueueAttention(entries []QueueEntry, selectedTaskID string) (QueueAttentionTarget, bool) {
	if len(entries) == 0 {
		return QueueAttentionTarget{}, false
	}
	start := -1
	if selectedTaskID != "" {
		start = slices.IndexFunc(entries, func(e QueueEntry) bool { return e.TaskID == selectedTaskID })
		if start >= 0 && entries[start].NeedsAttention {
			return attentionTargetFromEntry(entries[start]), true
		}
	}
	for offset := 1; offset <= len(entries); offset++ {
		idx := (start + offset + len(entries)) % len(entries)
		if entries[idx].NeedsAttention {
			return attentionTargetFromEntry(entries[idx]), true
		}
	}
	return QueueAttentionTarget{}, false
}

// ResolveBoardAttention resolves the next attention target starting in
// activeQueue (focused item first, then next in activeQueue), then checking the
// remaining queues in board order.
func ResolveBoardAttention(board QueueBoard, activeQueue, selectedTaskID string) (QueueAttentionTarget, bool) {
	active := NormalizeQueueName(activeQueue)
	if target, ok := ResolveQueueAttention(board.Entries[active], selectedTaskID); ok {
		return target, true
	}
	for _, q := range board.Queues {
		if q.Name == active {
			continue
		}
		if target, ok := ResolveQueueAttention(board.Entries[q.Name], ""); ok {
			return target, true
		}
	}
	return QueueAttentionTarget{}, false
}

func attentionTargetFromEntry(e QueueEntry) QueueAttentionTarget {
	paneID := e.AttentionPaneID
	if paneID == "" {
		paneID = e.AgentPaneID
	}
	return QueueAttentionTarget{
		TaskID:     e.TaskID,
		TaskTitle:  e.Title,
		QueueName:  e.QueueName,
		ProjectID:  e.ProjectID,
		PaneID:     paneID,
		PageID:     e.AttentionPageID,
		QuestionID: e.AttentionQuestionID,
		Reason:     e.AttentionReason,
	}
}

// QueueFilterAndGroup filters entries by whitespace-separated case-insensitive
// terms and groups them by "" (none), "status", or "project".
func QueueFilterAndGroup(entries []QueueEntry, query, groupBy string) []QueueEntry {
	filtered := filterQueueEntries(entries, query)
	return groupQueueEntries(filtered, groupBy)
}

func filterQueueEntries(entries []QueueEntry, query string) []QueueEntry {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return slices.Clone(entries)
	}
	out := make([]QueueEntry, 0, len(entries))
	for _, e := range entries {
		haystack := strings.ToLower(strings.Join([]string{
			e.Title, e.Status, e.Priority, e.ProjectTitle, e.LaneTitle, e.AgentTitle, e.AttentionReason,
		}, " "))
		if matchesAllTerms(haystack, terms) {
			out = append(out, e)
		}
	}
	return out
}

func matchesAllTerms(haystack string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func groupQueueEntries(entries []QueueEntry, groupBy string) []QueueEntry {
	if groupBy != "status" && groupBy != "project" {
		return entries
	}
	rank := map[string]int{}
	if groupBy == "status" {
		rank = map[string]int{AgentStatusNeedsYou: -4, AgentStatusWorking: -3, AgentStatusQueued: -2, AgentStatusDone: -1}
	}
	keyOf := func(e QueueEntry) string {
		if groupBy == "status" {
			if e.NeedsAttention {
				return AgentStatusNeedsYou
			}
			return e.Status
		}
		if e.ProjectTitle == "" {
			return "(no project)"
		}
		return e.ProjectTitle
	}
	for _, e := range entries {
		k := keyOf(e)
		if _, ok := rank[k]; !ok {
			rank[k] = len(rank)
		}
	}
	out := slices.Clone(entries)
	slices.SortStableFunc(out, func(a, b QueueEntry) int {
		return rank[keyOf(a)] - rank[keyOf(b)]
	})
	return out
}

// ApplyEnqueue adds or updates req in entries in priority order, deduplicating
// by TaskID, and returns the updated slice and 1-based position of the entry.
func ApplyEnqueue(entries []QueueEntry, req EnqueueRequest) ([]QueueEntry, int, error) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		return entries, 0, errors.New("data: task ID is required")
	}
	queueName := NormalizeQueueName(req.QueueName)
	out := slices.Clone(entries)
	idx := slices.IndexFunc(out, func(e QueueEntry) bool { return e.TaskID == taskID })
	if idx >= 0 {
		out[idx].QueueName = queueName
		out[idx].QueueID = req.QueueID
		out[idx].Status = AgentStatusQueued
		out[idx].Dispatched = false
		out[idx].AgentPaneID = ""
		out[idx].AgentTitle = ""
		if strings.TrimSpace(req.Priority) != "" {
			out[idx].Priority = strings.TrimSpace(req.Priority)
		}
	} else {
		out = append(out, QueueEntry{
			TaskID:    taskID,
			Title:     strings.TrimSpace(req.Title),
			QueueName: queueName,
			QueueID:   req.QueueID,
			Status:    AgentStatusQueued,
			Priority:  strings.TrimSpace(req.Priority),
			ProjectID: strings.TrimSpace(req.ProjectID),
			Order:     int64(len(out) + 1),
		})
	}
	slices.SortStableFunc(out, compareQueueEntries)
	pos := reindexQueuePositions(out, taskID)
	return out, pos, nil
}

func compareQueueEntries(a, b QueueEntry) int {
	if pa, pb := priorityRank(a.Priority), priorityRank(b.Priority); pa != pb {
		return pa - pb
	}
	if a.Order != b.Order {
		if a.Order < b.Order {
			return -1
		}
		return 1
	}
	return 0
}

func reindexQueuePositions(entries []QueueEntry, targetID string) int {
	pos := 0
	for i := range entries {
		entries[i].Position = i + 1
		if entries[i].TaskID == targetID {
			pos = entries[i].Position
		}
	}
	return pos
}

// ApplyClaim assigns paneID/agentTitle to taskID (marking it dispatched and
// working), or clears the claim when paneID is empty (returning it to queued).
func ApplyClaim(entries []QueueEntry, taskID, paneID, agentTitle string) ([]QueueEntry, bool) {
	out := slices.Clone(entries)
	idx := slices.IndexFunc(out, func(e QueueEntry) bool { return e.TaskID == taskID })
	if idx < 0 {
		return out, false
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		out[idx].AgentPaneID = ""
		out[idx].AgentTitle = ""
		out[idx].AgentState = ""
		out[idx].Dispatched = false
		out[idx].Status = AgentStatusQueued
		return out, true
	}
	out[idx].AgentPaneID = paneID
	out[idx].AgentTitle = strings.TrimSpace(agentTitle)
	out[idx].Dispatched = true
	out[idx].Status = AgentStatusWorking
	return out, true
}

// ApplyReorder shifts taskID by delta (-1 up, +1 down) within entries,
// re-numbering sequential Order and 1-based Position values.
func ApplyReorder(entries []QueueEntry, taskID string, delta int) ([]QueueEntry, int, bool) {
	if delta == 0 {
		return slices.Clone(entries), 0, false
	}
	idx := slices.IndexFunc(entries, func(e QueueEntry) bool { return e.TaskID == taskID })
	if idx < 0 {
		return slices.Clone(entries), 0, false
	}
	target := idx + delta
	if target < 0 {
		target = 0
	}
	if target >= len(entries) {
		target = len(entries) - 1
	}
	if target == idx {
		return slices.Clone(entries), idx + 1, false
	}
	out := slices.Clone(entries)
	item := out[idx]
	out = slices.Delete(out, idx, idx+1)
	out = slices.Insert(out, target, item)
	for i := range out {
		out[i].Order = int64(i + 1)
		out[i].Position = i + 1
	}
	return out, target + 1, true
}

// ApplyStatus transitions taskID to status. Transitioning to AgentStatusDone or
// AgentStatusNone removes the entry from the open queue list.
func ApplyStatus(entries []QueueEntry, taskID, status string) ([]QueueEntry, bool) {
	status = strings.TrimSpace(status)
	if !isValidAgentStatus(status) {
		return slices.Clone(entries), false
	}
	idx := slices.IndexFunc(entries, func(e QueueEntry) bool { return e.TaskID == taskID })
	if idx < 0 {
		return slices.Clone(entries), false
	}
	out := slices.Clone(entries)
	if status == AgentStatusDone || status == AgentStatusNone {
		out = slices.Delete(out, idx, idx+1)
		reindexQueuePositions(out, "")
		return out, true
	}
	out[idx].Status = status
	if status == AgentStatusNeedsYou {
		out[idx].NeedsAttention = true
		if out[idx].AttentionReason == "" {
			out[idx].AttentionReason = "status"
		}
	} else if status == AgentStatusQueued {
		out[idx].Dispatched = false
		out[idx].NeedsAttention = false
		out[idx].AttentionReason = ""
	}
	return out, true
}

func isValidAgentStatus(status string) bool {
	switch status {
	case AgentStatusNone, AgentStatusQueued, AgentStatusWorking, AgentStatusNeedsYou, AgentStatusDone:
		return true
	default:
		return false
	}
}
