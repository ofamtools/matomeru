package main

import (
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/daemon"
)

// The Go the pane index page's declarations cite (run plan 5.6, cut 3;
// pages/pane-index.json, nouns/pane.json), over today's listing and
// today's streams, nothing of the Panes page's state:
//   - the hook pane.terminal opens a pane's stream for the show page's live
//     view (the prop live, view terminal; ochazuke.detail.attach), the
//     stream every terminal mtm draws opens (openStream);
//   - the derivations pane.state-glyph and pane.side-mark are the Panes
//     page's gutter marks as props (stateGlyph and sideMarker, the same
//     functions the gutter calls), so the index shows them as columns.
//     Setting panes.index-marks off gives none.

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
	mark := func(of func(p pane) string) func(ochazuke.Ctx, ochazuke.Derivation, ochazuke.Thing) (any, bool) {
		return func(c ochazuke.Ctx, _ ochazuke.Derivation, t ochazuke.Thing) (any, bool) {
			if !r.Settings().Bool(settingIndexMarks) {
				return nil, false
			}
			p, ok := a.paneOfThing(t)
			if !ok {
				return nil, false
			}
			if text := of(p); text != " " && text != "" {
				return text, true
			}
			return nil, false
		}
	}
	r.RegisterDerivation("pane.state-glyph", ochazuke.DerivationKind{
		About:  "The agent's state as the Panes page's gutter glyph (? waiting on you, ● working, ✓ done, ✗ errored, ○ idle, × exited; none for a shell)",
		Type:   "string",
		Derive: mark(stateGlyph),
	})
	r.RegisterDerivation("pane.side-mark", ochazuke.DerivationKind{
		About: "◧ while the pane has a side pane open (leader v, t), as the Panes page's gutter shows it",
		Type:  "string",
		Derive: mark(func(p pane) string {
			if len(a.sideFields(p).SideWindows) > 0 {
				return sideMarker
			}
			return ""
		}),
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
	p.AgentState, _ = t["state"].(string)
	p.Exited, _ = t["exited"].(bool)
	if ended, _ := t[endedProp].(bool); ended {
		p.Exited = true
	}
	return p, true
}
