package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
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

// } and { on the outline run ochazuke.collection.next-class and
// previous-class over the project rows: they land on the next or previous project row (from a task, its own
// project first) and the modal and the tree keep focus; with the focus in
// the notes section they return it to the tree's selected row.
func TestOutlineFoldKeysJumpProjects(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "T") // the all view: several projects at the top level
	tree := a.r.Tree(outlineTree)
	var projects []string
	var task string
	for _, n := range tree.Nodes() {
		if n.Parent == "" {
			projects = append(projects, n.ID)
		} else if task == "" && n.Noun == outlineTaskNoun {
			task = n.ID
		}
	}
	if len(projects) < 2 || task == "" {
		t.Fatalf("fixture has projects %v and task %q", projects, task)
	}
	for _, n := range tree.Nodes() {
		if got := slices.Contains(n.Classes, outlineProjectClass); got != (n.Parent == "") {
			t.Fatalf("row %s (parent %q) has classes %v", n.ID, n.Parent, n.Classes)
		}
	}
	focusRow(t, a, projects[0])
	press(t, a, "}")
	if a.r.FocusID() != projects[1] || a.r.TopModal() != outlineModal || a.r.Mode.Name != "normal" {
		t.Fatalf("} from the first project: focus %q, modal %q, mode %q; want %q", a.r.FocusID(), a.r.TopModal(), a.r.Mode.Name, projects[1])
	}
	press(t, a, "{")
	if a.r.FocusID() != projects[0] || a.r.TopModal() != outlineModal {
		t.Fatalf("{ from the second project: focus %q, modal %q; want %q", a.r.FocusID(), a.r.TopModal(), projects[0])
	}
	press(t, a, "{")
	if a.r.FocusID() != projects[0] || a.r.TopModal() != outlineModal {
		t.Fatalf("{ at the first project moved to %q (modal %q)", a.r.FocusID(), a.r.TopModal())
	}
	// In the notes section } and { do not fail: they return the focus to the
	// tree's selected row.
	if err := a.work.PutNote(work.Note{ID: "fold-note", ProjectID: "matomeru", TaskID: taskOf(task), Text: "a note"}); err != nil {
		t.Fatal(err)
	}
	focusRow(t, a, task)
	if err := a.syncOutlineNotes(true); err != nil {
		t.Fatal(err)
	}
	a.m.Width, a.m.Height = 140, 35
	frameView(a)
	// ctrl+w w still reaches the notes section; } there is the app's
	// focus.next, back to the tree's selected row.
	press(t, a, "ctrl+w", "w")
	if !strings.HasPrefix(a.r.FocusID(), outlineNotesList) {
		t.Fatalf("ctrl+w w from a task with notes: focus %q, want a notes row", a.r.FocusID())
	}
	press(t, a, "}")
	if a.r.FocusID() != task || a.r.TopModal() != outlineModal || a.r.Mode.Name != "normal" {
		t.Fatalf("} in the notes: focus %q, modal %q, mode %q; want the tree row %q", a.r.FocusID(), a.r.TopModal(), a.r.Mode.Name, task)
	}
	focusRow(t, a, task)
	press(t, a, "{")
	if want := projectOfTask(tree, task); a.r.FocusID() != want || a.r.TopModal() != outlineModal {
		t.Fatalf("{ from a task: focus %q, want its project %q", a.r.FocusID(), want)
	}
	// in visual-line } extends the selection from the start row through the
	// next project row
	focusRow(t, a, task)
	press(t, a, "V", "}")
	next := projects[slices.Index(projects, projectOfTask(tree, task))+1]
	var want []string
	in := false
	for _, n := range tree.Nodes() {
		if n.ID == task {
			in = true
		}
		if in {
			want = append(want, n.ID)
		}
		if n.ID == next {
			break
		}
	}
	var marked []string
	for _, m := range tree.List().Marked() {
		marked = append(marked, m.ID)
	}
	if a.r.Mode.Name != "visual-line" || a.r.TopModal() != outlineModal || a.r.FocusID() != next || !slices.Equal(marked, want) {
		t.Fatalf("V } from a task: mode %q, modal %q, focus %q (want %q), marked %v (want %v)", a.r.Mode.Name, a.r.TopModal(), a.r.FocusID(), next, marked, want)
	}
	press(t, a, "esc")
	// a project created in the outline is walked too
	focusRow(t, a, projects[0])
	press(t, a, "o", "esc", "{")
	if a.r.FocusID() != projects[0] {
		t.Fatalf("{ from the new project: focus %q, want %q", a.r.FocusID(), projects[0])
	}
}

func projectOfTask(tree *ochazuke.Tree, id string) string {
	nodes := map[string]ochazuke.TreeNode{}
	for _, n := range tree.Nodes() {
		nodes[n.ID] = n
	}
	for nodes[id].Parent != "" {
		id = nodes[id].Parent
	}
	return id
}
