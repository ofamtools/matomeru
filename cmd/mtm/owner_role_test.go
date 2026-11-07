package main

import (
	"errors"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// A noun that declares an owner prop whose store cannot be read resolves the
// unassociated owner and the save path (resolveOwner) never fails.
func TestOwnerOfUnreadableRowIsUnassociated(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.r.Define("probe").Props(
		ochazuke.PropSpec{Name: "id", Key: true, Parameter: ochazuke.Parameter{Type: "string"}},
		ochazuke.PropSpec{Name: "project", Role: ochazuke.RoleOwner, Parameter: ochazuke.Parameter{Type: "string", Noun: "project"}},
	).Store(ochazuke.Store{Kind: ochazuke.Collection, Get: func(ochazuke.Ctx, string) (ochazuke.Thing, error) {
		return nil, errors.New("boom")
	}})
	a.cli = false
	inst := &ochazuke.Instance{ID: "p1", Noun: "probe"}
	o, found, err := a.instanceOwner(inst, "probe")
	if err != nil || !found || o != (Owner{}) {
		t.Fatalf("instanceOwner: %+v %v %v", o, found, err)
	}
	o, err = a.resolveOwner(ochazuke.Ctx{Runtime: a.r, Instance: inst}, nil)
	if err != nil || o != (Owner{}) {
		t.Fatalf("resolveOwner: %+v %v", o, err)
	}
}

// A listed pane resolves the owner the work store holds for it; one the
// listing lacks resolves unassociated, not an error.
func TestOwnerOfPaneRowMatchesTheListing(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "work", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	w1 := pane{Session: "work", SessionID: "s1", Pane: daemon.Pane{ID: "w1", Name: "Terminal 1a2b3c4d"}}
	a.setPanes([]pane{w1}, nil)
	want, err := a.work.PaneOwner(w1.ID, w1.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	o, found, err := a.instanceOwner(&ochazuke.Instance{ID: w1.rowID(), Noun: "pane", Data: w1}, "pane")
	if err != nil || !found || o.ProjectID != want.ProjectID || o.TaskID != want.TaskID || o.ProjectID != "ochazuke" {
		t.Fatalf("listed pane: %+v %v %v, want %+v", o, found, err, want)
	}
	o, found, err = a.instanceOwner(&ochazuke.Instance{ID: "pane:work/gone", Noun: "pane"}, "pane")
	if err != nil || !found || o != (Owner{}) {
		t.Fatalf("unlisted pane: %+v %v %v", o, found, err)
	}
	if so, found, err := a.instanceOwner(&ochazuke.Instance{ID: "session:s1", Noun: "session"}, "session"); err != nil || !found {
		t.Fatalf("session: %+v %v %v", so, found, err)
	}
}
