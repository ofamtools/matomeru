package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/compose"
	"ofam.dev/matomeru/internal/work"
)

// press sends named keys the way Bubble Tea v2 delivers them, failing on
// the first error the model shows.
func press(t *testing.T, a *App, keys ...string) {
	t.Helper()
	for _, key := range keys {
		a.Update(keyMsg(key))
		if a.m.Status != "" {
			t.Fatalf("key %q: %s", key, a.m.Status)
		}
	}
}

// keyMsg is one named key as Bubble Tea v2 delivers it.
func keyMsg(key string) tea.KeyPressMsg {
	codes := map[string]rune{"enter": tea.KeyEnter, "esc": tea.KeyEscape, "up": tea.KeyUp, "down": tea.KeyDown, "tab": tea.KeyTab, "right": tea.KeyRight, "left": tea.KeyLeft}
	switch {
	case codes[key] != 0:
		return tea.KeyPressMsg{Code: codes[key]}
	case strings.HasPrefix(key, "ctrl+"):
		return tea.KeyPressMsg{Code: rune(key[5]), Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(key[0]), Text: key}
}

// typeText types text rune by rune.
func typeText(t *testing.T, a *App, text string) {
	t.Helper()
	for _, r := range text {
		press(t, a, string(r))
	}
}

// pauseRecord delivers the record editor's latest pause timer, as the typing
// pause would: the change typed since is written.
func pauseRecord(t *testing.T, a *App) {
	t.Helper()
	if a.editor == nil {
		t.Fatal("no record editor open")
	}
	for seq := uint64(0); seq < 1<<16; seq++ {
		tick := ochazuke.RecordDebounceMsg{Editor: recordEditorID, Seq: seq}
		if a.editor.Form.DebounceCurrent(tick) {
			a.Update(tick)
			return
		}
	}
	t.Fatal("no pause timer is current")
}

// stored reads a task back from the database file.
func stored(t *testing.T, a *App, id string) work.Task {
	t.Helper()
	task, err := a.work.Task(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// open builds the app on a fresh database seeded with two projects.
func open(t *testing.T) *App {
	t.Helper()
	return openAt(t, seeded(t))
}

// seeded copies a closed, fully migrated database; every caller still owns
// its file and exercises the real store, including persistence and reopen.
func seeded(t *testing.T) string {
	t.Helper()
	data, err := seededTemplate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mtm.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

var seededTemplate = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "mtm-seed")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "mtm.db")
	app, err := New(Options{Data: path, Socket: filepath.Join(dir, "absent.sock"), lookupEnv: func(key string) (string, bool) {
		value, ok := map[string]string{"HOME": dir, "MTM_O_PROJ": "none"}[key]
		return value, ok
	}})
	if err != nil {
		return nil, err
	}
	defer app.Close()
	for i, name := range []string{"ochazuke", "matomeru"} {
		if err := app.work.PutProject(work.Project{ID: name, Name: name, Created: int64(i + 1), Theme: work.Theme{Glyph: "◆"}}); err != nil {
			return nil, err
		}
	}
	for i, title := range []string{"write the brief", "lift the store"} {
		if err := app.work.PutTask(work.Task{ID: "t" + string(rune('1'+i)), ProjectID: "matomeru", Title: title, Position: int64(i + 1), Status: work.StatusTodo}); err != nil {
			return nil, err
		}
	}
	// Closing the last SQLite connection checkpoints its WAL into the file.
	if err := app.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// openAt builds the app on an existing database file and goes to the Work
// page, its first project row focused: most tests start among the seeded
// projects. mtm itself opens on Panes; openWith keeps that landing.
func openAt(t *testing.T, path string) *App {
	t.Helper()
	app := openWith(t, Options{Data: path})
	toWork(t, app)
	return app
}

// toWork opens the Work page and focuses its selected project row.
func toWork(t *testing.T, app *App) {
	t.Helper()
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := app.projectList.Focus(); err != nil {
		t.Fatal(err)
	}
}

// openWith builds the app with opts and the test fakes.
func openWith(t *testing.T, opts Options) *App {
	t.Helper()
	if opts.lookupEnv == nil {
		env := map[string]string{"HOME": "/home/test", "MTM_O_PROJ": "none"}
		opts.lookupEnv = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	}
	// Pane fakes do not replace the raw client used by event subscriptions.
	// Never let a default fixture inherit the owner's daemon socket.
	// Reopens of the same database must retain the same server identity.
	if opts.Socket == "" && opts.Server == "" {
		opts.Socket = filepath.Join(filepath.Dir(opts.Data), "absent.sock")
	}
	// A fixture has no project bases until a directory test declares them.
	// In particular, do not stat the synthetic HOME (macOS automounts /home).
	if err := os.MkdirAll(opts.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	bases, err := os.OpenFile(filepath.Join(opts.ConfigDir(), "dirs.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, writeErr := bases.WriteString(`{"project_base_dirs":[]}`)
		if err := errors.Join(writeErr, bases.Close()); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.Close()
		// testing retains completed parallel tests and their cleanup backing
		// arrays until the parent ends. Do not retain every app with them.
		app = nil
	})
	// Most tests read the Notes, Review and Context rows without opening
	// those pages: load them as the TUI would on first need. TestPagesLoadLazily
	// calls New itself to see them unloaded.
	for _, l := range []*pageLoad{&app.notesLoad, &app.reviewsLoad, &app.cx.load, &app.inboxLoad} {
		if err := l.ensure(); err != nil {
			t.Fatal(err)
		}
	}
	clock := &fakeClock{t: time.Unix(1_790_000_000, 0)}
	app.m.Now = clock.now
	app.home = func() (string, error) { return "/home/test", nil }
	// Launches in tests carry no per-launch hook settings (agent_hooks_test.go covers them).
	app.executable = func() (string, error) { return "", errors.New("a test binary is not mtm") }
	// No test launches an application: the runtime's opener records instead.
	app.r.SetOpener(ochazuke.Opener{GOOS: "darwin", Start: func(string, ...string) error { return nil }})
	// No test reaches the real daemon.
	app.panesd = newFakePanes()
	app.renamer = &fakeRenamer{panes: app.panesd.(*fakePanes)} // never the real daemon
	app.pv.after = nil                                         // no timers: tests deliver followDue and refreshDue themselves
	app.ensure = func(context.Context) error { return nil }
	// No test reads the real machine: the status segments (cpu, ram,
	// disk) start off, so frames do not depend on live readings or on
	// when a refresh lands; segments_test.go covers them.
	for _, s := range app.r.Segments() {
		if err := app.r.AddSegment(ochazuke.Segment{ID: s.ID, Enabled: ochazuke.On(false)}); err != nil {
			t.Fatal(err)
		}
	}
	app.sleep = func(context.Context, time.Duration) error { return nil }
	app.journalSrc = nil             // the journal reader never follows the real daemon
	app.claudeProjects = t.TempDir() // no test reads the owner's Claude session logs
	app.ompSessions = t.TempDir()    // nor omp's
	app.codexSessions = t.TempDir()  // nor Codex's
	app.locator = nil                // nor the harnesses' session records
	// The prompt box's completions read a home of the test's, never o-ai.
	home := t.TempDir()
	app.pb.sources = compose.NewSources(compose.Env{Home: home, ClaudeDir: filepath.Join(home, ".claude"), OmpDir: filepath.Join(home, ".omp", "agent")})
	// Turns is the detail's default tab (TestTurnsIsTheDefaultTab); the
	// tests of the Screen tab, written when it was the default, start there.
	app.pv.tab = tabScreen
	// So they open every pane on the Screen tab (agent.view terminal),
	// and a tab a test switches to stays while the selection moves;
	// pane_view_test.go covers the views.
	if err := app.r.Settings().Set(settingAgentView, "terminal"); err != nil {
		t.Fatal(err)
	}
	app.pv.viewApplied = "terminal"
	app.runs.after = nil // no redraw timers
	// No test runs a real schedule: no timers, and a command a test did
	// not fake fails at once.
	app.sched.after = nil
	app.sched.runner.Exec = func(context.Context, []string, []string) ([]byte, error) {
		return nil, errors.New("tests run no real commands")
	}
	return app
}

func TestBindingRowsAreAtomic(t *testing.T) {
	t.Parallel()
	if _, err := open(t).r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

// TestNoActionAliasClashes: every alias mtm and ochazuke register lands
// (ochazuke's bare help and h, the docs noun, mtm's own aliases).
func TestNoActionAliasClashes(t *testing.T) {
	t.Parallel()
	if problems := open(t).r.ActionAliasProblems(); len(problems) > 0 {
		t.Fatalf("aliases dropped: %v", problems)
	}
}

func TestWorkPageListsStoredProjects(t *testing.T) {
	t.Parallel()
	app := open(t)
	app.m.Width, app.m.Height = 80, 24
	frame := ansi.Strip(frameView(app).Content)
	for _, want := range []string{"projects (3)", "◆ ochazuke", "◆ matomeru"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	if len(app.projects) != 2 || app.projects[0].ID != "ochazuke" {
		t.Fatalf("projects = %+v", app.projects)
	}
}

func TestOpenProjectReturnsTheRow(t *testing.T) {
	t.Parallel()
	app := open(t)
	out, err := app.r.Execute(ochazuke.Request{Verb: "open", Noun: "project", Target: "matomeru"})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := out.(work.Project); !ok || p.Name != "matomeru" {
		t.Fatalf("project.open = %#v", out)
	}
	if app.r.FocusID() != "t1" || len(app.tasks) != 2 {
		t.Fatalf("focus = %s, tasks = %+v", app.r.FocusID(), app.tasks)
	}
}

func TestStopsMoveBetweenProjectsAndTasks(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "enter")
	if app.r.FocusID() != "t1" {
		t.Fatalf("enter on a project: focus = %s", app.r.FocusID())
	}
	press(t, app, "{")
	if app.r.FocusID() != "matomeru" {
		t.Fatalf("{: focus = %s", app.r.FocusID())
	}
	press(t, app, "}")
	if app.r.FocusID() != "t1" {
		t.Fatalf("}: focus = %s", app.r.FocusID())
	}
	app.m.Width, app.m.Height = 80, 24
	frame := ansi.Strip(frameView(app).Content)
	for _, want := range []string{"[ ] write the brief", "[ ] lift the store"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
}

// Owner decision (2026-10-26): enter on a Work task row opens the record
// editor for that task, as i does; l still opens the task's detail.
func TestEnterOnATaskRowOpensTheRecordEditor(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "enter")
	if app.r.FocusID() != "t1" {
		t.Fatalf("focus %s", app.r.FocusID())
	}
	press(t, app, "enter")
	if app.r.FocusID() != app.editor.fieldID("title") || app.editor.Record.ID != "t1" || app.editor.Value("title") != "write the brief" {
		t.Fatalf("enter: focus = %s, editor = %+v", app.r.FocusID(), app.editor)
	}
	press(t, app, "esc", "q")
	if app.r.FocusID() != "t1" {
		t.Fatalf("leave: focus = %s", app.r.FocusID())
	}
	press(t, app, "l")
	if app.r.FocusID() != "t1" || app.detailTask != "t1" {
		t.Fatalf("l: focus = %s, detail = %q", app.r.FocusID(), app.detailTask)
	}
}

func TestTaskVerbsPersistThroughPanel(t *testing.T) {
	t.Parallel()
	app := open(t)
	press(t, app, "j", "enter")

	press(t, app, "i")
	if app.r.FocusID() != app.editor.fieldID("title") || app.editor.Value("title") != "write the brief" {
		t.Fatalf("open: focus = %s, editor = %+v", app.r.FocusID(), app.editor)
	}
	press(t, app, "esc", "q")
	if app.r.FocusID() != "t1" || stored(t, app, "t1").Title != "write the brief" {
		t.Fatalf("cancel: focus = %s, task = %+v", app.r.FocusID(), stored(t, app, "t1"))
	}
	press(t, app, "i")
	typeText(t, app, " first")
	press(t, app, "enter", "q") // enter moves to the status; q leaves
	if got := stored(t, app, "t1").Title; got != "write the brief first" {
		t.Fatalf("open commit: title = %q", got)
	}

	// i edits the title; ctrl+u clears the line.
	press(t, app, "j", "i", "ctrl+u")
	typeText(t, app, "lift store and daemon")
	press(t, app, "enter", "q") // enter moves to the status; q leaves
	if got := stored(t, app, "t2").Title; got != "lift store and daemon" || app.r.FocusID() != "t2" {
		t.Fatalf("edit: title = %q, focus = %s", got, app.r.FocusID())
	}

	// x marks it done.
	press(t, app, "x")
	if got := stored(t, app, "t2").Status; got != work.StatusDone {
		t.Fatalf("done: status = %q", got)
	}

	// n adds a task to the open project and selects it; done tasks sort
	// last.
	press(t, app, "n")
	typeText(t, app, "launch an agent")
	press(t, app, "enter", "q") // enter moves to the status; q leaves
	if len(app.tasks) != 3 || app.tasks[1].Title != "launch an agent" || app.r.FocusID() != app.tasks[1].ID || app.tasks[2].ID != "t2" {
		t.Fatalf("new: tasks = %+v, focus = %s", app.tasks, app.r.FocusID())
	}

	app.m.Width, app.m.Height = 80, 24
	frame := ansi.Strip(frameView(app).Content)
	for _, want := range []string{"[ ] write the brief first", "[x] lift store and daemon", "[ ] launch an agent"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskVerbsTakeArgumentsWithoutPrompts(t *testing.T) {
	t.Parallel()
	app := open(t)
	if _, err := app.r.Execute(ochazuke.Request{Verb: "open", Noun: "project", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "edit", Noun: "task", Target: "t2", Args: ochazuke.Arguments{"title": "renamed"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "done", Noun: "task", Target: "t2"}); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, app, "t2"); got.Title != "renamed" || got.Status != work.StatusDone {
		t.Fatalf("task = %+v", got)
	}
}

// testEnv overrides only this app, including directory scans and home expansion.
func testEnv(a *App, key, value string) {
	previous := a.opts.lookupEnv
	a.opts.lookupEnv = func(k string) (string, bool) {
		if k == key {
			return value, true
		}
		return previous(k)
	}
	a.getenv = a.opts.getenv
	if key == "HOME" {
		a.home = a.opts.home
	}
}

// testUnsetEnv preserves LookupEnv's distinction between unset and empty.
func testUnsetEnv(a *App, key string) {
	previous := a.opts.lookupEnv
	a.opts.lookupEnv = func(k string) (string, bool) {
		if k == key {
			return "", false
		}
		return previous(k)
	}
	a.getenv = a.opts.getenv
}
