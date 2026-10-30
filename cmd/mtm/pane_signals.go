package main

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/daemon"
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
//
// A terminal's finished commands are signals too (kind command, the rule
// in internal/panesignals/commands.go): inbox.terminal-commands says which
// (off, failed, long, all), inbox.terminal-commands.long-minutes what long
// is and inbox.terminal-commands.ignore which programs never raise. The
// listing carries each shell's command_seq and last command; a pane whose
// seq moved past the marks has its command-history read with the listing,
// off the loop, so a listing that skipped commands still raises each one.
// The keys carry the daemon's boot_id, which the event stream reports
// (subscribeEvents); while it is down nothing is raised and the marks hold.

// paneSignalState is what the last reconcile was built from; a listing
// that matches it writes nothing. marks are the command signals' high
// water (written only once a plan is written), boot the daemon start the
// event stream last reported ("" while it is down; the stream's goroutine
// writes it).
type paneSignalState struct {
	sig   string
	marks panesignals.Marks
	boot  atomic.Value // string
}

// setBoot records the daemon start the event stream reports.
func (s *paneSignalState) setBoot(id string) { s.boot.Store(id) }

// bootID is the daemon start the event stream last reported.
func (s *paneSignalState) bootID() string { id, _ := s.boot.Load().(string); return id }

// Settings of the terminal command signals.
const (
	settingTerminalCommands = "inbox.terminal-commands"
	settingTerminalLong     = "inbox.terminal-commands.long-minutes"
	settingTerminalIgnore   = "inbox.terminal-commands.ignore"
	defaultTerminalLong     = 10
)

// commandSettings are the terminal command signals' settings.
func commandSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingTerminalCommands, Type: ochazuke.SettingString, Default: panesignals.CommandsLong, Enum: panesignals.CommandModes,
			Description: "Raise an inbox item when a command finishes in a terminal pane (a shell that sends OSC 133 marks): off, failed (exit status not 0), long (failed, or ran at least inbox.terminal-commands.long-minutes) or all. A failed command's item is archived once a later command there succeeds or you attach the pane"},
		{Name: settingTerminalLong, Type: ochazuke.SettingInteger, Default: defaultTerminalLong,
			Description: "How many minutes a terminal command runs before its finish raises an inbox item (inbox.terminal-commands long)",
			Validate: func(v any) error {
				if n, _ := v.(int); n < 1 {
					return fmt.Errorf("at least 1 minute")
				}
				return nil
			}},
		{Name: settingTerminalIgnore, Type: ochazuke.SettingList, Default: panesignals.DefaultIgnore,
			Description: "Programs whose terminal commands never raise an inbox item (full-screen and interactive ones): a command any of whose pipeline steps runs one, or a pane whose recorded program is one"},
	}
}

// commandRule is the terminal command signals' settings as a rule.
func (a *App) commandRule() panesignals.CommandRule {
	set := a.r.Settings()
	return panesignals.CommandRule{Mode: set.String(settingTerminalCommands),
		Long:   time.Duration(max(1, set.Int(settingTerminalLong))) * time.Minute,
		Ignore: set.Strings(settingTerminalIgnore)}
}

// commandHistory is the daemon's command-history for one pane; tuios has
// it, and tests replace it.
type commandHistory interface {
	Commands(ctx context.Context, p pane) ([]daemon.CommandEntry, error)
}

// commandReads is what a listing reads beside itself, off the loop: the
// command-history of every terminal pane whose command_seq moved past the
// marks, so each command a listing skipped is raised, with its folder. A
// failed read leaves the listing's last command alone to raise. Nothing
// is read before the marks are seeded, with the setting off, or outside
// the TUI.
func (a *App) commandReads() func(context.Context, []pane) {
	h, ok := a.panesd.(commandHistory)
	if !ok || a.sched == nil || !a.sched.live || a.inbox == nil || !a.signals.marks.Seeded || a.commandRule().Mode == panesignals.CommandsOff {
		return func(context.Context, []pane) {}
	}
	seen := maps.Clone(a.signals.marks.Seen)
	return func(ctx context.Context, panes []pane) {
		for i := range panes {
			p := &panes[i]
			if p.ID == "" || p.Exited || p.isAgent() || p.CommandSeq <= seen[p.ID] {
				continue
			}
			if got, err := h.Commands(ctx, *p); err == nil {
				p.Commands = got
			}
		}
	}
}

// signalShells are the listing's panes as the command rule reads them.
func signalShells(panes []pane) []panesignals.Shell {
	out := make([]panesignals.Shell, 0, len(panes))
	for _, p := range panes {
		if p.ID == "" {
			continue
		}
		s := panesignals.Shell{Window: p.ID, Session: p.Session, Label: p.label(), Agent: p.isAgent(), Exited: p.Exited,
			Program: p.Program, Seq: p.CommandSeq}
		for _, c := range p.Commands {
			if c.Window == "" || c.Window == p.ID {
				s.Commands = append(s.Commands, panesignals.Finished{Seq: c.Seq, Cmdline: c.Cmdline, Exit: c.Exit,
					Duration: time.Duration(c.DurationMS) * time.Millisecond, Cwd: c.Cwd})
			}
		}
		// The listing's last command, after the history (whose record of
		// the same seq has the folder too).
		if p.CommandSeq > 0 && (p.LastCmdline != "" || p.LastExitCode != nil || p.LastDurationMS > 0) {
			s.Commands = append(s.Commands, panesignals.Finished{Seq: p.CommandSeq, Cmdline: p.LastCmdline, Exit: p.LastExitCode,
				Duration: time.Duration(p.LastDurationMS) * time.Millisecond})
		}
		out = append(out, s)
	}
	return out
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

func signalSig(panes []panesignals.Pane, kinds map[string]bool, shells []panesignals.Shell, rule panesignals.CommandRule, boot, watching string) string {
	var b strings.Builder
	b.WriteString(watching + "|" + boot + "|" + rule.Mode + "," + rule.Long.String() + "," + strings.Join(rule.Ignore, " ") + "|")
	for _, s := range shells {
		b.WriteString(s.Window + ":" + strconv.FormatUint(s.Seq, 10) + ":" + strconv.FormatBool(s.Agent || s.Exited) + ";")
	}
	b.WriteString("|")
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
	shells, rule, boot := signalShells(panes), a.commandRule(), a.signals.bootID()
	sig := signalSig(list, kinds, shells, rule, boot, a.attached.ID)
	if sig == a.signals.sig {
		return
	}
	marks, err := a.applyPaneSignals(list, kinds, shells, rule, boot)
	if err != nil {
		a.r.UI().Error("pane signals: " + err.Error())
		return
	}
	a.signals.sig, a.signals.marks = sig, marks
}

func (a *App) applyPaneSignals(list []panesignals.Pane, kinds map[string]bool, shells []panesignals.Shell, rule panesignals.CommandRule, boot string) (panesignals.Marks, error) {
	items, err := a.inbox.items.All()
	if err != nil {
		return a.signals.marks, err
	}
	plan := panesignals.Reconcile(list, items, kinds, a.attached.ID)
	commands, marks := panesignals.ReconcileCommands(shells, items, rule, a.signals.marks, boot, a.attached.ID)
	plan = plan.Merge(commands)
	if plan.Empty() {
		return marks, nil
	}
	return marks, a.writeSignals(plan)
}

// writeSignals writes a reconcile's plan to the inbox.
func (a *App) writeSignals(plan panesignals.Plan) error {
	for _, s := range plan.Add {
		o, _, err := a.actionOwner(ochazuke.Arguments{"pane": s.Window, "session": s.Session})
		if err != nil {
			return err
		}
		extra := s.Extra
		if extra == nil {
			extra = map[string]any{"state_at": s.StateAt}
		}
		it, _, err := a.inbox.items.Add(actionitems.Spec{Source: panesignals.Source, Key: s.Key, Kind: s.Kind, Title: s.Title, Why: s.Why,
			ProjectID: o.ProjectID, TaskID: o.TaskID, Window: s.Window, Session: s.Session, Extra: extra})
		if err != nil {
			return err
		}
		if s.Read && !it.Read() {
			if _, err := a.inbox.items.SetRead(it.ID, true); err != nil {
				return err
			}
		}
		if s.Archive && it.ArchivedAt == 0 {
			plan.Archive = append(plan.Archive, it.ID)
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
