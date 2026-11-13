package main

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"slices"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// A panel showing a thing draws the thing's project colour on its border
// and title (ochazuke's panel hue, setting panel.hue: both by default,
// border, title or off). A project has the colour stored in its color prop:
// one it was given (project set color=: a hex, an ANSI index or name such as
// bright-blue, or a palette tone), else a palette tone (palette.N) picked
// when the project was created and written as its color, so a theme can
// recolour it. A colour that does not parse draws the tone its id hashes to.
// A pane has its row's project (the cached owners of the last pane build,
// never a store read in a frame); a side terminal has its parent's. A thing
// with no project keeps the panel's usual look.

// registerProjectHue tells ochazuke the project noun carries a hue, so the
// generated pages' detail panels draw the shown thing's project colour.
func (a *App) registerProjectHue() { a.r.SetHueSource("project", a.projectHue) }

// projectHue is the hue of the project id, "" for an unknown one.
func (a *App) projectHue(id string) string { return a.hues[id] }

// setProjectHues computes every project's hue when the project list loads
// or changes, never in a frame: its stored colour when that parses, else the
// palette tone its id hashes to.
func (a *App) setProjectHues() {
	a.hues = make(map[string]string, len(a.projects))
	for _, p := range a.projects {
		if ochazuke.ValidHue(p.Theme.Color) {
			a.hues[p.ID] = p.Theme.Color
			continue
		}
		h := fnv.New32a()
		h.Write([]byte(p.ID))
		a.hues[p.ID] = ochazuke.PaletteToneAt(int(h.Sum32() >> 1))
	}
}

// paneHue is the hue of p's project, "" when it has none.
func (a *App) paneHue(p pane) string {
	if a.pv == nil {
		return ""
	}
	return a.projectHue(a.pv.view.owners[cmp.Or(p.AssociatedPane, p.ID)])
}

// pickProjectColor is the colour of a new project that names none: one of
// the palette tones the fewest of the taken colours use, chosen at random
// among them. A tone no live project holds is the fewest (zero); when every
// tone is held the least-used ones are, by the same rule. colorRand is the
// random source (tests seed it).
func (a *App) pickProjectColor(taken []string) string {
	count := map[string]int{}
	for _, c := range taken {
		count[c]++
	}
	least, tones := -1, []string(nil)
	for i := range ochazuke.PaletteSize() {
		tone := ochazuke.PaletteToneAt(i)
		switch n := count[tone]; {
		case least < 0 || n < least:
			least, tones = n, []string{tone}
		case n == least:
			tones = append(tones, tone)
		}
	}
	return tones[a.colorRand(len(tones))]
}

// colorRandom is the default random source of pickProjectColor.
func colorRandom(n int) int { return rand.IntN(n) }

// assignProjectColors gives every project with no colour a palette tone the
// way a new project gets one (pickProjectColor over the live projects'
// colours), oldest first, each stored through project set so it is a
// recorded change that history.undo reverts. A project with any colour value,
// valid or not, is left alone, so a second run changes nothing. It runs when
// the interactive app starts, not for a command or the MCP gateway.
func (a *App) assignProjectColors() error {
	if err := a.refreshProjects(); err != nil {
		return err
	}
	todo := slices.DeleteFunc(slices.Clone(a.projects), func(p work.Project) bool { return p.Theme.Color != "" })
	slices.SortFunc(todo, func(x, y work.Project) int {
		return cmp.Or(cmp.Compare(x.Created, y.Created), cmp.Compare(x.ID, y.ID))
	})
	for _, p := range todo {
		var taken []string
		for _, o := range a.projects {
			if !o.Archived && o.Theme.Color != "" {
				taken = append(taken, o.Theme.Color)
			}
		}
		_, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "set", Args: ochazuke.Arguments{"id": p.ID, "props": map[string]any{"color": a.pickProjectColor(taken)}}})
		if err != nil {
			return fmt.Errorf("color project %s: %w", p.ID, err)
		}
		if err := a.refreshProjects(); err != nil {
			return err
		}
	}
	return nil
}
