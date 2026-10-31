package main

import (
	"testing"
)

// F4: i from outside the box writes after the draft, not into it.
func TestPromptFromTheRowAppendsToTheDraft(t *testing.T) {
	t.Parallel()
	for _, style := range []string{turnsStyleCards, turnsStyleTree} {
		t.Run(style, func(t *testing.T) {
			app, _, _ := claudeTurnsApp(t)
			useTurnStyle(t, app, style)
			frame(app)
			press(t, app, "i")
			typeText(t, app, "hello")
			press(t, app, "esc", "esc", "esc")
			frame(app)
			if focusNoun(app) != "pane" {
				t.Fatalf("esc esc esc focused %s", where(app))
			}
			press(t, app, "i")
			typeText(t, app, "draft")
			if got := string(app.pb.input.Text); got != "hellodraft" {
				t.Fatalf("i from the row: %q", got)
			}
			press(t, app, "esc", "esc")
			frame(app)
			if focusNoun(app) != "turn" {
				t.Fatalf("esc esc focused %s", where(app))
			}
			press(t, app, "i")
			typeText(t, app, "!")
			if got := string(app.pb.input.Text); got != "hellodraft!" {
				t.Fatalf("i from a turn: %q", got)
			}
		})
	}
}
