package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// nestedTasks adds subtasks to the seeded matomeru tasks (t1 "write the
// brief", t2 "lift the store"): t2 is active; t1 has t3 (todo) and t4
// (active), t3 has t5; t6 is a done top-level task.
func nestedTasks(t *testing.T, a *App) {
	t.Helper()
	t2 := stored(t, a, "t2")
	t2.Status = work.StatusActive
	for _, task := range []work.Task{
		t2,
		{ID: "t3", ProjectID: "matomeru", ParentID: "t1", Title: "outline the store", Position: 3, Status: work.StatusTodo},
		{ID: "t4", ProjectID: "matomeru", ParentID: "t1", Title: "name the tables", Position: 4, Status: work.StatusActive},
		{ID: "t5", ProjectID: "matomeru", ParentID: "t3", Title: "list the queries", Position: 5, Status: work.StatusTodo},
		{ID: "t6", ProjectID: "matomeru", Title: "pick a name", Position: 6, Status: work.StatusDone},
	} {
		if err := a.work.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshTasks(); err != nil {
		t.Fatal(err)
	}
}

// taskTreeShape is the task tree's rows as "id<parent" in drawing order.
func taskTreeShape(a *App) []string {
	var out []string
	for _, n := range a.taskTree.Nodes() {
		out = append(out, n.ID+"<"+n.Parent)
	}
	return out
}

// The Work page's tasks are a tree by default (work.tasks-layout): parents
// before their children, and among each parent's children status order
// (active first), then position. Every row is a task row.
func TestWorkTasksAreATreeInStatusOrderPerLevel(t *testing.T) {
	a := pinned(t)
	nestedTasks(t, a)
	want := []string{"t2<", "t1<", "t4<t1", "t3<t1", "t5<t3", "t6<"}
	if got := taskTreeShape(a); !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	for _, n := range a.taskTree.Nodes() {
		if n.Noun != "task" {
			t.Fatalf("row %s noun %q, want task", n.ID, n.Noun)
		}
	}
	f := frameAt(a, 120)
	for _, want := range []string{"▾ [ ] write the brief", "    ▾ [ ] outline the store", "        [ ] list the queries"} {
		if !strings.Contains(f, want) {
			t.Fatalf("frame lacks %q:\n%s", want, f)
		}
	}
	// flat is the list before the tree: every task top level, status order.
	if err := a.r.Settings().Set(settingWorkTasksLayout, tasksLayoutFlat); err != nil {
		t.Fatal(err)
	}
	want = []string{"t2<", "t4<", "t1<", "t3<", "t5<", "t6<"}
	if got := taskTreeShape(a); !slices.Equal(got, want) {
		t.Fatalf("flat rows %q, want %q", got, want)
	}
}

// Task keys reach a subtask row: l opens it, x toggles it, and h folds
// its parent (the tree's collapse: l and enter stay task keys).
func TestTaskKeysWorkOnASubtaskRow(t *testing.T) {
	a := pinned(t)
	nestedTasks(t, a)
	if err := a.taskTree.Select("t5"); err != nil {
		t.Fatal(err)
	}
	if err := a.taskTree.Focus(); err != nil {
		t.Fatal(err)
	}
	if a.r.Focus != "t5" {
		t.Fatalf("focus %s, want t5", a.r.Focus)
	}
	press(t, a, "l")
	if a.detailTask != "t5" {
		t.Fatalf("l: detail %q, want t5", a.detailTask)
	}
	press(t, a, "x")
	if got := stored(t, a, "t5").Status; got != work.StatusDone {
		t.Fatalf("x: status %q", got)
	}
	if a.r.Focus != "t5" {
		t.Fatalf("x: focus %s, want t5", a.r.Focus)
	}
	press(t, a, "h") // a leaf: onto its parent
	if a.r.Focus != "t3" {
		t.Fatalf("h on a leaf: focus %s, want its parent t3", a.r.Focus)
	}
	press(t, a, "h") // an open parent: folded
	if !a.taskTree.Collapsed("t3") || a.r.Focus != "t3" {
		t.Fatalf("h on t3: collapsed %v focus %s", a.taskTree.Collapsed("t3"), a.r.Focus)
	}
	if strings.Contains(frameAt(a, 120), "list the queries") {
		t.Fatal("folded t3 still draws t5")
	}
	if !strings.Contains(frameAt(a, 120), "▸ [ ] outline the store") {
		t.Fatalf("folded t3 has no ▸:\n%s", frameAt(a, 120))
	}
}

// The task picker names a subtask after its ancestors (tasks.picker-path,
// on by default); off, the label is the task's alone.
func TestTaskPickerShowsTheAncestorPath(t *testing.T) {
	a := pinned(t)
	nestedTasks(t, a)
	row := func() ochazuke.ChooserRow {
		for _, r := range a.taskPickerRows(work.Assignment{}) {
			if r.ID == "task:t5" {
				return r
			}
		}
		t.Fatal("no t5 row")
		return ochazuke.ChooserRow{}
	}
	got := row()
	if !strings.HasPrefix(got.Label, "[ ] write the brief › outline the store › list the queries") {
		t.Fatalf("label %q", got.Label)
	}
	// The ancestors and their separators draw in the muted task.path tone;
	// the task's own title does not.
	prefix := "write the brief › outline the store › "
	want := []ochazuke.ToneSpan{ochazuke.SpanTone(0, 4, 4+utf8.RuneCountInString(prefix), toneTaskPath)}
	if !reflect.DeepEqual(got.Tones, want) {
		t.Fatalf("tones %+v, want %+v", got.Tones, want)
	}
	if style, ok := a.m.Appearance.AppTones.Rows()[toneTaskPath]; !ok || style.Fg.Dark != "muted" {
		t.Fatalf("task.path tone row %+v (declared %v)", style, ok)
	}
	if err := a.r.Settings().Set(settingTaskPickerPath, false); err != nil {
		t.Fatal(err)
	}
	if got := row(); !strings.HasPrefix(got.Label, "[ ] list the queries") || got.Tones != nil {
		t.Fatalf("path off: label %q tones %+v", got.Label, got.Tones)
	}
}
