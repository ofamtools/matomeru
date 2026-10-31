package main

import (
	"testing"

	"ofam.dev/ochazuke"
)

// sessionsApp is panesApp with session ids, grouped so session rows show.
func sessionsApp(t *testing.T) (*App, *fakePanes) {
	t.Helper()
	a, f := panesApp(t)
	groupBy(t, a, groupProjectSessionPane)
	for i := range f.panes {
		f.panes[i].SessionID = "id-" + f.panes[i].Session
	}
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	return a, f
}

// The session's verbs: get, list, set, watch and move are standard;
// rename, associate and kill named; no create or delete.
func TestSessionVerbsAreStandard(t *testing.T) {
	t.Parallel()
	a, _ := sessionsApp(t)
	standardIs(t, a, "session", map[string]string{
		"get": "get", "list": "list", "set": "set", "watch": "watch", "move": "move",
		"rename": "", "associate": "", "choose-move": "", "kill": "", "attach": "",
	}, "create", "delete")
	thing, _ := executeA(t, a, "session", "get", "", ochazuke.Arguments{"id": "session:id-matomeru"}).(map[string]any)
	if thing["name"] != "matomeru" || thing["panes"] != 1 {
		t.Fatalf("get: %v", thing)
	}
}

// session move writes the session owner; undo puts it back; an empty
// name is refused (the daemon needs one); rename runs as session.rename.
func TestSessionMoveRenameAndUndo(t *testing.T) {
	t.Parallel()
	a, f := sessionsApp(t)
	fr := &fakeRenamer{panes: f}
	a.renamer = fr
	executeA(t, a, "session", "move", "", ochazuke.Arguments{"id": "id-matomeru", "to": "ochazuke"})
	if o, _ := a.work.SessionOwner("id-matomeru"); o.ProjectID != "ochazuke" {
		t.Fatalf("move: %+v", o)
	}
	executeA(t, a, "history", "undo", "", nil)
	if o, _ := a.work.SessionOwner("id-matomeru"); o.ProjectID != "" {
		t.Fatalf("undo of the move: %+v", o)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "session", Verb: "set", Args: ochazuke.Arguments{"id": "id-matomeru", "prop": "name", "value": " "}}); err == nil {
		t.Fatal("an empty session name was written")
	}
	executeA(t, a, "session", "rename", "session:id-matomeru", ochazuke.Arguments{"name": "mtm"})
	settle(a, a.r.PendingCommands())
	if len(fr.calls) != 1 || fr.calls[0] != "session matomeru mtm" || len(jobsOf(a, "session.rename")) != 1 {
		t.Fatalf("calls %v jobs %+v", fr.calls, a.r.Jobs())
	}
}
