package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// With a pin, the work page starts on the pinned project's row, which
// carries the 📌, so l and enter act on the pin.
func TestWorkStartsOnThePinnedRow(t *testing.T) {
	path := seeded(t)
	a := openAt(t, path)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	b := openAt(t, path)
	if b.r.Focus != "matomeru" {
		t.Fatalf("focus %s", b.r.Focus)
	}
	for _, item := range b.projectList.Items() {
		if pinned := strings.Contains(item.Cells[0], "📌"); pinned != (item.ID == "matomeru") {
			t.Fatalf("row %s cells %q", item.ID, item.Cells)
		}
	}
}

// The tasks list follows the highlighted project without enter, and its
// heading says whose tasks they are; the project's columns are the
// noun's, which the catalog carries.
func TestTasksFollowTheHighlightedProject(t *testing.T) {
	a := open(t)
	executeA(t, a, "project", "unpin", "work", nil)
	if _, err := a.work.AddTask("ochazuke", "grid"); err != nil {
		t.Fatal(err)
	}
	if err := a.projectList.Select("ochazuke"); err != nil {
		t.Fatal(err)
	}
	if err := a.projectList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "j")
	if a.current != "matomeru" || a.tasksHeading() != "tasks · matomeru" || a.taskList.Len() != 2 {
		t.Fatalf("current %s heading %q tasks %d", a.current, a.tasksHeading(), a.taskList.Len())
	}
	press(t, a, "k")
	if a.current != "ochazuke" || a.taskList.Len() != 1 {
		t.Fatalf("current %s tasks %d", a.current, a.taskList.Len())
	}
	for _, noun := range []string{"project", "task", "note", "review", "known-directory", "project-dir", "project-base"} {
		if len(a.r.Noun(noun).Columns) == 0 {
			t.Fatalf("%s declares no columns", noun)
		}
	}
	if got := a.taskList.Columns(); slices.ContainsFunc(got, func(c ochazuke.Column) bool { return c.Title == "project" }) {
		t.Fatalf("one project's tasks repeat the project column: %+v", got)
	}
}

// rowWith reports whether one line of frame holds cells in order: a table
// row checked without its column widths.
func rowWith(frame string, cells ...string) bool {
	for _, line := range strings.Split(frame, "\n") {
		rest, ok := line, true
		for _, cell := range cells {
			i := strings.Index(rest, cell)
			if i < 0 {
				ok = false
				break
			}
			rest = rest[i+len(cell):]
		}
		if ok {
			return true
		}
	}
	return false
}
