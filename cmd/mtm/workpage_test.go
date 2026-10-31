package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// From sideBySide columns the tasks sit right of the projects under the
// highlighted project's header; j spills from the projects into the
// tasks; the selected task's detail and the editor sit under the table.
// Narrower, the stacked layout stays.
func TestWorkPageTasksInTheRightPane(t *testing.T) {
	t.Parallel()
	a := openAt(t, seededWith(t, map[string]string{"busy": "active", "stuck": "blocked"}))
	press(t, a, "j")
	for _, width := range []int{120, 160} {
		f := frameAt(a, width)
		lines := strings.Split(f, "\n")
		var header string
		for _, l := range lines {
			if strings.Contains(l, "1 active · 2 todo · 1 blocked") {
				header = l
			}
		}
		if header == "" || !strings.Contains(header, "◆ matomeru") || !strings.Contains(header, "projects (3)") {
			t.Fatalf("width %d: no project header beside the projects:\n%s", width, f)
		}
		if !strings.Contains(f, "[>] busy task") {
			t.Fatalf("width %d: tasks not shown:\n%s", width, f)
		}
		t.Logf("project highlighted, %d columns:\n%s", width, f)
	}
	// } walks to the task table and k from its first row spills back into
	// the projects, in drawing order; j from the last project row spills
	// into the (unassociated) tasks.
	press(t, a, "}")
	if a.r.Focus != "busy" {
		t.Fatalf("} did not reach the tasks: focus %s", a.r.Focus)
	}
	press(t, a, "k")
	if a.r.Focus != unassociatedID {
		t.Fatalf("k did not spill into the projects: focus %s", a.r.Focus)
	}
	// The spill does not rebuild the tasks: j goes back to the task left.
	press(t, a, "j")
	if a.r.Focus != "busy" {
		t.Fatalf("k then j did not come back: focus %s", a.r.Focus)
	}
	press(t, a, "k")
	if err := a.work.PutTask(work.Task{ID: "loose", Title: "loose end"}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "k", "j", "j")
	if a.r.Focus != "loose" {
		t.Fatalf("j did not spill into the tasks: focus %s", a.r.Focus)
	}
	press(t, a, "k", "k", "}")
	if a.r.Focus != "busy" {
		t.Fatalf("back on matomeru's tasks: focus %s", a.r.Focus)
	}
	f := frameAt(a, 120)
	if !strings.Contains(f, "status: active") || !strings.Contains(f, "[>] busy task") {
		t.Fatalf("task detail not under the table:\n%s", f)
	}
	t.Logf("task selected, 120 columns:\n%s", f)
	press(t, a, "i")
	f = frameAt(a, 160)
	if !strings.Contains(f, "editing task") || !strings.Contains(f, "[>] busy task") {
		t.Fatalf("editor did not open under the table:\n%s", f)
	}
	t.Logf("editing, 160 columns:\n%s", f)
	press(t, a, "esc", "q")
	f = frameAt(a, 80)
	if !strings.Contains(f, "tasks · matomeru") {
		t.Fatalf("80 columns lost the stacked layout:\n%s", f)
	}
	t.Logf("80 columns:\n%s", f)
}

// Test pass R5: stacked at 80 columns, the task table spans the full
// width (status, by and updated columns included), below the projects and
// the detail beside them.
func TestWorkStackedTaskTableUsesFullWidth(t *testing.T) {
	t.Parallel()
	a := open(t)
	long := "a task title long enough to need more than half of eighty columns"
	if _, err := a.work.AddSubtask("matomeru", "", long); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	f := frameAt(a, 80)
	if !strings.Contains(f, long[:45]) || !strings.Contains(f, "updated") {
		t.Fatalf("task table not full width at 80:\n%s", f)
	}
}

// UX review 2 F6: k off the first task spills into the projects' last row
// and j spills straight back onto that task, at both layouts; the tasks
// shown are not rebuilt by the spill. A move inside the projects still
// follows.
func TestWorkSpillRoundTripReturnsToTheTask(t *testing.T) {
	t.Parallel()
	for _, width := range []int{80, 140} {
		a := openAt(t, seededWith(t, map[string]string{"busy": "active", "stuck": "blocked"}))
		a.m.Width, a.m.Height = width, 24
		press(t, a, "j", "}")
		if a.r.Focus != "busy" {
			t.Fatalf("%d: focus %s", width, a.r.Focus)
		}
		frameAt(a, width)
		press(t, a, "k")
		if a.r.Focus != unassociatedID || a.current != "matomeru" {
			t.Fatalf("%d: k: focus %s current %q", width, a.r.Focus, a.current)
		}
		if f := frameAt(a, width); !strings.Contains(f, "busy task") {
			t.Fatalf("%d: the spill rebuilt the tasks:\n%s", width, f)
		}
		press(t, a, "j")
		if a.r.Focus != "busy" {
			t.Fatalf("%d: k then j: focus %s", width, a.r.Focus)
		}
		press(t, a, "k", "k")
		if a.r.Focus != "matomeru" || a.current != "matomeru" {
			t.Fatalf("%d: k k: focus %s current %q", width, a.r.Focus, a.current)
		}
		press(t, a, "j")
		if a.r.Focus != unassociatedID || a.current != "" {
			t.Fatalf("%d: a move in the projects did not follow: focus %s current %q", width, a.r.Focus, a.current)
		}
	}
}
