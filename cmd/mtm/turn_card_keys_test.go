package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// A turn card's state is its verbs' availability: enter opens a closed
// card (turn.open) and closes an open one (turn.close), l opens and then
// steps inside (timeline.enter), h closes and then goes up to the pane row.
func TestTurnCardEnterOpensThenCloses(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	app.m.Width, app.m.Height = 120, 50
	frame(app)
	press(t, app, "}")
	card := app.r.Focus
	if app.pv.cards.IsOpen(card) {
		t.Fatalf("the card starts open: %s", where(app))
	}
	settle(app, key(t, app, "enter"))
	frame(app)
	if !app.pv.cards.IsOpen(card) || app.r.Focus != card {
		t.Fatalf("enter on the closed card: %s open %v", where(app), app.pv.cards.IsOpen(card))
	}
	settle(app, key(t, app, "enter"))
	frame(app)
	if app.pv.cards.IsOpen(card) || app.r.Focus != card {
		t.Fatalf("enter on the open card: %s open %v", where(app), app.pv.cards.IsOpen(card))
	}
	settle(app, key(t, app, "l"))
	frame(app)
	if !app.pv.cards.IsOpen(card) || app.r.Focus != card {
		t.Fatalf("l on the closed card: %s", where(app))
	}
	press(t, app, "l")
	frame(app)
	if !app.pv.cards.InBody() {
		t.Fatalf("l on the open card: %s", where(app))
	}
	// enter inside goes on to the card's turn.close: the card closes and
	// the focus is on it.
	settle(app, key(t, app, "enter"))
	frame(app)
	if app.pv.cards.IsOpen(card) || app.r.Focus != card {
		t.Fatalf("enter inside: %s open %v", where(app), app.pv.cards.IsOpen(card))
	}
	if !app.r.Available(ochazuke.Request{Noun: "turn", Verb: "open"}, card, app.r.Mode) || app.r.Available(ochazuke.Request{Noun: "turn", Verb: "close"}, card, app.r.Mode) {
		t.Fatal("on a closed card turn.open is available and turn.close is not")
	}
}

// Inside a card the turn's rows answer (the body sits under the card): R
// rereads the pane's turns; the footer reads the body's copy hints, not
// raw verb names.
func TestTurnCardRowsAnswerInsideTheBody(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	app.m.Width, app.m.Height = 160, 50
	frame(app)
	press(t, app, "}")
	settle(app, key(t, app, "l"))
	press(t, app, "l")
	frame(app)
	if !app.pv.cards.InBody() {
		t.Fatalf("not inside: %s", where(app))
	}
	foot := footerLine(app)
	if strings.Contains(foot, "timeline-body.copy") || !strings.Contains(foot, "y copy step") {
		t.Fatalf("footer inside a card: %q", foot)
	}
	settle(app, key(t, app, "R"))
	ran := slices.ContainsFunc(app.r.HistoryOf(0, ochazuke.HistoryFetch), func(e ochazuke.HistoryEntry) bool {
		return e.Request.Key() == "turn.refresh-turns"
	})
	if !ran {
		t.Fatalf("R inside the card did not run turn.refresh-turns: %s", where(app))
	}
}
