package main

import (
	"strings"
	"testing"
)

// j at the bottom of the Turns tab never focuses the prompt box: on the
// newest turn, open and scrolled so its end is off screen, j scrolls (half
// the window at most per press) until the turn's end shows and then stays. The box is
// reached by }, tab and i.
func TestTurnsJAtTheBottomNeverFocusesTheBox(t *testing.T) {
	t.Parallel()
	app, _, log := claudeTurnsApp(t)
	moreClaudeTurns(t, log, 4, 5)
	reload(t, app)
	app.m.Width, app.m.Height = 120, 22
	frame(app)
	press(t, app, "}", "G")
	newest := app.r.Focus
	settle(app, key(t, app, "l"))
	frame(app)
	// Scroll the open newest turn up so its end is off screen (K holds
	// the selection there and stops following).
	for k := 0; k < 10; k++ {
		press(t, app, "K")
	}
	fr := frame(app)
	if !app.pv.cards.IsOpen(newest) || app.r.Focus != newest || strings.Contains(fr, "answer 4 line 8") {
		t.Fatalf("the newest turn's end is still in view (open %v, %s):\n%s", app.pv.cards.IsOpen(newest), where(app), fr)
	}
	for k := 0; k < 12; k++ {
		press(t, app, "j")
		fr = frame(app)
		if app.r.Focus == promptID || app.r.Focus != newest {
			t.Fatalf("j %d at the bottom focused %s\n%s", k+1, where(app), fr)
		}
	}
	if got := turnsRegion(t, app); !strings.Contains(got, "answer 4 line 8") || !strings.Contains(got, "┗") {
		t.Fatalf("the newest turn's end is not in view:\n%s", got)
	}
	// The same from inside the card, on its last step.
	press(t, app, "l")
	for k := 0; k < 20; k++ {
		press(t, app, "j")
		if app.r.Focus == promptID {
			t.Fatalf("j inside the newest turn focused the box")
		}
	}
	if !app.pv.cards.InBody() || !strings.Contains(turnsRegion(t, app), "answer 4 line 8") {
		t.Fatalf("j on the last step left the card: %s", where(app))
	}
	press(t, app, "esc")
	for _, keys := range [][]string{{"}"}, {"tab"}, {"i"}} {
		press(t, app, keys...)
		if app.r.Focus != promptID {
			t.Fatalf("%v from the newest turn focused %s, want the box", keys, where(app))
		}
		if app.r.BaseMode() == "insert" {
			press(t, app, "esc")
		}
		press(t, app, "k")
		if app.r.Focus != newest {
			t.Fatalf("k from the box focused %s", where(app))
		}
	}
}

// h inside a turn on its open answer folds the answer and keeps the cursor
// on it; h again goes back to the card.
func TestTurnCardHFoldsTheAnswer(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	app.m.Width, app.m.Height = 120, 50
	frame(app)
	press(t, app, "}")
	card := app.r.Focus
	settle(app, key(t, app, "l"))
	press(t, app, "l", "j", "j", "j", "j")
	frame(app)
	if got := stepText(t, app); got != "Fixed: the test now fakes the clock." {
		t.Fatalf("not on the answer: %q (%s)", got, where(app))
	}
	_, before, _ := app.pv.cards.Step()
	press(t, app, "h")
	fr := frame(app)
	if !app.pv.cards.Folded(card, "answer") || !app.pv.cards.InBody() {
		t.Fatalf("h on the answer: folded %v %s\n%s", app.pv.cards.Folded(card, "answer"), where(app), fr)
	}
	if _, step, _ := app.pv.cards.Step(); step != before || !strings.Contains(fr, "▌▸ 4 answer") && !strings.Contains(fr, "▌▸ 3 answer") {
		t.Fatalf("the cursor left the folded answer (step %d, was %d):\n%s", step, before, fr)
	}
	press(t, app, "h")
	if app.r.Focus != card || !app.pv.cards.IsOpen(card) {
		t.Fatalf("h on the folded answer: %s", where(app))
	}
}
