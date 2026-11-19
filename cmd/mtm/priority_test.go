package main

import (
	"encoding/json"
	"slices"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// listIDs is the ids a noun's list returns for the CLI arguments given.
func listIDs(t *testing.T, a *App, noun string, args ...string) []string {
	t.Helper()
	cmd := []string{noun, "list"}
	if noun == "task" {
		cmd = append(cmd, "--all")
	}
	out := mustCLI(t, a, append(cmd, args...)...)
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("%s list: %v\n%s", noun, err, out)
	}
	var ids []string
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// A task's priority is set and read through the standard verbs: P0 to P5,
// any case, or not set (""; none clears it too), which stores nothing; an
// old value is read as its level; a value outside the scale refuses the
// write.
func TestTaskPrioritySetGet(t *testing.T) {
	t.Parallel()
	a := open(t)
	thing, _ := executeA(t, a, "task", "get", "", ochazuke.Arguments{"id": "t1"}).(map[string]any)
	if thing["priority"] != work.PriorityNone {
		t.Fatalf("an unset priority reads %v, want blank", thing["priority"])
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t1", "props": map[string]any{"priority": "p1"}})
	if got := stored(t, a, "t1").Priority; got != "P1" {
		t.Fatalf("p1 stored %q", got)
	}
	mustCLI(t, a, "task", "set", "t2", "priority=urgent")
	if got := stored(t, a, "t2").Priority; got != "P0" {
		t.Fatalf("priority=urgent stored %q, want P0", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "set", Args: ochazuke.Arguments{"id": "t1", "props": map[string]any{"priority": "P6"}}}); err == nil {
		t.Fatal("priority P6 was written")
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t1", "props": map[string]any{"priority": "none"}})
	if got := stored(t, a, "t1").Priority; got != "" {
		t.Fatalf("none stored %q, want nothing", got)
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if got := stored(t, a, "t1").Priority; got != "P1" {
		t.Fatalf("undo: %q", got)
	}
}

// Sorting ranks P0 first and not set last (a P6 that shows blank), and
// filtering keeps one level, for tasks and projects alike.
func TestPrioritySortAndFilter(t *testing.T) {
	t.Parallel()
	a := open(t)
	third, err := a.work.AddTask("matomeru", "third")
	if err != nil {
		t.Fatal(err)
	}
	mustCLI(t, a, "task", "set", "t1", "priority=P5")
	mustCLI(t, a, "task", "set", "t2", "priority=P0")
	if got := listIDs(t, a, "task"); !slices.Equal(got, []string{"t1", "t2", third.ID}) {
		t.Fatalf("default order %v", got)
	}
	if got := listIDs(t, a, "task", "--sort", "priority"); !slices.Equal(got, []string{"t2", "t1", third.ID}) {
		t.Fatalf("sort priority %v, want P0, P5, not set", got)
	}
	if got := listIDs(t, a, "task", "--sort", "priority:desc"); !slices.Equal(got, []string{"t1", "t2", third.ID}) {
		t.Fatalf("sort priority:desc %v, want P5, P0, not set", got)
	}
	if got := listIDs(t, a, "task", "--where", "priority=P0"); !slices.Equal(got, []string{"t2"}) {
		t.Fatalf("where priority=P0 %v", got)
	}
	mustCLI(t, a, "project", "set", "matomeru", "priority=P2")
	if got := listIDs(t, a, "project", "--sort", "priority"); len(got) < 2 || got[0] != "matomeru" {
		t.Fatalf("project sort priority %v", got)
	}
	if got := listIDs(t, a, "project", "--where", "priority=P2"); !slices.Equal(got, []string{"matomeru"}) {
		t.Fatalf("project where priority=P2 %v", got)
	}
}

// The priority column shows only when tasks.columns lists it (default off).
func TestTaskPriorityColumnIsOptIn(t *testing.T) {
	t.Parallel()
	a := open(t)
	mustCLI(t, a, "task", "set", "t1", "priority=P1")
	has := func() bool {
		tasksOn(t, a, "t1")
		for _, c := range a.taskList.Columns() {
			if c.Title == columnPriority {
				return true
			}
		}
		return false
	}
	if has() {
		t.Fatal("the priority column shows by default")
	}
	if err := a.r.Settings().Set(settingTasksColumns, []string{columnBy, columnPriority}); err != nil {
		t.Fatal(err)
	}
	if !has() {
		t.Fatal("tasks.columns lists priority but the column is missing")
	}
	cells := a.taskCells(stored(t, a, "t1"), false, a.now())
	if !slices.Contains(cells, "P1") {
		t.Fatalf("t1 cells %v lack its priority", cells)
	}
}
