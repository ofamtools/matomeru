package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// telemetryView is the daemon hook telemetry the current page can show. The
// daemon reports every pane's tool and summary as they change, but only the
// Panes page (its rail) and a terminal on screen draw them; every page draws
// the coarse state (running, needs you, pending approvals). seq moves only
// when what the page can show changed, so the caches and the view memo keyed
// on it ignore a background agent's tool ticks on any other page.
type telemetryView struct {
	scope  string          // "" is every pane's detail; else the panes in detail
	detail map[string]bool // the panes whose tool and summary are drawn
	raw    uint64          // the telemetrySeq fp was taken at
	fp     string
	seq    uint64
	drawn  uint64 // the seq the last wake-up redrew for
}

// telemetryViewSeq is the revision of the telemetry the current page shows.
// It catches up with the daemon's telemetry on the way, so a reader that runs
// before the next View still sees it. UI goroutine only.
func (a *App) telemetryViewSeq() uint64 {
	if a.rt == nil {
		return 0
	}
	v := &a.rt.live.telView
	if raw := a.rt.live.telemetrySeq.Load(); raw != v.raw {
		a.refreshTelemetryView(raw)
	}
	return v.seq
}

// syncTelemetryScope sets which panes' detail the page in s shows and takes a
// new fingerprint when the scope or the daemon's telemetry moved.
func (a *App) syncTelemetryScope(s core.State) {
	if a.rt == nil {
		return
	}
	v := &a.rt.live.telView
	scope, detail := "", map[string]bool(nil)
	if s.ActivePage != SpacePanes {
		detail = a.visibleTerminalPanes(s)
		scope = "shown:" + strings.Join(slices.Sorted(maps.Keys(detail)), ",")
	}
	raw := a.rt.live.telemetrySeq.Load()
	if scope == v.scope && raw == v.raw {
		return
	}
	v.scope, v.detail = scope, detail
	a.refreshTelemetryView(raw)
}

func (a *App) refreshTelemetryView(raw uint64) {
	v := &a.rt.live.telView
	v.raw = raw
	if fp := a.shownTelemetryFingerprint(); fp != v.fp {
		v.fp = fp
		v.seq++
	}
}

// shownTelemetryFingerprint is telemetryFingerprint narrowed to the scope:
// every pane's state and every pending approval, and the tool, summary and
// subagent count only of the panes drawn in detail.
func (a *App) shownTelemetryFingerprint() string {
	l := &a.rt.live
	l.telemetryMu.RLock()
	defer l.telemetryMu.RUnlock()
	res := l.telemetry
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, id := range slices.Sorted(maps.Keys(res.Telemetry)) {
		pt := res.Telemetry[id]
		if pt == nil {
			continue
		}
		fmt.Fprintf(&b, "t:%s|%s|%s", id, pt.State, pt.Harness)
		if l.telView.scope == "" || l.telView.detail[id] {
			fmt.Fprintf(&b, "|%s|%s|%d", pt.Tool, pt.Summary, pt.SubagentCount)
		}
		b.WriteByte('\n')
	}
	for _, p := range res.PendingApprovals {
		if p != nil {
			fmt.Fprintf(&b, "a:%s|%s|%s|%s|%s\n", p.RequestID, p.PaneID, p.Tool, p.Summary, p.Harness)
		}
	}
	return b.String()
}

// syncLiveView lines the background samplers up with what the view in s is
// about to draw: the telemetry scope, and whether system stats are wanted.
func (a *App) syncLiveView(s core.State) {
	if a.rt == nil {
		return
	}
	a.syncTelemetryScope(s)
	a.rt.live.sysStats.SetPaused(!a.chromeSystemStatsEnabled())
}
