package main

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// openSaved opens the Saved page on the saved row id.
func openSaved(t *testing.T, a *App, id string) {
	t.Helper()
	if err := a.r.OpenPage(savedPage); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	if err := a.savedList.Select(id); err != nil {
		t.Fatal(err)
	}
	if err := a.savedList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
}

// The Saved detail shows the record's command, directory and window, and
// links its project, its live pane and its directory.
func TestSavedDetailShowsTheRecordAndItsLinks(t *testing.T) {
	t.Parallel()
	l := linkedFixture(t)
	a := l.a
	openSaved(t, a, l.savedID)
	fr := sizedFrame(a, 140, 30)
	t.Logf("Saved:\n%s", fr)
	for _, want := range []string{"builder", "command  go test ./...", "window   w1 (running)", "links (3)", "project ◆ matomeru", "pane    matomeru/claude", "dir     " + pathCell(l.alpha, "/home/test")} {
		if !strings.Contains(fr, want) {
			t.Fatalf("frame lacks %q:\n%s", want, fr)
		}
	}
	// A record whose window is gone links no pane.
	gone, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Window: "dead", Name: "old shell", ProjectID: "ochazuke"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshSaved(); err != nil {
		t.Fatal(err)
	}
	openSaved(t, a, gone.ID)
	if fr = sizedFrame(a, 140, 30); !strings.Contains(fr, "dead (not running)") || !strings.Contains(fr, "links (1)") || strings.Contains(fr, "pane    ") {
		t.Fatalf("a dead record's detail:\n%s", fr)
	}
}

// enter on a Saved link lands on the project's Work row, the pane's
// Panes row and the directory's Directories row (the scan, first run by
// the jump, selects it when it lands).
func TestSavedLinksJumpToTheirRows(t *testing.T) {
	t.Parallel()
	l := linkedFixture(t)
	a := l.a
	for _, c := range []struct{ kind, id, page, focus string }{
		{linkProject, "matomeru", "work", "matomeru"},
		{linkPane, l.paneRow, panesPage, l.paneRow},
		{linkDirectory, l.alpha, directoriesPage, "directory:" + l.alpha},
	} {
		openSaved(t, a, l.savedID)
		frame(a)
		followLinkTo(t, a, a.detail.saved, c.kind, c.id)
		if a.r.Page != c.page || a.r.Focus != c.focus {
			t.Fatalf("%s link: page %s focus %s, want %s on %s", c.kind, a.r.Page, a.r.Focus, c.focus, c.page)
		}
	}
}

// J and K scroll a long Saved detail (a record with a large environment)
// while the row keeps the focus.
func TestSavedDetailScrollsWithJK(t *testing.T) {
	t.Parallel()
	l := linkedFixture(t)
	a := l.a
	env := map[string]string{}
	for i := range 30 {
		env[fmt.Sprintf("VAR_%02d", i)] = "x"
	}
	builder, err := a.work.SavedPane(l.savedID)
	if err != nil {
		t.Fatal(err)
	}
	// Created after the builder, so the row sorts below it and k has a row
	// to move to: rows created in one millisecond tie-break on their random
	// ids, which put this row first in some runs.
	long, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Window: "w1", Name: "env heavy", Env: env, ProjectID: "matomeru", Created: builder.Created + 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshSaved(); err != nil {
		t.Fatal(err)
	}
	openSaved(t, a, long.ID)
	frame(a)
	d := a.detail.saved.d
	if !d.Overflows() {
		t.Fatalf("the detail fits:\n%s", frame(a))
	}
	press(t, a, "5", "J")
	if frame(a); d.Offset() != 5 || a.r.Focus != long.ID {
		t.Fatalf("5J: offset %d focus %s", d.Offset(), a.r.Focus)
	}
	press(t, a, "K")
	if fr := frame(a); d.Offset() != 4 || !strings.Contains(ochazuke.BindingHints(a.r, a.r.Focus), "J/K detail") {
		t.Fatalf("K: offset %d\n%s", d.Offset(), fr)
	}
	press(t, a, "k")
	if frame(a); a.r.Focus != l.savedID || d.Offset() != 0 {
		t.Fatalf("k: focus %s (want %s), offset %d", a.r.Focus, l.savedID, d.Offset())
	}
}
