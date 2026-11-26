package app

import (
	"strings"
	"testing"
)

// Moving over a task (or subtask) in the Project left nav keeps the full
// Tasks outline in the content area with that task selected; enter still
// opens its Show page. projects.navPreselectTask=false previews the Show page.
func TestProjectNavTaskHoverPreselectsInFullTasksView(t *testing.T) {
	for _, preselect := range []bool{true, false} {
		f := setupNavTreeFixture(t)
		a, d := f.app, f.driver
		if !preselect {
			setSettingForTest(t, a, SetProjectNavPreselectTask, false)
		}
		parentFocus := projectNavNodePrefix + "tasks." + f.parentTask.ID
		subFocus := projectNavNodePrefix + "tasks." + f.subTask.ID
		d.Keys("P", "j", "l", "j", "l", "j") // Tasks, expand, Epic Alpha, expand, Subtask Beta
		if a.Focus() != subFocus {
			t.Fatalf("preselect=%v: focus = %q, want the subtask nav node %q", preselect, a.Focus(), subFocus)
		}
		frame := d.Frame()
		outline := findNode(a, projectOutlineTree)
		selected, _ := a.trees.projectModel(f.proj.ID).Focused()
		if preselect {
			if outline == nil || findNode(a, showHeader) != nil {
				t.Fatalf("hovering a nav task did not keep the full Tasks outline:\n%s", frame)
			}
			if selected.ID != f.subTask.ID {
				t.Fatalf("Tasks outline selection = %q, want the hovered subtask %q", selected.ID, f.subTask.ID)
			}
			if !strings.Contains(frame, "Epic Alpha") || strings.Contains(frame, "preview · enter opens") {
				t.Fatalf("content is not the whole Tasks list:\n%s", frame)
			}
			d.Keys("k") // back onto the parent: the selection follows
			d.Frame()
			if sel, _ := a.trees.projectModel(f.proj.ID).Focused(); a.Focus() != parentFocus || sel.ID != f.parentTask.ID {
				t.Fatalf("selection did not follow the nav cursor: focus=%q selected=%q", a.Focus(), sel.ID)
			}
			d.Keys("j")
		} else {
			if outline != nil || !strings.Contains(frame, "preview · enter opens") || !strings.Contains(frame, "Subtask Beta") {
				t.Fatalf("projects.navPreselectTask off did not preview the task's Show page:\n%s", frame)
			}
		}
		// Either way enter opens the Show page on request, and esc returns.
		d.Keys("enter")
		if a.Focus() != showHeader {
			t.Fatalf("preselect=%v: enter on a nav task focus = %q, want %q", preselect, a.Focus(), showHeader)
		}
		d.Keys("esc")
		if a.Focus() != subFocus {
			t.Fatalf("preselect=%v: esc did not return to the nav node, focus=%q", preselect, a.Focus())
		}
	}
}
