package main

import (
	"cmp"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

const (
	settingTitleThrottle   = "panes.title-throttle"
	settingEventCoalesce   = "panes.event-coalesce"
	settingRetitleInPlace  = "panes.retitle-in-place"
	settingLightListing    = "panes.light-listing"
	settingHistoryCoalesce = "history.event-coalesce"
	settingHistoryVisible  = "history.visible-only"
)

func eventLoadSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingTitleThrottle, Type: ochazuke.SettingDuration, Default: 200 * time.Millisecond, Description: "Shortest interval between title-only event deliveries, with a trailing final title; 0 delivers immediately"},
		{Name: settingEventCoalesce, Type: ochazuke.SettingDuration, Default: 300 * time.Millisecond, Description: "Collect pane structure events for this interval before listing; 0 lists immediately"},
		{Name: settingRetitleInPlace, Type: ochazuke.SettingBoolean, Default: true, Description: "Update daemon window titles in memory without listing panes; false uses a listing"},
		{Name: settingLightListing, Type: ochazuke.SettingBoolean, Default: true, Description: "Event and timer listings skip side locks and exit probes; false reconciles every listing. Startup, explicit refresh and side operations always reconcile"},
		{Name: settingHistoryCoalesce, Section: "turns", Type: ochazuke.SettingDuration, Default: 300 * time.Millisecond, Description: "Collect history notifications for this interval before refreshing views; 0 refreshes immediately"},
		{Name: settingHistoryVisible, Section: "turns", Type: ochazuke.SettingBoolean, Default: true, Description: "Refresh history views only while shown; refresh Review when opened. False also refreshes hidden views"},
	}
}

// paneEventInbox keeps the latest payload per event kind/window until the
// loop takes it. A full wake channel never loses the final title or exit.
type paneEventInbox struct {
	mu     sync.Mutex
	events map[string]daemon.Event
	boot   string
}

func (q *paneEventInbox) add(e daemon.Event) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if e.BootID != "" && e.BootID != q.boot {
		clear(q.events)
		q.boot = e.BootID
	}
	pty, _ := e.Data["pty_id"].(string)
	q.events[e.Type+"\x00"+e.Session+"\x00"+e.Window+"\x00"+pty] = e
}
func (q *paneEventInbox) take() []daemon.Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]daemon.Event, 0, len(q.events))
	for _, e := range q.events {
		out = append(out, e)
	}
	clear(q.events)
	slices.SortFunc(out, func(a, b daemon.Event) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}

type paneTitle struct {
	name, title string
	seq         uint64
}

func retitlePane(p *pane, title string, rename bool) bool {
	if rename {
		if p.Name == title {
			return false
		}
		p.Name = title
		return true
	}
	if p.Title == title {
		return false
	}
	// display_name falls back to the terminal title; retain a custom name.
	if p.Name == p.Title {
		p.Name = title
	}
	p.Title = title
	return true
}

// reconcileEvents are the event types whose listing must reconcile: close
// exited windows (and with them their sides) and drop stale side links.
// Every other event, retitles above all, keeps the light listing.
var reconcileEvents = map[string]bool{"window-exit": true}

// applyPaneEvents updates payloads already in memory and says whether a
// structure listing is needed. Unknown retitles wait for a structure event.
func (a *App) applyPaneEvents(events []daemon.Event) bool {
	reload, retitled := len(events) == 0, false
	for _, e := range events {
		if e.BootID != "" && e.BootID != a.pv.eventBoot {
			a.pv.eventBoot = e.BootID
			clear(a.pv.titles)
			clear(a.pv.exited)
		}
		if e.Type == "window-retitled" && a.r.Settings().Bool(settingRetitleInPlace) {
			title, _ := e.Data["title"].(string) // omitted means cleared
			pty, _ := e.Data["pty_id"].(string)
			rename := pty == "" // explicit name changes have no PTY emitter
			var changed *pane
			for i := range a.panes {
				if a.panes[i].ID == e.Window && retitlePane(&a.panes[i], title, rename) {
					changed = &a.panes[i]
				}
			}
			if p, ok := a.companionPanes[e.Window]; ok && retitlePane(&p, title, rename) {
				a.companionPanes[e.Window] = p
				changed = &p
			}
			if a.attached.ID == e.Window {
				retitlePane(&a.attached, title, rename)
			}
			if changed != nil {
				retitled = true
				if a.paneRefreshRunning() {
					if a.pv.titles == nil {
						a.pv.titles = map[string]paneTitle{}
					}
					a.pv.titleSeq++
					a.pv.titles[e.Window] = paneTitle{name: changed.Name, title: changed.Title, seq: a.pv.titleSeq}
				}
			}
			continue
		}
		if e.Type == "window-exit" {
			if a.pv.exited == nil {
				a.pv.exited = map[string]bool{}
			}
			a.pv.exited[e.Window] = true
		}
		if reconcileEvents[e.Type] {
			a.pv.reconcilePending = true
		}
		reload = true
	}
	if retitled {
		a.pushPaneTitles()
	}
	return reload
}

// pushPaneTitles pushes cached rows without reading owners, recipes, history,
// command rings or side links. Keep ordering, selection and expanded children.
func (a *App) pushPaneTitles() {
	names := uniqueNames(a.panes)
	byID := make(map[string]pane, len(a.panes))
	for _, p := range a.panes {
		byID[p.ID] = p
	}
	update := func(nodes []ochazuke.TreeNode) bool {
		changed := false
		for i := range nodes {
			old, ok := nodes[i].Data.(pane)
			if !ok || nodes[i].Noun != "pane" {
				continue
			}
			p, ok := byID[old.ID]
			if !ok {
				continue
			}
			n := &nodes[i]
			name := ""
			if len(n.Cells) > 0 {
				name = markerGutter(p) + " " + names[p.ID]
				if p.Queued > 0 {
					name += " " + queuedMarker
				}
			}
			// Another pane's rename can change this pane's numeric suffix.
			if old.Name == p.Name && old.Title == p.Title && (len(n.Cells) == 0 || n.Cells[0] == name) {
				continue
			}
			changed = true
			n.Data, n.Label = p, p.label()
			if len(n.Cells) > 0 {
				// View, section and tree node snapshots may share these cells.
				n.Cells = slices.Clone(n.Cells)
				n.Cells[0], n.Tones = name, paneTones(p, name)
			}
		}
		return changed
	}
	update(a.pv.view.nodes)
	for _, s := range a.pv.sections {
		update(s.nodes)
		nodes := s.tree.Nodes()
		if !update(nodes) {
			continue
		}
		if err := s.tree.SetNodes(nodes); err != nil {
			a.paneErr = err.Error()
		}
	}
}

func (a *App) reloadPaneListing(reconcile bool) error {
	_, err := a.r.Execute(ochazuke.Request{Noun: "panes", Verb: "refresh", Target: paneListID, Args: ochazuke.Arguments{"reconcile": reconcile}})
	return err
}

// paneRefreshRunning derives ownership from the runtime: cancelled/stale
// jobs never call Then, so a private completion-cleared flag would stick.
func (a *App) paneRefreshRunning() bool {
	return a.r.HasRunningJob(paneListID, "refresh")
}

// One fixed window absorbs a sustained burst without starving the final
// listing. An in-flight job gets one trailing listing after it completes.
func (a *App) schedulePaneEvents() tea.Cmd {
	if a.pv.eventPending {
		return nil
	}
	if a.paneRefreshRunning() {
		a.pv.refreshQueued = true
		a.pv.reconcileQueued = a.pv.reconcileQueued || a.takeReconcilePending()
		return nil
	}
	delay := a.r.Settings().Duration(settingEventCoalesce)
	if delay <= 0 {
		if err := a.reloadPaneListing(a.takeReconcilePending()); err != nil {
			a.paneErr = err.Error()
		}
		return nil
	}
	a.pv.eventPending = true
	if a.pv.after != nil {
		return a.pv.after(delay, paneEventsDue{})
	}
	return nil
}

// takeReconcilePending says whether an event since the last scheduled
// listing needs reconciliation, and clears it for the next window.
func (a *App) takeReconcilePending() bool {
	r := a.pv.reconcilePending
	a.pv.reconcilePending = false
	return r
}

// queueExitReconcile is the fallback for a missed window-exit (a resync, a
// dropped subscription): a listing that shows a window exited which no
// reconciling listing has been asked to close yet queues one. Each window
// asks once, so a daemon that keeps listing it exited costs one probe round,
// not one per timer listing.
func (a *App) queueExitReconcile(panes []pane) {
	seen := make(map[string]bool, len(panes))
	for _, p := range panes {
		if p.ID == "" || !p.Exited {
			continue
		}
		seen[p.ID] = true
		if a.pv.exitAsked[p.ID] {
			continue
		}
		if a.pv.exitAsked == nil {
			a.pv.exitAsked = map[string]bool{}
		}
		a.pv.exitAsked[p.ID] = true
		a.pv.refreshQueued, a.pv.reconcileQueued = true, true
	}
	for id := range a.pv.exitAsked {
		if !seen[id] {
			delete(a.pv.exitAsked, id)
		}
	}
}
