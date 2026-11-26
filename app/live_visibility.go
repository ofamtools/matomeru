package app

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"ofam.dev/ochazuke/core"
)

// paneExitProber is the cheap exit probe a Host may offer: whether a session's
// process has exited, without rendering or copying its screen.
type paneExitProber interface {
	ExitStatus(id string) (exited bool, code int, ok bool)
}

// paneWatch is what the background loops know about the tracked sessions, so
// output on a session nobody is looking at does not wake the UI. The UI
// goroutine publishes it; the Host's change feed and the store watcher read it.
type paneWatch struct {
	fed     atomic.Bool                     // the change feed filters wake-ups by visibility
	prober  paneExitProber                  // the Host's exit probe, set before fed
	visible atomic.Pointer[map[string]bool] // sessions on screen
	hidden  atomic.Pointer[map[string]bool] // tracked sessions off screen -> exited when last read

	mu       sync.Mutex
	dirty    map[string]bool // hidden sessions with output not read yet
	dirtyDue time.Time       // when the watcher may next wake the UI to read them
}

// publishShownPanes tells the background loops which sessions are on screen
// and which tracked ones are not. It reports whether a session came on screen:
// its frame may be stale (hidden sessions are not re-read), so the caller wakes
// the UI to read it.
func (a *App) publishShownPanes(visible map[string]bool) (appeared bool) {
	w := &a.rt.live.panes
	if prev := w.visible.Load(); prev == nil || !maps.Equal(*prev, visible) {
		for id := range visible {
			if prev == nil || !(*prev)[id] {
				appeared = true
			}
		}
		w.visible.Store(&visible)
	}
	hidden := make(map[string]bool, len(a.rt.live.frames))
	for id, f := range a.rt.live.frames {
		if !visible[id] {
			hidden[id] = f.Exited
		}
	}
	if prev := w.hidden.Load(); prev == nil || !maps.Equal(*prev, hidden) {
		w.hidden.Store(&hidden)
	}
	w.mu.Lock()
	maps.DeleteFunc(w.dirty, func(id string, _ bool) bool { _, off := hidden[id]; return !off })
	w.mu.Unlock()
	return appeared
}

// syncShownPanes publishes the sessions the view just drew. It runs after
// every View while the change feed filters by visibility.
func (a *App) syncShownPanes(s core.State) {
	if a.rt == nil || a.panes == nil || a.panes.Host == nil || !a.rt.live.panes.fed.Load() {
		return
	}
	if a.publishShownPanes(a.visibleTerminalPanes(s)) {
		a.Notify()
	}
}

// paneChanged is the change feed's question for one session's output: should
// it wake the UI? Only for a session on screen. Output on a hidden session is
// remembered, and the store watcher picks it up at its own slow pace.
func (a *App) paneChanged(id string) bool {
	w := &a.rt.live.panes
	visible, hidden := w.visible.Load(), w.hidden.Load()
	if visible == nil || hidden == nil || (*visible)[id] {
		return true
	}
	if _, tracked := (*hidden)[id]; tracked {
		w.mu.Lock()
		if w.dirty == nil {
			w.dirty = map[string]bool{}
		}
		w.dirty[id] = true
		w.mu.Unlock()
	}
	return false
}

// hiddenPanesNeedWake is the store watcher's look at the sessions off screen:
// one exited or went away, or their unread output is due for a context
// harvest. It is safe from any goroutine and reads no screen.
func (a *App) hiddenPanesNeedWake() bool {
	w := &a.rt.live.panes
	hidden := w.hidden.Load()
	if !w.fed.Load() || hidden == nil || len(*hidden) == 0 {
		return false
	}
	if w.prober == nil {
		return true // no cheap probe: the wake-up reads the sessions itself
	}
	for id, wasExited := range *hidden {
		if exited, _, ok := w.prober.ExitStatus(id); !ok || exited != wasExited {
			return true
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if now := time.Now(); len(w.dirty) > 0 && !now.Before(w.dirtyDue) {
		w.dirtyDue = now.Add(liveScreenHarvestInterval)
		return true
	}
	return false
}

// hiddenPaneNeedsRead reports whether the wake-up must read a hidden session's
// screen: it exited or went away, or it has unread output and its context
// harvest is due. Anything else leaves the screen unread until it is shown.
func (a *App) hiddenPaneNeedsRead(id string) bool {
	w := &a.rt.live.panes
	if w.prober == nil {
		return true
	}
	if exited, _, ok := w.prober.ExitStatus(id); !ok || exited != a.rt.live.frames[id].Exited {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.dirty[id] || !a.shouldHarvestScreen(id) {
		return false
	}
	delete(w.dirty, id)
	return true
}

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

// screenMoved re-reads the sessions on screen and reports whether what is
// drawn changed, and which sessions exited. While the Host's change feed
// filters wake-ups, a tracked session off screen is read only when it exited
// or its context harvest is due; otherwise every tracked session is read.
func (a *App) screenMoved(s core.State) (bool, []paneExit) {
	if a.panes.Host == nil {
		return false, nil
	}
	visible := a.visibleTerminalPanes(s)
	fed := a.rt.live.panes.fed.Load()
	ids := slices.Collect(maps.Keys(visible))
	for id := range a.rt.live.frames {
		if !visible[id] && (!fed || a.hiddenPaneNeedsRead(id)) {
			ids = append(ids, id)
		}
	}
	moved := false
	var exited []paneExit
	for _, id := range ids {
		if id == "" {
			continue
		}
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
	if fed {
		a.publishShownPanes(visible)
	}
	return moved, exited
}
