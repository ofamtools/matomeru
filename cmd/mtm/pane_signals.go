package main

import (
	"strconv"
	"strings"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/panesignals"
)

// Pane signals: mtm's own pane state raised into the inbox, no outside
// tool. Each listing (setPanes) is reconciled with the items already
// raised (internal/panesignals has the rules): an agent that needs input,
// a finished turn and an errored agent each become an action item with
// source mtm, kind needs-input, done or errored, keyed by the window and
// when it entered the state, owned by the pane's task or project (record
// first: unassociated is fine). A needs-input or errored item is archived
// once the pane leaves that state; a done item is marked read then, or as
// soon as the pane is attached. Each kind is a setting
// (inbox.signal.needs-input, inbox.signal.done, inbox.signal.errored).
// Only the TUI raises them, and two TUIs raise the same keys (the store
// folds a repeat), so they never double.

// paneSignalState is what the last reconcile was built from; a listing
// that matches it writes nothing.
type paneSignalState struct {
	sig string
}

func signalSetting(kind string) string { return "inbox.signal." + kind }

// signalSettings are the on/off settings of the pane signal kinds.
func (a *App) signalSettings() []ochazuke.Setting {
	what := map[string]string{
		panesignals.NeedsInput: "an agent that waits for your input (archived when it stops waiting)",
		panesignals.Done:       "a finished agent turn (marked read once you attach the pane or it moves on)",
		panesignals.Errored:    "an agent that errored (archived when it recovers)",
	}
	var out []ochazuke.Setting
	for _, kind := range panesignals.Kinds {
		out = append(out, ochazuke.Setting{Name: signalSetting(kind), Type: ochazuke.SettingBoolean, Default: true,
			Description: "Raise an inbox item for " + what[kind]})
	}
	return out
}

func (a *App) signalKinds() map[string]bool {
	out := map[string]bool{}
	for _, kind := range panesignals.Kinds {
		out[kind] = a.r.Settings().Bool(signalSetting(kind))
	}
	return out
}

func signalPanes(panes []pane) []panesignals.Pane {
	out := make([]panesignals.Pane, 0, len(panes))
	for _, p := range panes {
		if p.ID == "" {
			continue
		}
		out = append(out, panesignals.Pane{Window: p.ID, Session: p.Session, Label: p.label(), Agent: p.isAgent(),
			State: p.AgentState, StateAt: p.StateAt, NeedsYou: p.NeedsYou, Exited: p.Exited})
	}
	return out
}

func signalSig(panes []panesignals.Pane, kinds map[string]bool, watching string) string {
	var b strings.Builder
	b.WriteString(watching + "|")
	for _, k := range panesignals.Kinds {
		b.WriteString(strconv.FormatBool(kinds[k]) + ",")
	}
	for _, p := range panes {
		if p.Agent {
			b.WriteString(p.Window + ":" + p.Kind() + ":" + strconv.FormatInt(p.StateAt, 10) + ";")
		}
	}
	return b.String()
}

// raisePaneSignals reconciles a full listing with the inbox. It runs only
// while the TUI runs; a listing that did not change since the last one
// writes nothing and reads nothing.
func (a *App) raisePaneSignals(panes []pane) {
	if a.sched == nil || !a.sched.live || a.inbox == nil {
		return
	}
	list, kinds := signalPanes(panes), a.signalKinds()
	sig := signalSig(list, kinds, a.attached.ID)
	if sig == a.signals.sig {
		return
	}
	if err := a.applyPaneSignals(list, kinds); err != nil {
		a.r.UI().Error("pane signals: " + err.Error())
		return
	}
	a.signals.sig = sig
}

func (a *App) applyPaneSignals(list []panesignals.Pane, kinds map[string]bool) error {
	items, err := a.inbox.items.All()
	if err != nil {
		return err
	}
	plan := panesignals.Reconcile(list, items, kinds, a.attached.ID)
	if plan.Empty() {
		return nil
	}
	for _, s := range plan.Add {
		o, _, err := a.actionOwner(ochazuke.Arguments{"pane": s.Window, "session": s.Session})
		if err != nil {
			return err
		}
		it, _, err := a.inbox.items.Add(actionitems.Spec{Source: panesignals.Source, Key: s.Key, Kind: s.Kind, Title: s.Title, Why: s.Why,
			ProjectID: o.ProjectID, TaskID: o.TaskID, Window: s.Window, Session: s.Session, Extra: map[string]any{"state_at": s.StateAt}})
		if err != nil {
			return err
		}
		if s.Read {
			if _, err := a.inbox.items.SetRead(it.ID, true); err != nil {
				return err
			}
		}
	}
	for _, id := range plan.Archive {
		if _, err := a.inbox.items.Archive(id); err != nil && !isNotFound(err) {
			return err
		}
	}
	for _, id := range plan.Read {
		if _, err := a.inbox.items.SetRead(id, true); err != nil && !isNotFound(err) {
			return err
		}
	}
	return a.refreshInbox()
}
