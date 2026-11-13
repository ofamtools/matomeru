package main

import (
	"slices"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/panesignals"
)

// Needs you, the Inbox page's top list, by one rule (inbox.needs-you-rule
// one, the default): a row is in Needs you while its item is open, that
// is not read, not archived and not snoozed, for every kind and every
// source. Reading, archiving or snoozing an item takes its row out; a new
// event (a new item, or the item marked unread, restored or reopened)
// brings it back. The one membership function (needsYouCategory) feeds
// the panel, its pane rows and their child rows; the one count (the
// panel's rows, paneView.needsYou) feeds the panel title, the header and
// W; attaching a pane marks its open items read at once
// (readAttachedItems), and a pane that closed clears its command items as
// its agent items clear (panesignals.ClearGone).
//
// legacy keeps the rules before it: a category per kind and source (read
// is ignored for needs-input, errored, failed commands and every outside
// item), a header that also counts a waiting pane with no row, W visiting
// only panes whose agent state needs input, and attach clearing done and
// command items at the next listing.

const (
	settingNeedsYouRule = "inbox.needs-you-rule"
	needsYouOne         = "one"
	needsYouLegacy      = "legacy"

	settingSortClock = "panes.sort-clock"
	clockEntered     = "entered"
	clockCreated     = "created"
	clockState       = "agent-state"

	settingWaitingLiveAttach = "panes.waiting-live-attach"

	nothingNeedsYou = "nothing needs you"
)

// needsYouSettings are the rule's settings.
func needsYouSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingNeedsYouRule, Type: ochazuke.SettingString, Default: needsYouOne, Enum: []string{needsYouOne, needsYouLegacy},
			Description: "What is in Needs you: one (a row stays while its item is open: not read, archived or snoozed, for every kind and source; the panel, the header count and W read the same rows; attaching a pane marks its items read) or legacy (by kind: read is ignored for needs-input, errored and outside items, the header also counts waiting panes with no row, W visits only waiting agents)"},
		{Name: settingSortClock, Type: ochazuke.SettingString, Default: clockEntered, Enum: []string{clockEntered, clockCreated, clockState},
			Description: "The time rows sort by, oldest first after the category rank: entered (when the row entered its state: an item when it was raised, reopened or woke from a snooze; a Working pane when its agent entered its state), created (an item when it was first raised) or agent-state (a pane's agent state time for every pane row, Needs you too; an item with no listed pane by when it entered)"},
		{Name: settingWaitingLiveAttach, Type: ochazuke.SettingBoolean, Default: true,
			Description: "The leader's W in live view, where the shown pane is the selected one: on goes to the next Needs-you row by attaching its pane (a row with no pane to attach leaves live and selects it); off leaves live and selects the next row, as W does outside"},
	}
}

// oneRule reports inbox.needs-you-rule one.
func (a *App) oneRule() bool {
	return a.r.Settings().String(settingNeedsYouRule) != needsYouLegacy
}

// kindCategory is an item's queue category by its kind and source, read
// or not: waiting (needs-input), errored (an errored agent, a failed
// command), outside (every other source) or unread (mtm's every other
// kind: a finished turn, a long command, a private project).
func kindCategory(it actionitems.Item) string {
	switch {
	case it.Source != panesignals.Source:
		return queueOutside
	case it.Kind == panesignals.NeedsInput:
		return signalWaiting
	case it.Kind == panesignals.Errored, panesignals.CommandFailed(it):
		return signalErrored
	}
	return signalUnread
}

// needsYouCategory is the membership function: an open item's queue
// category, "" for an item that is not in Needs you. One rule: an unread
// item's kind category; legacy: itemCategory.
func (a *App) needsYouCategory(it actionitems.Item, one bool) string {
	if !one {
		return itemCategory(it)
	}
	if it.Read() {
		return ""
	}
	return kindCategory(it)
}

// childCategory is the category that makes an item a pane's child row:
// with the one rule its kind's, read or not (panes.child-items says which
// states show), else the legacy category.
func (a *App) childCategory(it actionitems.Item) string {
	if a.oneRule() {
		return kindCategory(it)
	}
	return itemCategory(it)
}

// sortClocks are panes.sort-clock's values: the time an item's row sorts
// by; p is its pane when listed.
var sortClocks = map[string]func(it actionitems.Item, p pane, listed bool, now int64) int64{
	clockEntered: func(it actionitems.Item, _ pane, _ bool, now int64) int64 { return it.Entered(now) },
	clockCreated: func(it actionitems.Item, _ pane, _ bool, _ int64) int64 { return it.Created },
	clockState: func(it actionitems.Item, p pane, listed bool, now int64) int64 {
		if listed && p.StateAt > 0 {
			return p.StateAt
		}
		return it.Entered(now)
	},
}

// itemClock is the time an item's row sorts by (panes.sort-clock).
func (a *App) itemClock(it actionitems.Item, p pane, listed bool, now int64) int64 {
	clock, ok := sortClocks[a.r.Settings().String(settingSortClock)]
	if !ok {
		clock = sortClocks[clockEntered]
	}
	return clock(it, p, listed, now)
}

// needsYouAfter is the Needs-you row after row in the panel's order,
// wrapping once, or "" when the panel is empty. A child row counts as its
// parent row; a row outside the panel starts from the top.
func (a *App) needsYouAfter(row string) string {
	order := a.pv.view.needsYou
	if len(order) == 0 {
		return ""
	}
	if len(a.pv.sections) > 0 {
		for _, n := range a.pv.sections[0].nodes {
			if n.ID == row && n.Parent != "" {
				row = n.Parent
				break
			}
		}
	}
	start := slices.Index(order, row)
	return order[(start+1+len(order))%len(order)]
}

// nextNeedsYou is panes.waiting by the one rule: select the next
// Needs-you row (a pane row selects the pane, an item row the item).
func (a *App) nextNeedsYou() ochazuke.Result {
	row, _ := a.selectedRow()
	id := a.needsYouAfter(row)
	if id == "" {
		a.r.UI().Info(nothingNeedsYou)
		return ochazuke.Done(nil)
	}
	if err := a.selectPaneRow(id); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
}

// cycleNeedsYou is terminal.waiting by the one rule, in live view: go to
// the next Needs-you row after the attached pane's. In live view the shown
// pane is the selected one, so going to a pane attaches it (keeping the
// zoom); a row with no pane to attach, or panes.waiting-live-attach off,
// leaves live and selects the row.
func (a *App) cycleNeedsYou() ochazuke.Result {
	id := a.needsYouAfter(a.attached.rowID())
	if id == "" {
		a.r.UI().Info(nothingNeedsYou)
		return ochazuke.Done(nil)
	}
	p, err := a.paneAt(a.r.Instances[id])
	if err == nil && p.ID == a.attached.ID {
		return ochazuke.Done(nil) // the only row is the attached pane's
	}
	if err != nil || p.Exited || !a.r.Settings().Bool(settingWaitingLiveAttach) {
		if err := a.closeSideTerminal(); err != nil {
			return ochazuke.Failed(err)
		}
		if _, err := a.r.Execute(ochazuke.Request{Noun: "terminal", Verb: "detach", Args: ochazuke.Arguments{"id": paneTermID}}); err != nil {
			return ochazuke.Failed(err)
		}
		if err := a.selectPaneRow(id); err != nil {
			return ochazuke.Failed(err)
		}
		if err := a.r.SetFocus(id); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(id)
	}
	zoom := a.pv.zoom
	if err := a.selectPaneRow(id); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.attachLive(p, id); err != nil {
		return ochazuke.Failed(err)
	}
	a.pv.zoom = zoom
	return ochazuke.Done(p.label())
}

// readAttachedItems is the one rule's attach clear: every open, unread
// item of window, any source and kind, is marked read now (the person is
// looking at the pane), not at the next listing.
func (a *App) readAttachedItems(window string) error {
	if !a.oneRule() || a.inbox == nil || window == "" {
		return nil
	}
	now := a.inbox.items.Now()
	wrote := false
	for _, it := range a.inbox.all {
		if it.Window != window || !it.Open(now) || it.Read() {
			continue
		}
		if _, err := a.inbox.items.SetRead(it.ID, true); err != nil && !isNotFound(err) {
			return err
		}
		wrote = true
	}
	if !wrote {
		return nil
	}
	return a.inboxLoad.refresh()
}
