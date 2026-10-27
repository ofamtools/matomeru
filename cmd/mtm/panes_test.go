package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// fakePanes is the daemon of the panes page: two panes, a screen per
// window, and the streams it opened. err fails every call.
type fakePanes struct {
	err      error
	panes    []pane
	screens  map[string]string
	captured []string
	killed   []string
	streams  []*fakeStream
	exited   map[string]bool // windows whose process ended but stay listed
	// backs are the windows' scrollback captures (copy mode reads them).
	backs map[string]scrollback
	// transcript replaces the agents' default transcript when set.
	transcript string
	// fits are the window sizes FitWindow set, "window colsxrows".
	fits []string
}

// FitWindow records the size a new side window was given.
func (f *fakePanes) FitWindow(_ context.Context, p pane, cols, rows int) error {
	f.fits = append(f.fits, fmt.Sprintf("%s %dx%d", p.ID, cols, rows))
	return f.err
}

// Exited is the fake's exit probe: the windows marked in exited.
func (f *fakePanes) Exited(_ context.Context, p pane) (bool, error) {
	return f.exited[p.ID], nil
}

func newFakePanes() *fakePanes {
	return &fakePanes{
		panes: []pane{
			{Session: "matomeru", Pane: daemon.Pane{ID: "w1", Name: "claude"}},
			{Session: "ochazuke", Pane: daemon.Pane{ID: "w2", Name: "shell"}},
		},
		screens: map[string]string{"w1": "claude is thinking", "w2": "$ go test ./..."},
	}
}

func (f *fakePanes) ListPanes(context.Context) ([]pane, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]pane(nil), f.panes...), nil
}

func (f *fakePanes) CapturePane(_ context.Context, p pane) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.captured = append(f.captured, p.ID)
	return f.screens[p.ID], nil
}

func (f *fakePanes) KillPane(_ context.Context, p pane) error {
	if f.err != nil {
		return f.err
	}
	f.killed = append(f.killed, p.ID)
	for i, q := range f.panes {
		if q.ID == p.ID {
			f.panes = append(f.panes[:i], f.panes[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakePanes) KillSession(_ context.Context, session string) error {
	if f.err != nil {
		return f.err
	}
	f.killed = append(f.killed, "session:"+session)
	var keep []pane
	for _, q := range f.panes {
		if q.Session != session {
			keep = append(keep, q)
		}
	}
	f.panes = keep
	return nil
}

func (f *fakePanes) Activity(_ context.Context, p pane) (string, error) {
	return "12:00:00  tool  Read  main.go", f.err
}

func (f *fakePanes) Transcript(_ context.Context, p pane) (string, error) {
	if f.transcript != "" {
		return f.transcript, f.err
	}
	return "user\nfix the tests\n\nassistant\ndone", f.err
}

func (f *fakePanes) OpenStream(p pane) paneStream {
	s := &fakeStream{pane: p, frames: make(chan ochazuke.TerminalFrame, 4)}
	f.streams = append(f.streams, s)
	return s
}

// fakeStream records keys, the lease (the last Resize) and its release.
type fakeStream struct {
	pane     pane
	frames   chan ochazuke.TerminalFrame
	sent     [][]string
	lease    [2]int
	released bool
	keep     bool // hold: Release parks, as liveStream does
	parked   bool
	state    string // connState: "", reconnecting or gone
}

func (s *fakeStream) connState() string { return s.state }

func (s *fakeStream) Next(ctx context.Context) (ochazuke.TerminalFrame, error) {
	select {
	case f := <-s.frames:
		return f, nil
	case <-ctx.Done():
		return ochazuke.TerminalFrame{}, ctx.Err()
	}
}
func (s *fakeStream) Send(keys ...string) error   { s.sent = append(s.sent, keys); return nil }
func (s *fakeStream) Paste(string) error          { return nil }
func (s *fakeStream) Resize(cols, rows int) error { s.lease = [2]int{cols, rows}; return nil }
func (s *fakeStream) Release() error {
	if s.keep {
		s.parked = true
		return nil
	}
	return s.drop()
}
func (s *fakeStream) hold()       { s.keep = true }
func (s *fakeStream) drop() error { s.released, s.lease = true, [2]int{}; return nil }

// runOnce runs the commands an update returned and feeds their messages
// back, without running what those deliveries schedule (a terminal's next
// read blocks until the pane changes).
func runOnce(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := now(cmd).(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runOnce(a, c)
		}
	case nil:
	default:
		a.m.Update(msg)
	}
}

// panesApp opens the app, loads the fake's panes and opens the panes page.
func panesApp(t *testing.T) (*App, *fakePanes) {
	t.Helper()
	app := openWith(t, Options{Data: seeded(t)}) // mtm opens on Panes
	f := app.panesd.(*fakePanes)
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if app.r.Page != panesPage {
		t.Fatalf("page = %s", app.r.Page)
	}
	return app, f
}

// jobsOf lists the jobs of one verb.
func jobsOf(a *App, verb string) []ochazuke.JobInfo {
	var out []ochazuke.JobInfo
	for _, j := range a.r.Jobs() {
		if j.Verb == verb {
			out = append(out, j)
		}
	}
	return out
}

// follow delivers the follow timer for the current selection, as the
// pause after a move does: the detail loads the selected row's tab.
func follow(a *App) tea.Cmd {
	_, cmd := a.Update(followDue{seq: a.pv.seq})
	return cmd
}

func TestPanesPageListsTheDaemonsPanes(t *testing.T) {
	app, _ := panesApp(t)
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	f := frame(app)
	for _, want := range []string{"panes (2)", "unassociated (2)", "matomeru (1)", "claude", "ochazuke (1)", "shell", "[Screen] Activity Transcript Turns"} {
		if !strings.Contains(f, want) {
			t.Fatalf("frame lacks %q:\n%s", want, f)
		}
	}
	press(t, app, "ctrl+j")
	if app.r.Page != "work" || app.r.Focus != "ochazuke" {
		t.Fatalf("ctrl+j: page %s focus %s", app.r.Page, app.r.Focus)
	}
	press(t, app, "ctrl+k")
	if app.r.Page != panesPage || app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("ctrl+k: page %s focus %s", app.r.Page, app.r.Focus)
	}
}

// The Screen tab follows the selection: after the pause, pane.preview runs
// in the list's replacing slot with no key pressed.
func TestPreviewRunsInTheReplacingSlot(t *testing.T) {
	app, f := panesApp(t)
	cmd := follow(app)
	var jobs []ochazuke.JobInfo
	for _, j := range app.r.Jobs() {
		if j.Verb == "pane.preview" {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) != 1 || jobs[0].Slot != "preview" || jobs[0].Scope != paneListID || jobs[0].Owner != "pane:matomeru/w1" || jobs[0].State != ochazuke.JobRunning {
		t.Fatalf("preview jobs = %+v", jobs)
	}
	settle(app, cmd)
	if fr := frame(app); !strings.Contains(fr, "╭─ matomeru/claude ─") || !strings.Contains(fr, "│ shell") || !strings.Contains(fr, "claude is thinking") {
		t.Fatalf("frame after preview:\n%s", fr)
	}
	if !reflect.DeepEqual(f.captured, []string{"w1"}) {
		t.Fatalf("captured = %v", f.captured)
	}
	// A stale timer (the selection moved on) loads nothing.
	press(t, app, "j", "j")
	stale := app.pv.seq - 1
	app.Update(followDue{seq: stale})
	if len(jobsOf(app, "pane.preview")) != 1 {
		t.Fatalf("a stale follow timer previewed: %+v", jobsOf(app, "pane.preview"))
	}
}

func TestANewerPreviewCancelsTheOlder(t *testing.T) {
	app, f := panesApp(t)
	first := follow(app)
	press(t, app, "j", "j")
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	second := follow(app)
	var running []ochazuke.JobInfo
	for _, j := range jobsOf(app, "pane.preview") {
		if j.State == ochazuke.JobRunning {
			running = append(running, j)
		}
	}
	// Two rows asked; the list's slot keeps only the newest running.
	if jobs := jobsOf(app, "pane.preview"); len(running) != 1 || len(jobs) != 2 || jobs[0].State != ochazuke.JobCancelled ||
		jobs[0].Owner != "pane:matomeru/w1" || running[0].Owner != "pane:ochazuke/w2" || running[0].Scope != paneListID {
		t.Fatalf("preview jobs = %+v", jobs)
	}
	settle(app, second)
	settle(app, first) // the cancelled capture lands late and is dropped
	if len(f.captured) != 2 || app.preview.Pane != "pane:ochazuke/w2" {
		t.Fatalf("captured %v, preview %+v", f.captured, app.preview)
	}
	if fr := frame(app); !strings.Contains(fr, "$ go test ./...") || strings.Contains(fr, "claude is thinking") {
		t.Fatalf("frame:\n%s", fr)
	}
}

func TestAttachAndDetachThroughTheLeader(t *testing.T) {
	app, f := panesApp(t)
	cmd := key(t, app, "enter")
	term := app.r.Terminal(paneTermID)
	if term == nil || app.r.Instances[paneTermID].Parent != panesPage || app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" {
		t.Fatalf("after enter: terminal %v focus %s mode %s", term, app.r.Focus, app.r.Mode.Name)
	}
	if len(f.streams) != 1 || f.streams[0].pane.ID != "w1" {
		t.Fatalf("streams = %+v", f.streams)
	}
	s := f.streams[0]
	s.frames <- ochazuke.TerminalFrame{Lines: []string{"live screen"}, Hidden: true}
	runOnce(app, cmd)
	fr := frame(app)
	if !strings.Contains(fr, "╭━ matomeru/claude ━") || !strings.Contains(fr, "┃ shell") || !strings.Contains(fr, "┃ live screen") || !strings.Contains(fr, "live · ctrl+a ? keys") {
		t.Fatalf("frame while attached:\n%s", fr)
	}
	// The lease is the detail panel's inner cells less the header and tab
	// lines: the 120x24 frame's body is 116x18, the tree takes about 30% and
	// a gap, the panel the rest (no job shows, so the ticker takes no rows).
	if cw, ch := ochazuke.PanelContentSize(116-treeWidth(116)-1, 18); s.lease != [2]int{cw, ch - 2} {
		t.Fatalf("size lease = %v, want %dx%d", s.lease, cw, ch-2)
	}
	// Keys go to the pane; } and ctrl+j are the pane's keys here.
	press(t, app, "x", "}")
	if !reflect.DeepEqual(s.sent, [][]string{{"x"}, {"}"}}) {
		t.Fatalf("sent = %v", s.sent)
	}
	// ctrl+c reaches the pane while attached; it does not quit the app.
	press(t, app, "ctrl+c")
	if !reflect.DeepEqual(s.sent[len(s.sent)-1], []string{"ctrl+c"}) || app.r.Quit {
		t.Fatalf("ctrl+c: sent = %v quit %v", s.sent, app.r.Quit)
	}
	// ctrl+a ctrl+a is one literal ctrl+a.
	press(t, app, "ctrl+a", "ctrl+a")
	if !reflect.DeepEqual(s.sent[len(s.sent)-1], []string{"ctrl+a"}) {
		t.Fatalf("ctrl+a ctrl+a: sent = %v", s.sent)
	}
	press(t, app, "ctrl+a", "d")
	if app.r.Instances[paneTermID] != nil {
		t.Fatal("detach left the terminal")
	}
	// Detach parks the stream: the pane keeps its size while its preview
	// is on screen, so the preview draws the lines the live view drew.
	if s.released || !s.parked || s.lease == [2]int{} {
		t.Fatalf("detach should park the stream: released %v parked %v lease %v", s.released, s.parked, s.lease)
	}
	// Detach lands back on the row the attach came from.
	if app.r.Focus != "pane:matomeru/w1" || app.r.Mode.Name != "normal" {
		t.Fatalf("after detach: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
	if fr := frame(app); strings.Contains(fr, "╭━ matomeru/claude") {
		t.Fatalf("frame after detach:\n%s", fr)
	}
}

// ctrl+a z zooms the attached pane to the page body and back; every attach
// starts unzoomed. ctrl+a j and k attach the next and previous pane.
func TestLeaderZoomAndNextPane(t *testing.T) {
	app, f := panesApp(t)
	key(t, app, "enter")
	if app.pv.zoom {
		t.Fatal("attach started zoomed")
	}
	press(t, app, "ctrl+a", "z")
	if !app.pv.zoom || !strings.Contains(frame(app), "zoomed · ctrl+a z restores") || strings.Contains(frame(app), "panes (2)") {
		t.Fatalf("zoom: %v\n%s", app.pv.zoom, frame(app))
	}
	press(t, app, "ctrl+a", "j")
	if app.attached.ID != "w2" || app.r.Focus != paneTermID || !app.pv.zoom || len(f.streams) != 2 || !f.streams[0].released {
		t.Fatalf("ctrl+a j: attached %s focus %s zoom %v streams %d", app.attached.ID, app.r.Focus, app.pv.zoom, len(f.streams))
	}
	press(t, app, "ctrl+a", "j") // the last pane: a quiet edge
	if app.attached.ID != "w2" {
		t.Fatalf("ctrl+a j at the end: attached %s", app.attached.ID)
	}
	press(t, app, "ctrl+a", "z")
	if app.pv.zoom || !strings.Contains(frame(app), "panes (2)") {
		t.Fatalf("unzoom:\n%s", frame(app))
	}
	press(t, app, "ctrl+a", "k", "ctrl+a", "d")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("detach after ctrl+a k: focus %s", app.r.Focus)
	}
	key(t, app, "enter")
	if app.pv.zoom {
		t.Fatal("a new attach is zoomed")
	}
	if hints := ochazuke.BindingHints(app.r, app.r.Focus); !strings.Contains(hints, "z zoom") {
		t.Fatalf("leader hint: %q", hints)
	}
}

func TestStopsMoveBetweenThePaneListAndTheTerminal(t *testing.T) {
	app, f := panesApp(t)
	key(t, app, "enter")
	press(t, app, "ctrl+a", "d")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("after detach: focus %s", app.r.Focus)
	}
	press(t, app, "j", "j")
	key(t, app, "enter")
	press(t, app, "ctrl+a", "d")
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	// The first pane's stream was dropped when the selection left its
	// row; the second stays parked under its own preview.
	if len(f.streams) != 2 || !f.streams[0].released || !f.streams[1].parked || f.streams[1].released {
		t.Fatalf("streams: first released %v, second parked %v released %v", f.streams[0].released, f.streams[1].parked, f.streams[1].released)
	}
	// Attach again, focus the list, then } reaches the terminal stop. The
	// walk follows the last frame's drawing (the hidden Turns tree is not
	// in it), so a frame is drawn first, as the TUI always does.
	key(t, app, "enter")
	if err := app.paneList.Focus(); err != nil {
		t.Fatal(err)
	}
	frame(app)
	press(t, app, "}")
	if app.r.Focus != paneTermID {
		t.Fatalf("} from the list: focus %s", app.r.Focus)
	}
}

func TestKillConfirmsWithThePaneLabel(t *testing.T) {
	app, f := panesApp(t)
	press(t, app, "d", "d")
	settle(app, key(t, app, "n"))
	if len(f.killed) != 0 || len(jobsOf(app, "pane.kill")) != 0 || app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("n: killed %v jobs %+v focus %s", f.killed, app.r.Jobs(), app.r.Focus)
	}
	press(t, app, "d", "d")
	settle(app, key(t, app, "y"))
	if !reflect.DeepEqual(f.killed, []string{"w1"}) {
		t.Fatalf("killed = %v", f.killed)
	}
	if app.r.Instances["pane:matomeru/w1"] != nil || app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("after kill: focus %s", app.r.Focus)
	}
	// The kill completed on its own row, which its completion removed.
	if jobs := jobsOf(app, "pane.kill"); len(jobs) != 1 || jobs[0].Owner != "pane:matomeru/w1" || jobs[0].State != ochazuke.JobDone {
		t.Fatalf("kill jobs = %+v", jobs)
	}
	if fr := frame(app); !strings.Contains(fr, "panes (1)") {
		t.Fatalf("frame after kill:\n%s", fr)
	}
}

func TestAnUnreachableDaemonShowsAndTheAppKeepsWorking(t *testing.T) {
	app, f := panesApp(t)
	f.err = errors.New("dial unix tuios.sock: connect: no such file or directory")
	settle(app, follow(app))
	if !strings.Contains(app.m.Status, "preview matomeru/claude: dial unix") {
		t.Fatalf("status = %q", app.m.Status)
	}
	app.m.Status = ""
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	app.m.Status = ""
	fr := frame(app)
	// Two short lines and the remedy; the whole error is a notice.
	if !strings.Contains(fr, "is not running") || !strings.Contains(fr, "e starts it (daemon.ensure)") || strings.Contains(fr, "ochazuke/shell") || app.r.Instances["pane:matomeru/w1"] != nil {
		t.Fatalf("frame with no daemon:\n%s", fr)
	}
	if len(app.m.Notices) != 1 || !strings.Contains(app.m.Notices[0], "no such file") {
		t.Fatalf("notices = %q", app.m.Notices)
	}
	if app.r.Focus != paneListID {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	// e runs daemon.ensure from the empty list, and the panes come back.
	f.err = nil
	settle(app, key(t, app, "e"))
	if app.paneErr != "" || app.r.Instances["pane:matomeru/w1"] == nil {
		t.Fatalf("after e: err %q", app.paneErr)
	}
	press(t, app, "ctrl+j", "j")
	if app.r.Page != "work" || app.r.Focus != "matomeru" {
		t.Fatalf("page %s focus %s", app.r.Page, app.r.Focus)
	}
}

func TestDaemonKeyNames(t *testing.T) {
	for in, want := range map[string]string{"esc": "Escape", " ": "Space", "space": "Space", ",": "Comma", "ctrl+c": "ctrl+c", "x": "x", "enter": "enter"} {
		if got := daemonKey(in); got != want {
			t.Errorf("daemonKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPaneLabelShortensUUIDSessions(t *testing.T) {
	for _, c := range []struct {
		p    pane
		want string
	}{
		{pane{Session: "9332c352-fd31-49a0-bb93-6b18ed187d8d", Pane: daemon.Pane{ID: "w1", Name: "Terminal 7984e10b"}}, "9332c352/shell"},
		{pane{Session: "matomeru", Pane: daemon.Pane{ID: "w1", Name: "claude"}}, "matomeru/claude"},
		{pane{Session: "9332c352-fd31-49a0-bb93", Pane: daemon.Pane{ID: "w1", Name: "x"}}, "9332c352-fd31-49a0-bb93/x"},
	} {
		if got := c.p.label(); got != c.want {
			t.Errorf("label(%s) = %q, want %q", c.p.Session, got, c.want)
		}
	}
	p := pane{Session: "9332c352-fd31-49a0-bb93-6b18ed187d8d", Pane: daemon.Pane{ID: "w1"}}
	if got := p.rowID(); got != "pane:9332c352-fd31-49a0-bb93-6b18ed187d8d/w1" {
		t.Errorf("rowID = %q", got)
	}
}

// The panes tree is a lean table: the pane (indented, its state glyph
// first) and its short state; the harness, model, cwd under home as ~,
// state and age are the detail's header line. One window is one row: an
// agent's pane is the agent's row; a shell is a shell row.
func TestPaneTreeIsATable(t *testing.T) {
	app := open(t)
	if err := app.work.PutPaneRecipe(work.PaneRecipe{Window: "w1", Harness: "claude", Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	app.setPanes([]pane{
		{Session: "work", SessionID: "s1", Harness: "claude", Pane: daemon.Pane{ID: "w1", Name: "Terminal 1a2b3c4d", AgentState: "idle", Cwd: "/home/test/proj/x"}},
		{Session: "work", SessionID: "s1", Pane: daemon.Pane{ID: "w2", Name: "Terminal 5e6f7a8b", AgentState: "none", Cwd: "/home/test"}},
	}, nil)
	if got := app.paneList.Columns(); len(got) != 2 || got[0].Title != "pane" || got[1].Title != "state" {
		t.Fatalf("columns %+v", got)
	}
	items := app.paneList.Items()
	if len(items) != 4 || items[0].Noun != "pane-project" || items[1].Noun != "session" || items[2].Noun != "pane" || items[3].Noun != "pane" {
		t.Fatalf("rows %+v", items)
	}
	if !slices.Equal(items[2].Cells, []string{"      ○ claude", "idle", ""}) || !slices.Equal(items[3].Cells, []string{"        shell", "shell", ""}) {
		t.Fatalf("pane rows %+v / %+v", items[2].Cells, items[3].Cells)
	}
	for _, width := range []int{80, 120, 160} {
		app.m.Width, app.m.Height = width, 30
		if _, err := app.r.Execute(ochazuke.Request{Verb: "open", Noun: "page", Args: ochazuke.Arguments{"id": panesPage}}); err != nil {
			t.Fatal(err)
		}
		frame := ansi.Strip(app.m.View().Content)
		for _, want := range []string{"pane", "state", "○ claude", "unassociated (2)", "work (2)", "╭─ work/claude ─", "│ claude · opus · ~/proj/x · idle"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("%d columns: frame lacks %q:\n%s", width, want, frame)
			}
		}
		if strings.Count(frame, "~/proj/x") != 1 {
			t.Fatalf("%d columns: the cwd is still a tree column:\n%s", width, frame)
		}
		// From 100 columns the tree takes about 30% beside the detail.
		lines := strings.Split(frame, "\n")
		side := slices.ContainsFunc(lines, func(l string) bool {
			return strings.Contains(l, "panes (2)") && strings.Contains(l, "╭─ work/claude")
		})
		if side != (width >= sideBySide) {
			t.Fatalf("%d columns: side by side %v:\n%s", width, side, frame)
		}
	}
}

// The header names the owner only when the pin leaves the pane out.
func TestPaneHeaderNamesTheOwnerOutsideThePin(t *testing.T) {
	app := open(t)
	p := pane{Session: "s", Harness: "claude", Pane: daemon.Pane{ID: "w1", AgentState: "working", Cwd: "/tmp/x"}}
	app.pv.view.owners = map[string]string{"w1": "other"}
	if got := app.paneHeader(p); got != "claude · /tmp/x · working" {
		t.Fatalf("unpinned header %q", got)
	}
	app.pin = "mine"
	if got := app.paneHeader(p); got != "claude · /tmp/x · working · owner other" {
		t.Fatalf("pinned header %q", got)
	}
}

// An agent's header doubles as its status line: tuios metadata first, the
// launch recipe's model and flags behind it, nothing for a missing fact.
func TestPaneHeaderIsTheAgentStatusLine(t *testing.T) {
	app := open(t)
	p := pane{Session: "s", Harness: "claude", Model: "sonnet", LaunchFlags: []string{"--model", "sonnet", "--effort=high", "--permission-mode", "plan"},
		Pane: daemon.Pane{ID: "w1", AgentState: "working", Cwd: "/tmp/x"}}
	if got := app.paneHeader(p); got != "claude · sonnet · effort high · perms plan · /tmp/x · working" {
		t.Fatalf("recipe header %q", got)
	}
	p.Meta = map[string]string{"model": "opus", "context": "42%", "cost": "$0.12"}
	if got := app.paneHeader(p); got != "claude · opus · effort high · perms plan · ctx 42% · $0.12 · /tmp/x · working" {
		t.Fatalf("meta header %q", got)
	}
	shell := pane{Session: "s", Meta: map[string]string{"summary": "x"}, Pane: daemon.Pane{ID: "w2", Cwd: "/tmp/y"}}
	if got := app.paneHeader(shell); got != "shell · /tmp/y" {
		t.Fatalf("shell header %q", got)
	}
}

// Searching the pane tree and pressing enter chooses the highlighted pane: the
// filter clears, every row shows again and the selection stays on the
// chosen pane, in normal mode (the tree is added ClearFilterOnEnter).
func TestPaneSearchEnterChoosesAndClears(t *testing.T) {
	app, _ := panesApp(t)
	rows := app.paneList.Len()
	press(t, app, "/")
	typeText(t, app, "shell") // both panes' harness is shell; the selection stays on claude
	press(t, app, "down")
	if app.paneList.Filter() != "shell" || app.paneList.Shown() != 2 || app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("search: query %q shown %d of %d focus %s", app.paneList.Filter(), app.paneList.Shown(), rows, app.r.Focus)
	}
	press(t, app, "enter")
	if app.r.Mode.Name != "normal" || app.paneList.Filter() != "" || app.paneList.Shown() != rows {
		t.Fatalf("enter: mode %s query %q shown %d of %d", app.r.Mode.Name, app.paneList.Filter(), app.paneList.Shown(), rows)
	}
	if sel, ok := app.paneList.Selected(); !ok || sel.ID != "pane:ochazuke/w2" || app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("enter: selected %v focus %s", sel.ID, app.r.Focus)
	}
	if f := frame(app); !strings.Contains(f, "claude") || strings.Contains(f, "2 of 5") {
		t.Fatalf("every pane shows again and the query line is gone:\n%s", f)
	}
}
