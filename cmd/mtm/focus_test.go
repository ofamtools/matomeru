package main

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

// A list with rows hands the focus to its selected row, so the row's keys
// answer without a j first.

// mtm opens on Panes, its pane list focused, so its keys answer at once.
func TestStartupOpensPanesWithTheListFocused(t *testing.T) {
	t.Parallel()
	app := openWith(t, Options{Data: seeded(t)})
	if app.r.Page != panesPage || app.r.Focus != paneListID {
		t.Fatalf("startup: page %s focus %s", app.r.Page, app.r.Focus)
	}
}

func TestTheWorkPageFocusesTheFirstProjectRow(t *testing.T) {
	t.Parallel()
	app := open(t)
	f := &fakeDaemon{}
	app.launcher = f
	if app.r.Focus != "ochazuke" {
		t.Fatalf("focus on the Work page = %s", app.r.Focus)
	}
	runOnce(app, launchKey(t, app))
	if len(f.launches) != 1 || f.launches[0].Session != "ochazuke" {
		t.Fatalf("launches = %+v", f.launches)
	}
}

// The Screen tab follows the selection after its pause, with no key.
func TestThePanesPageAnswersPreviewAtOnce(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	settle(app, follow(app))
	if len(f.captured) != 1 || app.preview.Pane != "pane:matomeru/w1" {
		t.Fatalf("captured %v preview %+v", f.captured, app.preview)
	}
}

func TestTheReviewPageOpensOnAStoredReviewRow(t *testing.T) {
	t.Parallel()
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
	press(t, app, "ctrl+j") // Work, then Review
	if app.r.Page != reviewsPage || app.r.Focus != rv.ID {
		t.Fatalf("page %s focus %s", app.r.Page, app.r.Focus)
	}
	press(t, app, "x")
	if got, _ := app.work.Review(rv.ID); got.Status != work.ReviewAccepted {
		t.Fatalf("status = %s", got.Status)
	}
}
