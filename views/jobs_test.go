package views

import (
	"maps"
	"slices"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

func TestJobsViewAll(t *testing.T) {
	now := time.Date(2026, 11, 26, 11, 30, 0, 0, time.UTC)
	spec := JobsViewSpec{
		ActiveJobs:  []JobItem{{ID: "live", Title: "Build site", Status: "running", Active: true}},
		HistoryJobs: []JobItem{{ID: "done", Title: "Generate index", Status: "complete"}},
		Actions: []data.ActionLogEntry{{
			ID: "entry", Command: "example.refresh", At: now.Add(-2 * time.Minute), Outcome: "ok", Detail: "Index updated",
		}},
		FilterQuery: "ignored on summary",
		SelectedIdx: 2,
	}
	node := JobsViewAt(spec, now)
	if node.ID != JobsListID || node.Scope != ScopeJobs || node.Filter != nil {
		t.Fatalf("All root identity/filter: %q / %q / %+v", node.ID, node.Scope, node.Filter)
	}
	wantFacts := map[string]any{
		"jobsTab": JobsTabAll, "activeCount": 1, "historyCount": 1, "actionCount": 1,
		"hasSelection": true, "selectedID": "entry", "selectedDetail": "Index updated", "hasFilter": false,
	}
	if !maps.Equal(node.Facts, wantFacts) {
		t.Fatalf("facts = %+v, want %+v", node.Facts, wantFacts)
	}
	if titles := []string{node.Children[0].Title, node.Children[1].Title, node.Children[2].Title, node.Children[3].Title}; !slices.Equal(titles, []string{"[All (3)]", "[Active (1)]", "[History (1)]", "[Action Log (1)]"}) {
		t.Fatalf("tab titles = %v", titles)
	}
	rendered := frame(t, node, JobsListID, 110, 18)
	wantText(t, rendered, "1. Actively Running", "Build site", "running", "2. History & Action Log", "Generate index", "example.refresh", "2m ago")
}

func TestJobsViewFilterAndSelection(t *testing.T) {
	now := time.Date(2026, 11, 26, 11, 30, 0, 0, time.UTC)
	spec := JobsViewSpec{
		ActiveTab:   JobsTabActions,
		ActiveJobs:  []JobItem{{ID: "live", Title: "Build site"}},
		HistoryJobs: []JobItem{{ID: "done", Title: "Generate index"}},
		Actions: []data.ActionLogEntry{
			{ID: "first", Command: "example.refresh", Space: "work", Outcome: "ok"},
			{ID: "second", Command: "example.open", Space: "jobs", Source: "binding", Outcome: "error", Detail: "Unavailable"},
		},
		FilterQuery: "BINDING error",
		SelectedIdx: 0,
	}
	node := JobsViewAt(spec, now)
	if node.Filter == nil || node.Filter.ID != JobsListID || node.Filter.Prefix != "jobs " {
		t.Fatalf("Actions filter = %+v", node.Filter)
	}
	if node.Facts["hasSelection"] != true || node.Facts["selectedID"] != "second" || node.Facts["selectedDetail"] != "Unavailable" ||
		node.Facts["hasFilter"] != true || node.Facts["actionCount"] != 2 {
		t.Fatalf("filtered facts = %+v", node.Facts)
	}
	if child := node.Children[3]; child.Kind != ui.KindTable || len(child.Props["rows"].([]any)) != 1 {
		t.Fatalf("Actions tab table = %+v", child)
	}
	rendered := frame(t, node, JobsListID, 110, 18)
	wantText(t, rendered, "Command", "Outcome", "Source", "example.open", "binding", "Unavailable")

	spec.FilterQuery = "^example\\.refresh$"
	rows := JobsRowsWithFilterModeAt(spec, now, traits.MatchRegex)
	if len(rows) != 1 || rows[0].ID != "first" {
		t.Fatalf("regex-filtered rows = %+v", rows)
	}
	spec.FilterQuery = "missing"
	node = JobsViewAt(spec, now)
	if node.Facts["hasSelection"] != false || node.Facts["selectedID"] != "" || node.Facts["selectedDetail"] != "" {
		t.Fatalf("empty selection facts = %+v", node.Facts)
	}
}

func TestJobsViewActiveAndHistoryTabs(t *testing.T) {
	now := time.Date(2026, 11, 26, 11, 30, 0, 0, time.UTC)
	spec := JobsViewSpec{
		ActiveJobs:  []JobItem{{ID: "live", Title: "Build site"}},
		HistoryJobs: []JobItem{{ID: "done", Title: "Generate index"}},
	}
	for _, tab := range []struct {
		name string
		id   string
		idx  int
	}{
		{JobsTabActive, "live", 1},
		{JobsTabHistory, "done", 2},
	} {
		spec.ActiveTab = tab.name
		node := JobsViewAt(spec, now)
		if node.Filter == nil || node.Facts["jobsTab"] != tab.name || node.Facts["selectedID"] != tab.id {
			t.Fatalf("%q facts/filter = %+v / %+v", tab.name, node.Facts, node.Filter)
		}
		if child := node.Children[tab.idx]; child.Kind != ui.KindTable || len(child.Props["rows"].([]any)) != 1 {
			t.Fatalf("%q table = %+v", tab.name, child)
		}
	}
}
