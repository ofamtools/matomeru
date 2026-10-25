package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
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
	codes := map[string]rune{"enter": tea.KeyEnter, "esc": tea.KeyEscape, "up": tea.KeyUp, "down": tea.KeyDown}
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

// seeded writes the seeded database (see open) and returns its path.
func seeded(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"ochazuke", "matomeru"} {
		if err := w.PutProject(work.Project{ID: name, Name: name, Created: int64(i + 1), Theme: work.Theme{Glyph: "◆"}}); err != nil {
			t.Fatal(err)
		}
	}
	for i, title := range []string{"write the brief", "lift the store"} {
		if err := w.PutTask(work.Task{ID: "t" + string(rune('1'+i)), ProjectID: "matomeru", Title: title, Position: int64(i + 1), Status: work.StatusTodo}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	return path
}

// openAt builds the app on an existing database file.
func openAt(t *testing.T, path string) *App {
	t.Helper()
	return openWith(t, Options{Data: path})
}

// openWith builds the app with opts and the test fakes.
func openWith(t *testing.T, opts Options) *App {
	t.Helper()
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	app.home = func() (string, error) { return "/home/test", nil }
	// No test reaches the real daemon.
	app.panesd = newFakePanes()
	app.ensure = func(context.Context) error { return nil }
	app.sleep = func(context.Context, time.Duration) error { return nil }
	return app
}

func TestBindingRowsAreAtomic(t *testing.T) {
	if _, err := open(t).r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkPageListsStoredProjects(t *testing.T) {
	app := open(t)
	app.m.Width, app.m.Height = 80, 24
	frame := ansi.Strip(app.m.View().Content)
	for _, want := range []string{"projects (2)", "◆ ochazuke", "◆ matomeru"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	if len(app.projects) != 2 || app.projects[0].ID != "ochazuke" {
		t.Fatalf("projects = %+v", app.projects)
	}
}

func TestOpenProjectReturnsTheRow(t *testing.T) {
	app := open(t)
	out, err := app.r.Execute(ochazuke.Request{Verb: "open", Noun: "project", Target: "matomeru"})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := out.(work.Project); !ok || p.Name != "matomeru" {
		t.Fatalf("project.open = %#v", out)
	}
	if app.r.Focus != "t1" || len(app.tasks) != 2 {
		t.Fatalf("focus = %s, tasks = %+v", app.r.Focus, app.tasks)
	}
}

func TestStopsMoveBetweenProjectsAndTasks(t *testing.T) {
	app := open(t)
	press(t, app, "j", "enter")
	if app.r.Focus != "t1" {
		t.Fatalf("enter on a project: focus = %s", app.r.Focus)
	}
	press(t, app, "{")
	if app.r.Focus != "matomeru" {
		t.Fatalf("{: focus = %s", app.r.Focus)
	}
	press(t, app, "}")
	if app.r.Focus != "t1" {
		t.Fatalf("}: focus = %s", app.r.Focus)
	}
	app.m.Width, app.m.Height = 80, 24
	frame := ansi.Strip(app.m.View().Content)
	for _, want := range []string{"[ ] write the brief", "[ ] lift the store"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
}

func TestTaskVerbsPersistThroughPanel(t *testing.T) {
	app := open(t)
	press(t, app, "j", "enter")

	press(t, app, "i")
	if app.r.Focus != recordInputID || string(app.editor.Input.Text) != "write the brief" {
		t.Fatalf("open: focus = %s, editor = %+v", app.r.Focus, app.editor)
	}
	press(t, app, "esc", "q")
	if app.r.Focus != "t1" || stored(t, app, "t1").Title != "write the brief" {
		t.Fatalf("cancel: focus = %s, task = %+v", app.r.Focus, stored(t, app, "t1"))
	}
	press(t, app, "i")
	typeText(t, app, " first")
	press(t, app, "enter")
	if got := stored(t, app, "t1").Title; got != "write the brief first" {
		t.Fatalf("open commit: title = %q", got)
	}

	// i edits the title; ctrl+u clears the line.
	press(t, app, "j", "i", "ctrl+u")
	typeText(t, app, "lift store and daemon")
	press(t, app, "enter")
	if got := stored(t, app, "t2").Title; got != "lift store and daemon" || app.r.Focus != "t2" {
		t.Fatalf("edit: title = %q, focus = %s", got, app.r.Focus)
	}

	// x marks it done.
	press(t, app, "x")
	if got := stored(t, app, "t2").Status; got != work.StatusDone {
		t.Fatalf("done: status = %q", got)
	}

	// a adds a task to the open project and selects it.
	press(t, app, "a")
	typeText(t, app, "launch an agent")
	press(t, app, "enter")
	if len(app.tasks) != 3 || app.tasks[2].Title != "launch an agent" || app.r.Focus != app.tasks[2].ID {
		t.Fatalf("new: tasks = %+v, focus = %s", app.tasks, app.r.Focus)
	}

	app.m.Width, app.m.Height = 80, 24
	frame := ansi.Strip(app.m.View().Content)
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
