package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func TestOwnerPrecedenceAndCLIIsolation(t *testing.T) {
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

func TestDraftSurvivesNavigationRestartAndCancel(t *testing.T) {
	path := seeded(t)
	a := openAt(t, path)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, " unfinished")
	if got := stored(t, a, "t1").Title; got != "write the brief" {
		t.Fatalf("draft changed saved row: %q", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "open", Target: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	if err := b.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	if got := string(b.editor.Input.Text); got != "write the brief unfinished" {
		t.Fatalf("draft after restart: %q", got)
	}
	press(t, b, "esc", "q")
	var d editRecord
	if err := b.drafts.Load("task", "t1", &d); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancel left draft: %v", err)
	}
	if got := stored(t, b, "t1").Title; got != "write the brief" {
		t.Fatalf("cancel changed record: %q", got)
	}
}

func TestOmittedAndEmptyCreation(t *testing.T) {
	a := openAt(t, filepath.Join(t.TempDir(), "mtm.db"))
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "new", Args: ochazuke.Arguments{"name": ""}}); err != nil {
		t.Fatal(err)
	}
	if len(a.projects) != 1 || a.projects[0].Name != "" || projectLabel(a.projects[0]) != "untitled" {
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
	if _, err := a.work.Task(id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("omitted title saved before submit: %v", err)
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
	a := open(t)
	tui := func() error {
		// The first project has no tasks: creation must still keep its owner.
		press(t, a, "enter", "a")
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
