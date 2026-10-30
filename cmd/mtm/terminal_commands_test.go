package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/panesignals"
)

func exitCode(n int) *int { return &n }

// shellPane is a terminal pane whose shell has finished seq commands, the
// last cmdline with status code after ms.
func shellPane(window string, seq uint64, cmdline string, code *int, ms int64) pane {
	return pane{Session: "work", SessionID: "work", Pane: daemon.Pane{ID: window, Name: window, MarksCommands: true, CommandSeq: seq,
		AtPrompt: true, LastCmdline: cmdline, LastExitCode: code, LastDurationMS: ms}}
}

// commandItems are the inbox's terminal command items, oldest first.
func commandItems(t *testing.T, a *App) []actionitems.Item {
	t.Helper()
	var out []actionitems.Item
	for _, it := range mtmItems(t, a) {
		if it.Kind == panesignals.Command {
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(x, y actionitems.Item) int { return strings.Compare(x.Key, y.Key) })
	return out
}

// liveSignals is an App whose TUI runs (pane signals raise) and whose event
// stream reported boot.
func liveSignals(t *testing.T, a *App, boot string) {
	t.Helper()
	a.startSchedules()
	a.signals.setBoot(boot)
}

const minute = int64(60_000)

// A terminal's finished commands reach the inbox: the first listing seeds
// and raises nothing; a failed command raises an errored item (and moves
// the pane's row to the queue), a later success archives it, a long
// success raises an unread item that attaching the pane marks read; a
// short success raises nothing under the default (long).
func TestTerminalCommandsReachTheInbox(t *testing.T) {
	a, _, _ := scheduleApp(t)
	liveSignals(t, a, "b1")
	other := shellPane("w2", 1, "ls", exitCode(0), 10)
	a.setPanes([]pane{shellPane("w1", 3, "make", exitCode(2), 100), other}, nil)
	if got := commandItems(t, a); len(got) != 0 {
		t.Fatalf("seed raised %+v", got)
	}
	// The cursor rests on the other pane (the row under it stays put).
	if err := a.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.pv.sections[1].tree.Select("pane:work/w2"); err != nil {
		t.Fatal(err)
	}
	a.setPanes([]pane{shellPane("w1", 4, "go test ./...", exitCode(1), 12*minute+3_000), other}, nil)
	items := commandItems(t, a)
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	failed := items[0]
	if failed.Key != "command:w1:b1:4" || failed.Title != "work/w1" || failed.Why != "`go test ./...` exited 1 after 12m" || failed.Window != "w1" || failed.Read() {
		t.Fatalf("failed item %+v", failed)
	}
	if failed.Extra["cmdline"] != "go test ./..." || failed.Extra["failed"] != true {
		t.Fatalf("extra %v", failed.Extra)
	}
	if c := itemCategory(failed); c != signalErrored {
		t.Fatalf("category %q", c)
	}
	if q := sectionIDs(a, 0); !slices.Contains(q, "pane:work/w1") {
		t.Fatalf("queue %v: the failed terminal is not there", q)
	}
	// A short success: nothing raised, and the failure is archived.
	a.setPanes([]pane{shellPane("w1", 5, "true", exitCode(0), 5), other}, nil)
	if got := commandItems(t, a); len(got) != 1 {
		t.Fatalf("short success raised: %+v", got)
	}
	if it, _ := a.inbox.items.Get(failed.ID); it.State(a.inbox.items.Now()) != actionitems.StateArchived {
		t.Fatalf("failed item not archived: %+v", it)
	}
	if q := sectionIDs(a, 0); slices.Contains(q, "pane:work/w1") {
		t.Fatalf("queue %v still holds the terminal", q)
	}
	// A long success: unread, then read once the pane is attached.
	long := shellPane("w1", 6, "make build", exitCode(0), 14*minute)
	a.setPanes([]pane{long, other}, nil)
	items = commandItems(t, a)
	if len(items) != 2 || items[1].Why != "`make build` finished after 14m" || items[1].Read() || itemCategory(items[1]) != signalUnread {
		t.Fatalf("long item %+v", items)
	}
	a.attached = long
	a.setPanes([]pane{long, other}, nil)
	if it, _ := a.inbox.items.Get(items[1].ID); !it.Read() || it.State(a.inbox.items.Now()) != actionitems.StateOpen {
		t.Fatalf("attached: %+v", it)
	}
	// A failure while the pane is watched is raised read and archived.
	a.setPanes([]pane{shellPane("w1", 7, "false", exitCode(1), 5), other}, nil)
	items = commandItems(t, a)
	if len(items) != 3 || !items[2].Read() || items[2].State(a.inbox.items.Now()) != actionitems.StateArchived {
		t.Fatalf("watched failure %+v", items)
	}
}

// The item shows on the Inbox page with its why, beside the pane's row
// signal (the failed glyph in the queue).
func TestTerminalCommandShowsInTheInbox(t *testing.T) {
	a, _, _ := scheduleApp(t)
	liveSignals(t, a, "b1")
	a.setPanes([]pane{shellPane("w1", 1, "true", exitCode(0), 5)}, nil)
	a.setPanes([]pane{shellPane("w1", 2, "go test ./...", exitCode(1), 3*minute)}, nil)
	toPage(t, a, inboxPage)
	f := frameAt(a, 120)
	for _, want := range []string{"● work/w1", "`go test ./...` exited 1 after 3m", "mtm"} {
		if !strings.Contains(f, want) {
			t.Fatalf("inbox lacks %q:\n%s", want, f)
		}
	}
}

// Off raises nothing; failed raises only failures; all raises short
// successes too, still honouring the ignore list.
func TestTerminalCommandModes(t *testing.T) {
	a, _, _ := scheduleApp(t)
	liveSignals(t, a, "b1")
	a.setPanes([]pane{shellPane("w1", 1, "true", exitCode(0), 5)}, nil)
	mustCLI(t, a, "settings", "set", "inbox.terminal-commands", "off", "-y")
	a.setPanes([]pane{shellPane("w1", 2, "false", exitCode(1), 5)}, nil)
	if got := commandItems(t, a); len(got) != 0 {
		t.Fatalf("off raised %+v", got)
	}
	mustCLI(t, a, "settings", "set", "inbox.terminal-commands", "failed", "-y")
	a.setPanes([]pane{shellPane("w1", 3, "make build", exitCode(0), 60*minute)}, nil)
	if got := commandItems(t, a); len(got) != 0 {
		t.Fatalf("failed mode raised a long success %+v", got)
	}
	mustCLI(t, a, "settings", "set", "inbox.terminal-commands", "all", "-y")
	a.setPanes([]pane{shellPane("w1", 4, "ls", exitCode(0), 5)}, nil)
	a.setPanes([]pane{shellPane("w1", 5, "vim notes.md", exitCode(0), 5)}, nil)
	got := commandItems(t, a)
	if len(got) != 1 || got[0].Key != "command:w1:b1:4" {
		t.Fatalf("all: %+v", got)
	}
	mustCLI(t, a, "settings", "set", "inbox.terminal-commands", "long", "-y")
	mustCLI(t, a, "settings", "set", "inbox.terminal-commands.long-minutes", "1", "-y")
	a.setPanes([]pane{shellPane("w1", 6, "sleep 70", exitCode(0), 70_000)}, nil)
	if got := commandItems(t, a); len(got) != 2 {
		t.Fatalf("long-minutes 1: %+v", got)
	}
}

// Agent panes and exited panes raise no command items.
func TestTerminalCommandsSkipAgentsAndExited(t *testing.T) {
	a, _, _ := scheduleApp(t)
	liveSignals(t, a, "b1")
	ag, ex := shellPane("a1", 1, "x", exitCode(0), 5), shellPane("x1", 1, "x", exitCode(0), 5)
	ag.Harness, ag.AgentState = "claude", "working"
	a.setPanes([]pane{ag, ex}, nil)
	ag.CommandSeq, ex.CommandSeq, ex.Exited = 2, 2, true
	ag.LastExitCode, ex.LastExitCode = exitCode(1), exitCode(1)
	a.setPanes([]pane{ag, ex}, nil)
	if got := commandItems(t, a); len(got) != 0 {
		t.Fatalf("raised %+v", got)
	}
}

// Two TUIs on one database raise each command once; a restarted mtm seeds
// again and raises only what finishes after it starts.
func TestTerminalCommandsTwoTUIsAndRestart(t *testing.T) {
	path := seeded(t)
	var apps []*App
	for range 2 {
		a := openAt(t, path)
		liveSignals(t, a, "b1")
		a.setPanes([]pane{shellPane("w1", 1, "true", exitCode(0), 5)}, nil)
		apps = append(apps, a)
	}
	next := []pane{shellPane("w1", 2, "make", exitCode(2), 5)}
	for _, a := range apps {
		a.setPanes(next, nil)
	}
	if got := commandItems(t, apps[1]); len(got) != 1 {
		t.Fatalf("two TUIs: %+v", got)
	}
	// The person archives it in one TUI; the other's next reconcile does
	// not bring it back.
	if _, err := apps[0].inbox.items.Archive(commandItems(t, apps[0])[0].ID); err != nil {
		t.Fatal(err)
	}
	apps[1].signals.sig = ""
	apps[1].setPanes(next, nil)
	if got := commandItems(t, apps[1]); len(got) != 1 || got[0].ArchivedAt == 0 {
		t.Fatalf("archived item came back: %+v", got)
	}
	// Restart: a new mtm seeds from its first listing.
	again := openAt(t, path)
	liveSignals(t, again, "b1")
	again.setPanes([]pane{shellPane("w1", 3, "make", exitCode(2), 5)}, nil)
	if got := commandItems(t, again); len(got) != 1 {
		t.Fatalf("restart seed raised: %+v", got)
	}
	again.setPanes([]pane{shellPane("w1", 4, "make", exitCode(2), 5)}, nil)
	if got := commandItems(t, again); len(got) != 2 || got[1].Key != "command:w1:b1:4" {
		t.Fatalf("after restart: %+v", got)
	}
}

// historyPanes is the fake daemon with command-history.
type historyPanes struct {
	*fakePanes
	history map[string][]daemon.CommandEntry
	reads   []string
}

func (h *historyPanes) Commands(_ context.Context, p pane) ([]daemon.CommandEntry, error) {
	h.reads = append(h.reads, p.ID)
	return h.history[p.ID], nil
}

// A listing that skipped commands reads the pane's command-history with
// the listing and raises each skipped one, with its folder; panes whose
// seq did not move are not read.
func TestTerminalCommandsCatchUpFromHistory(t *testing.T) {
	a, f := panesApp(t)
	h := &historyPanes{fakePanes: f, history: map[string][]daemon.CommandEntry{}}
	a.panesd = h
	liveSignals(t, a, "b1")
	f.panes = []pane{shellPane("w1", 1, "true", exitCode(0), 5), shellPane("w2", 4, "ls", exitCode(0), 5)}
	reload(t, a)
	if len(h.reads) != 0 || len(commandItems(t, a)) != 0 {
		t.Fatalf("seed: reads %v items %+v", h.reads, commandItems(t, a))
	}
	f.panes[0] = shellPane("w1", 4, "make lint", exitCode(1), 5)
	h.history["w1"] = []daemon.CommandEntry{
		{Window: "w1", Seq: 4, Cmdline: "make lint", Exit: exitCode(1), DurationMS: 5, Cwd: "/src/a"},
		{Window: "w1", Seq: 3, Cmdline: "go vet ./...", Exit: exitCode(1), DurationMS: 5, Cwd: "/src/a"},
		{Window: "w1", Seq: 2, Cmdline: "make build", Exit: exitCode(0), DurationMS: 30 * minute, Cwd: "/src/b"},
		{Window: "w1", Seq: 1, Cmdline: "true", Exit: exitCode(0), DurationMS: 5},
	}
	reload(t, a)
	if !slices.Equal(h.reads, []string{"w1"}) {
		t.Fatalf("history reads %v", h.reads)
	}
	got := commandItems(t, a)
	var keys []string
	for _, it := range got {
		keys = append(keys, it.Key)
	}
	if want := []string{"command:w1:b1:2", "command:w1:b1:3", "command:w1:b1:4"}; !slices.Equal(keys, want) {
		t.Fatalf("caught up %v, want %v", keys, want)
	}
	if got[0].Extra["cwd"] != "/src/b" || got[2].Extra["cwd"] != "/src/a" {
		t.Fatalf("cwd %v %v", got[0].Extra, got[2].Extra)
	}
}

// Without the event stream's boot_id nothing is raised; once it reports
// one, the held commands raise.
func TestTerminalCommandsWaitForTheBoot(t *testing.T) {
	a, _, _ := scheduleApp(t)
	liveSignals(t, a, "")
	a.setPanes([]pane{shellPane("w1", 1, "true", exitCode(0), 5)}, nil)
	listing := []pane{shellPane("w1", 2, "false", exitCode(1), 5)}
	a.setPanes(listing, nil)
	if got := commandItems(t, a); len(got) != 0 {
		t.Fatalf("raised without a boot: %+v", got)
	}
	a.signals.setBoot("b7")
	a.setPanes(listing, nil)
	if got := commandItems(t, a); len(got) != 1 || got[0].Key != "command:w1:b7:2" {
		t.Fatalf("after the boot: %+v", got)
	}
}
