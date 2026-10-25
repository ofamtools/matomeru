package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
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
}

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
func (s *fakeStream) Release() error              { s.released, s.lease = true, [2]int{}; return nil }

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
	app := open(t)
	f := app.panesd.(*fakePanes)
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	press(t, app, "ctrl+j")
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

func TestPanesPageListsTheDaemonsPanes(t *testing.T) {
	app, _ := panesApp(t)
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	f := frame(app)
	for _, want := range []string{"panes (2)", "matomeru/claude", "ochazuke/shell", "p previews, enter attaches"} {
		if !strings.Contains(f, want) {
			t.Fatalf("frame lacks %q:\n%s", want, f)
		}
	}
	press(t, app, "ctrl+k")
	if app.r.Page != "work" || app.r.Focus != "ochazuke" {
		t.Fatalf("ctrl+k: page %s focus %s", app.r.Page, app.r.Focus)
	}
	press(t, app, "ctrl+j")
	if app.r.Page != panesPage || app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("ctrl+j: page %s focus %s", app.r.Page, app.r.Focus)
	}
}

func TestPreviewRunsInTheReplacingSlot(t *testing.T) {
	app, f := panesApp(t)
	cmd := key(t, app, "p")
	// The job belongs to the row; its slot is scoped to the list.
	out, err := app.r.Execute(ochazuke.Request{Verb: "list", Noun: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	var jobs []ochazuke.JobInfo
	for _, j := range out.([]ochazuke.JobInfo) {
		if j.Verb == "pane.preview" {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) != 1 || jobs[0].Slot != "preview" || jobs[0].Scope != paneListID || jobs[0].Owner != "pane:matomeru/w1" || jobs[0].State != ochazuke.JobRunning {
		t.Fatalf("preview jobs = %+v", jobs)
	}
	settle(app, cmd)
	if fr := frame(app); !strings.Contains(fr, "╭─ matomeru/claude · preview ─") || !strings.Contains(fr, "claude is thinking") {
		t.Fatalf("frame after preview:\n%s", fr)
	}
	if !reflect.DeepEqual(f.captured, []string{"w1"}) {
		t.Fatalf("captured = %v", f.captured)
	}
}

func TestANewerPreviewCancelsTheOlder(t *testing.T) {
	app, f := panesApp(t)
	first := key(t, app, "p")
	press(t, app, "j")
	second := key(t, app, "p")
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
	if !strings.Contains(fr, "╭━ matomeru/claude ━") || !strings.Contains(fr, "┃ live screen") {
		t.Fatalf("frame while attached:\n%s", fr)
	}
	// The lease is the panel's inner cells: the 120x24 frame's body is
	// 116x18, the list takes 1+5 rows and the panel the other 12 (no job
	// shows, so the framework's ticker takes no rows).
	if cw, ch := ochazuke.PanelContentSize(116, 12); s.lease != [2]int{cw, ch} {
		t.Fatalf("size lease = %v, want %dx%d", s.lease, cw, ch)
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
	press(t, app, "ctrl+a", "d")
	if app.r.Instances[paneTermID] != nil {
		t.Fatal("detach left the terminal")
	}
	if !s.released || s.lease != [2]int{} {
		t.Fatalf("lease not released: released %v lease %v", s.released, s.lease)
	}
	// Detach lands back on the row the attach came from.
	if app.r.Focus != "pane:matomeru/w1" || app.r.Mode.Name != "normal" {
		t.Fatalf("after detach: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
	if fr := frame(app); strings.Contains(fr, "━ matomeru/claude") {
		t.Fatalf("frame after detach:\n%s", fr)
	}
}

func TestStopsMoveBetweenThePaneListAndTheTerminal(t *testing.T) {
	app, f := panesApp(t)
	key(t, app, "enter")
	press(t, app, "ctrl+a", "d")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("after detach: focus %s", app.r.Focus)
	}
	key(t, app, "j")
	key(t, app, "enter")
	press(t, app, "ctrl+a", "d", "}")
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	if len(f.streams) != 2 || !f.streams[0].released || !f.streams[1].released {
		t.Fatalf("streams not released: %+v", f.streams)
	}
	// Attach again, detach by the framework verb, then } reaches the terminal stop.
	key(t, app, "enter")
	if _, err := app.r.Execute(ochazuke.Request{Verb: "detach", Noun: "terminal"}); err != nil {
		t.Fatal(err)
	}
	if app.r.Terminal(paneTermID) != nil {
		t.Fatal("terminal.detach did not remove the terminal")
	}
	press(t, app, "}")
	key(t, app, "enter")
	if err := app.paneList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, app, "}")
	if app.r.Focus != paneTermID {
		t.Fatalf("} from the list: focus %s", app.r.Focus)
	}
}

func TestKillConfirmsWithThePaneLabel(t *testing.T) {
	app, f := panesApp(t)
	key(t, app, "K")
	settle(app, key(t, app, "n"))
	if len(f.killed) != 0 || len(jobsOf(app, "pane.kill")) != 0 || app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("n: killed %v jobs %+v focus %s", f.killed, app.r.Jobs(), app.r.Focus)
	}
	key(t, app, "K")
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
	settle(app, key(t, app, "p"))
	if !strings.Contains(app.m.Status, "preview matomeru/claude: dial unix") {
		t.Fatalf("status = %q", app.m.Status)
	}
	if fr := frame(app); !strings.Contains(fr, "Error: preview matomeru/claude: dial unix") {
		t.Fatalf("footer lacks the error:\n%s", fr)
	}
	app.m.Status = ""
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	app.m.Status = ""
	fr := frame(app)
	if !strings.Contains(fr, "daemon not reachable") || !strings.Contains(fr, "no such file") || strings.Contains(fr, "ochazuke/shell") || app.r.Instances["pane:matomeru/w1"] != nil {
		t.Fatalf("frame with no daemon:\n%s", fr)
	}
	if app.r.Focus != paneListID {
		t.Fatalf("focus = %s", app.r.Focus)
	}
	press(t, app, "ctrl+k", "j")
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
		{pane{Session: "9332c352-fd31-49a0-bb93-6b18ed187d8d", Pane: daemon.Pane{ID: "w1", Name: "Terminal 7984e10b"}}, "9332c352/Terminal 7984e10b"},
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

// The panes list is a table of the pane noun's columns: pane rows fill
// every cell (the cwd under home as ~), session rows the pane and project.
func TestPaneListIsATable(t *testing.T) {
	app := open(t)
	app.setPanes([]pane{
		{Session: "work", SessionID: "s1", Pane: daemon.Pane{ID: "w1", Name: "claude", AgentState: "idle", Cwd: "/home/test/proj/x"}},
	}, nil)
	if got := app.paneList.Columns(); len(got) != 4 || got[0].Title != "pane" || got[3].Title != "cwd" {
		t.Fatalf("columns %+v", got)
	}
	items := app.paneList.Items()
	if len(items) != 2 || items[0].Noun != "session" || !slices.Equal(items[0].Cells, []string{"session work", "unassociated", "", ""}) {
		t.Fatalf("session row %+v", items)
	}
	if !slices.Equal(items[1].Cells, []string{"work/claude", "unassociated", "idle", "~/proj/x"}) {
		t.Fatalf("pane row %+v", items[1])
	}
	app.m.Width, app.m.Height = 80, 30
	if _, err := app.r.Execute(ochazuke.Request{Verb: "open", Noun: "page", Args: ochazuke.Arguments{"id": panesPage}}); err != nil {
		t.Fatal(err)
	}
	frame := ansi.Strip(app.m.View().Content)
	for _, want := range []string{"pane", "project", "state", "cwd", "work/claude", "~/proj/x"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame lacks %q:\n%s", want, frame)
		}
	}
}
