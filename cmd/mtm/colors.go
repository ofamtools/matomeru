package main

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"ofam.dev/ochazuke"
)

// Colours follow the pin. The owner's theme (the theme setting, appearance
// set) is the base every pin state is drawn over, and the setting
// pin.colors picks a row of pinPalettes: what each pin state does to that
// theme. accent (the default) draws the theme with its own accent pinned
// and with no pin, and with the orange accent under show all (S: title,
// borders, active tab, accent highlights); mono draws no pin in ochazuke's
// mono, as mtm did before; off draws the theme in every state. Errors and
// muted text keep their colours under an accent (ochazuke's
// Appearance.Accent), so state glyphs stay readable.

// showAllAccent is the show-all accent.
var showAllAccent color.Color = lipgloss.Color("#E8833A")

// The pin states and pin.colors' choices.
const (
	pinStatePinned = "pinned"
	pinStateAll    = "all"
	pinStateNone   = "none"

	settingPinColors = "pin.colors"
	pinColorsAccent  = "accent"
	pinColorsMono    = "mono"
	pinColorsOff     = "off"
)

// pinPalette is what one pin state draws: a theme in place of the base
// ("" keeps the base) and an accent (nil keeps the theme's).
type pinPalette struct {
	Theme  string
	Accent color.Color
}

// pinPalettes are pin.colors' rows: per pin state, its palette; a state
// with no row draws the base theme unchanged.
var pinPalettes = map[string]map[string]pinPalette{
	pinColorsAccent: {pinStateAll: {Accent: showAllAccent}},
	pinColorsMono:   {pinStateAll: {Accent: showAllAccent}, pinStateNone: {Theme: "mono"}},
	pinColorsOff:    {},
}

func pinColorsSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingPinColors, Type: ochazuke.SettingString, Default: pinColorsAccent, Enum: []string{pinColorsAccent, pinColorsMono, pinColorsOff},
			Description: "How the pin state colours the theme: accent (the theme pinned or not, an orange accent under show all), mono (no pin draws mono), or off (the theme in every state)"},
	}
}

// pinColors is what pinColors last wrote and the base theme beneath it.
type pinColors struct {
	base, wrote string
}

// pinState is the pin state's row name.
func (a *App) pinState() string {
	switch {
	case a.all:
		return pinStateAll
	case a.pin == "":
		return pinStateNone
	}
	return pinStatePinned
}

// applyPinColors sets the Model's theme and accent for the pin state. A
// theme someone else wrote since (the theme setting, appearance set) is
// the new base.
func (a *App) applyPinColors() {
	ap := a.m.Appearance
	if ap.Theme != a.colors.wrote {
		a.colors.base = ap.Theme
	}
	rows, ok := pinPalettes[a.r.Settings().String(settingPinColors)]
	if !ok {
		rows = pinPalettes[pinColorsAccent]
	}
	pal := rows[a.pinState()]
	theme := a.colors.base
	if pal.Theme != "" {
		theme = pal.Theme
	}
	ap.Theme, ap.Accent, a.colors.wrote = theme, pal.Accent, theme
}
