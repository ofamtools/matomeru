package panesignals

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
)

// Command signals: a terminal (shell) pane's finished commands, from the
// OSC 133 marks tuios reads (list-windows' command_seq and last_*, and
// command-history for the commands two listings skipped).
//
//   - A finished command of a non-agent, running pane raises one item of
//     kind command, keyed command:window:boot_id:command_seq. command_seq
//     counts one pane's finished commands within one daemon start
//     (boot_id), so the key names one command exactly once, whichever TUI
//     raises it (the store folds a repeat).
//   - Which commands raise is the rule's mode: off (none), failed (exit not
//     0), long (failed, or ran at least Long) and all (every one). A
//     command with no exit status counts as not failed. A command any of
//     whose pipeline steps runs an ignored program (vim, less, ssh, …), or
//     one in a pane whose recorded foreground program is ignored, raises
//     nothing.
//   - Marks are the per-window high water of command_seq. The first listing
//     seeds them and raises nothing, so starting mtm never floods the inbox
//     with commands that finished before it ran. A window first seen later
//     starts from 0 (its every command is new). A new boot_id (the daemon
//     restarted) starts every window from 0 again. While the boot_id is not
//     known (no event stream yet, or it broke) nothing is raised and the
//     marks hold, so the commands are raised once it is known.
//   - A failed command's open item is archived once a later command of the
//     same pane (same daemon start) succeeds (raised archived when the same
//     listing already holds that success), or once the person watches
//     the pane (attaches it). A long command that succeeded is marked read
//     once the person watches the pane. An item raised while the pane is
//     watched is raised read (and a failed one archived at once: the person
//     saw it fail).

// Command is the kind of a terminal command signal.
const Command = "command"

// Modes of the command rule.
const (
	CommandsOff    = "off"
	CommandsFailed = "failed"
	CommandsLong   = "long"
	CommandsAll    = "all"
)

// CommandModes are the modes, in the order the setting lists them.
var CommandModes = []string{CommandsOff, CommandsFailed, CommandsLong, CommandsAll}

// DefaultIgnore are the programs whose commands never raise: full-screen
// and interactive programs, which end when the person leaves them.
var DefaultIgnore = []string{"vim", "nvim", "vi", "less", "more", "man", "ssh", "mosh", "tmux", "top", "htop", "btop", "watch", "tail"}

// Finished is one finished command.
type Finished struct {
	Seq      uint64
	Cmdline  string
	Exit     *int // nil when the shell sent no status
	Duration time.Duration
	Cwd      string
}

// Failed reports a command that exited with a status other than 0.
func (f Finished) Failed() bool { return f.Exit != nil && *f.Exit != 0 }

// Succeeded reports a command that exited 0.
func (f Finished) Succeeded() bool { return f.Exit != nil && *f.Exit == 0 }

// CommandRule says which finished commands raise an item.
type CommandRule struct {
	Mode   string
	Long   time.Duration
	Ignore []string
}

// Raises reports whether f, finished in a pane whose foreground program is
// program ("" when unknown), raises an item.
func (r CommandRule) Raises(f Finished, program string) bool {
	if r.Mode == CommandsOff || r.Mode == "" || r.Ignored(f.Cmdline, program) {
		return false
	}
	switch r.Mode {
	case CommandsFailed:
		return f.Failed()
	case CommandsLong:
		return f.Failed() || r.Long > 0 && f.Duration >= r.Long
	case CommandsAll:
		return true
	}
	return false
}

// Ignored reports a command line any of whose steps (split at |, ;, &&
// and ||) runs an ignored program, or a pane whose program is ignored.
// A step's program is its first word after VAR=value assignments, by its
// base name (/usr/bin/vim is vim).
func (r CommandRule) Ignored(cmdline, program string) bool {
	if program != "" && slices.Contains(r.Ignore, path.Base(program)) {
		return true
	}
	for _, step := range strings.FieldsFunc(cmdline, func(c rune) bool { return c == '|' || c == ';' || c == '&' }) {
		for _, word := range strings.Fields(step) {
			if strings.Contains(word, "=") && !strings.HasPrefix(word, "=") {
				continue
			}
			if slices.Contains(r.Ignore, path.Base(word)) {
				return true
			}
			break
		}
	}
	return false
}

// Shell is what the command rule reads of a pane.
type Shell struct {
	Window  string
	Session string
	Label   string
	Agent   bool
	Exited  bool
	Program string // the foreground program mtm recorded ("vim"), "" when none
	Seq     uint64 // command_seq: how many commands it has finished
	// Commands are finished commands known for the pane (the listing's
	// last command, command-history's records), any order; a seq the
	// listing skipped and none of these names is not raised.
	Commands []Finished
}

// Marks are the per-window high water of command_seq, and the daemon start
// they belong to.
type Marks struct {
	Boot   string
	Seen   map[string]uint64
	Seeded bool
}

// Clone is a copy that shares nothing with m.
func (m Marks) Clone() Marks {
	out := m
	out.Seen = make(map[string]uint64, len(m.Seen))
	for k, v := range m.Seen {
		out.Seen[k] = v
	}
	return out
}

// CommandKey is the de-duplication key of one finished command.
func CommandKey(window, boot string, seq uint64) string {
	return Command + ":" + window + ":" + boot + ":" + strconv.FormatUint(seq, 10)
}

// commandKeyParts reads a command key back: window, boot and seq.
func commandKeyParts(key string) (string, string, uint64, bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 4 || parts[0] != Command || parts[1] == "" {
		return "", "", 0, false
	}
	seq, err := strconv.ParseUint(parts[3], 10, 64)
	return parts[1], parts[2], seq, err == nil
}

// CommandWhy is the one line a command item says: "`go test ./...` exited
// 1 after 12m", "`make build` finished after 14m".
func CommandWhy(f Finished) string {
	cmd := "`" + shortCmdline(f.Cmdline) + "`"
	if f.Cmdline == "" {
		cmd = "a command"
	}
	after := " after " + Span(f.Duration)
	switch {
	case f.Exit != nil && *f.Exit < 0:
		return cmd + " was ended by a signal" + after
	case f.Failed():
		return cmd + " exited " + strconv.Itoa(*f.Exit) + after
	}
	return cmd + " finished" + after
}

// shortCmdline is a command line cut for one line of the inbox.
func shortCmdline(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return s
}

// Span is a duration in its largest two units: 0s, 42s, 12m, 1h05m.
func Span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		if s := int(d.Seconds()) % 60; d < 10*time.Minute && s != 0 {
			return fmt.Sprintf("%dm%02ds", int(d.Minutes()), s)
		}
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// CommandExtra is a command item's extra fields.
func CommandExtra(f Finished, boot string) map[string]any {
	out := map[string]any{"cmdline": f.Cmdline, "seq": f.Seq, "duration_ms": f.Duration.Milliseconds(),
		"cwd": f.Cwd, "boot_id": boot, "failed": f.Failed()}
	if f.Exit != nil {
		out["exit"] = *f.Exit
	}
	return out
}

// CommandFailed reports a command item raised for a failed command.
func CommandFailed(it actionitems.Item) bool {
	failed, _ := it.Extra["failed"].(bool)
	return it.Kind == Command && failed
}

// ReconcileCommands is the plan for the shells of a full pane listing
// against the inbox's items, and the marks to keep once it is written.
// boot is the daemon start the listing belongs to, "" when not known;
// watching is the window the person watches, "" for none.
func ReconcileCommands(shells []Shell, items []actionitems.Item, rule CommandRule, marks Marks, boot, watching string) (Plan, Marks) {
	next := marks.Clone()
	var plan Plan
	if !next.Seeded {
		for _, s := range shells {
			if s.Window != "" {
				next.Seen[s.Window] = s.Seq
			}
		}
		next.Seeded, next.Boot = true, boot
		return clearCommands(plan, shells, items, nil, "", watching), next
	}
	if boot == "" {
		// Hold: which daemon start the seqs count in is not known.
		return clearCommands(plan, shells, items, nil, "", watching), next
	}
	if next.Boot != boot {
		if next.Boot != "" {
			next.Seen = map[string]uint64{} // the daemon restarted: every window counts from 0
		}
		next.Boot = boot
	}
	have := map[string]bool{}
	for _, it := range items {
		if it.Source == Source && it.Key != "" {
			have[it.Key] = true
		}
	}
	listed := map[string]bool{}
	finished := map[string][]Finished{} // window → the commands this listing names
	for _, s := range shells {
		if s.Window == "" {
			continue
		}
		listed[s.Window] = true
		from := next.Seen[s.Window]
		next.Seen[s.Window] = s.Seq
		if s.Seq <= from || s.Agent || s.Exited {
			continue
		}
		var fresh []Finished
		seen := map[uint64]bool{}
		for _, f := range s.Commands {
			if f.Seq > from && f.Seq <= s.Seq && !seen[f.Seq] {
				seen[f.Seq] = true
				fresh = append(fresh, f)
			}
		}
		slices.SortFunc(fresh, func(x, y Finished) int { return cmp.Compare(x.Seq, y.Seq) })
		finished[s.Window] = fresh
		for _, f := range fresh {
			if !rule.Raises(f, s.Program) {
				continue
			}
			key := CommandKey(s.Window, boot, f.Seq)
			if have[key] {
				continue
			}
			have[key] = true
			watched := s.Window == watching
			plan.Add = append(plan.Add, Signal{Kind: Command, Key: key, Window: s.Window, Session: s.Session, Title: s.Label,
				Why: CommandWhy(f), Read: watched, Archive: f.Failed() && (watched || laterSuccess(fresh, f.Seq)), Extra: CommandExtra(f, boot)})
		}
	}
	for w := range next.Seen {
		if !listed[w] {
			delete(next.Seen, w)
		}
	}
	return clearCommands(plan, shells, items, finished, boot, watching), next
}

// clearCommands adds the clears of open command items: a failed one when
// a later command of its pane (boot) succeeded, and every one of the
// watched pane (failed archived, the others marked read).
func clearCommands(plan Plan, shells []Shell, items []actionitems.Item, finished map[string][]Finished, boot, watching string) Plan {
	for _, it := range items {
		if it.Source != Source || it.Kind != Command || it.ArchivedAt > 0 {
			continue
		}
		window, itemBoot, seq, ok := commandKeyParts(it.Key)
		if !ok {
			continue
		}
		failed := CommandFailed(it)
		switch {
		case window == watching && failed:
			plan.Archive = append(plan.Archive, it.ID)
		case window == watching && !it.Read():
			plan.Read = append(plan.Read, it.ID)
		case failed && boot != "" && itemBoot == boot && laterSuccess(finished[window], seq):
			plan.Archive = append(plan.Archive, it.ID)
		}
	}
	return plan
}

func laterSuccess(fs []Finished, seq uint64) bool {
	return slices.ContainsFunc(fs, func(f Finished) bool { return f.Seq > seq && f.Succeeded() })
}
