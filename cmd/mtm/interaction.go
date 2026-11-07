package main

import "ofam.dev/ochazuke"

// The interaction reads. Handlers and dispatch read the live interaction
// through Runtime.FocusID and Runtime.CurrentMode; drawing code asks
// Runtime.PresentationAt for the surface it draws, so a page beneath an
// open modal draws the focus the modal saved when it opened. Code asks a
// mode's traits (Runtime.ModeTraits), never its name.

// shownFocus is the focus the surface holding id is drawn with: the live
// focus on the top surface, beneath an open modal the focus the modal
// above it saved.
func (a *App) shownFocus(id string) string { return a.r.PresentationAt(id).Focus }

// shownFocused reports whether id is drawn as its surface's focus.
func (a *App) shownFocused(id string) bool { return a.shownFocus(id) == id }

// shownBaseMode is the base mode view is drawn in: the live base mode
// (Runtime.BaseMode, under a pending prefix the mode it returns to) on the
// top surface, else the base mode the modal above saved.
func (a *App) shownBaseMode(view ochazuke.Presentation) string {
	if view.Live {
		return a.r.BaseMode()
	}
	return view.Mode.Name
}

// modeTraits is mode's own traits: whether it types text (Typing), rests
// the cursor on a character (CursorOnChar, the resting mode), is pending.
// An undefined mode has none.
func (a *App) modeTraits(mode string) ochazuke.ModeSpec {
	spec, _ := a.r.ModeTraits(mode)
	return spec
}

// liveModeTraits is the live mode's own traits.
func (a *App) liveModeTraits() ochazuke.ModeSpec { return a.modeTraits(a.r.CurrentMode().Name) }
