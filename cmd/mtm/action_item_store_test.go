package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
)

// aliasOf is the alias named name among action's aliases, nil for none.
func aliasOf(a *App, action, name string) *ochazuke.ActionAlias {
	for _, alias := range a.r.AliasesOf(action) {
		if alias.Alias == name {
			return &alias
		}
	}
	return nil
}

// The action item's verbs: the standard ones with rename (the title), move,
// duplicate, archive and restore; show, read, unread and toggle-read lasting aliases; add,
// count, open, snooze, the promotions, associate and choose-move named.
func TestActionItemVerbsAreStandard(t *testing.T) {
	a, _, _ := inboxApp(t)
	standardIs(t, a, actionItemNoun, map[string]string{
		"get": "get", "list": "list", "create": "create", "set": "set", "delete": "delete", "watch": "watch",
		"move": "move", "duplicate": "duplicate", "archive": "archive", "restore": "restore", "rename": "rename",
		"add": "", "count": "", "open": "", "snooze": "", "promote-todo": "", "promote-task": "", "associate": "", "choose-move": "",
	})
	for alias, action := range map[string]string{"show": "get", "read": "set", "unread": "set", "toggle-read": "set"} {
		got := aliasOf(a, actionItemNoun+"."+action, actionItemNoun+"."+alias)
		if got == nil || got.Deprecated() {
			t.Errorf("%s: %+v", alias, got)
		}
	}
}

// show ID is get: the item's props; list keeps --state (open by default)
// and --source; create always adds a row of its own (add refreshes a
// duplicate); the label falls back to the summary.
func TestActionItemShowListCreate(t *testing.T) {
	a, _, clock := inboxApp(t)
	pr, ping := seedInbox(t, a, clock)
	var shown map[string]any
	cliJSON(t, a, &shown, "action-item", "show", pr.ID)
	if shown["title"] != "Lift the store" || shown["project"] != "matomeru" || shown["state"] != "open" || shown["source"] != "gh" {
		t.Fatalf("show: %v", shown)
	}
	if _, err := a.inbox.items.Archive(ping.ID); err != nil {
		t.Fatal(err)
	}
	var open, archived, slack []actionThing
	cliJSON(t, a, &open, "action-item", "list")
	cliJSON(t, a, &archived, "action-item", "list", "--state", "archived")
	cliJSON(t, a, &slack, "action-item", "list", "--state", "all", "--source", "slack")
	if len(open) != 1 || open[0].ID != pr.ID || len(archived) != 1 || archived[0].ID != ping.ID || len(slack) != 1 || slack[0].ID != ping.ID {
		t.Fatalf("list: open %+v archived %+v slack %+v", open, archived, slack)
	}
	var made map[string]any
	cliJSON(t, a, &made, "action-item", "create", "--source", "gh", "--url", "https://github.com/o/r/pull/9", "--why", "again")
	if made["id"] == pr.ID || made["title"] != "" || made["summary"] != "again" {
		t.Fatalf("create: %v", made)
	}
	if items, _ := a.inbox.items.All(); len(items) != 3 {
		t.Fatalf("create merged: %d rows", len(items))
	}
}

// d d asks the store's prompt (x archives instead) and deletes the row;
// history.undo puts it back as it was. x and X are the standard archive
// and restore, and undo reverts an archive.
func TestActionItemDeleteKeyAndUndo(t *testing.T) {
	a, _, clock := inboxApp(t)
	pr, _ := seedInbox(t, a, clock)
	toPage(t, a, inboxPage)
	if err := a.inbox.list.Select(pr.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "x")
	if it, _ := a.inbox.items.Get(pr.ID); it.State(*clock) != actionitems.StateArchived || !it.Read() {
		t.Fatalf("x: %+v", it)
	}
	executeA(t, a, "history", "undo", "", nil)
	// The archive's side change (it reads the item) is undone too
	// (ochazuke v0.55: undo restores every prop the write changed).
	if it, _ := a.inbox.items.Get(pr.ID); it.State(*clock) != actionitems.StateOpen || it.Read() {
		t.Fatalf("undo of x: %+v", it)
	}
	before, _ := a.inbox.items.Get(pr.ID)
	if err := a.inbox.list.Select(pr.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "d", "d")
	if fr := frame(a); !strings.Contains(fr, "Delete Lift the store? x archives it instead.") {
		t.Fatalf("d d:\n%s", fr)
	}
	press(t, a, "y")
	if _, err := a.inbox.items.Get(pr.ID); err == nil {
		t.Fatal("y kept the row")
	}
	executeA(t, a, "history", "undo", "", nil)
	back, err := a.inbox.items.Get(pr.ID)
	back.Updated, before.Updated = 0, 0
	if err != nil || !reflect.DeepEqual(back, before) {
		t.Fatalf("undo: %+v %v, want %+v", back, err, before)
	}
}

// A move takes the project and keeps the pane; associate's old forms say
// they are old and run set; set writes several props at once.
func TestActionItemMoveAndAssociate(t *testing.T) {
	a, _, clock := inboxApp(t)
	pr, _ := seedInbox(t, a, clock)
	if _, err := a.inbox.items.Associate(pr.ID, actionitems.Owner{ProjectID: "matomeru", Window: "w1", Session: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, actionItemNoun, "move", "", ochazuke.Arguments{"id": pr.ID, "to": "ochazuke"})
	if it, _ := a.inbox.items.Get(pr.ID); it.ProjectID != "ochazuke" || it.Window != "w1" {
		t.Fatalf("move: %+v", it)
	}
	out, _, err := cli(a, "", "action-item", "associate", pr.ID, "--task", "t1")
	if err != nil || !strings.Contains(out, `"task": "t1"`) {
		t.Fatalf("associate --task: %s %v", out, err)
	}
	if it, _ := a.inbox.items.Get(pr.ID); it.TaskID != "t1" || it.ProjectID != "matomeru" || it.Window != "" {
		t.Fatalf("associate --task: %+v", it)
	}
	executeA(t, a, actionItemNoun, "set", "", ochazuke.Arguments{"id": pr.ID, "changes": map[string]any{"title": "Lift it", "read": true}})
	if it, _ := a.inbox.items.Get(pr.ID); it.Title != "Lift it" || !it.Read() {
		t.Fatalf("set: %+v", it)
	}
}

// An archive ends a snooze; its undo puts the snooze back and clears the
// archive and the read mark (every prop the write changed, though the
// request named one).
func TestActionItemUndoOfArchiveKeepsTheSnooze(t *testing.T) {
	a, _, clock := inboxApp(t)
	pr, _ := seedInbox(t, a, clock)
	until := *clock + int64(time.Hour/time.Millisecond)
	executeA(t, a, "action-item", "set", "", ochazuke.Arguments{"id": pr.ID, "changes": map[string]any{"read": false, "snoozed-until": until}})
	if it, _ := a.inbox.items.Get(pr.ID); it.SnoozedUntil != until || it.Read() {
		t.Fatalf("snooze: %+v", it)
	}
	executeA(t, a, "action-item", "archive", "", ochazuke.Arguments{"id": pr.ID})
	if it, _ := a.inbox.items.Get(pr.ID); it.ArchivedAt == 0 || it.SnoozedUntil != 0 || !it.Read() {
		t.Fatalf("archive: %+v", it)
	}
	executeA(t, a, "history", "undo", "", nil)
	if it, _ := a.inbox.items.Get(pr.ID); it.ArchivedAt != 0 || it.SnoozedUntil != until || it.Read() {
		t.Fatalf("undo of archive: %+v", it)
	}
}
