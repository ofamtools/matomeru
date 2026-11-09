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
// Framework verbs (Operation.Builtin) are checked by ochazuke.
func TestReadVerbsAreFetches(t *testing.T) {
	t.Parallel()
	app := open(t)
	want := []string{
		"agent.guide", "choice.list", "agent.peek-prompt", "agent.scan", "agent.status", "pane.capture", "pane.wait", "session.events", "session.list-panes",
		"action-item.count", "action-item.get", "action-item.list", "action-item.watch",
		"context-item.get", "context-item.list", "context-item.watch", "daemon.show-log", "daemon.status", "dirs.bases", "dirs.refresh",
		"extension.all", "extension.available", "extension.peer-rows", "extension.peers", "extension.setup-plan", "extension.setup-status",
		"harness.all", "harness.models", "harness.names", "history-log.all", "history-log.show", "license.all", "license.show",
		"note.get", "note.list", "note.watch", "pane.activity", "pane.get", "pane.list", "pane.preview", "pane.read-settings", "pane.read-turns", "pane.show", "pane.refresh", "pane.refresh-turns", "pane.settings", "pane.transcript", "pane.watch", "pane.whoami",
		"panes.read-activities", "panes.refresh", "project.get", "project.list", "project.watch", "projects.all", "projects.directories", "projects.names", "projects.state",
		"reminder.get", "reminder.history", "reminder.list", "reminder.notify-status", "reminder.watch",
		"review.get", "review.list", "review.watch", "run.all", "run.show", "saved.get", "saved.list", "saved.watch", "schedule.get", "schedule.history", "schedule.list", "schedule.watch", "session.get", "session.list", "session.refresh", "session.watch", "setup.all", "setup.plan",
		"side.all",
		"store-table.all", "store-table.show", "store.tables", "task.get", "task.list", "task.watch", "turn-line.refresh-turns", "turn-separator.refresh-turns", "turn-step.refresh-turns", "turn.refresh-turns", "vim.get", "vim.list", "vim.watch",
	}
	var got []string
	for key, op := range app.r.Operations.Entries() {
		if op.Kind != ochazuke.KindApplication || op.Builtin {
			continue
		}
		if op.HistoryKindOf() == ochazuke.HistoryFetch {
			got = append(got, key)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("fetch verbs\n got %v\nwant %v", got, want)
	}
	for _, key := range []string{"agent.launch", "agent.start", "agent.choose", "side.show", "side.restart", "reminder.notify-due"} {
		op, ok := app.r.Operations.Find(ochazuke.Key(key))
		if !ok || op.HistoryKindOf() != ochazuke.HistoryChange {
			t.Errorf("%s must remain an app change, got %+v", key, op)
		}
	}
}
