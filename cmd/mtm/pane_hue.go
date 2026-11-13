package main

import (
	"cmp"
	"slices"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// A panel showing a thing draws the thing's project colour on its border
// and title (ochazuke's panel hue, setting panel.hue: both by default,
// border, title or off). A project has the colour it was given (project
// set color=, a hex, an ANSI index or name such as bright-blue); one with
// none, or one that does not parse, has the palette tone of its creation
// rank (setProjectHues), so a theme recolours it. A derived colour is
// computed, never written to the data.
// A pane has its row's project (the cached owners of the last pane build,
// never a store read in a frame); a side terminal has its parent's. A
// thing with no project keeps the panel's usual look.

// registerProjectHue tells ochazuke the project noun carries a hue, so the
// generated pages' detail panels draw the shown thing's project colour.
func (a *App) registerProjectHue() { a.r.SetHueSource("project", a.projectHue) }

// projectHue is the hue of the project id, "" for an unknown one: its own
// colour when it parses, else the palette tone of its rank.
func (a *App) projectHue(id string) string {
	return a.hues[id]
}

// setProjectHues computes every project's hue when the project list loads
// or changes, never in a frame. A project's palette position is its rank by
// creation time (ties by id) among all projects, archived ones included, so
// the first palette-size projects are distinct and adding or archiving one
// never recolours another. Nothing is written to the data.
func (a *App) setProjectHues() {
	ranked := slices.Clone(a.projects)
	slices.SortStableFunc(ranked, func(x, y work.Project) int {
		return cmp.Or(cmp.Compare(x.Created, y.Created), cmp.Compare(x.ID, y.ID))
	})
	a.hues = make(map[string]string, len(ranked))
	for i, p := range ranked {
		if ochazuke.ValidHue(p.Theme.Color) {
			a.hues[p.ID] = p.Theme.Color
		} else {
			a.hues[p.ID] = ochazuke.PaletteToneAt(i)
		}
	}
}

// paneHue is the hue of p's project, "" when it has none.
func (a *App) paneHue(p pane) string {
	if a.pv == nil {
		return ""
	}
	return a.projectHue(a.pv.view.owners[cmp.Or(p.AssociatedPane, p.ID)])
}
