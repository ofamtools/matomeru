package main

import (
	"slices"

	"ofam.dev/ochazuke"
)

// The Panes page rail (pane-rail plan, lane rail-1-mtm; owner decisions of
// 2026-11-13: full look, Needs you and Focus today open and the rest a
// picker of kinds, the pane's things then its project's, reply links
// marked, a float over a zoomed terminal, j/k stay at the ends, on by
// default). The rail is ochazuke's (Runtime.AddRail): a related stack of
// the pane the page shows, drawn right of the detail. What it lists is
// data: the referrers of pane (context items split by type, action items,
// notes, relations, and any extension noun that declares a pane prop),
// the day's focus list, and the groups and order below. Z toggles it
// (ochazuke.rail.toggle); ctrl+a Z reaches it from live view through the
// leader fall-through.

const (
	paneRailID = panesPage + "/rail"
	// settingRailZoom is the rail's look over a zoomed live pane.
	settingRailZoom = "rail.zoom"
	railZoomFloat   = "float"
	railZoomFull    = "full"
	railZoomNone    = "none"
)

// railZoom is how a rail.zoom value draws the rail over a zoomed pane: in
// which look, or not at all.
type railZoom struct {
	look  string
	shown bool
}

// railZooms are the rail.zoom values.
var railZooms = map[string]railZoom{
	railZoomFloat: {look: ochazuke.RailLookFloat, shown: true},
	railZoomFull:  {look: ochazuke.RailLookFull, shown: true},
	railZoomNone:  {},
}

// railSettings are the rail settings mtm declares before AddRail: its own
// defaults for three of ochazuke's rows (AddRail keeps a row the
// application declared first) and rail.zoom, which only mtm reads.
func railSettings() []ochazuke.Setting {
	framework := func(s ochazuke.Setting) ochazuke.Setting {
		s.Section, s.Owner = "rail", ochazuke.OwnerFramework
		return s
	}
	return append([]ochazuke.Setting{
		framework(ochazuke.Setting{Name: ochazuke.RailCollapsedSetting, Type: ochazuke.SettingList, Default: []string{"*", "!needs-you", "!focus", "!new", "!starred"},
			Description: "Section ids that start folded to their heading and count (* every one: a picker of kinds first; !ID keeps one open). Default: Focus today, New, Needs you and Starred open, the rest folded"}),
		framework(ochazuke.Setting{Name: ochazuke.RailHiddenSetting, Type: ochazuke.SettingList, Default: []string{contextNoun + ".window", actionItemNoun + ".pane"},
			Description: "Section ids the rail leaves out (a trailing * is a prefix; !ID takes one back out of an earlier pattern). Default: the pane's every context item and every action item, which New, the kinds, Needs you and Resolved list triaged"}),
		framework(ochazuke.Setting{Name: ochazuke.RailGroupsSetting, Type: ochazuke.SettingList,
			Default: []string{
				"new = context-item." + contextNewPane,
				"needs-you = action-item." + actionNeedsPane + ", question.decision, question.question, cmd.cmd",
				"starred = context-item." + contextStarredPane,
				"this-project = context-item.project-pane",
				"resolved = action-item." + actionResolvedPane,
				"dismissed = context-item." + contextDismissPane,
			},
			Description: "Headings that draw several sections under one, as ID = MEMBER, MEMBER (a member is a section id or a noun). new is the pane's context items neither starred nor dismissed; needs-you is what waits on you (open, oldest first); starred the starred items; this-project the rest of the pane's project's context (add it to rail.hidden for the pane's own alone); resolved the read and archived action items; dismissed the dismissed items (rail.dismissed collapsed)"}),
		framework(ochazuke.Setting{Name: ochazuke.RailOrderSetting, Type: ochazuke.SettingList,
			Default: []string{"focus", "new", "needs-you", "starred",
				"context-item.type=link", "context-item.type=file", "context-item.type=dir", "context-item.type=pr",
				"context-item.type=branch", "context-item.type=commit", "context-item.type=command", "this-project", "resolved", "dismissed"},
			Description: "Section ids (NOUN.PROP, a group's or a list section's id) the rail draws first, in this order; the others follow"}),
		{Name: settingRailZoom, Section: "rail", Type: ochazuke.SettingString, Default: railZoomFloat, Enum: []string{railZoomFloat, railZoomFull, railZoomNone},
			Description: "The rail over a zoomed live pane: float (a panel over its top-right corner; the terminal keeps its width), full (a column; the terminal narrows) or hidden (no rail until unzoomed)"},
	}, railTriageSettings()...)
}

// addPaneRail registers the Panes page's rail and binds Z to its toggle.
func (a *App) addPaneRail() error {
	rail, err := a.r.AddRail(paneRailID, ochazuke.RailSpec{Stack: ochazuke.RelatedSpec{
		Target:  a.railTarget,
		GroupBy: map[string]string{contextNoun + "." + contextKeptPane: "type"},
		Lists:   []ochazuke.RelatedList{{ID: "focus", Noun: "task", Filter: focusFilterName, Label: "Focus today", New: focusItemNoun + ".new"}},
	}}, ochazuke.Parent(panesPage))
	if err != nil {
		return err
	}
	a.rail = rail
	a.r.Define("app").Mode("normal").Bind("Z", ochazuke.Do(ochazuke.RailNoun+".toggle").Hinted("rail"))
	return nil
}

// railTarget is the pane the rail lists: in live view the attached pane,
// or the side pane while its terminal holds the focus; else the selected
// row's pane.
func (a *App) railTarget() (string, string) {
	if a.liveShown() {
		if side := a.r.Terminal(sideTermID); side != nil && side.DrawnFocused() {
			if p, ok := a.r.Instances[sideTermID].Data.(pane); ok && p.ID != "" {
				return "pane", p.ID
			}
		}
		if a.attached.ID != "" {
			return "pane", a.attached.ID
		}
	}
	return a.paneTarget()
}

// withRail draws main (drawn by draw at the size it is given) with the
// rail beside or over it in an area of width x height, in z's look (""
// for rail.look; not shown: no rail). The main is drawn at the size the
// rail leaves it, so a live terminal in it is sized to that. least is the
// width the main needs to keep the layout it has at the whole width (a
// side beside its parent): a column that would leave less steps down to
// the float, so the rail never changes the main's layout.
func (a *App) withRail(layout ochazuke.Layout, width, height, least int, z railZoom, draw func(w, h int) ochazuke.Block) ochazuke.Block {
	if a.rail == nil || !z.shown {
		return draw(width, height)
	}
	fit := a.rail.FitWith(width, height, z.look)
	if fit.Look == ochazuke.RailLookFull && fit.MainWidth < least {
		fit = a.rail.FitWith(width, height, ochazuke.RailLookFloat)
	}
	// A float with nothing to list covers the main for nothing; the full
	// column stays, so a live terminal is not resized as sections come and
	// go. (App-side workaround: upstream, FitRail could leave out an empty
	// float.)
	empty := fit.Look == ochazuke.RailLookFloat && !slices.Contains(a.r.Ancestry(a.r.Focus), paneRailID) && a.rail.Stack().FocusTarget() == a.rail.Stack().ID()
	if fit.Look == "" || empty {
		return draw(width, height)
	}
	layout.Width, layout.Height = width, height
	main := draw(fit.MainWidth, fit.MainHeight)
	a.decorateRail() // after the main, whose draw may refill a section
	return a.rail.Compose(layout, fit, main)
}
