package main

import "testing"

// openCards counts the shown timeline's open cards.
func openCards(a *App) int {
	n := 0
	for _, c := range a.pv.cards.Cards() {
		if a.pv.cards.IsOpen(c.ID) {
			n++
		}
	}
	return n
}

// pressRefresh sends R and runs what it scheduled; it reports whether a
// refresh ran (the status names what it found) and clears the status.
func pressRefresh(t *testing.T, a *App) bool {
	t.Helper()
	_, cmd := a.Update(keyMsg("R"))
	settle(a, cmd)
	settle(a, a.r.PendingCommands())
	ran := a.m.Info == "turns: transcript s1"
	a.m.Info = ""
	return ran
}

// R (pane.refresh-turns) and the timeline's z R (open-all) live side by
// side on the Turns tab: on a card z M and z R close and open every card
// without refreshing, and R refreshes from a card, a tree row and the pane
// row, in both styles.
func TestTurnsRefreshKeyAndOpenAll(t *testing.T) {
	app, _, _ := cardsApp(t)
	press(t, app, "}")
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "turn" {
		t.Fatalf("} focused %s", where(app))
	}
	press(t, app, "z", "M")
	if n := openCards(app); n != 0 {
		t.Fatalf("z M left %d cards open", n)
	}
	press(t, app, "z", "R")
	if n, all := openCards(app), len(app.pv.cards.Cards()); n != all || all < 2 {
		t.Fatalf("z R opened %d of %d cards", n, all)
	}
	if !pressRefresh(t, app) {
		t.Fatalf("R on a card did not refresh (%s)", where(app))
	}
	press(t, app, "esc")
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "pane" {
		t.Fatalf("esc focused %s", where(app))
	}
	if !pressRefresh(t, app) {
		t.Fatal("R on the pane row (cards) did not refresh")
	}

	useTurnStyle(t, app, turnsStyleTree)
	frame(app)
	if !pressRefresh(t, app) {
		t.Fatal("R on the pane row (tree) did not refresh")
	}
	press(t, app, "}")
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "turn" {
		t.Fatalf("} in the tree focused %s", where(app))
	}
	if !pressRefresh(t, app) {
		t.Fatalf("R on a tree row did not refresh (%s)", where(app))
	}
}
