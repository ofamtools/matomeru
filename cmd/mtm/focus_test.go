package main

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// A list with rows hands the focus to its selected row, so the row's keys
// answer without a j first.

func TestStartupFocusesTheFirstProjectRow(t *testing.T) {
	app := open(t)
	app.launcher = &fakeDaemon{}
	if app.r.Focus != "ochazuke" {
		t.Fatalf("focus at startup = %s", app.r.Focus)
	}
	key(t, app, "l")
	if prompt, _ := confirmPrompt(t, app); prompt != "Launch into ochazuke?" {
		t.Fatalf("confirm = %q", prompt)
	}
}

func TestStartupWithNoProjectsKeepsTheList(t *testing.T) {
	app := openAt(t, filepath.Join(t.TempDir(), "mtm.db"))
	if app.r.Focus != "projects" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
}

func TestThePanesPageAnswersPreviewAtOnce(t *testing.T) {
	app, f := panesApp(t)
	settle(app, key(t, app, "p"))
	if len(f.captured) != 1 || app.preview.Pane != "pane:matomeru/w1" {
		t.Fatalf("captured %v preview %+v", f.captured, app.preview)
	}
}

func TestTheReviewPageOpensOnAStoredReviewRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	rv, err := w.AddReview(work.Review{ProjectID: "p", Session: "s", Window: "w1", Pane: "s/w1"})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	app := openAt(t, path)
	press(t, app, "ctrl+j", "ctrl+j")
	if app.r.Page != reviewsPage || app.r.Focus != rv.ID {
		t.Fatalf("page %s focus %s", app.r.Page, app.r.Focus)
	}
	press(t, app, "a")
	if got, _ := app.work.Review(rv.ID); got.Status != work.ReviewAccepted {
		t.Fatalf("status = %s", got.Status)
	}
}
