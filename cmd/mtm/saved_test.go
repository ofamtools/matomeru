package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func savedCall(t *testing.T, a *App, verb string, args ochazuke.Arguments) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "saved", Verb: verb, Target: savedListID, Args: args}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	for _, job := range a.r.Jobs() {
		if job.State == ochazuke.JobFailed {
			t.Fatalf("%s: %+v", verb, job)
		}
	}
}

func TestSavedNoProjectLiveReopenAndForgetNeverLaunchOrKill(t *testing.T) {
	a := open(t)
	f := &fakeDaemon{panes: a.panesd.(*fakePanes)}
	a.launcher = f
	d := &recordingDriver{Driver: f, work: a.work}
	id, err := d.OpenWindow(context.Background(), harness.Window{Session: mtmOnlySession, Name: "shell", Cwd: "/home/test"})
	if err != nil {
		t.Fatal(err)
	}
	savedCall(t, a, "save", ochazuke.Arguments{"pane": id, "name": "scratch shell"})
	rows, err := a.work.SavedPanes()
	if err != nil || len(rows) != 1 || rows[0].ProjectID != "" || rows[0].TaskID != "" {
		t.Fatalf("save unassociated: %+v, %v", rows, err)
	}
	savedCall(t, a, "reopen", ochazuke.Arguments{"id": rows[0].ID})
	if len(f.shells) != 1 || a.attached.ID != id {
		t.Fatalf("live reopen duplicated pane: shells=%d attached=%+v", len(f.shells), a.attached)
	}
	savedCall(t, a, "forget", ochazuke.Arguments{"id": rows[0].ID})
	if len(f.shells) != 1 || a.attached.ID != id {
		t.Fatal("forget disturbed live pane")
	}
}

func TestSavedRelaunchUsesRecordedArgvNotChangedHarnessDefaults(t *testing.T) {
	a := open(t)
	f := &fakeDaemon{panes: a.panesd.(*fakePanes)}
	a.launcher = f
	recipe := work.PaneRecipe{Window: "dead", Session: "arbitrary-session", Name: "agent", Cwd: "/home/test", Harness: "removed-harness", Model: "old-model", Command: []string{"/bin/sh", "-c", "printf '%s' 'a b'; exec /bin/sh"}, ProjectID: "matomeru"}
	row, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: recipe})
	if err != nil {
		t.Fatal(err)
	}
	savedCall(t, a, "reopen", ochazuke.Arguments{"id": row.ID})
	if len(f.launches) != 1 {
		t.Fatalf("launches: %+v", f.launches)
	}
	got := f.launches[0]
	if !reflect.DeepEqual(got.Command, recipe.Command) || got.Cwd != recipe.Cwd || got.Session != "matomeru" {
		t.Fatalf("relaunch changed recipe: %+v", got)
	}
	owner, err := a.work.PaneOwner(got.ID, "")
	if err != nil || owner.ProjectID != "matomeru" {
		t.Fatalf("owner not immediately persisted: %+v %v", owner, err)
	}
	persisted, err := a.work.SavedPane(row.ID)
	if err != nil || persisted.Window != got.ID || persisted.ID != row.ID {
		t.Fatalf("link: %+v %v", persisted, err)
	}
	savedCall(t, a, "reopen", ochazuke.Arguments{"id": row.ID})
	if len(f.launches) != 1 {
		t.Fatal("second reopen duplicated relaunched pane")
	}
}

func TestSavedEnvironmentKeepsArgumentBoundaries(t *testing.T) {
	got := savedCommand(work.PaneRecipe{Command: []string{"/bin/sh", "-c", "echo a b"}, Env: map[string]string{"Z": "a b", "A": "=quoted"}})
	want := []string{"env", "A==quoted", "Z=a b", "/bin/sh", "-c", "echo a b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q; want %q", got, want)
	}
}

func TestSavedRowsRespectPinAndAll(t *testing.T) {
	a := open(t)
	for _, owner := range []string{"matomeru", "ochazuke", ""} {
		if _, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Name: owner, ProjectID: owner}}); err != nil {
			t.Fatal(err)
		}
	}
	a.pin = "matomeru"
	rows, err := a.savedRows()
	if err != nil || len(rows) != 1 || rows[0].Data.(work.SavedPane).ProjectID != "matomeru" || len(rows[0].Cells) != len(savedColumns) {
		t.Fatalf("scoped rows: %+v, %v", rows, err)
	}
	a.all = true
	rows, err = a.savedRows()
	if err != nil || len(rows) != 3 {
		t.Fatalf("all rows: %+v %v", rows, err)
	}
}

func TestSavedScratchRelaunchLeavesFormerProjectSession(t *testing.T) {
	a := open(t)
	f := &fakeDaemon{panes: a.panesd.(*fakePanes)}
	a.launcher = f
	row, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Window: "dead", Session: "matomeru", ProjectID: "matomeru", Name: "shell", Cwd: "/home/test"}})
	if err != nil {
		t.Fatal(err)
	}
	savedCall(t, a, "associate", ochazuke.Arguments{"id": row.ID, "scratch": true})
	savedCall(t, a, "reopen", ochazuke.Arguments{"id": row.ID})
	if len(f.shells) != 1 || f.shells[0].Session != mtmOnlySession {
		t.Fatalf("scratch destination: %+v", f.shells)
	}
	owner, err := a.work.PaneOwner(f.shells[0].ID, "")
	if err != nil || owner.ProjectID != "" {
		t.Fatalf("scratch owner: %+v %v", owner, err)
	}
}

func TestSavedRenamePromptCommitsWithoutSaveKey(t *testing.T) {
	a := open(t)
	row, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Name: ""}})
	if err != nil {
		t.Fatal(err)
	}
	savedCall(t, a, "rename", ochazuke.Arguments{"id": row.ID})
	if a.ask == nil {
		t.Fatal("rename did not open a prompt")
	}
	typeText(t, a, "renamed")
	press(t, a, "enter")
	got, err := a.work.SavedPane(row.ID)
	if err != nil || got.Name != "renamed" || a.ask != nil {
		t.Fatalf("rename: %+v %v", got, err)
	}
}
