package panesignals

import (
	"slices"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
)

func exit(n int) *int { return &n }

func ran(seq uint64, cmdline string, code *int, d time.Duration) Finished {
	return Finished{Seq: seq, Cmdline: cmdline, Exit: code, Duration: d}
}

var testRule = CommandRule{Mode: CommandsLong, Long: 10 * time.Minute, Ignore: DefaultIgnore}

// The rule table: each mode against a failed, a long, a short, an
// unknown-status and an ignored command.
func TestCommandRuleTable(t *testing.T) {
	cases := []struct {
		name string
		f    Finished
		want map[string]bool // mode → raises
	}{
		{"failed short", ran(1, "go test ./...", exit(1), time.Second), map[string]bool{CommandsOff: false, CommandsFailed: true, CommandsLong: true, CommandsAll: true}},
		{"ok long", ran(1, "make build", exit(0), 14*time.Minute), map[string]bool{CommandsOff: false, CommandsFailed: false, CommandsLong: true, CommandsAll: true}},
		{"ok exactly long", ran(1, "make", exit(0), 10*time.Minute), map[string]bool{CommandsFailed: false, CommandsLong: true, CommandsAll: true}},
		{"ok short", ran(1, "ls", exit(0), time.Second), map[string]bool{CommandsOff: false, CommandsFailed: false, CommandsLong: false, CommandsAll: true}},
		{"unknown status short", ran(1, "ls", nil, time.Second), map[string]bool{CommandsFailed: false, CommandsLong: false, CommandsAll: true}},
		{"unknown status long", ran(1, "sleep 900", nil, 15*time.Minute), map[string]bool{CommandsFailed: false, CommandsLong: true, CommandsAll: true}},
		{"signal", ran(1, "make", exit(-1), time.Second), map[string]bool{CommandsFailed: true, CommandsLong: true}},
		{"ignored vim", ran(1, "vim main.go", exit(1), time.Hour), map[string]bool{CommandsFailed: false, CommandsLong: false, CommandsAll: false}},
		{"ignored in a pipe", ran(1, "git log | less", exit(0), time.Hour), map[string]bool{CommandsLong: false, CommandsAll: false}},
		{"ignored by path after env", ran(1, "TERM=xterm /usr/bin/ssh host", exit(255), time.Hour), map[string]bool{CommandsFailed: false, CommandsAll: false}},
		{"ignored after &&", ran(1, "cd x && nvim .", exit(0), time.Hour), map[string]bool{CommandsAll: false}},
		{"word not program", ran(1, "grep vim notes.txt", exit(1), time.Second), map[string]bool{CommandsFailed: true}},
	}
	for _, c := range cases {
		for mode, want := range c.want {
			r := testRule
			r.Mode = mode
			if got := r.Raises(c.f, ""); got != want {
				t.Errorf("%s, mode %s: raises %v, want %v", c.name, mode, got, want)
			}
		}
	}
	r := testRule
	r.Mode = CommandsAll
	if r.Raises(ran(1, "v main.go", exit(0), time.Second), "vim") {
		t.Fatal("a pane whose program is vim raised")
	}
	r.Ignore = nil
	if !r.Raises(ran(1, "vim main.go", exit(0), time.Second), "") {
		t.Fatal("an empty ignore list still ignored vim")
	}
}

func TestCommandWhy(t *testing.T) {
	cases := map[string]Finished{
		"`go test ./...` exited 1 after 12m":       ran(1, "go test ./...", exit(1), 12*time.Minute+3*time.Second),
		"`make build` finished after 14m":          ran(1, "make build", exit(0), 14*time.Minute),
		"`sleep 3` finished after 3s":              ran(1, "sleep 3", nil, 3*time.Second),
		"`make` was ended by a signal after 2m05s": ran(1, "make", exit(-1), 2*time.Minute+5*time.Second),
		"`x` finished after 1h05m":                 ran(1, "x", exit(0), time.Hour+5*time.Minute),
	}
	for want, f := range cases {
		if got := CommandWhy(f); got != want {
			t.Errorf("why %q, want %q", got, want)
		}
	}
}

func shell(window string, seq uint64, fs ...Finished) Shell {
	return Shell{Window: window, Session: "s", Label: "s/" + window, Seq: seq, Commands: fs}
}

// stored is a plan's adds as the store holds them.
func stored(plan Plan, ids ...string) []actionitems.Item {
	var out []actionitems.Item
	for i, s := range plan.Add {
		it := actionitems.Item{ID: ids[i], Source: Source, Key: s.Key, Kind: s.Kind, Window: s.Window, Why: s.Why, Extra: s.Extra}
		if s.Read {
			it.ReadAt = 1
		}
		if s.Archive {
			it.ArchivedAt = 1
		}
		out = append(out, it)
	}
	return out
}

func keys(plan Plan) []string {
	var out []string
	for _, s := range plan.Add {
		out = append(out, s.Key)
	}
	return out
}

// The first listing seeds the marks and raises nothing, however many
// commands already failed; the next command raises once.
func TestSeedingRaisesNothing(t *testing.T) {
	first := []Shell{shell("w1", 7, ran(7, "make", exit(2), time.Second)), shell("w2", 3)}
	plan, marks := ReconcileCommands(first, nil, testRule, Marks{}, "b1", "")
	if !plan.Empty() || !marks.Seeded || marks.Seen["w1"] != 7 || marks.Seen["w2"] != 3 || marks.Boot != "b1" {
		t.Fatalf("seed: plan %+v marks %+v", plan, marks)
	}
	if again, _ := ReconcileCommands(first, nil, testRule, marks, "b1", ""); !again.Empty() {
		t.Fatalf("same listing: %+v", again)
	}
	next := []Shell{shell("w1", 8, ran(8, "go test ./...", exit(1), 12*time.Minute)), shell("w2", 3)}
	plan, marks = ReconcileCommands(next, nil, testRule, marks, "b1", "")
	if !slices.Equal(keys(plan), []string{"command:w1:b1:8"}) || plan.Add[0].Why != "`go test ./...` exited 1 after 12m" || plan.Add[0].Title != "s/w1" {
		t.Fatalf("next: %+v", plan.Add)
	}
	if ex := plan.Add[0].Extra; ex["exit"] != 1 || ex["seq"] != uint64(8) || ex["failed"] != true || ex["cmdline"] != "go test ./..." {
		t.Fatalf("extra %v", ex)
	}
	// Raised again (a second TUI, or the listing repeating): the stored key
	// keeps it from raising twice.
	items := stored(plan, "a")
	if again, _ := ReconcileCommands(next, items, testRule, Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 7}}, "b1", ""); len(again.Add) != 0 {
		t.Fatalf("second TUI raised again: %+v", again.Add)
	}
	if marks.Seen["w1"] != 8 {
		t.Fatalf("marks %+v", marks)
	}
}

// A listing that skipped seqs raises every one of them that command-history
// names, oldest first; a seq none names is passed over.
func TestCatchUpSkippedSeqs(t *testing.T) {
	marks := Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 2}}
	s := shell("w1", 6,
		ran(6, "make lint", exit(1), time.Second), // the listing's last_*
		ran(5, "go vet", exit(3), time.Second),
		ran(3, "make build", exit(0), 20*time.Minute),
		ran(2, "old", exit(1), time.Second), // at the mark: already handled
		ran(6, "make lint", exit(1), time.Second))
	plan, next := ReconcileCommands([]Shell{s}, nil, testRule, marks, "b1", "")
	if want := []string{"command:w1:b1:3", "command:w1:b1:5", "command:w1:b1:6"}; !slices.Equal(keys(plan), want) {
		t.Fatalf("caught up %v, want %v", keys(plan), want)
	}
	if next.Seen["w1"] != 6 {
		t.Fatalf("marks %+v", next)
	}
}

// A window first seen after the seed counts from 0; a window gone from the
// listing drops its mark.
func TestNewWindowCountsFromZero(t *testing.T) {
	marks := Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"old": 4}}
	plan, next := ReconcileCommands([]Shell{shell("new", 1, ran(1, "false", exit(1), 0))}, nil, testRule, marks, "b1", "")
	if !slices.Equal(keys(plan), []string{"command:new:b1:1"}) {
		t.Fatalf("new window: %v", keys(plan))
	}
	if _, ok := next.Seen["old"]; ok || next.Seen["new"] != 1 {
		t.Fatalf("marks %+v", next)
	}
}

// Without a boot_id nothing raises and the marks hold; once it is known
// the held commands raise. A new boot_id starts every window from 0.
func TestBootHoldsAndRestarts(t *testing.T) {
	marks := Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 2}}
	listing := []Shell{shell("w1", 3, ran(3, "false", exit(1), 0))}
	plan, held := ReconcileCommands(listing, nil, testRule, marks, "", "")
	if !plan.Empty() || held.Seen["w1"] != 2 {
		t.Fatalf("held: %+v %+v", plan, held)
	}
	plan, _ = ReconcileCommands(listing, nil, testRule, held, "b1", "")
	if !slices.Equal(keys(plan), []string{"command:w1:b1:3"}) {
		t.Fatalf("after hold: %v", keys(plan))
	}
	// The daemon restarted: its new count starts again at 1.
	plan, next := ReconcileCommands([]Shell{shell("w1", 1, ran(1, "false", exit(1), 0))}, nil, testRule, held, "b2", "")
	if !slices.Equal(keys(plan), []string{"command:w1:b2:1"}) || next.Boot != "b2" {
		t.Fatalf("restart: %v %+v", keys(plan), next)
	}
	// Seeded before the boot was known: the first boot is adopted, not a restart.
	_, seeded := ReconcileCommands([]Shell{shell("w1", 5)}, nil, testRule, Marks{}, "", "")
	plan, _ = ReconcileCommands([]Shell{shell("w1", 6, ran(6, "false", exit(1), 0))}, nil, testRule, seeded, "b1", "")
	if !slices.Equal(keys(plan), []string{"command:w1:b1:6"}) {
		t.Fatalf("adopted boot: %v", keys(plan))
	}
}

// Agent and exited panes raise nothing (their marks still move).
func TestAgentsAndExitedRaiseNothing(t *testing.T) {
	marks := Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"a": 0, "x": 0}}
	ag, ex := shell("a", 1, ran(1, "false", exit(1), 0)), shell("x", 1, ran(1, "false", exit(1), 0))
	ag.Agent, ex.Exited = true, true
	plan, next := ReconcileCommands([]Shell{ag, ex}, nil, testRule, marks, "b1", "")
	if !plan.Empty() || next.Seen["a"] != 1 || next.Seen["x"] != 1 {
		t.Fatalf("plan %+v marks %+v", plan, next)
	}
}

// The setting off raises nothing; open items still clear.
func TestCommandsOff(t *testing.T) {
	off := testRule
	off.Mode = CommandsOff
	marks := Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 1}}
	failed := actionitems.Item{ID: "f", Source: Source, Kind: Command, Key: "command:w1:b1:1", Window: "w1", Extra: map[string]any{"failed": true}}
	plan, _ := ReconcileCommands([]Shell{shell("w1", 2, ran(2, "true", exit(0), time.Hour))}, []actionitems.Item{failed}, off, marks, "b1", "")
	if len(plan.Add) != 0 || !slices.Equal(plan.Archive, []string{"f"}) {
		t.Fatalf("off: %+v", plan)
	}
}

// A failed item is archived once a later command of its pane succeeds (not
// a later failure, not another pane's success, not another boot's); a
// success item stays.
func TestFailedClearsOnLaterSuccess(t *testing.T) {
	marks := Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 0, "w2": 0}}
	plan, marks := ReconcileCommands([]Shell{
		shell("w1", 2, ran(1, "make", exit(2), 0), ran(2, "make long", exit(0), time.Hour)),
		shell("w2", 1, ran(1, "make", exit(2), 0)),
	}, nil, testRule, marks, "b1", "")
	// w1's failure is already followed by a success in the same listing:
	// it is raised archived.
	if len(plan.Add) != 3 || !plan.Add[0].Archive || plan.Add[1].Archive || plan.Add[2].Archive {
		t.Fatalf("raised %+v", plan.Add)
	}
	items := stored(plan, "w1fail", "w1long", "w2fail")
	plan, marks = ReconcileCommands([]Shell{
		shell("w1", 3, ran(3, "false", exit(1), 0)),
		shell("w2", 1, ran(1, "make", exit(2), 0)),
	}, items, testRule, marks, "b1", "")
	if len(plan.Archive) != 0 || len(plan.Add) != 1 {
		t.Fatalf("a later failure: %+v", plan)
	}
	items = append(items, stored(plan, "w1fail3")...)
	plan, _ = ReconcileCommands([]Shell{
		shell("w1", 4, ran(4, "true", exit(0), 0)),
		shell("w2", 1, ran(1, "make", exit(2), 0)),
	}, items, testRule, marks, "b1", "")
	if !slices.Equal(plan.Archive, []string{"w1fail3"}) || len(plan.Read) != 0 {
		t.Fatalf("later success: %+v", plan)
	}
	// Another boot's success does not clear b1's item.
	plan, _ = ReconcileCommands([]Shell{shell("w2", 1, ran(1, "true", exit(0), 0))}, items, testRule, marks, "b2", "")
	if slices.Contains(plan.Archive, "w2fail") {
		t.Fatalf("other boot cleared: %+v", plan)
	}
}

// Watching a pane archives its failed items and marks the others read; a
// command raised while it is watched is raised read (a failed one also
// archived).
func TestWatchingClearsCommands(t *testing.T) {
	marks := Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 0}}
	plan, _ := ReconcileCommands([]Shell{shell("w1", 2, ran(1, "make", exit(2), 0), ran(2, "make long", exit(0), time.Hour))}, nil, testRule, marks, "b1", "w1")
	if len(plan.Add) != 2 || !plan.Add[0].Read || !plan.Add[0].Archive || !plan.Add[1].Read || plan.Add[1].Archive {
		t.Fatalf("raised while watched: %+v", plan.Add)
	}
	items := []actionitems.Item{
		{ID: "f", Source: Source, Kind: Command, Key: "command:w1:b1:1", Window: "w1", Extra: map[string]any{"failed": true}},
		{ID: "l", Source: Source, Kind: Command, Key: "command:w1:b1:2", Window: "w1", Extra: map[string]any{"failed": false}},
		{ID: "o", Source: Source, Kind: Command, Key: "command:w9:b1:2", Window: "w9", Extra: map[string]any{"failed": true}},
	}
	plan, _ = ReconcileCommands([]Shell{shell("w1", 2)}, items, testRule, Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 2}}, "b1", "w1")
	if !slices.Equal(plan.Archive, []string{"f"}) || !slices.Equal(plan.Read, []string{"l"}) || len(plan.Add) != 0 {
		t.Fatalf("watched: %+v", plan)
	}
	// Watching clears even while the boot is unknown.
	plan, _ = ReconcileCommands([]Shell{shell("w1", 2)}, items, testRule, Marks{Seeded: true, Boot: "b1", Seen: map[string]uint64{"w1": 2}}, "", "w1")
	if !slices.Equal(plan.Archive, []string{"f"}) {
		t.Fatalf("watched, boot unknown: %+v", plan)
	}
}

// The agent Reconcile leaves command items alone, and windowOf reads the
// four-part key.
func TestAgentReconcileLeavesCommands(t *testing.T) {
	it := actionitems.Item{ID: "c", Source: Source, Kind: Command, Key: "command:w1:b1:3", Window: "w1"}
	if w, k := windowOf(it); w != "w1" || k != Command {
		t.Fatalf("windowOf %q %q", w, k)
	}
	if plan := Reconcile(nil, []actionitems.Item{it}, nil, "w1"); !plan.Empty() {
		t.Fatalf("agent reconcile touched a command item: %+v", plan)
	}
}
