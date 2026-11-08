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

// A task's priority is set and read through the standard verbs; a row
// written before the field existed reads none; the stored row keeps nothing
// for none; a value outside the set refuses the write.
func TestTaskPrioritySetGet(t *testing.T) {
	t.Parallel()
	a := open(t)
	if got := stored(t, a, "t1").Priority; got != "" {
		t.Fatalf("a seeded task stores %q, want nothing", got)
	}
	thing, _ := executeA(t, a, "task", "get", "", ochazuke.Arguments{"id": "t1"}).(map[string]any)
	if thing["priority"] != work.PriorityNone {
		t.Fatalf("an unset priority reads %v, want none", thing["priority"])
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t1", "props": map[string]any{"priority": "high"}})
	if got := stored(t, a, "t1").Priority; got != "high" {
		t.Fatalf("stored priority %q", got)
	}
	mustCLI(t, a, "task", "set", "t2", "priority=urgent")
	if got := stored(t, a, "t2").Priority; got != "urgent" {
		t.Fatalf("priority=urgent stored %q", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "set", Args: ochazuke.Arguments{"id": "t1", "props": map[string]any{"priority": "soon"}}}); err == nil {
		t.Fatal("priority soon was written")
	}
	if got := stored(t, a, "t1").Priority; got != "high" {
		t.Fatalf("a refused write changed the priority to %q", got)
	}
	// Other sets keep the priority; none clears it back to nothing stored.
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t1", "props": map[string]any{"status": "active"}})
	if got := stored(t, a, "t1").Priority; got != "high" {
		t.Fatalf("a status set lost the priority: %q", got)
	}
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t1", "props": map[string]any{"priority": "none"}})
	if got := stored(t, a, "t1").Priority; got != "" {
		t.Fatalf("none stored %q, want nothing", got)
	}
	// One undo takes the last write back.
	executeA(t, a, "history-entry", "undo", "", nil)
	if got := stored(t, a, "t1").Priority; got != "high" {
		t.Fatalf("undo: %q", got)
	}
}

// A task can be created with a priority.
func TestTaskCreateWithPriority(t *testing.T) {
	t.Parallel()
	a := open(t)
	thing := map[string]any{}
	if err := json.Unmarshal([]byte(mustCLI(t, a, "task", "create", "--json", `{"title":"first","project":"matomeru","priority":"low"}`)), &thing); err != nil {
		t.Fatal(err)
	}
	id, _ := thing["id"].(string)
	if got := stored(t, a, id).Priority; got != "low" {
		t.Fatalf("created with priority %q", got)
	}
}

// Sorting ranks priorities in enum order (descending puts urgent first),
// and filtering keeps one value. The default order is untouched.
func TestTaskPrioritySortAndFilter(t *testing.T) {
	t.Parallel()
	a := open(t)
	third, err := a.work.AddTask("matomeru", "third")
	if err != nil {
		t.Fatal(err)
	}
	mustCLI(t, a, "task", "set", "t1", "priority=low")
	mustCLI(t, a, "task", "set", "t2", "priority=urgent")
	plain := listIDs(t, a, "task")
	if want := []string{"t1", "t2", third.ID}; !slices.Equal(plain, want) {
		t.Fatalf("default order %v, want %v", plain, want)
	}
	if got := listIDs(t, a, "task", "--sort", "priority:desc"); !slices.Equal(got, []string{"t2", "t1", third.ID}) {
		t.Fatalf("sort priority:desc %v", got)
	}
	if got := listIDs(t, a, "task", "--sort", "priority"); !slices.Equal(got, []string{third.ID, "t1", "t2"}) {
		t.Fatalf("sort priority %v", got)
	}
	if got := listIDs(t, a, "task", "--where", "priority=urgent"); !slices.Equal(got, []string{"t2"}) {
		t.Fatalf("where priority=urgent %v", got)
	}
	if got := listIDs(t, a, "task", "--where", "priority=none"); !slices.Equal(got, []string{third.ID}) {
		t.Fatalf("where priority=none %v", got)
	}
}

// A project's priority: set, get, sort, filter, create; legacy rows read none.
func TestProjectPriority(t *testing.T) {
	t.Parallel()
	a := open(t)
	thing, _ := executeA(t, a, "project", "get", "", ochazuke.Arguments{"id": "ochazuke"}).(map[string]any)
	if thing["priority"] != work.PriorityNone {
		t.Fatalf("an unset project priority reads %v", thing["priority"])
	}
	mustCLI(t, a, "project", "set", "matomeru", "priority=high")
	p, err := a.work.Project("matomeru")
	if err != nil || p.Priority != "high" {
		t.Fatalf("stored %q %v", p.Priority, err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "set", Args: ochazuke.Arguments{"id": "matomeru", "props": map[string]any{"priority": "huge"}}}); err == nil {
		t.Fatal("priority huge was written")
	}
	// A rename keeps it.
	mustCLI(t, a, "project", "set", "matomeru", "name=mtm")
	if p, _ = a.work.Project("matomeru"); p.Priority != "high" || p.Name != "mtm" {
		t.Fatalf("after rename: %+v", p)
	}
	if got := listIDs(t, a, "project", "--sort", "priority:desc"); len(got) < 2 || got[0] != "matomeru" {
		t.Fatalf("sort priority:desc %v", got)
	}
	if got := listIDs(t, a, "project", "--where", "priority=high"); !slices.Equal(got, []string{"matomeru"}) {
		t.Fatalf("where priority=high %v", got)
	}
	made := map[string]any{}
	if err := json.Unmarshal([]byte(mustCLI(t, a, "project", "create", "--json", `{"name":"new","priority":"medium"}`)), &made); err != nil {
		t.Fatal(err)
	}
	id, _ := made["id"].(string)
	if p, err = a.work.Project(id); err != nil || p.Priority != "medium" {
		t.Fatalf("created %q %v", p.Priority, err)
	}
}

// The priority column shows only when tasks.columns lists it (default off).
func TestTaskPriorityColumnIsOptIn(t *testing.T) {
	t.Parallel()
	a := open(t)
	mustCLI(t, a, "task", "set", "t1", "priority=high")
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
	if !slices.Contains(cells, "high") {
		t.Fatalf("t1 cells %v lack its priority", cells)
	}
}
