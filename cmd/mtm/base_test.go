package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func TestOwnerPrecedenceAndCLIIsolation(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	loose, err := a.work.AddTask("", "loose")
	if err != nil {
		t.Fatal(err)
	}
	ctx := ochazuke.Ctx{Runtime: a.r, Instance: a.r.Instances["t1"]}
	for _, tc := range []struct {
		name          string
		args          ochazuke.Arguments
		cli           bool
		project, task string
		notice        bool
	}{
		{name: "selection", project: "matomeru", task: "t1"},
		{name: "CLI ignores selection", cli: true},
		{name: "explicit project", args: ochazuke.Arguments{"project": "ochazuke"}, project: "ochazuke"},
		{name: "task wins conflict", args: ochazuke.Arguments{"project": "ochazuke", "task": "t1"}, project: "matomeru", task: "t1", notice: true},
		{name: "unassociated task wins", args: ochazuke.Arguments{"project": "ochazuke", "task": loose.ID}, task: loose.ID, notice: true},
		{name: "scratch wins", args: ochazuke.Arguments{"scratch": true, "task": "t1"}},
		{name: "explicit empty", args: ochazuke.Arguments{"project": ""}},
		{name: "unknown project", args: ochazuke.Arguments{"project": "unknown"}, notice: true},
		{name: "unknown task", args: ochazuke.Arguments{"task": "unknown"}, notice: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.cli = tc.cli
			owner, err := a.resolveOwner(ctx, tc.args)
			if err != nil || owner.ProjectID != tc.project || owner.TaskID != tc.task || (owner.Notice != "") != tc.notice {
				t.Fatalf("owner: %+v, %v", owner, err)
			}
		})
	}
}

// Commit on change: typing is written after the pause (the debounce
// message), leaving the page writes a pending change at once, and the
// change is in the store after a restart; esc q never discards.
func TestEditorWritesOnPauseNavigationAndRestart(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	a := openAt(t, path)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, " paused")
	if got := stored(t, a, "t1").Title; got != "write the brief" {
		t.Fatalf("written before the pause: %q", got)
	}
	pauseRecord(t, a)
	if got := stored(t, a, "t1").Title; got != "write the brief paused" {
		t.Fatalf("not written after the pause: %q", got)
	}
	typeText(t, a, " more")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "open", Target: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	if got := stored(t, b, "t1").Title; got != "write the brief paused more" {
		t.Fatalf("after restart: %q", got)
	}
	var d editRecord
	if err := b.drafts.Load("task", "t1", &d); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a draft file was written: %v", err)
	}
	if err := b.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, b, "!")
	press(t, b, "esc", "q")
	if got := stored(t, b, "t1").Title; got != "write the brief paused more!" {
		t.Fatalf("esc q discarded: %q", got)
	}
}

// u in the editor's normal mode undoes the last change in the store.
func TestEditorUndoIsWritten(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, " oops")
	press(t, a, "esc")
	if got := stored(t, a, "t1").Title; got != "write the brief oops" {
		t.Fatalf("esc did not write: %q", got)
	}
	press(t, a, "u")
	if got := stored(t, a, "t1").Title; got != "write the brief" {
		t.Fatalf("u not written: %q", got)
	}
}

func TestOmittedAndEmptyCreation(t *testing.T) {
	t.Parallel()
	a := openAt(t, filepath.Join(t.TempDir(), "mtm.db"))
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "new", Args: ochazuke.Arguments{"name": ""}}); err != nil {
		t.Fatal(err)
	}
	if len(a.projects) != 1 || a.projects[0].Name != "" || a.projects[0].Theme.Glyph != "" || projectLabel(a.projects[0]) != "◆ untitled" {
		t.Fatalf("projects: %+v", a.projects)
	}
	a.cli = true
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "new", Args: ochazuke.Arguments{"title": ""}}); err != nil {
		t.Fatal(err)
	}
	if len(a.tasks) != 1 || a.tasks[0].Title != "" || a.tasks[0].ProjectID != "" {
		t.Fatalf("tasks: %+v", a.tasks)
	}
	saved := a.tasks[0].ID
	if err := a.reveal(saved); err != nil {
		t.Fatal(err)
	}
	if a.current != "" || a.r.Instances[saved] == nil {
		t.Fatal("unassociated target not revealed")
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "new"}); err != nil {
		t.Fatal(err)
	}
	id := a.editor.Record.ID
	if got := stored(t, a, id); got.Title != "" {
		t.Fatalf("a new task is written when the editor opens (record first): %+v", got)
	}
	typeText(t, a, "   ")
	press(t, a, "enter")
	if got := stored(t, a, id); got.Title != "" || got.ProjectID != "" {
		t.Fatalf("empty editor save: %+v", got)
	}
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultTUIUsesSelectedProjectForNewTask(t *testing.T) {
	t.Parallel()
	a := open(t)
	tui := func() error {
		// The first project has no tasks: creation must still keep its owner.
		press(t, a, "enter", "n")
		typeText(t, a, "selected project")
		press(t, a, "enter")
		tasks, err := a.work.Tasks("ochazuke")
		if err != nil || len(tasks) != 1 || tasks[0].Title != "selected project" {
			t.Fatalf("selected project tasks: %+v, %v", tasks, err)
		}
		return nil
	}
	if err := a.commands(nil, strings.NewReader(""), io.Discard, io.Discard, tui).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCreationFromReviewKeepsRecordedOwner(t *testing.T) {
	t.Parallel()
	a := open(t)
	review, err := a.work.AddReview(work.Review{ProjectID: "ochazuke", TaskID: "t1", Session: "review-session", Window: "w"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshReviews(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(reviewsPage); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "new", Target: review.ID, Args: ochazuke.Arguments{"title": "review follow-up"}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := a.work.Tasks("ochazuke")
	if err != nil || len(tasks) != 1 || tasks[0].Title != "review follow-up" {
		t.Fatalf("review owner tasks: %+v, %v", tasks, err)
	}
	if a.r.Page != "work" {
		t.Fatalf("created task hidden on page %s", a.r.Page)
	}
}

// The editor's status and harness fields are chosen from the lists the s
// and L choosers offer, never typed; a stored value outside them is shown
// with the reason and never written back (UX review 2 F2).
func TestEditorNeverWritesAnUnknownStatusOrHarness(t *testing.T) {
	t.Parallel()
	a := openAt(t, seeded(t))
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	was := stored(t, a, "t1").Status
	press(t, a, "esc", "}", "i")
	if a.r.TopModal() == "" {
		t.Fatal("i on the status field did not open its chooser")
	}
	typeText(t, a, "Z")
	if got := stored(t, a, "t1").Status; got != was {
		t.Fatalf("status %q written, want %q kept", got, was)
	}
	press(t, a, "q")
	// A value an older mtm stored outside the list is shown with the reason.
	press(t, a, "esc")
	task := stored(t, a, "t1")
	task.Status = "todoZ"
	if err := a.work.PutTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	if f := frame(a); !strings.Contains(f, "status · not written: one of todo, active, blocked, done") {
		t.Fatalf("no reason shown:\n%s", f)
	}
	press(t, a, "esc", "}", "i", "j", "j", "j", "enter")
	if got := stored(t, a, "t1").Status; got != "done" {
		t.Fatalf("chosen status not written: %q", got)
	}
	if !a.knownHarness("claude") || !a.knownHarness("") || a.knownHarness("bogusharness") {
		t.Fatal("harness check")
	}
	if f := a.recordFields("project")[3]; f.Name != "harness" || f.Source != "harness.names" || f.Free {
		t.Fatalf("harness field %+v", f)
	}
}

// A draft an older mtm left is never written by opening its record: the
// stored record may be newer. Opening shows the stored values, writes
// nothing and keeps the draft (the heading says so); draft.open writes it.
func TestOpeningARecordNeverWritesALeftoverDraft(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.drafts.Save("task", "t1", editRecord{Noun: "task", ID: "t1", ProjectID: "matomeru", Text: "stale draft", Fields: map[string]string{"status": "todo"}}); err != nil {
		t.Fatal(err)
	}
	newer := stored(t, a, "t1")
	newer.Title, newer.Status = "edited after the draft", "active"
	if err := a.work.PutTask(newer); err != nil {
		t.Fatal(err)
	}
	newer = stored(t, a, "t1")
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	if got := a.editor.Value("title"); got != "edited after the draft" {
		t.Fatalf("editor shows %q, not the stored title", got)
	}
	if f := frame(a); !strings.Contains(f, "a draft from an older mtm is kept") {
		t.Fatalf("heading does not mention the draft:\n%s", f)
	}
	press(t, a, "esc", "q")
	if got := stored(t, a, "t1"); got != newer {
		t.Fatalf("opening wrote %+v over %+v", got, newer)
	}
	var d editRecord
	if err := a.drafts.Load("task", "t1", &d); err != nil || d.Text != "stale draft" {
		t.Fatalf("draft not kept: %+v %v", d, err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "draft", Verb: "open", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, a, "t1"); got.Title != "stale draft" || got.Status != "todo" {
		t.Fatalf("draft.open did not write the draft: %+v", got)
	}
	if err := a.drafts.Load("task", "t1", &d); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("draft.open kept the draft file: %v", err)
	}
}
