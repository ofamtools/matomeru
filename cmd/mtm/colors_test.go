package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// The pin state picks the colours: no pin is mono, pinned the owner's
// theme, show all (S) that theme with the orange accent; the theme set
// while unpinned is the base the pin returns to.
func TestColoursFollowThePin(t *testing.T) {
	app := open(t)
	executeA(t, app, "project", "unpin", "", nil)
	app.View()
	ap := app.m.Appearance
	if ap.Theme != "mono" || ap.Accent != nil {
		t.Fatalf("no pin: theme %q accent %v", ap.Theme, ap.Accent)
	}
	executeA(t, app, "appearance", "set", "", ochazuke.Arguments{"theme": "dark"})
	app.View()
	if ap.Theme != "mono" {
		t.Fatalf("no pin after appearance set: theme %q", ap.Theme)
	}
	executeA(t, app, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	app.View()
	if ap.Theme != "dark" || ap.Accent != nil {
		t.Fatalf("pinned: theme %q accent %v", ap.Theme, ap.Accent)
	}
	plain := rawFrame(app)
	press(t, app, "S")
	if ap.Theme != "dark" || ap.Accent != showAllAccent {
		t.Fatalf("show all: theme %q accent %v", ap.Theme, ap.Accent)
	}
	// #E8833A as a truecolor foreground, drawn only under show all.
	const orange = "232;131;58"
	if strings.Contains(plain, orange) || !strings.Contains(rawFrame(app), orange) {
		t.Fatalf("the accent is not drawn under show all only")
	}
	press(t, app, "S")
	if ap.Accent != nil {
		t.Fatalf("S again kept the accent")
	}
}
