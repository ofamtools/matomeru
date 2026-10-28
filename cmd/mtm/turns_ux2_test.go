package main

import (
	"testing"
)

// The Turns tab and its prompt box, round 2 of the UX punch list
// (~/hub/o-plans/matomeru-features.md, "Turns and prompt box UX round 2").

// turnOpen reports whether the focused turn shows its body: an open card,
// or an expanded tree row.
func turnOpen(a *App) bool {
	ref, _ := a.r.Instances[a.r.Focus].Data.(turnRef)
	if ref.Card {
		return a.pv.cards.IsOpen(a.r.Focus)
	}
	return !a.pv.turns.Collapsed(a.r.Focus)
}

// F8: h on an open turn closes it and stays; h on a closed turn goes back
// to the pane row the turns belong to, in both styles.
func TestHOnAClosedTurnGoesToThePaneRow(t *testing.T) {
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		t.Run(style, func(t *testing.T) {
			app, _, _ := claudeTurnsApp(t)
			useTurnStyle(t, app, style)
			row := app.pv.turnsFor
			frame(app)
			press(t, app, "}")
			frame(app)
			if focusNoun(app) != "turn" {
				t.Fatalf("}: %s", where(app))
			}
			turn := app.r.Focus
			if !turnOpen(app) {
				press(t, app, "l")
				frame(app)
			}
			press(t, app, "h")
			frame(app)
			if app.r.Focus != turn || turnOpen(app) {
				t.Fatalf("h on the open turn: %s open %v", where(app), turnOpen(app))
			}
			press(t, app, "h")
			frame(app)
			if app.r.Focus != row {
				t.Fatalf("h on the closed turn: %s, want the pane row %s", where(app), row)
			}
		})
	}
}

// F9: the first l (or }) from the pane row lands on the newest turn in
// both styles; the cards follow the newest and the tree now starts there.
func TestFirstEntryLandsOnTheNewestTurn(t *testing.T) {
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		for _, k := range []string{"l", "}"} {
			t.Run(style+"/"+k, func(t *testing.T) {
				app, _, log := claudeTurnsApp(t)
				useTurnStyle(t, app, style)
				moreClaudeTurns(t, log, 2, 6)
				reload(t, app)
				frame(app)
				press(t, app, k)
				frame(app)
				items := app.turnsList().Items()
				var newest string
				for _, it := range items {
					if it.Noun == "turn" {
						newest = it.ID
					}
				}
				if app.r.Focus != newest {
					t.Fatalf("first %s: %s, want the newest turn %s", k, where(app), newest)
				}
				// Moving up and leaving keeps the place: the next l comes back to it.
				press(t, app, "k")
				frame(app)
				was := app.r.Focus
				press(t, app, "esc")
				frame(app)
				press(t, app, "l")
				frame(app)
				if app.r.Focus != was {
					t.Fatalf("second l: %s, want %s", where(app), was)
				}
			})
		}
	}
}
