package main

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/schedules"
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
		"add": "", "edit": "", "history": "", "run-now": "", "toggle": "", "set-interval": "", "reset": "",
	}, "move", "rename", "restore")
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
	executeA(t, a, "history-entry", "undo", "", nil)
	if got := s.String("schedule.slack-signals.enabled"); got != schedules.Off {
		t.Fatalf("undo of e: %q", got)
	}
	press(t, a, "i", "enter")
	if got := s.Duration("schedule.slack-signals.interval"); got != time.Minute {
		t.Fatalf("i: %s", got)
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if got := s.Duration("schedule.slack-signals.interval"); got != 5*time.Minute {
		t.Fatalf("undo of i: %s", got)
	}
	executeA(t, a, scheduleNoun, "set", "", ochazuke.Arguments{"name": "gh-signals", "props": map[string]any{"description": "mine", "enabled": "off"}})
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
	executeA(t, a, "history-entry", "undo", "", nil)
	back, ok := a.scheduleDef("sweep-the-inbox")
	if !ok || back.Prompt != before.Prompt || back.Every() != 30*time.Minute || back.Created != before.Created {
		t.Fatalf("undo: %+v, want %+v", back, before)
	}
	if _, _, err := cli(a, "", "schedule", "delete", "slack-signals", "--yes"); err == nil || !strings.Contains(err.Error(), "schedule disable slack-signals") {
		t.Fatalf("deleted a built-in: %v", err)
	}
}

// Every optional enum field clears to its default (empty text is a choice
// unless the field is strict), by edit and by set, and a bad value is
// refused in applyScheduleField's own words, not the prop enum's.
func TestScheduleOptionalEnumFieldsClearAndKeepTheirErrors(t *testing.T) {
	t.Parallel()
	a, _, _ := scheduleApp(t)
	if _, _, err := cli(a, "", "schedule", "add", "--name", "enum-check", "--command", "echo hi", "--when", "every 30m"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ field, good, bad, msg, cleared string }{
		{"fast", "on", "maybe", "fast: fast is on or off", ""},
		{"catch-up", "once", "later", "catch-up: catch-up is one of none, once", "none"},
		{"interval-from", "finish", "middle", "interval-from: interval-from is one of start, finish", ""},
		{"into", "pane", "bogus", "into: target is one of new, pane", ""},
	} {
		for _, verb := range []string{"set", "edit"} {
			set := func(v string) error {
				if verb == "set" {
					_, _, err := cli(a, "", "schedule", "set", "--name", "enum-check", tc.field+"="+v)
					return err
				}
				_, _, err := cli(a, "", "schedule", "edit", "enum-check", "--"+tc.field, v)
				return err
			}
			if err := set(tc.good); err != nil {
				t.Fatalf("%s %s=%s: %v", verb, tc.field, tc.good, err)
			}
			if got := scheduleField(mustSchedule(t, a, "enum-check"), tc.field); got != tc.good {
				t.Fatalf("%s %s stored %q", verb, tc.field, got)
			}
			want := tc.msg
			if verb == "edit" && tc.field == "into" {
				want = "into must be one of new, pane" // edit's own (non-free) parameter enum, as before
			}
			err := set(tc.bad)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s %s=%s: %v, want %q", verb, tc.field, tc.bad, err, want)
			}
			if verb == "edit" && tc.field == "into" {
				continue // edit's own parameter enum refuses an empty into (unchanged)
			}
			if err := set(""); err != nil {
				t.Fatalf("%s clear %s: %v", verb, tc.field, err)
			}
			if got := scheduleField(mustSchedule(t, a, "enum-check"), tc.field); got != tc.cleared {
				t.Fatalf("%s clear %s stored %q", verb, tc.field, got)
			}
		}
	}
}

func mustSchedule(t *testing.T, a *App, name string) schedules.Def {
	t.Helper()
	d, err := a.scheduleNamed(name)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
