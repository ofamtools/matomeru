package main

import (
	"slices"
	"sort"
	"testing"

	"ofam.dev/ochazuke"
)

// mtm's application verbs that only read are fetches (NounBuilder.Fetch, or
// Rows/State), so a list, a reload or a pick-list source never fills the
// user's history or dot-repeat; every other application verb of mtm's own
// stays a change. A new read verb goes in this list with .Fetch().
func TestReadVerbsAreFetches(t *testing.T) {
	t.Parallel()
	app := open(t)
	want := []string{
		"action-item.count", "action-item.get", "action-item.list", "action-item.watch",
		"context-item.get", "context-item.list", "context-item.watch", "dirs.bases", "dirs.list", "dirs.refresh",
		"extension.available", "extension.list", "extension.setup-plan", "extension.setup-status",
		"harness.list", "harness.models", "harness.names",
		"note.get", "note.list", "note.watch", "ochazuke.markdown-view.copy", "pane.activity", "pane.get", "pane.list", "pane.preview", "pane.read-settings", "pane.read-turns", "pane.refresh-turns", "pane.settings", "pane.transcript", "pane.watch", "pane.whoami",
		"panes.read-activities", "panes.refresh", "project.get", "project.list", "project.watch", "projects.directories", "projects.list", "projects.state",
		"review.get", "review.list", "review.watch", "run.list", "run.show", "saved.get", "saved.list", "saved.watch", "schedule.get", "schedule.history", "schedule.list", "schedule.watch", "session.get", "session.list", "session.watch", "setup.list", "setup.plan",
		"side.list",
		"store-table.list", "store-table.show", "store.tables", "task.get", "task.list", "task.watch", "turn-line.refresh-turns", "turn-separator.refresh-turns", "turn-step.refresh-turns", "turn.refresh-turns", "vim.get", "vim.list", "vim.watch",
	}
	var got []string
	for key, op := range app.r.Operations {
		if op.Kind != ochazuke.KindApplication || frameworkVerbs()[key] {
			continue
		}
		if op.HistoryKindOf() == ochazuke.HistoryFetch {
			got = append(got, key)
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("fetch verbs\n got %v\nwant %v", got, want)
	}
	for _, key := range []string{"agent.launch", "agent.start", "agent.choose", "side.show", "side.restart"} {
		op, ok := app.r.Operations[key]
		if !ok || op.HistoryKindOf() != ochazuke.HistoryChange {
			t.Errorf("%s must remain an app change, got %+v", key, op)
		}
	}
}
