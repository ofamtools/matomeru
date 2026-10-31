package main

import (
	"strings"
	"testing"
)

// F6: a send selects the newest turn and follows it; focusing the box
// does not move the turns.
func TestSendFollowsTheNewTurn(t *testing.T) {
	t.Parallel()
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		t.Run(style, func(t *testing.T) {
			app, _, log := claudeTurnsApp(t)
			useTurnStyle(t, app, style)
			moreClaudeTurns(t, log, 2, 6)
			reload(t, app)
			frame(app)
			press(t, app, "}")
			frame(app)
			press(t, app, "g", "g")
			frame(app)
			first := app.turnsList().Items()[0].ID
			if sel, _ := app.turnsList().Selected(); sel.ID != first {
				t.Fatalf("gg selected %s", sel.ID)
			}
			press(t, app, "i")
			frame(app)
			if sel, _ := app.turnsList().Selected(); sel.ID != first {
				t.Fatalf("focusing the box moved the turns to %s", sel.ID)
			}
			typeText(t, app, "prompt number 6")
			moreClaudeTurns(t, log, 6, 7)
			pressSettle(t, app, "enter")
			frame(app)
			items := app.turnsList().Items()
			last := items[len(items)-1]
			if !strings.Contains(last.Label, "prompt number 6") {
				t.Fatalf("the new turn is not shown: last %q", last.Label)
			}
			if sel, _ := app.turnsList().Selected(); sel.ID != last.ID {
				t.Fatalf("after the send the turns are on %s, want %s", sel.ID, last.ID)
			}
			if style == turnsStyleCards && !app.pv.cards.Following() {
				t.Fatal("the cards do not follow the newest after a send")
			}
			if app.r.Focus != promptID {
				t.Fatalf("the send moved the focus: %s", where(app))
			}
		})
	}
}
