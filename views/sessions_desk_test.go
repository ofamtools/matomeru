package views

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func sessionsDeskFixture(now time.Time) []data.SessionDeskRow {
	return []data.SessionDeskRow{
		{
			PaneID: "pane1", PaneRef: core.Ref{Kind: data.KindPane, ID: "pane1"}, Title: "parser pane", Harness: "claude",
			ProjectID: "p1", ProjectTitle: "Apollo", QueueName: "bugs", LaneID: "l1", LaneTitle: "Parser lane",
			RunStatus: data.SessionNeedsYou, NeedsYou: true, Unread: true,
			ActiveStep: "Fix the lexer", ChecklistDone: 3, ChecklistTotal: 5, UpdatedAt: now.Add(-5 * time.Minute),
		},
		{PaneID: "pane2", Title: "codex: docs", Harness: "codex", RunStatus: data.SessionIdle, UpdatedAt: now.Add(-72 * time.Hour)},
	}
}

func sessionsDeskCells(t *testing.T, page ui.ViewNode) (columns []any, rows []ui.TableRowSpec) {
	t.Helper()
	table := page.Find(SessionsDeskTableID)
	if table == nil || table.Kind != ui.KindTable || !table.Focusable {
		t.Fatalf("sessions table = %+v", table)
	}
	for _, r := range table.Props["rows"].([]any) {
		rows = append(rows, r.(ui.TableRowSpec))
	}
	return table.Props["columns"].([]any), rows
}

func TestSessionsDeskViewTableHeaderAndFacts(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	rows := sessionsDeskFixture(now)
	page := SessionsDeskView(SessionsDeskSpec{
		Rows: rows, SelectedIdx: 0, Totals: data.CountSessionDeskRows(rows), ShowChecklist: true, Now: now,
		GroupBy: data.SessionGroupNone, SortBy: data.SessionSortAttention,
	})
	if page.ID != SessionsDeskPageID || page.Scope != ScopeSessionsDesk {
		t.Fatalf("page = %s scope %s", page.ID, page.Scope)
	}
	for k, want := range map[string]any{"hasSelection": true, "sessionPaneId": "pane1", "sessionStatus": data.SessionNeedsYou,
		"sessionNeedsYou": true, "sessionProjectId": "p1", "count": 2, "total": 2, "running": 0, "needsYou": 1, "idle": 1,
		"hasFilter": false, "showChecklist": true} {
		if page.Facts[k] != want {
			t.Errorf("fact %s = %v, want %v", k, page.Facts[k], want)
		}
	}
	header := page.Find("sessions.desk.header").Props["text"].(string)
	for _, want := range []string{"0 running", "1 needs you", "1 idle", "2 total"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q does not show %q", header, want)
		}
	}
	if strings.Contains(header, "grouped by") || strings.Contains(header, "sorted by") {
		t.Errorf("header %q names the default grouping or order", header)
	}

	table := page.Find(SessionsDeskTableID)
	if table.Filter == nil || table.Filter.ID != SessionsDeskTableID {
		t.Fatalf("the table declares no filter: %+v", table.Filter)
	}
	if table.Props["selected"] != 0 {
		t.Errorf("selected = %v", table.Props["selected"])
	}
	columns, cells := sessionsDeskCells(t, page)
	if len(columns) != 7 || columns[4] != "Progress" || columns[5] != "Active step" {
		t.Fatalf("columns = %v", columns)
	}
	want := []any{"◆ needs you", "● parser pane (claude)", "Apollo", "bugs ▸ Parser lane", "███░░░ 3/5", "Fix the lexer", "5m ago"}
	for i, w := range want {
		if cells[0].Cells[i] != w {
			t.Errorf("cell %d = %q, want %q", i, cells[0].Cells[i], w)
		}
	}
	if cells[0].CellTones[0] != "state.needs-input" || cells[0].CellTones[1] != "unread" {
		t.Errorf("tones = %v", cells[0].CellTones)
	}
	if got := cells[1].Cells; got[0] != "○ idle" || got[1] != "codex: docs" || got[2] != "—" || got[4] != "—" || got[6] != "3d ago" {
		t.Errorf("idle row = %v", got)
	}
	if hint := page.Find(SessionsDeskHintID); hint == nil || !strings.Contains(hint.Props["text"].(string), "n new session") {
		t.Errorf("hint = %+v", hint)
	}
}

func TestSessionsDeskViewChecklistOffFilterAndEmpty(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	rows := sessionsDeskFixture(now)
	page := SessionsDeskView(SessionsDeskSpec{
		Rows: rows[:1], SelectedIdx: -1, Totals: data.CountSessionDeskRows(rows), FilterQuery: "parser", Now: now,
		GroupBy: data.SessionGroupProject, SortBy: data.SessionSortRecent,
	})
	columns, _ := sessionsDeskCells(t, page)
	if len(columns) != 5 {
		t.Fatalf("with the checklist off columns = %v, want no Progress or Active step", columns)
	}
	if page.Facts["hasSelection"] != false || page.Facts["sessionPaneId"] != "" || page.Facts["hasFilter"] != true || page.Facts["count"] != 1 {
		t.Errorf("facts = %v", page.Facts)
	}
	header := page.Find("sessions.desk.header").Props["text"].(string)
	for _, want := range []string{"2 total", "1 shown", "grouped by project", "sorted by recent"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q does not show %q", header, want)
		}
	}
	if f := page.Find("sessions.desk.filter"); f == nil || f.Props["text"] != " /parser" {
		t.Errorf("filter line = %+v", f)
	}

	empty := SessionsDeskView(SessionsDeskSpec{SelectedIdx: -1, Now: now})
	if n := empty.Find(SessionsDeskTableID); n == nil || n.Kind != ui.KindText || !strings.Contains(n.Props["text"].(string), "n starts one") {
		t.Errorf("empty desk = %+v", n)
	}
}

func TestSessionProgressBar(t *testing.T) {
	for _, tc := range []struct {
		done, total int
		want, tone  string
	}{
		{0, 0, "—", "muted"},
		{0, 4, "░░░░ 0/4", "state.working"},
		{1, 9, "█░░░ 1/9", "state.working"},
		{2, 4, "██░░ 2/4", "state.working"},
		{4, 4, "████ 4/4", "state.done"},
	} {
		if got, tone := SessionProgressBar(tc.done, tc.total, 4); got != tc.want || tone != tc.tone {
			t.Errorf("bar %d/%d = %q %s, want %q %s", tc.done, tc.total, got, tone, tc.want, tc.tone)
		}
	}
}
