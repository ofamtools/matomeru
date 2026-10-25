package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func executeA(t *testing.T, a *App, noun, verb, target string, args ochazuke.Arguments) any {
	t.Helper()
	out, err := a.r.Execute(ochazuke.Request{Noun: noun, Verb: verb, Target: target, Args: args, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPinFiltersAndAllPreservesPin(t *testing.T) {
	a := open(t)
	panes := []pane{
		{Session: "not-project-name", SessionID: "sa", Pane: daemon.Pane{ID: "wa", Name: "A-shell"}},
		{Session: "not-project-name", SessionID: "sa", Pane: daemon.Pane{ID: "wb", Name: "B-agent", AgentState: "working"}},
		{Session: "outside", SessionID: "su", Pane: daemon.Pane{ID: "wu", Name: "scratch"}},
	}
	if err := a.work.SetSessionOwner(work.SessionOwner{ID: "sa", Name: "not-project-name", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SetPaneOwner(work.Assignment{Window: "wb", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	for _, rv := range []work.Review{{Window: "wa", ProjectID: "matomeru"}, {Window: "wb", ProjectID: "ochazuke"}, {Window: "wu"}} {
		rv.Session = "not-project-name"
		if _, err := a.work.AddReview(rv); err != nil {
			t.Fatal(err)
		}
	}
	a.setPanes(panes, nil)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	if a.r.Instances["session:sa"] == nil || a.r.Instances[panes[0].rowID()] == nil || a.r.Instances[panes[1].rowID()] != nil || a.r.Instances[panes[2].rowID()] != nil || a.r.Instances["session:su"] != nil || len(a.reviews) != 1 {
		t.Fatalf("scope rows %+v reviews %+v", a.paneList.Items(), a.reviews)
	}
	executeA(t, a, "scope", "all", "work", nil)
	if a.pin != "matomeru" || len(a.reviews) != 3 || a.paneList.Len() != 5 {
		t.Fatalf("all rows %+v pin %s", a.paneList.Items(), a.pin)
	}
	for _, row := range a.paneList.Items() {
		if row.ID == panes[2].rowID() && !strings.Contains(row.Label, "unassociated") {
			t.Fatalf("unlabeled scratch %+v", row)
		}
	}
	executeA(t, a, "session", "associate", "session:sa", ochazuke.Arguments{"project": "ochazuke"})
	if got, _ := a.work.PaneOwner("wa", "sa"); got.ProjectID != "ochazuke" {
		t.Fatalf("inherited %+v", got)
	}
	executeA(t, a, "session", "associate", "session:sa", ochazuke.Arguments{"project": "matomeru"})
	if got, _ := a.work.PaneOwner("wb", "sa"); got.ProjectID != "ochazuke" {
		t.Fatalf("explicit moved %+v", got)
	}
	executeA(t, a, "pane", "associate", panes[2].rowID(), ochazuke.Arguments{"project": "matomeru"})
	executeA(t, a, "pane", "associate", panes[2].rowID(), ochazuke.Arguments{"project": "ochazuke"})
	executeA(t, a, "pane", "associate", panes[2].rowID(), ochazuke.Arguments{"scratch": true})
	if got, _ := a.work.PaneOwner("wu", "sa"); got.ProjectID != "" {
		t.Fatalf("scratch inherited %+v", got)
	}
}

func TestLaunchUsesPinWhileBrowsingAndScratchOverrides(t *testing.T) {
	a, d := cliApp(t)
	d.panes = a.panesd.(*fakePanes)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	executeA(t, a, "project", "open", "ochazuke", nil)
	for _, c := range []struct {
		args           []string
		session, owner string
	}{
		{[]string{"pane", "new", "--yes"}, "matomeru", "matomeru"},
		{[]string{"agent", "launch", "--yes"}, "matomeru", "matomeru"},
		{[]string{"agent", "launch", "--project", "ochazuke", "--yes"}, "ochazuke", "ochazuke"},
		{[]string{"pane", "new", "--scratch", "--yes"}, "mtm-only", ""},
		{[]string{"agent", "launch", "--scratch", "--yes"}, "no-project", ""},
	} {
		_, stderr, err := cli(a, "", c.args...)
		if err != nil {
			t.Fatalf("%v: %v %s", c.args, err, stderr)
		}
		var w *fakeWindow
		if c.args[0] == "pane" {
			w = d.shells[len(d.shells)-1]
		} else {
			w = d.launches[len(d.launches)-1]
		}
		o, err := a.work.PaneOwner(w.ID, "")
		if err != nil || w.Session != c.session || o.ProjectID != c.owner || a.pin != "matomeru" {
			t.Fatalf("%v pane %+v owner %+v pin %s err %v", c.args, w, o, a.pin, err)
		}
	}
}

type waitingDriver struct {
	*fakeDaemon
	entered chan struct{}
	release chan struct{}
}

func (d waitingDriver) WaitReady(context.Context, string, string, harness.Ready) error {
	close(d.entered)
	<-d.release
	return nil
}

func TestOwnerWrittenBeforeReadinessAndLateLaunchDoesNotNavigate(t *testing.T) {
	a := open(t)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	d := waitingDriver{fakeDaemon: &fakeDaemon{panes: a.panesd.(*fakePanes)}, entered: make(chan struct{}), release: make(chan struct{})}
	a.launcher = d
	executeA(t, a, "agent", "start", "work", ochazuke.Arguments{"project": "matomeru", "task": "t1", "session": "unrelated"})
	cmd := a.r.PendingCommands()
	messages := make(chan any, 1)
	go func() { messages <- cmd() }()
	<-d.entered
	if owner, err := a.work.PaneOwner("a1", ""); err != nil || owner.ProjectID != "matomeru" || owner.TaskID != "t1" {
		t.Fatalf("owner not persisted before ready %+v %v", owner, err)
	}
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "ochazuke"})
	close(d.release)
	a.m.Update(<-messages)
	if a.r.Page != "work" || a.r.Terminal(paneTermID) != nil {
		t.Fatalf("late launch navigated: %s", a.r.Page)
	}
	if owner, _ := a.work.PaneOwner("a1", ""); owner.ProjectID != "matomeru" {
		t.Fatalf("late owner %+v", owner)
	}
	reviews, err := a.work.Reviews()
	if err != nil || len(reviews) != 1 || reviews[0].ProjectID != "matomeru" {
		t.Fatalf("review %+v %v", reviews, err)
	}
}

func TestListingFailureKeepsCreatedPaneAndOwner(t *testing.T) {
	a := open(t)
	a.panesd.(*fakePanes).err = errors.New("listing unavailable")
	a.launcher = &fakeDaemon{}
	executeA(t, a, "pane", "start", panesPage, ochazuke.Arguments{"project": "matomeru", "session": "unrelated"})
	runOnce(a, a.r.PendingCommands())
	if owner, err := a.work.PaneOwner("s1", ""); err != nil || owner.ProjectID != "matomeru" {
		t.Fatalf("lost owner %+v %v", owner, err)
	}
	if a.r.Instances["pane:unrelated/s1"] == nil || !strings.Contains(a.landErr, "listing panes failed") {
		t.Fatalf("lost pane or warning: %+v %q", a.panes, a.landErr)
	}
}

func TestUnpinSurvivesAppRestartAndPickerWorksEveryPage(t *testing.T) {
	path := seeded(t)
	a := openAt(t, path)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	b := openAt(t, path)
	if b.pin != "matomeru" || b.m.Title != "Matomeru · matomeru" {
		t.Fatalf("restart pin %q title %q", b.pin, b.m.Title)
	}
	executeA(t, b, "project", "unpin", "work", nil)
	c := openAt(t, path)
	if c.pin != "" {
		t.Fatalf("unpin restart %q", c.pin)
	}
	for _, page := range []string{"work", panesPage, reviewsPage} {
		if err := c.r.OpenPage(page); err != nil {
			t.Fatal(err)
		}
		press(t, c, "P")
		if c.r.TopModal() != "pin-picker" {
			t.Fatalf("picker unavailable on %s", page)
		}
		press(t, c, "esc")
	}
	if _, err := c.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestCreatingAndAssociatingTaskOutsidePin(t *testing.T) {
	a, _ := cliApp(t)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	executeA(t, a, "task", "new", "work", ochazuke.Arguments{"title": "outside", "project": "ochazuke"})
	rows, err := a.work.Tasks("ochazuke")
	if err != nil || len(rows) != 1 || rows[0].Title != "outside" {
		t.Fatalf("outside task %+v %v", rows, err)
	}
	if a.taskList.Len() != 0 || a.pin != "matomeru" {
		t.Fatalf("outside task leaked into pin: %+v", a.taskList.Items())
	}
	mustCLI(t, a, "task", "associate", "--target", rows[0].ID, "--project", "matomeru")
	got, err := a.work.Task(rows[0].ID)
	if err != nil || got.ProjectID != "matomeru" {
		t.Fatalf("association %+v %v", got, err)
	}
}
