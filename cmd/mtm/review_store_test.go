package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The review verbs: get, list, set and watch standard; no create, delete
// or move (a review follows its pane); status a lasting alias of set of
// status; open, accept, reject, choose-status and saveAnswer named.
func TestReviewVerbsAreStandard(t *testing.T) {
	a := open(t)
	standardIs(t, a, "review", map[string]string{
		"get": "get", "list": "list", "set": "set", "watch": "watch",
		"open": "", "accept": "", "reject": "", "choose-status": "", "saveAnswer": "",
	}, "create", "delete", "move", "rename", "duplicate")
	if status := aliasOf(a, "review.set", "review.status"); status == nil || status.Deprecated() || status.Args["prop"] != "status" {
		t.Fatalf("status: %+v", status)
	}
}

// x, the s chooser and an answer are standard sets: undo reverts each;
// review status ID STATUS and review get read and write over the CLI.
func TestReviewWritesAreStandardAndUndo(t *testing.T) {
	app, _, rv := reviewApp(t)
	press(t, app, "x")
	press(t, app, "s", "r", "enter")
	if got := storedReview(t, app, rv.ID).Status; got != work.ReviewRejected {
		t.Fatalf("s: %q", got)
	}
	executeA(t, app, "history", "undo", "", nil)
	if got := storedReview(t, app, rv.ID).Status; got != work.ReviewAccepted {
		t.Fatalf("undo of s: %q", got)
	}
	executeA(t, app, "history", "undo", "", nil)
	if got := storedReview(t, app, rv.ID).Status; got != work.ReviewPending {
		t.Fatalf("undo of x: %q", got)
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "saveAnswer", Noun: "review", Target: rv.ID, Args: ochazuke.Arguments{"answer": "ship it"}}); err != nil {
		t.Fatal(err)
	}
	executeA(t, app, "history", "undo", "", nil)
	if got := storedReview(t, app, rv.ID).Answer; got != "" {
		t.Fatalf("undo of the answer: %q", got)
	}
	// The deprecated --status (now --value) still sets it, with a notice.
	_, errOut, err := cli(app, "", "review", "status", rv.ID, "--status", "rejected")
	if err != nil {
		t.Fatalf("review status --status rejected: %v\n%s", err, errOut)
	}
	if got := storedReview(t, app, rv.ID).Status; got != work.ReviewRejected {
		t.Fatalf("review status --status: %q", got)
	}
	if !strings.Contains(errOut, "notice: review.status: status is an old name of value") {
		t.Fatalf("review status --status: no deprecation notice on stderr:\n%s", errOut)
	}
	mustCLI(t, app, "review", "status", rv.ID, "accepted")
	var got map[string]any
	cliJSON(t, app, &got, "review", "get", rv.ID)
	if got["status"] != work.ReviewAccepted || got["project"] != "matomeru" || got["task"] != "t1" {
		t.Fatalf("get: %v", got)
	}
	var rows []map[string]any
	cliJSON(t, app, &rows, "review", "list", "--all")
	if len(rows) != 1 || rows[0]["id"] != rv.ID {
		t.Fatalf("list: %v", rows)
	}
}

// A pane mtm did not launch has a row from its journaled turns; a set on
// it writes its record first (record first).
func TestReviewSetWritesAJournalRowsRecord(t *testing.T) {
	path := seeded(t)
	capturedTurns(t, path, "first answer")
	app := openWith(t, Options{Data: path})
	reload(t, app)
	items := app.reviewList.Items()
	if len(items) != 1 {
		t.Fatalf("rows %+v", items)
	}
	id := items[0].ID
	if _, err := app.work.Review(id); err == nil {
		t.Fatal("the journal row was stored before a write")
	}
	executeA(t, app, "review", "set", "", ochazuke.Arguments{"id": id, "prop": "status", "value": "accepted"})
	if got := storedReview(t, app, id); got.Status != work.ReviewAccepted || got.Window == "" {
		t.Fatalf("record: %+v", got)
	}
}
