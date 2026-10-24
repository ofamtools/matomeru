package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// open builds the app on a fresh database seeded with two projects.
func open(t *testing.T) *App {
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
	db.Close()
	app, err := New(Options{Data: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
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
}
