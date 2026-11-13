package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// Rail icons (rail.icons): each rail row starts with a one-cell glyph of
// its kind, read from railGlyphs by the row's noun and, for a noun whose
// things have kinds (a context item's type), noun:kind. A noun with no row
// takes the default. The table is the only place a kind is named.
//
// App-side workaround: ochazuke draws a related row's label as its label
// prop holds it, with no per-row glyph, so mtm prefixes the rail's row
// labels before each draw (decorateRail). Upstream, a noun could declare a
// glyph prop that related rows draw first (todo in o-plans).

// railGlyph is one kind's glyph in each rail.icons style.
type railGlyph struct{ symbol, nerd, letter string }

// railGlyphs are the glyphs by noun or noun:kind ("" the default), the
// data in rail_glyphs.json. Every glyph is one cell wide
// (TestRailGlyphsAreOneCell).
var railGlyphs = func() map[string]railGlyph {
	var rows map[string]struct{ Symbol, Nerd, Letter string }
	if err := json.Unmarshal(railGlyphsJSON, &rows); err != nil {
		panic("rail_glyphs.json: " + err.Error())
	}
	out := make(map[string]railGlyph, len(rows))
	for k, g := range rows {
		out[k] = railGlyph{symbol: g.Symbol, nerd: g.Nerd, letter: g.Letter}
	}
	return out
}()

//go:embed rail_glyphs.json
var railGlyphsJSON []byte

// railGlyphFor is the glyph of a row's thing in style ("" when off).
func railGlyphFor(style, noun string, data any) string {
	if style == railIconsOff || style == "" {
		return ""
	}
	kind := ""
	if t, ok := data.(ochazuke.Thing); ok {
		if v, ok := t["type"].(string); ok {
			kind = v
		}
	}
	g, ok := railGlyphs[noun+":"+kind]
	if !ok {
		if g, ok = railGlyphs[noun]; !ok {
			g = railGlyphs[""]
		}
	}
	switch style {
	case railIconsNerd:
		return g.nerd
	case railIconsLetters:
		return g.letter
	}
	return g.symbol
}

// railDecor is a row label decorateRail wrote: the label it came with and
// the one it drew, so a refill (a fresh label) and a style change are told
// apart from its own work.
type railDecor struct{ raw, drawn string }

// decorateRail prefixes the rail's thing rows with their glyph: every
// section list under the rail's stack, its rows that are not headings.
// It changes a list only when a row's label is not the one it drew.
func (a *App) decorateRail() {
	if a.rail == nil {
		return
	}
	style := a.r.Settings().String(settingRailIcons)
	if a.railDecor == nil || len(a.railDecor) > 4096 {
		a.railDecor = map[string]railDecor{}
	}
	stack := a.rail.Stack()
	stack.Key() // in step with the pane first, so the rows decorated are the ones drawn
	for _, section := range a.r.Children(stack.ID()) {
		list := a.r.List(section.ID + "/list")
		if list == nil {
			continue
		}
		items := list.Items()
		changed := false
		for i, item := range items {
			if item.Noun == ochazuke.RelatedNoun || strings.HasSuffix(item.ID, "/@head") {
				continue
			}
			raw := item.Label
			if d, ok := a.railDecor[item.ID]; ok && d.drawn == item.Label {
				raw = d.raw
			}
			drawn := raw
			if glyph := railGlyphFor(style, item.Noun, item.Data); glyph != "" {
				drawn = glyph + " " + raw
			}
			a.railDecor[item.ID] = railDecor{raw: raw, drawn: drawn}
			if drawn != item.Label {
				items[i].Label = drawn
				changed = true
			}
		}
		if changed {
			if err := list.SetItems(items); err != nil {
				a.r.UI().Error(fmt.Sprintf("rail icons: %v", err))
			}
		}
	}
}

// syncRailArchived shows the archived things of the sections rail.archived
// names (each section list's own view state, as a list's archived toggle
// is) and reads the stack again when one changed.
func (a *App) syncRailArchived() {
	if a.rail == nil {
		return
	}
	stack := a.rail.Stack()
	stack.Key()
	named := a.r.Settings().Strings(settingRailArchived)
	changed := false
	for _, section := range a.r.Children(stack.ID()) {
		list := a.r.List(section.ID + "/list")
		if list == nil {
			continue
		}
		want := slices.Contains(named, strings.TrimPrefix(section.ID, stack.ID()+"/"))
		if list.ShowsArchived() != want {
			list.SetShowArchived(want)
			changed = true
		}
	}
	if changed {
		stack.Refill()
	}
}
