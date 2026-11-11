package main

import (
	"strings"
	"testing"
)

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

// A new blank row draws its box at the row's indent, before the open line
// (ochazuke tree: a blank row's whole first cell is its prefix, v0.126.1).
func TestOutlineBlankRowBoxIsAtTheIndent(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "j", "O")
	focusRow(t, a, outlineNode("t1"))
	press(t, a, "o", "x")
	if f := frame(a); !strings.Contains(f, "│     [ ] x ") {
		t.Fatalf("the box is not at the indent:\n%s", f)
	}
}
