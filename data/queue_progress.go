package data

import (
	"slices"
	"strings"
)

// SessionProgressSegment is one AI session's share of a progress bar: its
// checklist steps, or a single unit when it has no checklist.
type SessionProgressSegment struct {
	PaneID  string  `json:"paneId"`
	Title   string  `json:"title,omitempty"`
	Harness string  `json:"harness,omitempty"`
	State   string  `json:"state,omitempty"`
	Current string  `json:"current,omitempty"` // the step being worked on
	Done    int     `json:"done"`
	Total   int     `json:"total"`
	Ratio   float64 `json:"ratio"`
}

// LaneProgressSegment is one lane's share of a progress bar, rolled up from
// its child lanes and AI sessions.
type LaneProgressSegment struct {
	LaneID   string                   `json:"laneId"`
	Title    string                   `json:"title,omitempty"`
	Status   string                   `json:"status,omitempty"`
	TaskID   string                   `json:"taskId,omitempty"`
	Done     int                      `json:"done"`
	Total    int                      `json:"total"`
	Ratio    float64                  `json:"ratio"`
	Children []LaneProgressSegment    `json:"children,omitempty"`
	Sessions []SessionProgressSegment `json:"sessions,omitempty"`
}

// QueueProgress is the rollup of one named queue:
// Queue -> Lanes -> Child Lanes / AI Sessions -> SessionChecklist steps.
type QueueProgress struct {
	QueueID   string `json:"queueId,omitempty"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`

	// Item counts by agent status.
	Queued   int `json:"queued"`
	Working  int `json:"working"`
	NeedsYou int `json:"needsYou"`
	Finished int `json:"finished"`

	Done  int     `json:"done"`
	Total int     `json:"total"`
	Ratio float64 `json:"ratio"`

	Lanes []LaneProgressSegment `json:"lanes,omitempty"`
	// Sessions are the AI sessions working the queue outside any lane.
	Sessions []SessionProgressSegment `json:"sessions,omitempty"`
}

// QueueSessionInput is one live AI session ComputeQueueProgress rolls up.
// LaneID and TaskID say what the session works on; either may be empty.
type QueueSessionInput struct {
	PaneID    string
	Title     string
	Harness   string
	State     string
	LaneID    string
	TaskID    string
	Checklist *SessionChecklist
}

// QueueProgressInput is the pure input of ComputeQueueProgress. Items are the
// queue's tasks in every agent status; Lanes are the lanes working them (with
// their descendants); Sessions are the live agent panes.
type QueueProgressInput struct {
	QueueID   string
	Name      string
	ProjectID string
	Items     []AgentQueueItem
	Lanes     []Record
	Sessions  []QueueSessionInput
}

// ComputeQueueProgress rolls a queue up into segments for a segmented bar.
//
// A session counts its checklist steps (one unit when it has none, done when
// the session is done). A lane counts its child lanes and sessions (one unit
// when it has neither, done when its status is). Cancelled lanes are left
// out. A queue counts its root lanes, its sessions outside any lane, and one
// unit for every item no lane or session is working yet.
func ComputeQueueProgress(in QueueProgressInput) QueueProgress {
	out := QueueProgress{QueueID: in.QueueID, Name: NormalizeQueueName(in.Name), ProjectID: in.ProjectID}

	laneByID := make(map[string]Record, len(in.Lanes))
	for _, lane := range in.Lanes {
		if lane.ID != "" && !lane.IsArchived() && LaneProgressOf(lane) != LaneProgressExcluded {
			laneByID[lane.ID] = lane
		}
	}
	childLanes := map[string][]Record{}
	var roots []Record
	for _, lane := range in.Lanes {
		if _, ok := laneByID[lane.ID]; !ok {
			continue
		}
		if parent, ok := lane.RefField("parentLane"); ok && parent.ID != "" && parent.ID != lane.ID {
			if _, known := laneByID[parent.ID]; known {
				childLanes[parent.ID] = append(childLanes[parent.ID], lane)
				continue
			}
		}
		roots = append(roots, lane)
	}

	sessionsByLane := map[string][]SessionProgressSegment{}
	covered := map[string]bool{} // task IDs a lane or session already accounts for
	for _, sess := range in.Sessions {
		seg := sessionSegment(sess)
		if sess.TaskID != "" {
			covered[sess.TaskID] = true
		}
		if _, ok := laneByID[sess.LaneID]; ok {
			sessionsByLane[sess.LaneID] = append(sessionsByLane[sess.LaneID], seg)
			continue
		}
		out.Sessions = append(out.Sessions, seg)
		out.Done += seg.Done
		out.Total += seg.Total
	}

	visiting := map[string]bool{}
	var build func(lane Record) LaneProgressSegment
	build = func(lane Record) LaneProgressSegment {
		seg := LaneProgressSegment{
			LaneID: lane.ID,
			Title:  strings.TrimSpace(lane.Text("title")),
			Status: strings.TrimSpace(lane.Text(laneStatusField)),
		}
		if ref, ok := lane.RefField("task"); ok {
			seg.TaskID = ref.ID
			covered[ref.ID] = true
		}
		visiting[lane.ID] = true
		for _, child := range childLanes[lane.ID] {
			if visiting[child.ID] {
				continue
			}
			c := build(child)
			seg.Children = append(seg.Children, c)
			seg.Done += c.Done
			seg.Total += c.Total
		}
		delete(visiting, lane.ID)
		for _, s := range sessionsByLane[lane.ID] {
			seg.Sessions = append(seg.Sessions, s)
			seg.Done += s.Done
			seg.Total += s.Total
		}
		finished := LaneProgressOf(lane) == LaneProgressDone
		switch {
		case seg.Total == 0:
			seg.Total = 1
			if finished {
				seg.Done = 1
			}
		case finished:
			seg.Done = seg.Total // a finished lane's leftovers no longer count against it
		}
		seg.Ratio = ratio(seg.Done, seg.Total)
		return seg
	}
	for _, lane := range roots {
		seg := build(lane)
		out.Lanes = append(out.Lanes, seg)
		out.Done += seg.Done
		out.Total += seg.Total
	}

	for _, item := range in.Items {
		status := effectiveAgentStatus(item.Task)
		switch status {
		case AgentStatusQueued:
			out.Queued++
		case AgentStatusWorking:
			out.Working++
		case AgentStatusNeedsYou:
			out.NeedsYou++
		case AgentStatusDone:
			out.Finished++
		}
		if covered[item.Task.ID] {
			continue
		}
		out.Total++
		if status == AgentStatusDone {
			out.Done++
		}
	}
	out.Ratio = ratio(out.Done, out.Total)
	return out
}

func sessionSegment(sess QueueSessionInput) SessionProgressSegment {
	seg := SessionProgressSegment{
		PaneID:  sess.PaneID,
		Title:   strings.TrimSpace(sess.Title),
		Harness: strings.TrimSpace(sess.Harness),
		State:   strings.TrimSpace(sess.State),
	}
	seg.Done, seg.Total = sess.Checklist.Counts()
	if cur, ok := sess.Checklist.Current(); ok {
		seg.Current = cur.Title
	}
	if seg.Total == 0 {
		seg.Total = 1
		if strings.EqualFold(seg.State, "done") {
			seg.Done = 1
		}
	}
	seg.Ratio = ratio(seg.Done, seg.Total)
	return seg
}

func ratio(done, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(done) / float64(total)
}

// QueueLanes returns the lanes working queueName: lanes that name the queue,
// lanes on one of taskIDs, and every descendant of those.
func QueueLanes(lanes []Record, queueName string, taskIDs map[string]bool, queueTitles map[string]string) []Record {
	queueName = NormalizeQueueName(queueName)
	in := map[string]bool{}
	for _, lane := range lanes {
		if ref, ok := lane.RefField("task"); ok && taskIDs[ref.ID] {
			in[lane.ID] = true
			continue
		}
		_, hasRef := lane.RefField(FieldQueue)
		named := hasRef || strings.TrimSpace(lane.Text(FieldQueueName)) != ""
		if named && QueueNameOf(lane, queueTitles) == queueName {
			in[lane.ID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, lane := range lanes {
			if in[lane.ID] {
				continue
			}
			if parent, ok := lane.RefField("parentLane"); ok && in[parent.ID] {
				in[lane.ID] = true
				changed = true
			}
		}
	}
	out := make([]Record, 0, len(in))
	for _, lane := range lanes {
		if in[lane.ID] {
			out = append(out, lane)
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// QueuePane is one live agent pane QueueSessions places in a queue.
type QueuePane struct {
	PaneID      string
	Title       string
	Harness     string
	State       string
	TaskID      string
	WatchMode   string
	WatchQueues string
	IntervalSec int
	Checklist   *SessionChecklist
}

// QueueSessions returns the AI sessions working items: a pane is working an
// item when the item was last dispatched to it and is not finished. The
// session sits under the open lane working that item, when there is one
// (lanes is parallel to items).
func QueueSessions(items []AgentQueueItem, lanes []QueueLaneHandoff, panes []QueuePane) []QueueSessionInput {
	byID := make(map[string]QueuePane, len(panes))
	for _, p := range panes {
		byID[p.PaneID] = p
	}
	var out []QueueSessionInput
	seen := map[string]bool{}
	for i, item := range items {
		if effectiveAgentStatus(item.Task) == AgentStatusDone {
			continue
		}
		ref, ok := item.Task.RefField("agentDispatchedPane")
		if !ok || seen[ref.ID] {
			continue
		}
		pane, live := byID[ref.ID]
		if !live {
			continue
		}
		seen[ref.ID] = true
		sess := QueueSessionInput{
			PaneID: pane.PaneID, Title: pane.Title, Harness: pane.Harness, State: pane.State,
			TaskID: item.Task.ID, Checklist: pane.Checklist,
		}
		if i < len(lanes) {
			sess.LaneID = lanes[i].LaneID
		}
		out = append(out, sess)
	}
	return out
}

// LanesInProject keeps the lanes that belong to a project scope: lanes on one
// of the project's tasks, and task-less lanes that reference one of the
// project's queue records (queueTitles, keyed by queue ID).
func LanesInProject(lanes []Record, projectTaskIDs map[string]bool, queueTitles map[string]string) []Record {
	out := make([]Record, 0, len(lanes))
	for _, lane := range lanes {
		if ref, ok := lane.RefField("task"); ok && ref.ID != "" {
			if projectTaskIDs[ref.ID] {
				out = append(out, lane)
			}
			continue
		}
		if ref, ok := lane.RefField(FieldQueue); ok && queueTitles[ref.ID] != "" {
			out = append(out, lane)
		}
	}
	return out
}
