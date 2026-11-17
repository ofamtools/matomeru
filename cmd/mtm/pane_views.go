package main

import (
	"strings"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/daemon"
)

// The Go the pane index page's declarations cite (run plan 5.6, cut 3;
// pages/pane-index.json, nouns/pane.json), over today's listing and
// today's streams, nothing of the Panes page's state:
//   - the hook pane.terminal opens a pane's stream for the show page's live
//     view (the prop live, view terminal; ochazuke.detail.attach), the
//     stream every terminal mtm draws opens (openStream);
//   - the derivation pane.gutter is the Panes page's gutter as a prop
//     (markerGutter, the function the gutter calls: star, side, state),
//     so the index shows it as a column. Setting panes.index-marks off
//     gives none.

// settingIndexMarks turns the pane index's gutter marks on (default) or off.
const settingIndexMarks = "panes.index-marks"

func (a *App) paneViews() {
	r := a.r
	r.AddHook("pane.terminal", ochazuke.TerminalHook(func(c ochazuke.Ctx, key string) (ochazuke.TerminalStream, error) {
		p, ok := a.paneLookup(c, key)
		if !ok {
			p = pane{Pane: daemon.Pane{ID: key}} // not listed (yet): the stream says what it finds
		}
		return a.openStream(p), nil
	}))
	r.RegisterDerivation("pane.gutter", ochazuke.DerivationKind{
		About: "The Panes page's gutter: ★ starred, ◧ a side pane open, the agent's state glyph (? waiting on you, ● working, ✓ done, ✗ errored, ○ idle, × exited)",
		Type:  "string",
		Derive: func(c ochazuke.Ctx, _ ochazuke.Derivation, t ochazuke.Thing) (any, bool) {
			p, ok := a.paneOfThing(t)
			if !r.Settings().Bool(settingIndexMarks) || !ok {
				return nil, false
			}
			if g := markerGutter(a.sideFields(p)); strings.TrimSpace(g) != "" {
				return g, true
			}
			return nil, false
		},
	})
}

// paneOfThing is the pane a pane thing stands for: the listed one (with
// what only the listing holds: sides, subagents, needs you), else the
// thing's own props (a read before the Panes page listed; an ended pane's
// copy, as the Panes page shows it).
func (a *App) paneOfThing(t ochazuke.Thing) (pane, bool) {
	id, _ := t["id"].(string)
	if ended, _ := t[endedProp].(bool); !ended {
		if p, err := a.paneByWindow(id); err == nil {
			return p, true
		}
	}
	if id == "" {
		return pane{}, false
	}
	p := endedPane(t)
	p.Starred, _ = t["starred"].(bool)
	p.AgentState, _ = t["state"].(string)
	p.Exited, _ = t["exited"].(bool)
	if ended, _ := t[endedProp].(bool); ended {
		p.Exited = true
	}
	return p, true
}
