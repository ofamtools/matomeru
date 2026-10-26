package main

import (
	"context"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// fakeRenamer renames the fake daemon's panes and sessions in place.
type fakeRenamer struct {
	panes *fakePanes
	calls []string
}

func (f *fakeRenamer) RenameSession(_ context.Context, session, name string) error {
	f.calls = append(f.calls, "session "+session+" "+name)
	for i := range f.panes.panes {
		if f.panes.panes[i].Session == session {
			f.panes.panes[i].Session = name
		}
	}
	return nil
}

func (f *fakeRenamer) RenameWindow(_ context.Context, session, window, name string) error {
	f.calls = append(f.calls, "window "+session+"/"+window+" "+name)
	for i := range f.panes.panes {
		if f.panes.panes[i].Session == session && f.panes.panes[i].ID == window {
			f.panes.panes[i].Name = name
		}
	}
	return nil
}

// r on a pane row asks for the name with the current one filled in; enter
// renames the window on the daemon and the list reloads with it.
func TestRenameAPaneFromItsRow(t *testing.T) {
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
	if app.r.Focus != fieldID || string(app.field.Text) != "claude" {
		t.Fatalf("r: focus %s field %q", app.r.Focus, string(app.field.Text))
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

// session rename takes the name as an argument (the command line's way)
// and reloads the list under the new name.
func TestRenameASession(t *testing.T) {
	app, f := panesApp(t)
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
	if _, err := app.r.Execute(ochazuke.Request{Noun: "session", Verb: "rename", Target: "session:id-matomeru", Args: ochazuke.Arguments{"name": "renamed"}}); err != nil {
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

// esc closes the rename prompt and keeps what was typed (esc never
// discards); esc with nothing changed renames nothing (UX review 2 F4).
func TestRenameEscKeepsTheTypedName(t *testing.T) {
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
	_, cmd := app.Update(keyMsg("esc"))
	settle(app, cmd)
	if len(fr.calls) != 0 || app.r.TopModal() != "" {
		t.Fatalf("unchanged esc: calls %v modal %q", fr.calls, app.r.TopModal())
	}
	press(t, app, "r", "ctrl+u")
	typeText(t, app, "shellbuild")
	_, cmd = app.Update(keyMsg("esc"))
	settle(app, cmd)
	if len(fr.calls) != 1 || fr.calls[0] != "window matomeru/w1 shellbuild" || app.r.TopModal() != "" {
		t.Fatalf("esc after typing: calls %v modal %q", fr.calls, app.r.TopModal())
	}
}

// UX review 2 F22: a saved record of a pane follows the pane's rename
// while it still carries the old name; one renamed on its own keeps it.
func TestRenamingAPaneRenamesItsSavedRecord(t *testing.T) {
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
	_, err = app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "rename", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"name": "builder"}})
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
