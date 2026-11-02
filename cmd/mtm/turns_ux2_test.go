package main

import (
	"testing"

	"ofam.dev/ochazuke"
)

// The Turns tab and its prompt box must preserve turn expansion and navigation.

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
	t.Parallel()
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
	t.Parallel()
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

// F10: a paste that completes after the person left the box goes into the
// draft and leaves the focus where they put it; one that completes in the
// box still focuses it in insert mode.
func TestPasteCompletionKeepsTheFocusWhereItWent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		set  func(*fakeClipboard)
		want string
	}{
		{"text", func(c *fakeClipboard) { c.text = "hello there" }, "draft hello there"},
		{"image", func(c *fakeClipboard) { c.image = true }, "draft [image 1] "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _, clip := clipApp(t)
			tc.set(clip)
			typeText(t, app, "draft ")
			paste := key(t, app, "ctrl+v") // the clipboard read is still running
			press(t, app, "esc", "esc")    // insert, normal, then out to the turns
			left := app.r.Focus
			if left == promptID {
				t.Fatalf("esc esc stayed in the box: %s", where(app))
			}
			settle(app, paste)
			if app.r.Focus != left {
				t.Fatalf("the paste pulled the focus: %s, want %s", where(app), left)
			}
			if got := string(app.pb.input.Text); got != tc.want {
				t.Fatalf("draft %q, want %q", got, tc.want)
			}
			if app.m.Status != "" {
				t.Fatalf("status %q", app.m.Status)
			}
			// Back in the box, i writes after the pasted text.
			press(t, app, "i")
			if app.r.Focus != promptID || app.pb.input.Cursor != len(app.pb.input.Text) {
				t.Fatalf("i: %s cursor %d of %d", where(app), app.pb.input.Cursor, len(app.pb.input.Text))
			}
		})
	}
}

// toScreen: pane.copy and pane.hints run on a pane row from another tab
// switch the detail to Screen. Run while the focus is in the turns (the
// CLI, an action, the palette with a target), the switch takes the focus
// off the turns, which the Screen tab no longer draws (F1's rule), to the
// pane row. Leaving copy mode or hints stays on Screen, as after ]: they
// return to the row, which every tab draws; only live, which returns into
// the turns, brings the Turns tab back (F5).
func TestCopyAndHintsFromTheTurnsLeaveTheHiddenTurns(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"copy", "hints"} {
		t.Run(verb, func(t *testing.T) {
			app, _, _ := claudeTurnsApp(t)
			row := app.pv.turnsFor
			frame(app)
			press(t, app, "l")
			if focusNoun(app) != "turn" && focusNoun(app) != "turn-card" {
				t.Fatalf("l: %s", where(app))
			}
			_, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: verb, Target: row})
			if err != nil {
				t.Fatal(err)
			}
			if app.pv.tab != tabScreen {
				t.Fatalf("pane.%s: tab %d", verb, app.pv.tab)
			}
			frame(app) // the frame that drops the turns moves the focus (ochazuke R3.13)
			if app.r.Focus != row {
				t.Fatalf("pane.%s left the focus on what Screen hides: %s", verb, where(app))
			}
			settle(app, app.r.PendingCommands())
			frame(app)
			if verb == "copy" {
				if app.r.Focus != copyID {
					t.Fatalf("copy mode: %s", where(app))
				}
			}
			press(t, app, "esc")
			frame(app)
			if app.r.Focus != row || app.pv.tab != tabScreen {
				t.Fatalf("after %s: %s tab %d, want the row on Screen", verb, where(app), app.pv.tab)
			}
		})
	}
}
