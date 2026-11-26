package app

import (
	"slices"

	"ofam.dev/ochazuke/core"
)

// visibleTerminalPanes returns the set of pane IDs whose live terminal screen
// is currently visible in s. Background PTY output on hidden panes must not
// force full-page UI rebuilds while the owner reads a Project Page or Work view.
func (a *App) visibleTerminalPanes(s core.State) map[string]bool {
	vis := map[string]bool{}
	if sm := a.activeScratch(); sm != nil && sm.paneID != "" {
		vis[sm.paneID] = true
	}
	if s.ActivePage == SpacePanes {
		_, _, onProj := a.selectedRailProject()
		if !onProj {
			if a.rt.live.shown != "" {
				vis[a.rt.live.shown] = true
			}
			if a.rt.live.shownSide != "" {
				vis[a.rt.live.shownSide] = true
			}
		} else if cid := a.activeChildPaneID(s); cid != "" && a.projectChildPaneViewEnabled() {
			vis[cid] = true
			if a.rt.live.shownSide != "" {
				vis[a.rt.live.shownSide] = true
			}
		}
	} else if isProjectTab(s.ActivePage) {
		if cid := a.activeChildPaneID(s); cid != "" && a.projectChildPaneViewEnabled() {
			vis[cid] = true
			if a.rt.live.shownSide != "" {
				vis[a.rt.live.shownSide] = true
			}
		}
	}
	return vis
}

func (a *App) screenMoved(s core.State) (bool, []paneExit) {
	if a.panes.Host == nil {
		return false, nil
	}
	visible := a.visibleTerminalPanes(s)
	ids := make([]string, 0, len(a.rt.live.frames)+3)
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for id := range visible {
		add(id)
	}
	for id := range a.rt.live.frames {
		add(id)
	}
	moved := false
	var exited []paneExit
	for _, id := range ids {
		m, exID, code := a.pollShownSession(id)
		// Background PTY output on hidden panes (e.g. while sitting on a Project Page
		// with no live terminal open) must not force full-page UI rebuilds.
		if m && visible[id] {
			moved = true
		}
		if exID != "" {
			moved = true
			exited = append(exited, paneExit{id: exID, code: code})
		}
	}
	return moved, exited
}
