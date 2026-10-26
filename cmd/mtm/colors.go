package main

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Colours follow the pin: pinned draws the owner's theme unchanged, show
// all (S) the same theme with an orange accent (title, borders, active
// tab, accent highlights), no pin ochazuke's mono. Errors and muted text
// keep their colours under the accent (ochazuke's Appearance.Accent), so
// state glyphs stay readable; mono is colourless by design. The owner's
// theme (the theme setting, appearance set) is the base the pin state is
// drawn over.

// showAllAccent is the show-all accent.
var showAllAccent color.Color = lipgloss.Color("#E8833A")

// pinColors is what pinColors last wrote and the base theme beneath it.
type pinColors struct {
	base, wrote string
}

// applyPinColors sets the Model's theme and accent for the pin state. A
// theme someone else wrote since (the theme setting, appearance set) is
// the new base.
func (a *App) applyPinColors() {
	ap := a.m.Appearance
	if ap.Theme != a.colors.wrote {
		a.colors.base = ap.Theme
	}
	theme, accent := a.colors.base, color.Color(nil)
	switch {
	case a.all:
		accent = showAllAccent
	case a.pin == "":
		theme = "mono"
	}
	ap.Theme, ap.Accent, a.colors.wrote = theme, accent, theme
}
