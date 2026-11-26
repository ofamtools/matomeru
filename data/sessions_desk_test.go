package data

import (
	"slices"
	"testing"
	"time"
)

func sessionDeskPane(id string, updated time.Time, fields map[string]any) Record {
	return Record{Kind: KindPane, ID: id, Fields: fields, UpdatedAt: updated}
}

func sessionDeskIDs(rows []SessionDeskRow) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.PaneID
	}
	return ids
}

func TestBuildSessionDeskRowsJoinsAndSortsByAttention(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	panes := []Record{
		sessionDeskPane("idle1", now.Add(-time.Minute), map[string]any{"title": "codex: docs", "harness": "codex", "runStatus": "idle"}),
		sessionDeskPane("run-old", now.Add(-2*time.Hour), map[string]any{"title": "claude: parser", "harness": "claude", "runStatus": "running"}),
		sessionDeskPane("run-new", now.Add(-time.Hour), map[string]any{"harness": "claude", "runStatus": "idle",
			FieldWatchMode: WatchModeOnDone, FieldWatchQueues: "nightly"}),
		sessionDeskPane("ask", now.Add(-3*time.Hour), map[string]any{"title": "claude: lexer", "harness": "claude", "runStatus": "running"}),
		sessionDeskPane("gone", now, map[string]any{"title": "old", "harness": "claude", "runStatus": "exited"}),
	}
	lanes := []Record{
		{Kind: KindLane, ID: "l-done", Fields: map[string]any{"title": "Finished lane", "laneStatus": "done"}},
		{Kind: KindLane, ID: "l-run", Fields: map[string]any{"title": "Parser lane", "laneStatus": "running", FieldQueueName: "bugs"}},
		{Kind: KindLane, ID: "l-arch", Fields: map[string]any{"title": "Archived lane", "laneStatus": "running"}, ArchiveStatus: ArchiveArchived},
	}
	claims := map[string][]string{"run-old": {"l-arch", "l-done", "l-run"}, "idle1": {"missing"}}
	checklists := map[string]*SessionChecklist{"run-old": {
		UpdatedAt: now.Add(-30 * time.Minute),
		Steps: []ChecklistStep{
			{Title: "Read the code", Status: StepDone},
			{Title: "Fix the lexer", Status: StepInProgress},
			{Title: "Run the tests", Status: StepPending},
		},
	}}
	statuses := map[string]string{"ask": "needs_you", "run-new": "running", "idle1": "done"}
	signals := SessionDeskSignals{
		PaneProjects: map[string]string{"run-old": "p1", "ask": "p1"},
		Unread:       map[string]bool{"ask": true},
	}

	rows := BuildSessionDeskRows(panes, lanes, claims, map[string]string{"p1": "Apollo"}, checklists, statuses, signals)

	if got, want := sessionDeskIDs(rows), []string{"ask", "run-old", "run-new", "idle1", "gone"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v (needs you, then running newest first, then idle, then exited)", got, want)
	}
	byID := map[string]SessionDeskRow{}
	for _, r := range rows {
		byID[r.PaneID] = r
	}

	ask := byID["ask"]
	if ask.RunStatus != SessionNeedsYou || !ask.NeedsYou || !ask.Unread || ask.ProjectTitle != "Apollo" || ask.PaneRef != panes[3].Ref() {
		t.Errorf("needs-you row = %+v", ask)
	}

	run := byID["run-old"]
	if run.LaneID != "l-run" || run.LaneTitle != "Parser lane" || run.QueueName != "bugs" {
		t.Errorf("lane = %q %q queue %q, want the running claimed lane and its queue", run.LaneID, run.LaneTitle, run.QueueName)
	}
	if run.ChecklistDone != 1 || run.ChecklistTotal != 3 || run.ActiveStep != "Fix the lexer" {
		t.Errorf("checklist = %d/%d step %q", run.ChecklistDone, run.ChecklistTotal, run.ActiveStep)
	}
	if !run.UpdatedAt.Equal(now.Add(-30 * time.Minute)) {
		t.Errorf("updatedAt = %v, want the checklist's newer time", run.UpdatedAt)
	}

	watcher := byID["run-new"]
	if watcher.Title != "claude" || watcher.RunStatus != SessionRunning || watcher.QueueName != "nightly" {
		t.Errorf("watcher row = %+v, want the harness as title, the live state over runStatus, and the watched queue", watcher)
	}
	if idle := byID["idle1"]; idle.RunStatus != SessionIdle || idle.LaneID != "" || idle.NeedsYou {
		t.Errorf("idle row = %+v", idle)
	}
	if byID["gone"].RunStatus != SessionExited {
		t.Errorf("exited row = %+v", byID["gone"])
	}

	if got, want := CountSessionDeskRows(rows), (SessionDeskTotals{Running: 2, NeedsYou: 1, Idle: 1, Exited: 1, Total: 5}); got != want {
		t.Errorf("totals = %+v, want %+v", got, want)
	}
}

func TestSortAndGroupSessionDeskRows(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	rows := []SessionDeskRow{
		{PaneID: "a", Title: "zeta", Harness: "codex", ProjectTitle: "Borealis", RunStatus: SessionIdle, UpdatedAt: now},
		{PaneID: "b", Title: "Alpha", Harness: "claude", RunStatus: SessionRunning, UpdatedAt: now.Add(-time.Hour)},
		{PaneID: "c", Title: "mid", Harness: "codex", ProjectTitle: "Apollo", RunStatus: SessionWaiting, UpdatedAt: now.Add(-2 * time.Hour)},
	}
	for by, want := range map[string][]string{
		SessionSortAttention: {"c", "b", "a"},
		SessionSortRecent:    {"a", "b", "c"},
		SessionSortProject:   {"c", "a", "b"}, // no project sorts last
		SessionSortTitle:     {"b", "c", "a"},
		"nonsense":           {"c", "b", "a"},
	} {
		if got := sessionDeskIDs(SortSessionDeskRows(rows, by)); !slices.Equal(got, want) {
			t.Errorf("sort %s = %v, want %v", by, got, want)
		}
	}
	for by, want := range map[string][]string{
		SessionGroupNone:    {"a", "b", "c"},
		SessionGroupHarness: {"a", "c", "b"}, // groups in order of first appearance
		SessionGroupStatus:  {"c", "b", "a"}, // status groups follow the attention order
		SessionGroupProject: {"a", "b", "c"},
	} {
		if got := sessionDeskIDs(GroupSessionDeskRows(rows, by)); !slices.Equal(got, want) {
			t.Errorf("group %s = %v, want %v", by, got, want)
		}
	}
	if rows[0].PaneID != "a" {
		t.Error("sorting or grouping changed the caller's slice")
	}
}
