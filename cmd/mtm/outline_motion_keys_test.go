package main

import (
	"testing"
)

// Hidden default rows must not change what a key does (ochazuke v0.144.1,
// rule check hiddenkeys): on the task outline, k on the top row stays in
// normal mode with no form over the outline, j and k in visual-line grow
// and shrink the selection, and d in visual-line deletes the selected
// tasks at once instead of waiting for a second key (ochazuke v0.143.0's
// hidden rows ran outline.row-key on k, outline.indent and outline.leave
// on j and k, and made d wait for "d a").
func TestOutlineMotionKeysKeepTheirMeaning(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "j", "O")
	if a.r.TopModal() != outlineModal {
		t.Fatalf("O opened %q", a.r.TopModal())
	}
	tree := a.r.Tree(outlineTree)
	top := tree.Nodes()[0].ID
	focusRow(t, a, top)
	press(t, a, "k")
	if a.r.Mode.Name != "normal" || a.r.TopModal() != outlineModal || a.r.FocusID() != top {
		t.Fatalf("k on the top row: mode %q, modal %q, focus %q (was %q)", a.r.Mode.Name, a.r.TopModal(), a.r.FocusID(), top)
	}
	first := taskOf(tree.Nodes()[1].ID)
	focusRow(t, a, tree.Nodes()[1].ID)
	steps := []struct {
		key  string
		rows int
	}{{"V", 1}, {"j", 2}, {"k", 1}, {"j", 2}}
	for i, s := range steps {
		press(t, a, s.key)
		if got := len(tree.List().Marked()); got != s.rows || a.r.Mode.Name != "visual-line" || a.r.TopModal() != outlineModal {
			t.Fatalf("step %d (%s): %d rows selected in %q, modal %q; want %d in visual-line", i, s.key, got, a.r.Mode.Name, a.r.TopModal(), s.rows)
		}
	}
	before := len(tasksOf(t, a, "matomeru"))
	press(t, a, "d")
	if a.r.Mode.Name == "visual-line" || a.r.TopModal() != outlineModal {
		t.Fatalf("d in visual-line: mode %q, modal %q; want the selection deleted", a.r.Mode.Name, a.r.TopModal())
	}
	if got := len(tasksOf(t, a, "matomeru")); got != before-2 {
		t.Fatalf("d in visual-line: %d tasks, want %d", got, before-2)
	}
	if _, err := a.work.Task(first); err == nil {
		t.Fatalf("d in visual-line kept %s", first)
	}
}
