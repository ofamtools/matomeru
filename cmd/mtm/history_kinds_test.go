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
		"context-item.list", "dirs.bases", "dirs.list", "dirs.refresh",
		"extension.available", "extension.list",
		"harness.list", "harness.models", "harness.names",
		"note.list", "pane.activity", "pane.list", "pane.preview", "pane.read-turns", "pane.transcript",
		"panes.refresh", "projects.directories", "projects.list", "projects.state",
		"review.list", "saved.list", "schedule.history", "schedule.list", "schedule.show", "session.list", "setup.list", "setup.plan",
		"store.tables", "task.list", "vim.list",
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
