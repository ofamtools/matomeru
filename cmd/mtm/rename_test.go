package main

import (
	"context"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// editFormID is ochazuke's standard edit form (r is edit {field:
// "@label"}); its one field is editFormID/name.
const editFormID = "edit-form"

// editing is the open standard edit form, nil when none is open.
func editing(a *App) *ochazuke.RecordEditor { return a.r.RecordEditor(editFormID) }

// fakeRenamer renames the fake daemon's panes and sessions in place. Its
// renames run as jobs, several at once: the fake daemon's lock guards its
// calls and its panes.
type fakeRenamer struct {
	panes *fakePanes
	calls []string
}

func (f *fakeRenamer) RenameSession(_ context.Context, session, name string) error {
	f.panes.mu.Lock()
	defer f.panes.mu.Unlock()
	f.calls = append(f.calls, "session "+session+" "+name)
	for i := range f.panes.panes {
		if f.panes.panes[i].Session == session {
			f.panes.panes[i].Session = name
		}
	}
	return nil
}

func (f *fakeRenamer) RenameWindow(_ context.Context, session, window, name string) error {
	f.panes.mu.Lock()
	defer f.panes.mu.Unlock()
	f.calls = append(f.calls, "window "+session+"/"+window+" "+name)
	for i := range f.panes.panes {
		if f.panes.panes[i].Session == session && f.panes.panes[i].ID == window {
			f.panes.panes[i].Name = name
		}
	}
	return nil
}

// r on a pane row opens the rename form with the current name filled in;
// enter renames the window on the daemon and the list reloads with it.
func TestRenameAPaneFromItsRow(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	fr := &fakeRenamer{panes: f}
	app.renamer = fr
	row := "pane:matomeru/w1"
	if err := app.paneList.Select(row); err != nil {
		t.Fatal(err)
	}
	if err := app.paneList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, app, "r")
	if app.r.Focus != editFormID+"/name" || editing(app).Values()["name"] != "claude" || app.r.Mode.Name != "insert" {
		t.Fatalf("r: focus %s mode %s values %v", app.r.Focus, app.r.Mode.Name, editing(app).Values())
	}
	press(t, app, "ctrl+u")
	typeText(t, app, "reviewer")
	_, cmd := app.Update(keyMsg("enter"))
	settle(app, cmd)
	if len(fr.calls) != 1 || fr.calls[0] != "window matomeru/w1 reviewer" {
		t.Fatalf("calls %v", fr.calls)
	}
	if app.r.Instances[row] == nil || !strings.HasPrefix(app.r.Instances[row].Label, "matomeru/reviewer") {
		t.Fatalf("row %+v", app.r.Instances[row])
	}
}

// r on a pane row opens the name selected (rename.select, on by default):
// typing replaces the whole name, with no ctrl+u first.
func TestRenameAPaneOpensTheNameSelected(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	fr := &fakeRenamer{panes: f}
	app.renamer = fr
	if err := app.paneList.Select("pane:matomeru/w1"); err != nil {
		t.Fatal(err)
	}
	if err := app.paneList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, app, "r")
	in := app.r.Input(app.r.Focus)
	if in == nil {
		t.Fatalf("no input at %s", app.r.Focus)
	}
	if lo, hi, ok := in.SelectedText(); !ok || lo != 0 || hi != len([]rune("claude")) || app.r.Mode.Name != "insert" {
		t.Fatalf("r: selected %d..%d %v mode %s", lo, hi, ok, app.r.Mode.Name)
	}
	typeText(t, app, "reviewer")
	if got := editing(app).Values()["name"]; got != "reviewer" {
		t.Fatalf("typing over the selection: %q", got)
	}
	_, cmd := app.Update(keyMsg("enter"))
	settle(app, cmd)
	if len(fr.calls) != 1 || fr.calls[0] != "window matomeru/w1 reviewer" {
		t.Fatalf("calls %v", fr.calls)
	}
}

// session rename takes the name as an argument (the command line's way)
// and reloads the list under the new name.
func TestRenameASession(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	for i := range f.panes {
		f.panes[i].SessionID = "id-" + f.panes[i].Session
	}
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	fr := &fakeRenamer{panes: f}
	app.renamer = fr
	if app.r.Instances["session:id-matomeru"] == nil {
		t.Fatalf("no session row: %+v", app.paneList.Items())
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "session", Verb: "set", Target: "session:id-matomeru", Args: ochazuke.Arguments{"props": map[string]any{"@label": "renamed"}}}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if len(fr.calls) != 1 || fr.calls[0] != "session matomeru renamed" {
		t.Fatalf("calls %v", fr.calls)
	}
	found := false
	for _, p := range app.panes {
		found = found || p.Session == "renamed"
	}
	if !found {
		t.Fatalf("panes %+v", app.panes)
	}
}

// esc closes the rename form (edit {field: "@label"}: esc ends insert, a
// second esc closes it) and keeps what was typed (esc never discards);
// esc with nothing changed renames nothing (UX review 2 F4).
func TestRenameEscKeepsTheTypedName(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	fr := &fakeRenamer{panes: f}
	app.renamer = fr
	if err := app.paneList.Select("pane:matomeru/w1"); err != nil {
		t.Fatal(err)
	}
	if err := app.paneList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, app, "r", "esc") // insert ends; a second esc closes the form
	_, cmd := app.Update(keyMsg("esc"))
	settle(app, cmd)
	if len(fr.calls) != 0 || app.r.TopModal() != "" {
		t.Fatalf("unchanged esc: calls %v modal %q", fr.calls, app.r.TopModal())
	}
	press(t, app, "r", "ctrl+u")
	typeText(t, app, "shellbuild")
	pressJob(t, app, "esc") // insert ends: the name is written as a job
	pressJob(t, app, "esc")
	if len(fr.calls) != 1 || fr.calls[0] != "window matomeru/w1 shellbuild" || app.r.TopModal() != "" {
		t.Fatalf("esc after typing: calls %v modal %q", fr.calls, app.r.TopModal())
	}
}

// UX review 2 F22: a saved record of a pane follows the pane's rename
// while it still carries the old name; one renamed on its own keeps it.
func TestRenamingAPaneRenamesItsSavedRecord(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	app.renamer = &fakeRenamer{panes: f}
	follows, err := app.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Window: "w1", Session: "matomeru", Name: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	keeps, err := app.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Window: "w1", Session: "matomeru", Name: "my reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"props": map[string]any{"@label": "builder"}}})
	if err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if got, _ := app.work.SavedPane(follows.ID); got.Name != "builder" {
		t.Fatalf("saved record kept the launch name %q", got.Name)
	}
	if got, _ := app.work.SavedPane(keeps.ID); got.Name != "my reviewer" {
		t.Fatalf("a record named on its own changed to %q", got.Name)
	}
}

// pauseForm delivers a record editor's latest pause timer, as the typing
// pause would.
func pauseForm(t *testing.T, a *App, form *ochazuke.RecordEditor) {
	t.Helper()
	for seq := uint64(0); seq < 1<<16; seq++ {
		tick := ochazuke.RecordDebounceMsg{Editor: form.ID(), Seq: seq}
		if form.DebounceCurrent(tick) {
			_, cmd := a.Update(tick)
			settle(a, cmd)
			return
		}
	}
	t.Fatal("no pause timer is current")
}

// The session rename form renames as you type: each write renames the
// session as the last one left it; an empty name is not written.
func TestRenameASessionAsYouType(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	for i := range f.panes {
		f.panes[i].SessionID = "id-" + f.panes[i].Session
	}
	if err := app.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	fr := &fakeRenamer{panes: f}
	app.renamer = fr
	if err := app.r.SetFocus("session:id-matomeru"); err != nil {
		t.Fatal(err)
	}
	press(t, app, "r", "end")
	typeText(t, app, "2")
	pauseForm(t, app, editing(app))
	press(t, app, "ctrl+u")
	pauseForm(t, app, editing(app))
	if !strings.Contains(editing(app).Problem("name"), "needs a name") {
		t.Fatalf("empty name: problem %q", editing(app).Problem("name"))
	}
	typeText(t, app, "m3")
	pressJob(t, app, "esc") // insert ends: the name is written as a job
	pressJob(t, app, "esc")
	if want := []string{"session matomeru matomeru2", "session matomeru2 m3"}; strings.Join(fr.calls, ",") != strings.Join(want, ",") || app.r.TopModal() != "" {
		t.Fatalf("calls %v modal %q", fr.calls, app.r.TopModal())
	}
}
