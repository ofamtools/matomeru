package main

import (
	"testing"

	"ofam.dev/ochazuke"
)

// Handlers tell the user through the runtime's UI, not the Model: with a
// RecordingSink attached in its place a delete's notice lands there and
// the footer stays empty.
func TestHandlerNoticesGoThroughTheUISink(t *testing.T) {
	t.Parallel()
	app := open(t)
	note, err := app.work.AddNote("matomeru", "", "scratch")
	if err != nil {
		t.Fatal(err)
	}
	app.all = true
	if err := app.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	rec := &ochazuke.RecordingSink{}
	prev := app.r.UI().Attach(rec)
	defer app.r.UI().Attach(prev)
	if _, err := app.r.Execute(ochazuke.Request{Noun: "note", Verb: "delete", Target: note.ID, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if got := rec.Notices(); len(got) != 1 || got[0] != "Deleted note scratch" {
		t.Fatalf("recorded notices %q", got)
	}
	if len(app.m.Notices) != 0 {
		t.Fatalf("footer notices %q", app.m.Notices)
	}
}

// Expiry takes notices back by id: a key clears the ones shown before it,
// and a notice cleared elsewhere is not cleared twice.
func TestNoticeExpiryClearsByID(t *testing.T) {
	t.Parallel()
	app := open(t)
	first := app.r.UI().Notify("first")
	app.Update(nil)
	app.r.UI().ClearNotice(first)
	app.r.UI().Notify("second")
	app.Update(nil)
	if len(app.m.Notices) != 1 || app.m.Notices[0] != "second" || len(app.noticeSink.shown) != 1 {
		t.Fatalf("notices %q shown %v", app.m.Notices, app.noticeSink.shown)
	}
	app.Update(keyMsg("j"))
	if len(app.m.Notices) != 0 || len(app.noticeSink.shown) != 0 {
		t.Fatalf("after key: notices %q shown %v", app.m.Notices, app.noticeSink.shown)
	}
}
