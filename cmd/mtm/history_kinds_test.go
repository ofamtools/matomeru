package main

import (
	"slices"
	"sort"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// mtm's application verbs that only read are fetches (NounBuilder.Fetch, or
// Rows/State), so a list, a reload or a pick-list source never fills the
// user's history or dot-repeat; every other application verb of mtm's own
// stays a change. A new read verb goes in this list with .Fetch().
func TestReadVerbsAreFetches(t *testing.T) {
	app := open(t)
	want := []string{
		"action-item.count", "action-item.list", "action-item.show",
		"context-item.get", "context-item.list", "context-item.watch", "dirs.bases", "dirs.list", "dirs.refresh",
		"extension.available", "extension.list",
		"harness.list", "harness.models", "harness.names",
		"note.get", "note.list", "note.watch", "pane.activity", "pane.list", "pane.preview", "pane.read-settings", "pane.read-turns", "pane.refresh-turns", "pane.settings", "pane.transcript",
		"panes.refresh", "project.get", "project.list", "project.watch", "projects.directories", "projects.list", "projects.state",
		"review.list", "run.list", "run.show", "saved.list", "schedule.history", "schedule.list", "schedule.show", "session.list", "setup.list", "setup.plan",
		"store-table.list", "store-table.show", "store.tables", "task.get", "task.list", "task.watch", "turn-line.refresh-turns", "turn-separator.refresh-turns", "turn-step.refresh-turns", "turn.refresh-turns", "vim.list",
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
}
