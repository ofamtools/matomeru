package main

import (
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/schedules"
)

// The schedule verbs: get, list, create, set, delete and watch standard;
// no rename (its name is the key), no move; show, enable and disable
// lasting aliases; add, edit, duplicate, history, run-now, toggle,
// set-interval and reset named.
func TestScheduleVerbsAreStandard(t *testing.T) {
	t.Parallel()
	a, _, _ := scheduleApp(t)
	standardIs(t, a, scheduleNoun, map[string]string{
		"get": "get", "list": "list", "create": "create", "set": "set", "delete": "delete", "watch": "watch",
		"add": "", "edit": "", "duplicate": "", "history": "", "run-now": "", "toggle": "", "set-interval": "", "reset": "",
	}, "rename", "move")
	for alias, action := range map[string]string{"show": "get", "enable": "set", "disable": "set"} {
		if got := aliasOf(a, scheduleNoun+"."+action, scheduleNoun+"."+alias); got == nil || got.Deprecated() {
			t.Errorf("%s: %+v", alias, got)
		}
	}
}

// e and i on the page are the standard set of enabled and interval, and
// undo reverts them; set writes a field to a stored copy and enabled to
// the row's setting.
func TestScheduleKeysAreSetsAndUndo(t *testing.T) {
	t.Parallel()
	a, _, _ := scheduleApp(t)
	s := a.r.Settings()
	toPage(t, a, inboxPage)
	press(t, a, "S", "j", "e")
	if got := s.String("schedule.slack-signals.enabled"); got != schedules.On {
		t.Fatalf("e: %q", got)
	}
	executeA(t, a, "history", "undo", "", nil)
	if got := s.String("schedule.slack-signals.enabled"); got != schedules.Off {
		t.Fatalf("undo of e: %q", got)
	}
	press(t, a, "i", "enter")
	if got := s.Duration("schedule.slack-signals.interval"); got != time.Minute {
		t.Fatalf("i: %s", got)
	}
	executeA(t, a, "history", "undo", "", nil)
	if got := s.Duration("schedule.slack-signals.interval"); got != 5*time.Minute {
		t.Fatalf("undo of i: %s", got)
	}
	executeA(t, a, scheduleNoun, "set", "", ochazuke.Arguments{"name": "gh-signals", "changes": map[string]any{"description": "mine", "enabled": "off"}})
	d, _ := a.scheduleDef("gh-signals")
	if d.Source != schedules.SourceStored || d.Description != "mine" || d.Enabled != schedules.Off {
		t.Fatalf("set: %+v", d)
	}
}

// show is get with the recent runs; create names a row after its prompt;
// d d asks and deletes a stored row, undo puts it back as it was; a
// built-in is refused.
func TestScheduleShowCreateDeleteAndUndo(t *testing.T) {
	t.Parallel()
	a, _, _ := scheduleApp(t)
	mustCLI(t, a, "schedule", "run-now", "gh-signals")
	var shown scheduleAnswerJSON
	cliJSON(t, a, &shown, "schedule", "show", "gh-signals")
	if shown.Name != "gh-signals" || len(shown.Runs) != 1 {
		t.Fatalf("show: %+v", shown)
	}
	made, _ := executeA(t, a, scheduleNoun, "create", "", ochazuke.Arguments{"prompt": "Sweep the inbox", "when": "every 30m"}).(map[string]any)
	if made["name"] != "sweep-the-inbox" || made["enabled"] != schedules.On || made["source"] != schedules.SourceStored {
		t.Fatalf("create: %v", made)
	}
	before, _ := a.scheduleDef("sweep-the-inbox")
	toPage(t, a, schedulesPage)
	if err := a.sched.list.Select(scheduleRowID("sweep-the-inbox")); err != nil {
		t.Fatal(err)
	}
	press(t, a, "d", "d")
	if fr := frame(a); !strings.Contains(fr, "Delete schedule sweep-the-inbox?") {
		t.Fatalf("d d:\n%s", fr)
	}
	press(t, a, "y")
	if _, ok := a.scheduleDef("sweep-the-inbox"); ok {
		t.Fatal("y kept the row")
	}
	executeA(t, a, "history", "undo", "", nil)
	back, ok := a.scheduleDef("sweep-the-inbox")
	if !ok || back.Prompt != before.Prompt || back.Every() != 30*time.Minute || back.Created != before.Created {
		t.Fatalf("undo: %+v, want %+v", back, before)
	}
	if _, _, err := cli(a, "", "schedule", "delete", "slack-signals", "--yes"); err == nil || !strings.Contains(err.Error(), "schedule disable slack-signals") {
		t.Fatalf("deleted a built-in: %v", err)
	}
}
