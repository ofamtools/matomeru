package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"ofam.dev/matomeru/internal/work"
)

func TestWorkTaskWrapAlignsDecoratedNestedTitles(t *testing.T) {
	t.Parallel()
	a := pinned(t)
	root, child := stored(t, a, "t1"), stored(t, a, "t2")
	root.Title, root.Status, root.Private = "testing wrap begins here and continues below", work.StatusActive, true
	root.CreatedBy = work.Actor{Kind: work.ActorAgent, Harness: "omp"}
	child.Title, child.Status, child.ParentID = "nested wrap begins here and continues below", work.StatusDone, root.ID
	child.CreatedBy = root.CreatedBy
	for _, task := range []work.Task{root, child} {
		if err := a.work.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.r.Settings().Set(settingTasksColumns, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshTasks(); err != nil {
		t.Fatal(err)
	}

	// Render the actual Work tree, including its real column declarations.
	// At 44 cells the title has 27 cells beside status and updated.
	f := ansi.Strip(a.taskTree.Block(44, 16).Text)
	t.Logf("Work task tree, wrapped at 44 cells:\n%s", f)
	writeFrame(t, "work-wrap-nested.txt", f)
	lines := strings.Split(f, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	at := frameLine(lines, "testing wrap")
	want := []string{
		"▾ [>] 🔒 ✦ testing wrap      active",
		"           begins here and",
		"           continues below",
		"    [x] 🔒 ✦ nested wrap     done",
		"             begins here",
		"             and continues",
		"             below",
	}
	if at < 0 || at+len(want) > len(lines) || !slices.Equal(lines[at:at+len(want)], want) {
		t.Fatalf("wrapped rows want %q:\n%s", want, f)
	}
	for _, row := range []struct {
		line, column int
		text         string
	}{
		{at, 11, "testing wrap"},
		{at + 1, 11, "begins here and"},
		{at + 3, 13, "nested wrap"},
		{at + 4, 13, "begins here"},
	} {
		prefix, _, ok := strings.Cut(lines[row.line], row.text)
		if !ok || ansi.StringWidth(prefix) != row.column {
			t.Fatalf("line %q: title must start at cell %d", lines[row.line], row.column)
		}
	}

	if err := a.r.Settings().Set("tree.wrap", false); err != nil {
		t.Fatal(err)
	}
	f = ansi.Strip(a.taskTree.Block(44, 16).Text)
	t.Logf("Work task tree, wrap disabled:\n%s", f)
	writeFrame(t, "work-wrap-off.txt", f)
	if !strings.Contains(f, "▾ [>] 🔒 ✦ testing wrap be…  active") || strings.Contains(f, "continues below") {
		t.Fatalf("wrap off must clip the decorated title on one line:\n%s", f)
	}
}

func TestWorkTaskWrapKeepsDraftBadgesOutsideTitles(t *testing.T) {
	t.Parallel()
	a := pinned(t)
	task := stored(t, a, "t1")
	task.Title = "testing wrap starts here"
	if err := a.work.PutTask(task); err != nil {
		t.Fatal(err)
	}
	for _, record := range []editRecord{
		{Noun: "task", ID: "t1", ProjectID: "matomeru", Text: "older draft"},
		{Noun: "task", ID: "new-task-draft", ProjectID: "matomeru", New: true},
	} {
		if err := a.drafts.Save("task", record.ID, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.r.Settings().Set(settingTasksColumns, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshTasks(); err != nil {
		t.Fatal(err)
	}
	f := ansi.Strip(a.taskTree.Block(44, 16).Text)
	t.Logf("Work task tree with saved and new drafts:\n%s", f)
	writeFrame(t, "work-wrap-drafts.txt", f)
	lines := strings.Split(f, "\n")
	at := frameLine(lines, "testing wrap")
	if at < 0 || at+1 >= len(lines) || strings.TrimRight(lines[at], " ") != "  [ ] testing wrap  [draft]  todo" ||
		strings.TrimRight(lines[at+1], " ") != "      starts here" || !strings.Contains(f, "  [ ] untitled      [draft]") {
		t.Fatalf("draft badges must stay on the first line beside the editable title:\n%s", f)
	}
}

func TestWorkWideSizesTaskTreeByWrappedHeight(t *testing.T) {
	t.Parallel()
	a := pinned(t)
	root, child := stored(t, a, "t1"), stored(t, a, "t2")
	root.Title = "root " + strings.Repeat("long ", 20) + "tail"
	child.Title, child.ParentID = "visible nested task", root.ID
	for _, task := range []work.Task{root, child} {
		if err := a.work.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshTasks(); err != nil {
		t.Fatal(err)
	}
	// A logical-row-sized host leaves only two body lines, hiding the child
	// behind the wrapped parent even though the page has room for both.
	f := sizedFrame(a, 120, 40)
	t.Logf("Actual Work page at 120x40:\n%s", f)
	writeFrame(t, "work-wrap-page.txt", f)
	lines := strings.Split(f, "\n")
	first, tail, nested := frameLine(lines, "[ ] root long"), frameLine(lines, "tail"), frameLine(lines, "[ ] visible nested task")
	if first < 0 || tail <= first || nested <= tail {
		t.Fatalf("Work must allocate space for the parent's continuation and following child:\n%s", f)
	}
	if !strings.Contains(lines[nested], "    [ ] visible nested task") {
		t.Fatalf("following task lost its nesting:\n%s", f)
	}
}
