package panesignals

import "ofam.dev/matomeru/internal/actionitems"

// The one rule's clears (inbox.needs-you-rule one): an item is in Needs
// you while it is open and unread, whatever its kind or source, so the
// clears below read items instead of archiving them by kind.
//
//   - Watching a pane is looking at it: every open, unread item of the
//     watched window, any source and any kind, is marked read, and every
//     item raised for it is raised read. Nothing is archived for being
//     watched (Reconcile and ReconcileCommands are then given no watched
//     window, so their kind-by-kind watch clears do not run).
//   - A pane gone from the listing clears its command items as its agent
//     items clear: a failed one is archived (as needs-input and errored
//     are), any other one marked read (as done is).

// WatchRead adds the one rule's watch clears for watching ("" for none)
// to plan. now is Unix ms.
func WatchRead(plan Plan, items []actionitems.Item, watching string, now int64) Plan {
	if watching == "" {
		return plan
	}
	for i := range plan.Add {
		if plan.Add[i].Window == watching {
			plan.Add[i].Read, plan.Add[i].Archive = true, false
		}
	}
	for _, it := range items {
		if it.Window == watching && it.Open(now) && !it.Read() {
			plan.Read = append(plan.Read, it.ID)
		}
	}
	return plan
}

// ClearGone adds the clears of the command items whose pane the listing
// (every pane it holds, shells and agents) no longer has.
func ClearGone(plan Plan, listed []string, items []actionitems.Item) Plan {
	have := make(map[string]bool, len(listed))
	for _, w := range listed {
		have[w] = true
	}
	for _, it := range items {
		window, _, _, ok := commandKeyParts(it.Key) // only a command's key parses
		if it.Source != Source || !ok || it.ArchivedAt > 0 || have[window] {
			continue
		}
		switch {
		case CommandFailed(it):
			plan.Archive = append(plan.Archive, it.ID)
		case !it.Read():
			plan.Read = append(plan.Read, it.ID)
		}
	}
	return plan
}
