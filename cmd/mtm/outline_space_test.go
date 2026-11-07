package main

import (
	"strings"
	"testing"
)

// frameLine is the index of the first frame line holding text, -1 if none.
func frameLine(lines []string, text string) int {
	for i, l := range lines {
		if strings.Contains(l, text) {
			return i
		}
	}
	return -1
}

// In the all view each project row but the first has a blank line above it
// (its tasks end, a gap, the next project); the first project row sits
// right under the header, and a project added in the outline gets the gap
// too. The gap is not a row: j and k step from task to project.
func TestAllViewProjectRowsHaveSpaceAbove(t *testing.T) {
	t.Parallel()
	a := allOutline(t, projectNode("ochazuke"))
	for _, n := range a.r.Tree(outlineTree).Nodes() {
		if (n.Parent == "") != n.SpaceAbove {
			t.Fatalf("row %s (parent %q) SpaceAbove %v", n.ID, n.Parent, n.SpaceAbove)
		}
	}
	lines := strings.Split(frame(a), "\n")
	first, second, third := frameLine(lines, "│   ochazuke"), frameLine(lines, "│ ▾ matomeru"), frameLine(lines, "│   no project")
	header := frameLine(lines, "│ task ")
	if first != header+1 {
		t.Fatalf("the first project row is %d lines under the header (%d), want right under:\n%s", first-header, header, frame(a))
	}
	for _, at := range []int{second, third} {
		if at < 2 || strings.Trim(lines[at-1], "│ ") != "" {
			t.Fatalf("no blank line above line %d:\n%s", at, frame(a))
		}
	}
	focusRow(t, a, outlineNode("t2"))
	press(t, a, "j")
	if a.r.FocusID() != projectNode("") {
		t.Fatalf("j from the last task landed on %q, want the next project row", a.r.FocusID())
	}
	press(t, a, "o")
	typeText(t, a, "newproj")
	press(t, a, "esc")
	for _, n := range a.r.Tree(outlineTree).Nodes() {
		if n.Parent == "" && !n.SpaceAbove {
			t.Fatalf("project row %s lost its gap", n.ID)
		}
	}
}

// d d on an expanded task passes its subtasks up through the outline's
// writer: the moves are stored before the delete, and one u puts the task
// and its subtasks back as they were.
func TestOutlineDeletePassesSubtasksUp(t *testing.T) {
	t.Parallel()
	a := allOutline(t, outlineNode("t2"))
	press(t, a, "tab") // t2 under t1
	if got := stored(t, a, "t2"); got.ParentID != "t1" {
		t.Fatalf("tab: %+v", got)
	}
	focusRow(t, a, outlineNode("t1"))
	press(t, a, "d", "d")
	if _, err := a.work.Task("t1"); err == nil {
		t.Fatal("d d kept t1")
	}
	got := stored(t, a, "t2")
	if got.ParentID != "" || got.ProjectID != "matomeru" {
		t.Fatalf("t2 after deleting its parent: %+v, want it moved up under the project", got)
	}
	if a.r.FocusID() != outlineNode("t2") {
		t.Fatalf("cursor on %q, want the promoted t2", a.r.FocusID())
	}
	press(t, a, "u")
	if got := stored(t, a, "t2"); got.ParentID != "t1" {
		t.Fatalf("u: t2 %+v, want it under t1 again", got)
	}
	if got := stored(t, a, "t1"); got.ProjectID != "matomeru" || got.ParentID != "" || got.Title != "write the brief" {
		t.Fatalf("u: t1 %+v", got)
	}
	// Collapsed, the subtask goes with the task.
	focusRow(t, a, outlineNode("t1"))
	press(t, a, "h", "d", "d")
	if _, err := a.work.Task("t2"); err == nil {
		t.Fatal("d d on the collapsed t1 kept its subtask")
	}
	press(t, a, "u")
	if got := stored(t, a, "t2"); got.ParentID != "t1" {
		t.Fatalf("u on the collapsed delete: %+v", got)
	}
}
