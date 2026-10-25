package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// reviewApp launches an agent on task t1 of matomeru through the fakes,
// then opens the review page on the review the launch recorded.
func reviewApp(t *testing.T) (*App, *fakePanes, work.Review) {
	t.Helper()
	app := open(t)
	f := app.panesd.(*fakePanes)
	app.launcher = &fakeDaemon{}
	press(t, app, "j", "enter")
	key(t, app, "l")
	runOnce(app, key(t, app, "y"))
	reviews, err := app.work.Reviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews after launch = %+v", reviews)
	}
	// The launch landed attached on the panes page: detach, then the
	// next page is the review page.
	press(t, app, "ctrl+a", "d", "ctrl+j")
	if app.r.Page != reviewsPage {
		t.Fatalf("page = %s", app.r.Page)
	}
	return app, f, reviews[0]
}

// storedReview reads a review back from the database.
func storedReview(t *testing.T, a *App, id string) work.Review {
	t.Helper()
	rv, err := a.work.Review(id)
	if err != nil {
		t.Fatal(err)
	}
	return rv
}

func TestALaunchRecordsAReviewOnTheReviewPage(t *testing.T) {
	app, _, rv := reviewApp(t)
	want := work.Review{ProjectID: "matomeru", TaskID: "t1", Harness: "claude", Session: "matomeru", Window: "w1", Pane: "matomeru/write the brief", Status: work.ReviewPending}
	got := rv
	got.ID, got.Created, got.Updated = "", 0, 0
	if got != want {
		t.Fatalf("review = %+v", rv)
	}
	if app.r.Focus != rv.ID {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	fr := frame(app)
	for _, want := range []string{"reviews (1)", "enter captures"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("frame lacks %q:\n%s", want, fr)
		}
	}
	if !rowWith(fr, "pending", "matomeru/write the brief", "claude", "matomeru") {
		t.Fatalf("frame lacks the review row:\n%s", fr)
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestReviewsLoadAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutProject(work.Project{ID: "matomeru", Name: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	rv, err := w.AddReview(work.Review{ProjectID: "matomeru", Session: "matomeru", Window: "w9", Pane: "matomeru/claude"})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	app := openAt(t, path)
	if len(app.reviews) != 1 || app.r.Instances[rv.ID] == nil || app.r.Instances[rv.ID].Label != "[pending] matomeru · (no task) · matomeru/claude" {
		t.Fatalf("reviews at startup = %+v", app.reviews)
	}
}

func TestOpenCapturesThePaneIntoTheDetail(t *testing.T) {
	app, f, rv := reviewApp(t)
	cmd := key(t, app, "enter")
	jobs := jobsOf(app, "review.open")
	if len(jobs) != 1 || jobs[0].Slot != "capture" || jobs[0].Scope != reviewListID || jobs[0].Owner != rv.ID {
		t.Fatalf("open jobs = %+v", jobs)
	}
	settle(app, cmd)
	if !reflect.DeepEqual(f.captured, []string{"w1"}) {
		t.Fatalf("captured = %v", f.captured)
	}
	if got := storedReview(t, app, rv.ID); got.Output != "claude is thinking" || got.CapturedAt == 0 {
		t.Fatalf("stored = %+v", got)
	}
}

func TestAcceptAndRejectPersistAndLeaveTheTask(t *testing.T) {
	app, _, rv := reviewApp(t)
	press(t, app, "a")
	if got := storedReview(t, app, rv.ID).Status; got != work.ReviewAccepted {
		t.Fatalf("accept: status = %q", got)
	}
	if fr := frame(app); !rowWith(fr, "accepted", "matomeru") {
		t.Fatalf("frame after accept:\n%s", fr)
	}
	if got := stored(t, app, "t1").Status; got != work.StatusTodo {
		t.Fatalf("accept changed the task: %q", got)
	}
	press(t, app, "r")
	if got := storedReview(t, app, rv.ID).Status; got != work.ReviewRejected {
		t.Fatalf("reject: status = %q", got)
	}
	if label := app.r.Instances[rv.ID].Label; !strings.HasPrefix(label, "[rejected] ") {
		t.Fatalf("row label = %q", label)
	}
	if app.r.Focus != rv.ID {
		t.Fatalf("focus = %s", app.r.Focus)
	}
}

func TestSaveAnswerThroughThePrompt(t *testing.T) {
	app, _, rv := reviewApp(t)
	press(t, app, "s")
	if app.r.Focus != fieldID || string(app.field.Text) != "" {
		t.Fatalf("s: focus %s field %q", app.r.Focus, string(app.field.Text))
	}
	typeText(t, app, "ship it")
	press(t, app, "enter")
	if got := storedReview(t, app, rv.ID).Answer; got != "ship it" {
		t.Fatalf("answer = %q", got)
	}
	if app.r.Focus != rv.ID || !strings.HasSuffix(app.r.Instances[rv.ID].Label, "(answered)") {
		t.Fatalf("after save: focus %s label %q", app.r.Focus, app.r.Instances[rv.ID].Label)
	}
	// The prompt opens with the saved answer; esc leaves it unchanged.
	press(t, app, "s")
	if string(app.field.Text) != "ship it" {
		t.Fatalf("prefill = %q", string(app.field.Text))
	}
	typeText(t, app, " now")
	press(t, app, "esc")
	if got := storedReview(t, app, rv.ID).Answer; got != "ship it" || app.r.Focus != rv.ID {
		t.Fatalf("esc: answer %q focus %s", got, app.r.Focus)
	}
}

func TestSaveAnswerTakesAnArgumentWithoutAPrompt(t *testing.T) {
	app, _, rv := reviewApp(t)
	if _, err := app.r.Execute(ochazuke.Request{Verb: "saveAnswer", Noun: "review", Target: rv.ID, Args: ochazuke.Arguments{"answer": "from the cli"}}); err != nil {
		t.Fatal(err)
	}
	if app.ask != nil || app.r.Focus == fieldID {
		t.Fatal("the argument path opened a prompt")
	}
	if got := storedReview(t, app, rv.ID).Answer; got != "from the cli" {
		t.Fatalf("answer = %q", got)
	}
}

func TestTheAnswerAreaIsTheSecondStop(t *testing.T) {
	app, _, rv := reviewApp(t)
	settle(app, key(t, app, "enter"))
	press(t, app, "}")
	if app.r.Focus != answerID || app.r.Mode.Name != "insert" {
		t.Fatalf("}: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
	typeText(t, app, "looks right")
	if fr := frame(app); !strings.Contains(fr, "looks right") {
		t.Fatalf("frame while editing:\n%s", fr)
	}
	press(t, app, "enter")
	if got := storedReview(t, app, rv.ID).Answer; got != "looks right" || app.r.Focus != rv.ID {
		t.Fatalf("enter: answer %q focus %s", got, app.r.Focus)
	}
	press(t, app, "}")
	typeText(t, app, " maybe")
	press(t, app, "esc")
	if got := storedReview(t, app, rv.ID).Answer; got != "looks right" || string(app.answer.Text) != "looks right" || app.r.Focus != rv.ID {
		t.Fatalf("esc: answer %q area %q focus %s", got, string(app.answer.Text), app.r.Focus)
	}
}

func TestAFailingCaptureShowsAndTheAppKeepsWorking(t *testing.T) {
	app, f, rv := reviewApp(t)
	f.err = errors.New("dial unix tuios.sock: connect: no such file or directory")
	settle(app, key(t, app, "enter"))
	if !strings.Contains(app.m.Status, "capture matomeru/write the brief: dial unix") {
		t.Fatalf("status = %q", app.m.Status)
	}
	app.m.Status = ""
	if got := storedReview(t, app, rv.ID).Output; got != "" {
		t.Fatalf("output = %q", got)
	}
	press(t, app, "a")
	if got := storedReview(t, app, rv.ID).Status; got != work.ReviewAccepted {
		t.Fatalf("status = %q", got)
	}
	press(t, app, "ctrl+k", "ctrl+k")
	if app.r.Page != "work" {
		t.Fatalf("page = %s", app.r.Page)
	}
}
