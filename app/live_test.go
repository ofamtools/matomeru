package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
)

const liveDeadline = 10 * time.Second

// liveApp is an app over a store on disk with the given live parts. The
// watcher never ticks on its own here: a test looks with WatchCheck and
// delivers the wake-up itself, so nothing depends on timing.
func liveApp(t *testing.T, seed bool, opts Options) (*App, *Driver) {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	if seed {
		if err := SeedDemo(store); err != nil {
			t.Fatal(err)
		}
	}
	opts.FS = smokeFS()
	if opts.Watch == 0 {
		opts.Watch = time.Hour
	}
	a, err := New(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.Close()
		store.Close()
	})
	enableLegacyPagesForTest(a, SpacePanes)
	return a, NewDriver(a, 120, 36)
}

// wake delivers one wake-up, as the pump of Start would.
func wake(d *Driver) { d.Send(wakeMsg()) }

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(liveDeadline); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func script(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// An idle store costs no wake-up and no redraw; someone else's write costs
// exactly one; the app's own writes cost none.
func TestWatcherRedrawsOnlyOnChange(t *testing.T) {
	a, d := liveApp(t, true, Options{})
	d.Keys("2")
	renders, refreshes := a.Shell.Renders, a.rt.live.refreshes
	for range 20 {
		if a.WatchCheck() {
			t.Fatal("the watcher reported a change in an idle store")
		}
	}
	if len(a.rt.live.wake) != 0 || a.Shell.Renders != renders || a.rt.live.refreshes != refreshes {
		t.Fatalf("an idle store woke the app: wake %d, renders %d -> %d", len(a.rt.live.wake), renders, a.Shell.Renders)
	}

	d.Keys("s", "s") // the owner's own writes are drawn by their own commands
	if a.WatchCheck() || len(a.rt.live.wake) != 0 {
		t.Fatal("the app's own write came back as a change")
	}
	wake(d) // a wake-up that finds nothing new redraws nothing new
	if a.rt.live.refreshes != refreshes {
		t.Fatalf("a wake-up with nothing new refreshed the view (%d -> %d)", refreshes, a.rt.live.refreshes)
	}

	// Another process (an agent's MCP call, the CLI) writes to the store.
	if _, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Written elsewhere"}},
		core.Actor{Kind: "agent", ID: "mcp"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Frame(), "Written elsewhere") {
		t.Fatal("the view changed before the watcher looked")
	}
	if !a.WatchCheck() || len(a.rt.live.wake) != 1 {
		t.Fatalf("the watcher missed the write (wake %d)", len(a.rt.live.wake))
	}
	if a.WatchCheck() {
		t.Fatal("one write was reported twice")
	}
	<-a.rt.live.wake
	wake(d)
	if a.rt.live.refreshes != refreshes+1 || !strings.Contains(d.Frame(), "Written elsewhere") {
		t.Fatalf("refreshes %d -> %d; frame:\n%s", refreshes, a.rt.live.refreshes, d.Frame())
	}
}

// Start's loops: the watcher wakes the program once per outside write and
// stays silent while the store is idle.
func TestStartWakesOnOutsideWrites(t *testing.T) {
	a, _ := liveApp(t, false, Options{Watch: 2 * time.Millisecond})
	msgs := make(chan tea.Msg, 16)
	stop := a.Start(func(m tea.Msg) { msgs <- m })
	defer stop()
	time.Sleep(40 * time.Millisecond) // twenty looks at an idle store
	if len(msgs) != 0 {
		t.Fatalf("%d wake-ups from an idle store", len(msgs))
	}
	if _, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "x"}}, core.Actor{Kind: "agent", ID: "mcp"}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-msgs:
		if ev, ok := m.(core.CommandEvent); !ok || ev.Command.ID != CmdWake {
			t.Fatalf("woke with %#v", m)
		}
	case <-time.After(liveDeadline):
		t.Fatal("no wake-up after an outside write")
	}
	time.Sleep(40 * time.Millisecond)
	if len(msgs) != 0 {
		t.Fatalf("one write woke the app %d more times", len(msgs))
	}
}

// Runs: n runs a command, r runs it again, "!cmd" in the palette runs one,
// x cancels. Each is a real process recorded as a run.
func TestRunsAreLive(t *testing.T) {
	a, d := liveApp(t, false, Options{Live: true})
	selected := func() data.Record {
		t.Helper()
		row, ok := a.records(runsTable, data.KindRun).FocusedRow()
		if !ok || row.Ref == nil {
			t.Fatalf("no run is selected; status %q\n%s", a.Status(), d.Frame())
		}
		rec, err := a.Store.Get(*row.Ref)
		if err != nil {
			t.Fatal(err)
		}
		return *rec
	}
	finish := func(id string) data.Record {
		t.Helper()
		select {
		case <-a.rt.Runner.Done(id):
		case <-time.After(liveDeadline):
			t.Fatalf("run %s did not end", id)
		}
		wake(d)
		rec, err := a.Store.Get(core.Ref{Kind: data.KindRun, ID: id})
		if err != nil {
			t.Fatal(err)
		}
		return *rec
	}

	d.Keys("3", "n")
	if a.Modal() != "launch" || !strings.Contains(d.Frame(), "Run a command") {
		t.Fatalf("n did not open the run form: modal %q\n%s", a.Modal(), d.Frame())
	}
	d.Keys("i")
	d.Type("echo live-run-out")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("the form stayed open: %s", a.Status())
	}
	first := finish(selected().ID)
	if first.Text("runStatus") != runs.StatusDone || !strings.Contains(first.Text("output"), "live-run-out") {
		t.Fatalf("run = %v", first.Fields)
	}
	if frame := d.Frame(); !strings.Contains(frame, "live-run-out") || !strings.Contains(frame, "echo live-run-out") {
		t.Fatalf("the finished run is not shown:\n%s", frame)
	}

	d.Keys("r")
	again := finish(selected().ID)
	rels, err := a.Store.Related(again.Ref(), runs.RelRerunOf, data.Outgoing)
	if again.ID == first.ID || err != nil || len(rels) != 1 || rels[0].To != first.Ref() || !strings.Contains(again.Text("output"), "live-run-out") {
		t.Fatalf("rerun = %v, rerunOf %v (%v)", again.Fields, rels, err)
	}

	d.Keys(":")
	d.Type("!sleep 30")
	d.Keys("enter")
	long := selected()
	if a.Modal() != "" || long.Text("command") != "sleep 30" || long.Text("runStatus") != runs.StatusRunning {
		t.Fatalf("!sleep 30: modal %q, run %v, status %q", a.Modal(), long.Fields, a.Status())
	}
	d.Keys("x")
	if got := finish(long.ID); got.Text("runStatus") != runs.StatusCancelled {
		t.Fatalf("x did not cancel the run: %v (%s)", got.Fields, a.Status())
	}
	d.Keys("x")
	if !strings.Contains(a.Status(), "is not running") {
		t.Fatalf("x on a finished run: %q", a.Status())
	}
	// The owner's undo never takes a run back.
	d.Keys("u")
	if res, _ := a.Store.Query(data.Query{Kind: data.KindRun}); res.Total != 3 {
		t.Fatalf("u removed a run: %d left", res.Total)
	}
}

// Extensions: R finds and probes them, enter (or r) runs one of their
// commands as a run.
func TestExtensionsAreLive(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin") // keep the owner's real o-* tools out of reach
	dir := t.TempDir()
	script(t, dir, "o-git", `case "$1" in
--version) echo "o-git 1.2.3" ;;
capabilities) echo '{"summary":"git views","capabilities":["git.repo"],"commands":["status","log"]}' ;;
status) echo "clean tree" ;;
esac
`)
	a, d := liveApp(t, false, Options{Live: true, Extensions: ext.New(ext.Options{Tools: []string{"o-git", "o-none"}}), ExtensionDirs: []string{dir}})
	d.Keys("4", "R")
	a.WaitBackground()
	wake(d)
	rec, err := a.Store.GetByField(data.KindExtension, "name", "o-git")
	if err != nil || rec.Text("runStatus") != ext.StatusHealthy || !strings.Contains(a.Status(), "1 of 2 extensions found, 1 healthy") {
		t.Fatalf("after R: %v, %v, status %q", rec, err, a.Status())
	}
	if frame := d.Frame(); !strings.Contains(frame, "o-git") || !strings.Contains(frame, "healthy") || !strings.Contains(frame, "git views") {
		t.Fatalf("the found extension is not shown:\n%s", frame)
	}

	d.Keys("enter")
	if a.Modal() != "launch" || !strings.Contains(d.Frame(), "Run o-git") || !strings.Contains(d.Frame(), "status") {
		t.Fatalf("enter did not open the extension's run form: modal %q, focus %q\n%s", a.Modal(), a.Focus(), d.Frame())
	}
	d.Keys("ctrl+s")
	a.WaitBackground()
	wake(d)
	run, err := a.Store.GetByField(data.KindRun, "title", "o-git status")
	if err != nil {
		t.Fatalf("no run was recorded: %v (%s)", err, a.Status())
	}
	select {
	case <-a.rt.Runner.Done(run.ID):
	case <-time.After(liveDeadline):
		t.Fatal("the extension's run did not end")
	}
	run, _ = a.Store.Get(run.Ref())
	if run.Text("runStatus") != runs.StatusDone || !strings.Contains(run.Text("output"), "clean tree") {
		t.Fatalf("run = %v", run.Fields)
	}
	d.Keys("r")
	if a.Modal() != "launch" {
		t.Fatalf("r did not open the run form: %q", a.Status())
	}
}

// The Screen is the pane's real terminal: keys reach its program, ctrl+a [
// is Copy Mode over its history, ctrl+a t and ctrl+a v open side panes, and
// Close ends every session.
func TestScreenIsALiveTerminal(t *testing.T) {
	bin := t.TempDir()
	script(t, bin, "nvim", "echo fake-editor\nexec cat\n")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")
	a, d := liveApp(t, false, Options{Live: true})
	cwd := t.TempDir()
	pane, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "echoer", "harness": "cat", "command": "echo ready-pty; exec cat", "cwd": cwd}}, User)
	if err != nil {
		t.Fatal(err)
	}
	shows := func(id, line string) {
		t.Helper()
		eventually(t, id+" showing "+line, func() bool {
			f, ok := a.panes.Host.Snapshot(id)
			return ok && slices.ContainsFunc(f.Plain, func(l string) bool { return strings.TrimSpace(l) == line })
		})
		wake(d)
		if !strings.Contains(d.Frame(), line) {
			t.Fatalf("the Screen does not show %q:\n%s", line, d.Frame())
		}
	}

	wake(d) // the pane was written behind the app's back
	d.Keys("1", "l")
	if strings.Contains(d.Frame(), "No live terminal is attached") || !strings.Contains(d.Frame(), "Enter starts one") {
		t.Fatalf("before attaching:\n%s", d.Frame())
	}
	d.Keys("enter")
	if a.Mode() != ModeTerminal || !a.hasSession(pane.ID) {
		t.Fatalf("enter did not attach: mode %s, status %q", a.Mode(), a.Status())
	}
	shows(pane.ID, "ready-pty")
	if !strings.Contains(d.Frame(), "live") {
		t.Fatalf("no live badge:\n%s", d.Frame())
	}

	d.Type("hi")
	d.Keys("enter")
	shows(pane.ID, "hi")
	eventually(t, "cat's answer", func() bool {
		f, _ := a.panes.Host.Snapshot(pane.ID)
		return len(f.Plain) >= 3 && f.Plain[1] == "hi" && f.Plain[2] == "hi"
	})
	if after, _ := a.Store.Get(pane.Ref()); !after.Time("promptedAt").After(pane.Time("promptedAt")) {
		t.Fatal("the submitted line was not recorded as the owner's prompt")
	}
	d.Keys("q", "2") // typed, not run
	if a.Mode() != ModeTerminal || a.Space() != SpacePanes {
		t.Fatalf("q and 2 left the terminal: %s in %s", a.Mode(), a.Space())
	}
	shows(pane.ID, "q2")

	// Copy Mode: k moves up to cat's "hi", y yanks the line.
	d.Keys("ctrl+a", "[")
	if a.panes.Term.Mode(pane.ID) != ptyhost.ModeCopy || a.Mode() != ModeTerminal || !strings.Contains(d.Frame(), "copy") {
		t.Fatalf("ctrl+a [: %s, status %q\n%s", a.panes.Term.Mode(pane.ID), a.Status(), d.Frame())
	}
	d.Keys("k", "y")
	yanked := slices.ContainsFunc(a.Shell.LastEffects, func(e core.Effect) bool {
		c, ok := e.(core.CustomEffect)
		return ok && c.Kind == effectClipboard && c.Data == "hi"
	})
	if a.rt.live.clipboard != "hi" || !yanked || a.panes.Term.Mode(pane.ID) != ptyhost.ModePassthrough {
		t.Fatalf("yank: clipboard %q, effect %v, mode %s", a.rt.live.clipboard, a.Shell.LastEffects, a.panes.Term.Mode(pane.ID))
	}

	// ctrl+a t: a shell beside the pane, in its directory (split view, rail stays on parent).
	d.Keys("ctrl+a", "t")
	side, ok := a.termPane(a.State())
	if !ok || side.ID == pane.ID || side.Text("cwd") != cwd || side.Text("harness") != "sh" || !a.hasSession(side.ID) || a.Mode() != ModeTerminal {
		t.Fatalf("ctrl+a t: %v, status %q", side, a.Status())
	}
	d.Type("echo side-$((40+2))")
	d.Keys("enter")
	shows(side.ID, "side-42")

	// ctrl+a v: an editor beside the same parent pane.
	d.Keys("ctrl+a", "v")
	editor, ok := a.termPane(a.State())
	if !ok || editor.ID == side.ID || editor.Text("command") != "nvim" || !a.hasSession(editor.ID) {
		t.Fatalf("ctrl+a v: %v, status %q", editor, a.Status())
	}
	shows(editor.ID, "fake-editor")

	d.Keys("ctrl+a", "d")
	if a.Mode() != ModeNormal {
		t.Fatalf("ctrl+a d: %s", a.Mode())
	}
	a.Close()
	for _, id := range []string{pane.ID, side.ID, editor.ID} {
		if a.hasSession(id) {
			t.Fatalf("session %s survived Close", id)
		}
	}
}

// A live pane's program is told which pane and task it is; the Screen's
// title carries its PID; ctrl+a esc reaches it as a real escape; ctrl+a z
// hides everything but the pane.
func TestLivePaneEnvPIDEscapeZoom(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	a, d := liveApp(t, false, Options{Live: true})
	pane, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "envpane", "command": `echo "env=$MTM_PANE_ID/$MTM_TASK_ID"; exec cat -v`, "cwd": t.TempDir(),
		"task": core.Ref{Kind: data.KindTask, ID: "t77"}}}, User)
	if err != nil {
		t.Fatal(err)
	}
	has := func(line string) func() bool {
		return func() bool {
			f, ok := a.panes.Host.Snapshot(pane.ID)
			return ok && slices.ContainsFunc(f.Plain, func(l string) bool { return strings.TrimSpace(l) == line })
		}
	}
	wake(d)
	d.Keys("1", "enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("not attached: %s, status %q", a.Mode(), a.Status())
	}
	eventually(t, "the pane's environment", has("env="+pane.ID+"/t77"))
	wake(d)
	f, _ := a.panes.Host.Snapshot(pane.ID)
	if want := fmt.Sprintf("Screen · pid %d", f.PID); f.PID <= 0 || !strings.Contains(d.Frame(), want) {
		t.Fatalf("the Screen's title does not show %q:\n%s", want, d.Frame())
	}
	d.Keys("ctrl+a", "esc")
	d.Keys("enter")
	eventually(t, "the escape cat -v echoes", has("^["))
	if a.panes.escapes != 1 || a.Mode() != ModeTerminal {
		t.Fatalf("escapes %d, mode %s", a.panes.escapes, a.Mode())
	}
	d.Keys("ctrl+a", "z")
	if frame := d.Frame(); !a.panes.zoom || strings.Contains(frame, "Pages") || strings.Contains(frame, "envpane") || !strings.Contains(frame, "zoomed") || a.Focus() != siteScreen {
		t.Fatalf("zoom (focus %s):\n%s", a.Focus(), frame)
	}
	d.Keys("ctrl+a", "z", "ctrl+a", "d")
	if a.panes.zoom || a.Mode() != ModeNormal || !strings.Contains(d.Frame(), "Pages") {
		t.Fatalf("unzoom and detach: zoom %v mode %s", a.panes.zoom, a.Mode())
	}
}
