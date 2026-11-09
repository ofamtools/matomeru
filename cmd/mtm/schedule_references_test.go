package main

import (
	"reflect"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/schedules"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func TestScheduleReferenceChooserPersistsOnlySelection(t *testing.T) {
	t.Parallel()
	a, _, _ := scheduleApp(t)
	const alpha = "6f7a9d1c0b2245f6a8e30d4c5b219076"
	const beta = "a9e25f04781c46bda30f6d529b8170ce"
	for _, p := range []work.Project{{ID: alpha, Name: "alpha"}, {ID: beta, Name: "beta"}} {
		if err := a.work.PutProject(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, scheduleNoun, "add", "", ochazuke.Arguments{
		"name": "chooser-check", "prompt": "inspect", "when": "never", "project": alpha, "pane": "nightly-agent",
	})
	read := func() schedules.Def {
		t.Helper()
		defs, err := a.sched.runner.Stored()
		if err != nil || len(defs) != 1 {
			t.Fatalf("stored schedules: %+v %v", defs, err)
		}
		return defs[0]
	}
	if err := a.r.Settings().Set(settingEditDebounce, "1ms"); err != nil {
		t.Fatal(err)
	}
	before := read()
	if err := a.openScheduleForm("chooser-check", "Edit schedule chooser-check"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "esc")
	if err := a.r.SetFocus(scheduleFormID + "/project"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "i")
	for _, key := range []string{"ctrl+u", "b", "e", "t", "a"} {
		_, cmd := a.Update(keyMsg(key))
		// Settle the actual debounce command, not just job completions.
		settle(a, cmd)
		if got := read(); !reflect.DeepEqual(got, before) {
			t.Fatalf("search key %q persisted an intermediate reference: %+v, before %+v", key, got, before)
		}
		if notices := strings.Join(a.m.Notices, "\n"); strings.Contains(notices, "unknown project") {
			t.Fatalf("search key %q emitted an unknown-project notice: %s", key, notices)
		}
	}
	pressJob(t, a, "enter")
	if got := read(); got.Agent.Project != beta || got.Agent.Pane != "nightly-agent" {
		t.Fatalf("selection did not persist beta's id while keeping the pane name: %+v", got.Agent)
	}
	// Searching and cancelling also keeps the last committed reference.
	for _, key := range []string{"enter", "ctrl+u", "a", "esc", "q"} {
		pressJob(t, a, key)
	}
	if got := read(); got.Agent.Project != beta {
		t.Fatalf("cancel changed the selected reference: %+v", got.Agent)
	}
	// Clearing is an explicit choice, and undo restores the reference id.
	pressJob(t, a, "enter")
	pressJob(t, a, "ctrl+u")
	typeText(t, a, "(none)")
	pressJob(t, a, "enter")
	if got := read(); got.Agent.Project != "" {
		t.Fatalf("explicit none did not clear project: %+v", got.Agent)
	}
	pressJob(t, a, "u")
	if got := read(); got.Agent.Project != beta {
		t.Fatalf("undo did not restore beta's id: %+v", got.Agent)
	}
}

func TestScheduleDetailProjectLabelAndMissingFallback(t *testing.T) {
	t.Parallel()
	a, _, _ := scheduleApp(t)
	const id = "a9e25f04781c46bda30f6d529b8170ce"
	if err := a.work.PutProject(work.Project{ID: id, Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ project, want string }{{id, "beta"}, {"missing-project-id", "missing-project-id"}} {
		d := schedules.Def{Name: "detail-check", Kind: schedules.KindPrompt, Agent: schedules.Agent{Project: row.project}}
		text := a.scheduleFacts(a.viewSchedule(d, schedules.State{}, a.scheduleNow()))
		if !strings.Contains(text, row.want) || row.project == id && strings.Contains(text, id) {
			t.Fatalf("detail project %q: want %q without a resolved opaque id:\n%s", row.project, row.want, text)
		}
	}
}
