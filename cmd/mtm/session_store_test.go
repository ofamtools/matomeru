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

// The session's props come from the daemon listing.
func TestSessionGetReadsDaemon(t *testing.T) {
	t.Parallel()
	a, _ := sessionsApp(t)
	thing, _ := executeA(t, a, "session", "get", "", ochazuke.Arguments{"id": "session:id-matomeru"}).(map[string]any)
	if thing["name"] != "matomeru" || thing["panes"] != 1 {
		t.Fatalf("get: %v", thing)
	}
}

// session move writes the session owner; undo puts it back; an empty
// name is refused (the daemon needs one); a rename runs as session.set.
func TestSessionMoveRenameAndUndo(t *testing.T) {
	t.Parallel()
	a, f := sessionsApp(t)
	fr := &fakeRenamer{panes: f}
	a.renamer = fr
	executeA(t, a, "session", "set", "", ochazuke.Arguments{"id": "id-matomeru", "props": map[string]any{"@owner": "ochazuke"}})
	if o, _ := a.work.SessionOwner("id-matomeru"); o.ProjectID != "ochazuke" {
		t.Fatalf("move: %+v", o)
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if o, _ := a.work.SessionOwner("id-matomeru"); o.ProjectID != "" {
		t.Fatalf("undo of the move: %+v", o)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "session", Verb: "set", Args: ochazuke.Arguments{"id": "id-matomeru", "props": map[string]any{"name": " "}}}); err == nil {
		t.Fatal("an empty session name was written")
	}
	executeA(t, a, "session", "set", "session:id-matomeru", ochazuke.Arguments{"props": map[string]any{"@label": "mtm"}})
	settle(a, a.r.PendingCommands())
	if len(fr.calls) != 1 || fr.calls[0] != "session matomeru mtm" || len(jobsOf(a, "session.set")) != 1 {
		t.Fatalf("calls %v jobs %+v", fr.calls, a.r.Jobs())
	}
}
