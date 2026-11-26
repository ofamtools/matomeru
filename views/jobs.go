package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

// Jobs page scopes, tabs and filter identity.
const (
	ScopeJobs      = "jobs"
	JobsTabAll     = "all"
	JobsTabActive  = "active"
	JobsTabHistory = "history"
	JobsTabActions = "actions"
	JobsListID     = "jobs.list"
)

// JobItem is the display data of a selectable Jobs row.
type JobItem struct {
	ID, Title, Kind, Status, Target, Elapsed, Detail string
	Active                                           bool
}

// Summary formats a row for copying.
func (item JobItem) Summary() string {
	parts := make([]string, 0, 6)
	for _, value := range []string{item.Title, item.Kind, item.Status, item.Target, item.Elapsed, item.Detail} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " · ")
}

// JobsViewSpec supplies a snapshot of the Jobs page and its view state.
type JobsViewSpec struct {
	ActiveTab   string
	ActiveJobs  []JobItem
	HistoryJobs []JobItem
	Actions     []data.ActionLogEntry
	FilterQuery string
	SelectedIdx int
}

type jobRow struct {
	JobItem
	space, source string
}

// JobsView presents the Jobs page using the current time.
func JobsView(spec JobsViewSpec) ui.ViewNode {
	return JobsViewAt(spec, time.Now())
}

// JobsViewAt presents the Jobs page at a specified time.
func JobsViewAt(spec JobsViewSpec, now time.Time) ui.ViewNode {
	return JobsViewWithFilterModeAt(spec, now, traits.MatchSubstring)
}

// JobsViewWithFilterModeAt presents the Jobs page with the selected match mode.
func JobsViewWithFilterModeAt(spec JobsViewSpec, now time.Time, mode traits.MatchMode) ui.ViewNode {
	tab := jobsTab(spec.ActiveTab)
	rows := jobsRows(spec, now, mode)
	facts := map[string]any{
		"jobsTab":        tab,
		"activeCount":    len(spec.ActiveJobs),
		"historyCount":   len(spec.HistoryJobs),
		"actionCount":    len(spec.Actions),
		"hasSelection":   false,
		"selectedID":     "",
		"selectedDetail": "",
		"hasFilter":      tab != JobsTabAll && strings.TrimSpace(spec.FilterQuery) != "",
	}
	selected := spec.SelectedIdx
	if selected >= 0 && selected < len(rows) {
		facts["hasSelection"] = true
		facts["selectedID"] = rows[selected].ID
		facts["selectedDetail"] = rows[selected].Detail
	} else {
		selected = -1
	}

	tabs := []struct {
		id    string
		label string
		count int
	}{
		{JobsTabAll, "All", len(spec.ActiveJobs) + len(spec.HistoryJobs) + len(spec.Actions)},
		{JobsTabActive, "Active", len(spec.ActiveJobs)},
		{JobsTabHistory, "History", len(spec.HistoryJobs)},
		{JobsTabActions, "Action Log", len(spec.Actions)},
	}
	root := ui.ViewNode{
		ID: JobsListID, Kind: ui.KindTabs, Scope: ScopeJobs, Focusable: true, Facts: facts,
		Box: layout.Constraints{Flex: 1}, Props: map[string]any{"active": "jobs." + tab},
	}
	if tab != JobsTabAll {
		root.Filter = &ui.FilterSpec{ID: JobsListID, Prefix: "jobs "}
	}
	for _, def := range tabs {
		id := "jobs." + def.id
		child := ui.ViewNode{ID: id, Kind: ui.KindVStack}
		if def.id == tab {
			if tab == JobsTabAll {
				active := len(spec.ActiveJobs)
				child.Box.Flex = 1
				child.Gap = 1
				child.Children = []ui.ViewNode{
					jobsSection("jobs.all.active", "1. Actively Running", rows[:active], selected),
					jobsSection("jobs.all.history", "2. History & Action Log", rows[active:], selected-active),
				}
			} else {
				child = jobsTable(id, rows, selected, tab == JobsTabActions)
			}
		}
		child.Title = fmt.Sprintf("[%s (%d)]", def.label, def.count)
		root.Children = append(root.Children, child)
	}
	return root
}

// JobsRowsWithFilterModeAt uses the same filtering and order as the presenter.
func JobsRowsWithFilterModeAt(spec JobsViewSpec, now time.Time, mode traits.MatchMode) []JobItem {
	rows := jobsRows(spec, now, mode)
	items := make([]JobItem, len(rows))
	for i, row := range rows {
		items[i] = row.JobItem
	}
	return items
}

func jobsTab(tab string) string {
	switch tab {
	case JobsTabActive, JobsTabHistory, JobsTabActions:
		return tab
	default:
		return JobsTabAll
	}
}

func jobsRows(spec JobsViewSpec, now time.Time, mode traits.MatchMode) []jobRow {
	tab := jobsTab(spec.ActiveTab)
	var rows []jobRow
	if tab == JobsTabAll || tab == JobsTabActive {
		for _, item := range spec.ActiveJobs {
			rows = append(rows, jobRow{JobItem: item})
		}
	}
	if tab == JobsTabAll || tab == JobsTabHistory {
		for _, item := range spec.HistoryJobs {
			rows = append(rows, jobRow{JobItem: item})
		}
	}
	if tab == JobsTabAll || tab == JobsTabActions {
		for _, action := range spec.Actions {
			rows = append(rows, jobRow{
				JobItem: JobItem{
					ID: action.ID, Title: action.Command, Kind: "Action", Status: action.Outcome,
					Target: action.Target, Elapsed: formatRelativeAge(action.At, now), Detail: action.Detail,
				},
				space: action.Space, source: action.Source,
			})
		}
	}
	if tab == JobsTabAll || strings.TrimSpace(spec.FilterQuery) == "" {
		return rows
	}
	match, _ := traits.CompileMultiTerm(spec.FilterQuery, mode, traits.FilterConfig{})
	visible := rows[:0]
	for _, row := range rows {
		if match.MatchAny(row.ID, row.Title, row.Kind, row.Status, row.Target, row.Elapsed, row.Detail, row.space, row.source) {
			visible = append(visible, row)
		}
	}
	return visible
}

func jobsSection(id, title string, rows []jobRow, selected int) ui.ViewNode {
	var body ui.ViewNode
	height := len(rows) + 3
	if len(rows) == 0 {
		body = text(id+".empty", "No entries", "muted")
		height = 2
	} else {
		body = jobsTable(id+".rows", rows, selected, false)
	}
	return ui.ViewNode{
		ID: id, Kind: ui.KindVStack, Box: layout.Constraints{Flex: 1, MaxH: height},
		Children: []ui.ViewNode{fixed(text(id+".heading", title, "accent"), 1), body},
	}
}

func jobsTable(id string, rows []jobRow, selected int, actions bool) ui.ViewNode {
	columns := []any{"Job", "Kind", "Status", "Target", "Elapsed", "Detail"}
	if actions {
		columns = []any{"Command", "Outcome", "Target", "Space", "Source", "When", "Detail"}
	}
	cells := make([]any, 0, len(rows))
	for _, row := range rows {
		if actions {
			cells = append(cells, []any{row.Title, row.Status, row.Target, row.space, row.source, row.Elapsed, row.Detail})
		} else {
			cells = append(cells, []any{row.Title, row.Kind, row.Status, row.Target, row.Elapsed, row.Detail})
		}
	}
	return ui.ViewNode{
		ID: id, Kind: ui.KindTable, Box: layout.Constraints{Flex: 1},
		Props: map[string]any{"columns": columns, "rows": cells, "selected": selected},
	}
}
