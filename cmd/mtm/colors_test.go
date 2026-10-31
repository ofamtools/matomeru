package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// The pin state picks the colours under the default pin.colors accent: no
// pin and pinned draw the owner's theme with its own accent, show all (S)
// that theme with the orange accent.
func TestColoursFollowThePin(t *testing.T) {
	t.Parallel()
	app := open(t)
	executeA(t, app, "project", "unpin", "", nil)
	executeA(t, app, "appearance", "set", "", ochazuke.Arguments{"theme": "dark"})
	app.View()
	ap := app.m.Appearance
	if ap.Theme != "dark" || ap.Accent != nil {
		t.Fatalf("no pin: theme %q accent %v", ap.Theme, ap.Accent)
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

// With no pin the frame has colour: a truecolor foreground, where mono
// draws only attributes.
func TestNoPinFrameHasColour(t *testing.T) {
	t.Parallel()
	app := open(t)
	executeA(t, app, "project", "unpin", "", nil)
	if raw := rawFrame(app); !strings.Contains(raw, "38;2;") {
		t.Fatalf("no pin drew no colour:\n%q", raw)
	}
}

// pin.colors: mono draws no pin in mono (and returns to the theme set
// while unpinned once pinned); off draws the theme in every state, with
// no show-all accent.
func TestPinColorsSetting(t *testing.T) {
	t.Parallel()
	app := open(t)
	set := func(v string) {
		t.Helper()
		if err := app.r.Settings().Set(settingPinColors, v); err != nil {
			t.Fatal(err)
		}
		app.View()
	}
	ap := app.m.Appearance
	executeA(t, app, "project", "unpin", "", nil)
	set(pinColorsMono)
	if ap.Theme != "mono" || ap.Accent != nil {
		t.Fatalf("mono, no pin: theme %q accent %v", ap.Theme, ap.Accent)
	}
	if raw := rawFrame(app); strings.Contains(raw, "38;2;") {
		t.Fatalf("mono, no pin drew colour")
	}
	executeA(t, app, "appearance", "set", "", ochazuke.Arguments{"theme": "dark"})
	app.View()
	if ap.Theme != "mono" {
		t.Fatalf("mono, no pin after appearance set: theme %q", ap.Theme)
	}
	executeA(t, app, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	app.View()
	if ap.Theme != "dark" || ap.Accent != nil {
		t.Fatalf("mono, pinned: theme %q accent %v", ap.Theme, ap.Accent)
	}
	press(t, app, "S")
	if ap.Accent != showAllAccent {
		t.Fatalf("mono, show all: accent %v", ap.Accent)
	}
	set(pinColorsOff)
	if ap.Theme != "dark" || ap.Accent != nil {
		t.Fatalf("off, show all: theme %q accent %v", ap.Theme, ap.Accent)
	}
	press(t, app, "S")
	executeA(t, app, "project", "unpin", "", nil)
	app.View()
	if ap.Theme != "dark" || ap.Accent != nil {
		t.Fatalf("off, no pin: theme %q accent %v", ap.Theme, ap.Accent)
	}
}
