package main

import (
	"context"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
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
