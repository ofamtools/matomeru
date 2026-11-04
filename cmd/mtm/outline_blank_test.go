package main

import "testing"

// A new row left blank is removed however it was indented: o shift+tab esc
// on a project's outline makes a project row and leaves no project with an
// empty name; o tab esc leaves no task.
func TestOutlineNewRowLeftBlankIsRemoved(t *testing.T) {
	t.Parallel()
	for _, keys := range [][]string{{"o", "shift+tab", "esc"}, {"o", "tab", "esc"}} {
		a := open(t)
		press(t, a, "j", "O")
		tasks := len(tasksOf(t, a, "matomeru"))
		for _, k := range keys {
			a.Update(keyMsg(k)) // shift+tab may be refused here: the keys go on
		}
		if made := newProjects(t, a); len(made) != 0 {
			t.Fatalf("%v left projects %+v", keys, made)
		}
		if got := len(tasksOf(t, a, "matomeru")); got != tasks {
			t.Fatalf("%v left %d tasks, want %d", keys, got, tasks)
		}
	}
}

// The same in the all view, where the project row stays on screen.
func TestAllViewNewRowLeftBlankIsRemoved(t *testing.T) {
	t.Parallel()
	for _, keys := range [][]string{{"o", "shift+tab", "esc"}, {"o", "tab", "esc"}} {
		a := allOutline(t, outlineNode("t1"))
		tasks := len(tasksOf(t, a, "matomeru"))
		press(t, a, keys...)
		if made := newProjects(t, a); len(made) != 0 {
			t.Fatalf("%v left projects %+v", keys, made)
		}
		if got := len(tasksOf(t, a, "matomeru")); got != tasks {
			t.Fatalf("%v left %d tasks, want %d", keys, got, tasks)
		}
	}
}
