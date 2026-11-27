package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// AI Sessions Control Desk scope and node IDs.
const (
	// ScopeSessionsDesk is the page's own scope, apart from any noun's.
	ScopeSessionsDesk   = "sessions.desk"
	SessionsDeskPageID  = "sessions.desk"
	SessionsDeskTableID = "sessions.desk.table"
	SessionsDeskHintID  = "sessions.desk.hint"
	// SessionsDeskHint names the page's verbs under the table.
	SessionsDeskHint = "c/enter prompt without leaving the desk · n new session · o open its terminal · b group · s sort · / filter"

	sessionsDeskBarCells = 6
)

// sessionStatusStyle is how one run status is shown in the Status column.
type sessionStatusStyle struct {
	Icon, Label, Tone string
}

// sessionStatusStyles is the Status column's vocabulary, by run status.
var sessionStatusStyles = map[string]sessionStatusStyle{
	data.SessionNeedsYou: {"◆", "needs you", "state.needs-input"},
	data.SessionWaiting:  {"◔", "waiting", "warning"},
	data.SessionRunning:  {"▶", "running", "state.working"},
	data.SessionIdle:     {"○", "idle", "muted"},
	data.SessionExited:   {"×", "exited", "muted"},
}

// SessionsDeskSpec is the pure data contract of SessionsDeskView.
type SessionsDeskSpec struct {
	// Rows are the table's sessions in display order (already filtered,
	// sorted and grouped by the caller); SelectedIdx is the focused one, or
	// -1 for none.
	Rows        []data.SessionDeskRow
	SelectedIdx int
	// Totals count every session of the desk, whatever the filter shows.
	Totals      data.SessionDeskTotals
	FilterQuery string
	GroupBy     string
	SortBy      string
	// ShowChecklist shows the live checklist columns (Progress, Active step).
	ShowChecklist bool
	// Now is the clock recency is told against.
	Now time.Time
}

// SessionsDeskView presents every AI session in one table. It reads nothing
// but its spec.
func SessionsDeskView(spec SessionsDeskSpec) ui.ViewNode {
	filtering := strings.TrimSpace(spec.FilterQuery) != ""
	facts := map[string]any{
		"count":            len(spec.Rows),
		"total":            spec.Totals.Total,
		"running":          spec.Totals.Running,
		"needsYou":         spec.Totals.NeedsYou,
		"idle":             spec.Totals.Idle,
		"hasFilter":        filtering,
		"hasSelection":     false,
		"sessionPaneId":    "",
		"sessionStatus":    "",
		"sessionNeedsYou":  false,
		"sessionProjectId": "",
		"groupBy":          spec.GroupBy,
		"sortBy":           spec.SortBy,
		"showChecklist":    spec.ShowChecklist,
	}
	selected := -1
	if i := spec.SelectedIdx; i >= 0 && i < len(spec.Rows) {
		selected = i
		row := spec.Rows[i]
		facts["hasSelection"] = true
		facts["sessionPaneId"] = row.PaneID
		facts["sessionStatus"] = row.RunStatus
		facts["sessionNeedsYou"] = row.NeedsYou
		facts["sessionProjectId"] = row.ProjectID
	}

	children := []ui.ViewNode{sessionsDeskHeader(spec)}
	if filtering {
		children = append(children, fixed(text("sessions.desk.filter", " /"+spec.FilterQuery, "accent"), 1))
	}
	children = append(children,
		sessionsDeskTable(spec, selected, filtering),
		fixed(text(SessionsDeskHintID, " "+SessionsDeskHint, "muted"), 1),
	)
	return ui.ViewNode{
		ID:       SessionsDeskPageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeSessionsDesk,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
}

// sessionsDeskHeader is the one-line title with the live totals and, when
// they are not the defaults, the grouping and the order.
func sessionsDeskHeader(spec SessionsDeskSpec) ui.ViewNode {
	t := spec.Totals
	var line spanLine
	line.write(" AI Sessions ", "accent")
	line.write(fmt.Sprintf(" %d running", t.Running), "state.working")
	line.write(" · ", "muted")
	needsTone := "muted"
	if t.NeedsYou > 0 {
		needsTone = "state.needs-input"
	}
	line.write(fmt.Sprintf("%d needs you", t.NeedsYou), needsTone)
	line.write(fmt.Sprintf(" · %d idle · %d total", t.Idle, t.Total), "muted")
	if len(spec.Rows) != t.Total {
		line.write(fmt.Sprintf(" · %d shown", len(spec.Rows)), "muted")
	}
	if g := spec.GroupBy; g != "" && g != data.SessionGroupNone {
		line.write("  grouped by "+g, "muted")
	}
	if s := spec.SortBy; s != "" && s != data.SessionSortAttention {
		line.write("  sorted by "+s, "muted")
	}
	return fixed(ui.ViewNode{
		ID:    "sessions.desk.header",
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

// sessionsDeskColumn is one column of the table: its heading and how a row
// fills its cell (text and tone).
type sessionsDeskColumn struct {
	Title string
	Cell  func(row data.SessionDeskRow, now time.Time) (string, string)
}

// sessionsDeskColumns are the table's columns in order; checklist marks the
// ones shown only with the live checklist.
var sessionsDeskColumns = []struct {
	sessionsDeskColumn
	checklist bool
}{
	{sessionsDeskColumn{"Status", sessionStatusCell}, false},
	{sessionsDeskColumn{"Session / Harness", sessionNameCell}, false},
	{sessionsDeskColumn{"Project", func(r data.SessionDeskRow, _ time.Time) (string, string) {
		return mutedDash(r.ProjectTitle)
	}}, false},
	{sessionsDeskColumn{"Queue / Lane", func(r data.SessionDeskRow, _ time.Time) (string, string) {
		return mutedDash(SessionQueueLaneLabel(r))
	}}, false},
	{sessionsDeskColumn{"Progress", func(r data.SessionDeskRow, _ time.Time) (string, string) {
		return SessionProgressBar(r.ChecklistDone, r.ChecklistTotal, sessionsDeskBarCells)
	}}, true},
	{sessionsDeskColumn{"Active step", func(r data.SessionDeskRow, _ time.Time) (string, string) {
		return mutedDash(r.ActiveStep)
	}}, true},
	{sessionsDeskColumn{"Updated", func(r data.SessionDeskRow, now time.Time) (string, string) {
		return ThreadRecency(r.UpdatedAt, now)
	}}, false},
}

func mutedDash(s string) (string, string) {
	if strings.TrimSpace(s) == "" {
		return "—", "muted"
	}
	return s, ""
}

func sessionStatusCell(r data.SessionDeskRow, _ time.Time) (string, string) {
	style, ok := sessionStatusStyles[r.RunStatus]
	if !ok {
		style = sessionStatusStyles[data.SessionIdle]
	}
	return style.Icon + " " + style.Label, style.Tone
}

// sessionNameCell is the session's title with its harness when the title
// does not already say it; an unread session is marked and toned.
func sessionNameCell(r data.SessionDeskRow, _ time.Time) (string, string) {
	name := r.Title
	if h := strings.TrimSpace(r.Harness); h != "" && !containsFold(name, h) {
		name += " (" + h + ")"
	}
	if r.Unread {
		return "● " + name, "unread"
	}
	return name, ""
}

// SessionQueueLaneLabel names what a session works through: its queue, its
// lane, or both.
func SessionQueueLaneLabel(r data.SessionDeskRow) string {
	queue, lane := strings.TrimSpace(r.QueueName), strings.TrimSpace(r.LaneTitle)
	switch {
	case queue != "" && lane != "":
		return queue + " ▸ " + lane
	case queue != "":
		return queue
	}
	return lane
}

// SessionProgressBar draws a checklist as "██░░ 3/5" over cells cells, and
// its tone: done when every step is. A session with no checklist shows a dash.
func SessionProgressBar(done, total, cells int) (string, string) {
	if total <= 0 || cells <= 0 {
		return "—", "muted"
	}
	done = min(max(done, 0), total)
	filled := done * cells / total
	if done > 0 && filled == 0 {
		filled = 1
	}
	tone := "state.working"
	if done == total {
		tone = "state.done"
	}
	return fmt.Sprintf("%s%s %d/%d", strings.Repeat("█", filled), strings.Repeat("░", cells-filled), done, total), tone
}

func sessionsDeskTable(spec SessionsDeskSpec, selected int, filtering bool) ui.ViewNode {
	var titles []any
	var cols []sessionsDeskColumn
	for _, col := range sessionsDeskColumns {
		if col.checklist && !spec.ShowChecklist {
			continue
		}
		titles = append(titles, col.Title)
		cols = append(cols, col.sessionsDeskColumn)
	}
	rows := make([]any, 0, len(spec.Rows))
	for _, r := range spec.Rows {
		cells := make([]any, len(cols))
		tones := make([]string, len(cols))
		for i, col := range cols {
			cells[i], tones[i] = col.Cell(r, spec.Now)
		}
		rows = append(rows, ui.TableRowSpec{Cells: cells, CellTones: tones})
	}
	table := ui.ViewNode{
		ID:        SessionsDeskTableID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: SessionsDeskTableID, Prefix: "sessions ", Fields: []string{"session", "harness", "project", "queue", "lane", "status", "step"}},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":   titles,
			"rows":      rows,
			"selected":  selected,
			"scrollKey": spec.GroupBy + "|" + spec.SortBy,
		},
	}
	if len(rows) == 0 {
		empty := "No live AI sessions · n starts one with a new or existing agent"
		if filtering {
			empty = "No sessions match the filter."
		}
		table.Kind = ui.KindText
		table.Tone = "muted"
		table.Props = map[string]any{"text": " " + empty}
	}
	return table
}
