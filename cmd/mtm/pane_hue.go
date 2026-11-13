package main

import (
	"cmp"

	"ofam.dev/ochazuke"
)

// A panel showing a thing draws the thing's project colour on its border
// and title (ochazuke's panel hue, setting panel.hue: both by default,
// border, title or off). A project has the colour it was given (project
// set color=, a hex, an ANSI index or name such as bright-blue); one with
// none has a palette tone picked by its id (ochazuke.PaletteTone), so it
// keeps its colour whatever else exists and a theme recolours it. The
// derived colour is computed here every time, never written to the data.
// A pane has its row's project (the cached owners of the last pane build,
// never a store read in a frame); a side terminal has its parent's. A
// thing with no project keeps the panel's usual look.

// registerProjectHue tells ochazuke the project noun carries a hue, so the
// generated pages' detail panels draw the shown thing's project colour.
func (a *App) registerProjectHue() { a.r.SetHueSource("project", a.projectHue) }

// projectHue is the hue of the project id, "" for an unknown one.
func (a *App) projectHue(id string) string {
	if id == "" {
		return ""
	}
	if p, ok := a.project(id); ok && p.Theme.Color != "" {
		return p.Theme.Color
	}
	return ochazuke.PaletteTone(id)
}

// paneHue is the hue of p's project, "" when it has none.
func (a *App) paneHue(p pane) string {
	if a.pv == nil {
		return ""
	}
	return a.projectHue(a.pv.view.owners[cmp.Or(p.AssociatedPane, p.ID)])
}
