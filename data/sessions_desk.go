package data

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// Run statuses of a session on the AI Sessions Control Desk. They fold the
// pane's runStatus and the live hook state into the five the desk tells apart.
const (
	SessionRunning  = "running"
	SessionWaiting  = "waiting"
	SessionNeedsYou = "needs_you"
	SessionIdle     = "idle"
	SessionExited   = "exited"
)

// Sort orders of the desk.
const (
	SessionSortAttention = "attention" // needs you, running, idle, exited; newest first inside each
	SessionSortRecent    = "recent"
	SessionSortProject   = "project"
	SessionSortTitle     = "title"
)

// Groupings of the desk.
const (
	SessionGroupNone    = "none"
	SessionGroupStatus  = "status"
	SessionGroupProject = "project"
	SessionGroupHarness = "harness"
)

// SessionSortModes and SessionGroupModes are the orders and groupings the
// desk cycles through, default first.
var (
	SessionSortModes  = []string{SessionSortAttention, SessionSortRecent, SessionSortProject, SessionSortTitle}
	SessionGroupModes = []string{SessionGroupNone, SessionGroupStatus, SessionGroupProject, SessionGroupHarness}
)

// sessionStatusRank orders the run statuses for the attention sort: what
// waits for the owner first, then what is working, then the quiet rest.
var sessionStatusRank = map[string]int{
	SessionNeedsYou: 0,
	SessionWaiting:  1,
	SessionRunning:  2,
	SessionIdle:     3,
	SessionExited:   4,
}

// sessionStatusByState maps a pane runStatus or a hook state onto a desk run
// status. A state it does not name is idle.
var sessionStatusByState = map[string]string{
	"needs_you": SessionNeedsYou,
	"running":   SessionRunning,
	"waiting":   SessionWaiting,
	"idle":      SessionIdle,
	"done":      SessionIdle,
	"unknown":   SessionIdle,
	"dead":      SessionExited,
	"exited":    SessionExited,
}

// SessionDeskRow is one AI session on the AI Sessions Control Desk: the pane
// it runs in, what it belongs to and how far it got.
type SessionDeskRow struct {
	PaneID         string    `json:"paneId"`
	PaneRef        core.Ref  `json:"paneRef"`
	Title          string    `json:"title"`
	Harness        string    `json:"harness"`
	ProjectID      string    `json:"projectId,omitempty"`
	ProjectTitle   string    `json:"projectTitle,omitempty"`
	QueueName      string    `json:"queueName,omitempty"`
	LaneID         string    `json:"laneId,omitempty"`
	LaneTitle      string    `json:"laneTitle,omitempty"`
	RunStatus      string    `json:"runStatus"` // running, waiting, needs_you, idle, exited
	ActiveStep     string    `json:"activeStep,omitempty"`
	ChecklistDone  int       `json:"checklistDone"`
	ChecklistTotal int       `json:"checklistTotal"`
	NeedsYou       bool      `json:"needsYou"`
	Unread         bool      `json:"unread"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// SessionDeskSignals are the per-pane facts BuildSessionDeskRows cannot read
// from the records it is given.
type SessionDeskSignals struct {
	// PaneProjects is each pane's project ID, by pane ID.
	PaneProjects map[string]string
	// Unread marks the panes with something the owner has not seen.
	Unread map[string]bool
}

// SessionDeskTotals counts the desk's sessions by what they are doing.
type SessionDeskTotals struct {
	Running  int `json:"running"`
	NeedsYou int `json:"needsYou"`
	Idle     int `json:"idle"`
	Exited   int `json:"exited"`
	Total    int `json:"total"`
}

// NormalizeSessionStatus maps a pane runStatus or a hook state onto a desk
// run status.
func NormalizeSessionStatus(state string) string {
	if st, ok := sessionStatusByState[strings.ToLower(strings.TrimSpace(state))]; ok {
		return st
	}
	return SessionIdle
}

// BuildSessionDeskRows turns the session panes into desk rows, sorted with
// the sessions that need the owner first, then the running ones, then the
// rest, newest first inside each.
//
// lanes are the lanes a pane may have claimed and claims the lane IDs each
// pane claims, by pane ID. projects holds project titles by project ID.
// checklists and statuses are the live plan and the live state of each pane,
// by pane ID; a pane without a live state keeps its runStatus.
func BuildSessionDeskRows(panes []Record, lanes []Record, claims map[string][]string, projects map[string]string, checklists map[string]*SessionChecklist, statuses map[string]string, signals SessionDeskSignals) []SessionDeskRow {
	laneByID := make(map[string]Record, len(lanes))
	for _, lane := range lanes {
		laneByID[lane.ID] = lane
	}
	rows := make([]SessionDeskRow, 0, len(panes))
	for _, pane := range panes {
		row := SessionDeskRow{
			PaneID:  pane.ID,
			PaneRef: pane.Ref(),
			Title:   strings.TrimSpace(pane.Text("title")),
			Harness: strings.TrimSpace(pane.Text("harness")),
			Unread:  signals.Unread[pane.ID],
		}
		if row.Title == "" {
			row.Title = cmp.Or(row.Harness, pane.ID)
		}
		row.ProjectID = signals.PaneProjects[pane.ID]
		row.ProjectTitle = projects[row.ProjectID]

		if lane, ok := sessionLane(claims[pane.ID], laneByID); ok {
			row.LaneID, row.LaneTitle = lane.ID, strings.TrimSpace(lane.Text("title"))
			row.QueueName = strings.TrimSpace(lane.Text(FieldQueueName))
		}
		if row.QueueName == "" && NormalizeWatchMode(pane.Text(FieldWatchMode)) != WatchModeManual {
			row.QueueName = strings.TrimSpace(pane.Text(FieldWatchQueues))
		}

		row.RunStatus = NormalizeSessionStatus(cmp.Or(strings.TrimSpace(statuses[pane.ID]), pane.Text("runStatus")))
		row.NeedsYou = row.RunStatus == SessionNeedsYou || row.RunStatus == SessionWaiting

		row.UpdatedAt = latestTime(pane.UpdatedAt, pane.Time("promptedAt"), pane.Time("lastAgentInputAt"))
		if list := checklists[pane.ID]; list != nil {
			row.ChecklistDone, row.ChecklistTotal = list.Counts()
			if step, ok := list.Current(); ok {
				row.ActiveStep = strings.TrimSpace(step.Title)
			}
			row.UpdatedAt = latestTime(row.UpdatedAt, list.UpdatedAt)
		}
		rows = append(rows, row)
	}
	return SortSessionDeskRows(rows, SessionSortAttention)
}

// sessionLane picks the lane a session is shown with: the first claimed lane
// that is running, else the first claimed lane that is not archived.
func sessionLane(claimed []string, laneByID map[string]Record) (Record, bool) {
	var first *Record
	for _, id := range claimed {
		lane, ok := laneByID[id]
		if !ok || lane.IsArchived() {
			continue
		}
		if lane.Text("laneStatus") == "running" {
			return lane, true
		}
		if first == nil {
			first = &lane
		}
	}
	if first == nil {
		return Record{}, false
	}
	return *first, true
}

func latestTime(times ...time.Time) time.Time {
	var out time.Time
	for _, t := range times {
		if t.After(out) {
			out = t
		}
	}
	return out
}

// compareSessionRecency orders newest first, then by title and pane ID so
// the order is stable between renders.
func compareSessionRecency(a, b SessionDeskRow) int {
	return cmp.Or(
		b.UpdatedAt.Compare(a.UpdatedAt),
		cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
		cmp.Compare(a.PaneID, b.PaneID),
	)
}

// SortSessionDeskRows returns rows in the order by names; an order it does
// not know is the attention order. Sessions without a project sort last by
// project.
func SortSessionDeskRows(rows []SessionDeskRow, by string) []SessionDeskRow {
	out := slices.Clone(rows)
	var primary func(a, b SessionDeskRow) int
	switch by {
	case SessionSortRecent:
		primary = func(a, b SessionDeskRow) int { return 0 }
	case SessionSortProject:
		primary = func(a, b SessionDeskRow) int {
			return cmp.Or(
				cmp.Compare(boolRank(a.ProjectTitle == ""), boolRank(b.ProjectTitle == "")),
				cmp.Compare(strings.ToLower(a.ProjectTitle), strings.ToLower(b.ProjectTitle)),
			)
		}
	case SessionSortTitle:
		primary = func(a, b SessionDeskRow) int {
			return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		}
	default:
		primary = func(a, b SessionDeskRow) int {
			return cmp.Compare(sessionStatusRank[a.RunStatus], sessionStatusRank[b.RunStatus])
		}
	}
	slices.SortStableFunc(out, func(a, b SessionDeskRow) int {
		return cmp.Or(primary(a, b), compareSessionRecency(a, b))
	})
	return out
}

func boolRank(v bool) int {
	if v {
		return 1
	}
	return 0
}

// SessionGroupKey is the group a row falls in under groupBy ("" when the
// desk is not grouped).
func SessionGroupKey(row SessionDeskRow, groupBy string) string {
	switch groupBy {
	case SessionGroupStatus:
		return row.RunStatus
	case SessionGroupProject:
		return row.ProjectTitle
	case SessionGroupHarness:
		return row.Harness
	}
	return ""
}

// GroupSessionDeskRows orders rows so each group is contiguous, keeping the
// rows' order inside a group and the groups in order of first appearance.
// Status groups follow the attention order instead.
func GroupSessionDeskRows(rows []SessionDeskRow, groupBy string) []SessionDeskRow {
	if groupBy == SessionGroupNone || !slices.Contains(SessionGroupModes, groupBy) {
		return rows
	}
	rank := map[string]int{}
	if groupBy == SessionGroupStatus {
		for status, r := range sessionStatusRank {
			rank[status] = r - len(sessionStatusRank)
		}
	}
	for _, row := range rows {
		key := SessionGroupKey(row, groupBy)
		if _, ok := rank[key]; !ok {
			rank[key] = len(rank)
		}
	}
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b SessionDeskRow) int {
		return rank[SessionGroupKey(a, groupBy)] - rank[SessionGroupKey(b, groupBy)]
	})
	return out
}

// CountSessionDeskRows totals the rows by what the sessions are doing. A
// session that waits for the owner counts as "needs you" whichever of the
// two states says so.
func CountSessionDeskRows(rows []SessionDeskRow) SessionDeskTotals {
	t := SessionDeskTotals{Total: len(rows)}
	for _, row := range rows {
		switch {
		case row.NeedsYou:
			t.NeedsYou++
		case row.RunStatus == SessionRunning:
			t.Running++
		case row.RunStatus == SessionExited:
			t.Exited++
		default:
			t.Idle++
		}
	}
	return t
}
