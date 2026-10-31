package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// tasksOf is the project's tasks as "title<parent title" in position order
// per parent, for comparing outlines.
func tasksOf(t *testing.T, a *App, project string) []work.Task {
	t.Helper()
	tasks, err := a.work.Tasks(project)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func taskTitled(tasks []work.Task, title string) work.Task {
	for _, t := range tasks {
		if t.Title == title {
			return t
		}
	}
	return work.Task{}
}

// O opens the project's tasks as an outline; every edit is a task write
// as it is made (typing on leaving the line), tab nests a subtask, u
// undoes in the store, and esc closes with nothing to save.
func TestTaskOutlineWritesEveryChange(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j")
	if app.r.Focus != "matomeru" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	press(t, app, "O")
	if app.r.TopModal() != outlineModal || app.r.Focus != outlineNode("t1") {
		t.Fatalf("modal %q focus %s", app.r.TopModal(), app.r.Focus)
	}
	if f := frame(app); !strings.Contains(f, "write the brief") || !strings.Contains(f, "lift the store") {
		t.Fatalf("outline:\n%s", f)
	}
	// o: a new task right away, typed, then nested under "write the brief".
	press(t, app, "o")
	before := len(tasksOf(t, app, "matomeru"))
	if before != 3 {
		t.Fatalf("o did not write the new task: %d", before)
	}
	typeText(t, app, "draft the outline")
	press(t, app, "tab", "esc")
	tasks := tasksOf(t, app, "matomeru")
	sub, parent := taskTitled(tasks, "draft the outline"), taskTitled(tasks, "write the brief")
	if sub.ID == "" || sub.ParentID != parent.ID {
		t.Fatalf("subtask %+v parent %+v", sub, parent)
	}
	// u undoes the nesting in the store (the typing stays).
	press(t, app, "u")
	if got := taskTitled(tasksOf(t, app, "matomeru"), "draft the outline"); got.ParentID != "" {
		t.Fatalf("u did not un-nest: %+v", got)
	}
	// d d deletes and u brings the task back.
	press(t, app, "d", "d")
	if taskTitled(tasksOf(t, app, "matomeru"), "draft the outline").ID != "" {
		t.Fatal("d d kept the task")
	}
	press(t, app, "u")
	if taskTitled(tasksOf(t, app, "matomeru"), "draft the outline").ID == "" {
		t.Fatal("u did not restore the task")
	}
	press(t, app, "esc")
	if app.r.TopModal() != "" {
		t.Fatalf("esc left %q open", app.r.TopModal())
	}
}
