// Package panesignals turns mtm's own pane state into inbox items: an
// agent that needs input, a turn that finished, an agent that errored.
// It is pure: Reconcile compares a pane listing with the items already
// raised and says what to add, archive and mark read; the caller writes.
//
// The rules (one per kind, the same shape for each):
//
//   - An agent pane in the kind's state raises one item, source mtm, kind
//     needs-input, done or errored, keyed kind:window:state_at. state_at is
//     when the pane entered the state, so a repeat of the same episode
//     folds into the same item (and an archived one stays archived), and a
//     new episode raises a new one. This is matomeru-old's stuck-check key
//     (panes/stuck.go: pane, rule and the time the state began).
//   - When the pane leaves that episode (another state, a new state_at,
//     exited, or gone from the listing) its open item is cleared:
//     needs-input and errored items are archived (the wait or the error is
//     over), a done item is only marked read (the finished turn is still
//     worth reviewing; leaving done means it was looked at or prompted
//     again).
//   - A done item raised while the person is watching that pane (it is
//     attached in mtm) is raised read, and watching a pane marks its done
//     item read.
//
// A kind that is turned off raises nothing; its open items still clear.
//
// A terminal's finished commands are the command kind, with rules of their
// own (commands.go, ReconcileCommands).
package panesignals

import (
	"strconv"
	"strings"

	"ofam.dev/matomeru/internal/actionitems"
)

// Source is the inbox source of every pane signal.
const Source = "mtm"

// Kinds of pane signal.
const (
	NeedsInput = "needs-input"
	Done       = "done"
	Errored    = "errored"
)

// Kinds are every kind, in the order the settings list them.
var Kinds = []string{NeedsInput, Done, Errored}

// Pane is what the rules read of a pane.
type Pane struct {
	Window  string
	Session string
	Label   string
	Agent   bool   // the pane runs an agent
	State   string // the agent state: working, needs_input, idle, done, errored, …
	StateAt int64  // when it entered the state, Unix ms (0 when unknown)
	// NeedsYou is list-agents' needs_you: the agent waits on the person.
	NeedsYou bool
	Exited   bool
}

// Kind is the signal the pane raises now, "" for none.
func (p Pane) Kind() string {
	if p.Exited || !p.Agent {
		return ""
	}
	switch {
	case p.State == "needs_input" || p.NeedsYou:
		return NeedsInput
	case p.State == "done":
		return Done
	case p.State == "errored":
		return Errored
	}
	return ""
}

// Key is the de-duplication key of one episode.
func Key(kind, window string, stateAt int64) string {
	return kind + ":" + window + ":" + strconv.FormatInt(stateAt, 10)
}

// Why is the one line an item of kind says.
func Why(kind string) string {
	switch kind {
	case NeedsInput:
		return "waits for your input"
	case Done:
		return "turn finished"
	case Errored:
		return "agent errored"
	}
	return kind
}

// Signal is one item to add.
type Signal struct {
	Kind    string
	Key     string
	Window  string
	Session string
	Title   string
	Why     string
	StateAt int64
	// Read raises it already read (the person watches the pane), Archive
	// raises it and archives it at once (a failed command the person
	// watched fail).
	Read    bool
	Archive bool
	// Extra is the item's extra fields (state_at when nil).
	Extra map[string]any
}

// Plan is what Reconcile asks the caller to write.
type Plan struct {
	Add     []Signal
	Archive []string // item ids
	Read    []string // item ids
}

// Merge is p followed by q.
func (p Plan) Merge(q Plan) Plan {
	return Plan{Add: append(p.Add, q.Add...), Archive: append(p.Archive, q.Archive...), Read: append(p.Read, q.Read...)}
}

// Empty reports a plan with nothing to write.
func (p Plan) Empty() bool { return len(p.Add) == 0 && len(p.Archive) == 0 && len(p.Read) == 0 }

// Reconcile is the plan for a full pane listing against the inbox's items
// (any source; only mtm's are read). enabled says which kinds raise items
// (a kind missing from it is on); watching is the window the person
// watches, "" for none.
func Reconcile(panes []Pane, items []actionitems.Item, enabled map[string]bool, watching string) Plan {
	current := map[string]string{} // window+kind → the key the pane holds now
	for _, p := range panes {
		if p.Window == "" {
			continue
		}
		if kind := p.Kind(); kind != "" {
			current[p.Window+"|"+kind] = Key(kind, p.Window, p.StateAt)
		}
	}
	have := map[string]bool{}
	var plan Plan
	for _, it := range items {
		if it.Source != Source || it.Key == "" {
			continue
		}
		have[it.Key] = true
		window, kind := windowOf(it)
		if window == "" || kind == Command || it.ArchivedAt > 0 { // commands clear in ReconcileCommands
			continue
		}
		left := current[window+"|"+kind] != it.Key
		switch {
		case left && (kind == NeedsInput || kind == Errored):
			plan.Archive = append(plan.Archive, it.ID)
		case kind == Done && !it.Read() && (left || window == watching):
			plan.Read = append(plan.Read, it.ID)
		}
	}
	for _, p := range panes {
		kind := p.Kind()
		if kind == "" || p.Window == "" {
			continue
		}
		if on, set := enabled[kind]; set && !on {
			continue
		}
		key := Key(kind, p.Window, p.StateAt)
		if have[key] {
			continue
		}
		have[key] = true
		plan.Add = append(plan.Add, Signal{Kind: kind, Key: key, Window: p.Window, Session: p.Session, Title: p.Label,
			Why: Why(kind), StateAt: p.StateAt, Read: kind == Done && p.Window == watching})
	}
	return plan
}

// windowOf reads an mtm item's window and kind back from its key: an
// agent signal's kind:window:state_at, or a command's
// command:window:boot_id:command_seq ("" for a key that is neither).
func windowOf(it actionitems.Item) (string, string) {
	if window, _, _, ok := commandKeyParts(it.Key); ok {
		return window, Command
	}
	parts := strings.Split(it.Key, ":")
	if len(parts) == 3 {
		for _, k := range Kinds {
			if parts[0] == k {
				return parts[1], k
			}
		}
	}
	return "", ""
}
