package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
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
	t.Parallel()
	a := open(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, a, groupProjectSessionPane)
	panes := []pane{
		{Session: "not-project-name", SessionID: "sa", Pane: daemon.Pane{ID: "wa", Name: "A-shell"}},
		{Session: "not-project-name", SessionID: "sa", Pane: daemon.Pane{ID: "wb", Name: "B-agent", AgentState: "idle"}},
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
	// The all view is a tree: projects (unassociated last), their sessions
	// (a session split between owners shows under each), then the panes.
	if a.pin != "matomeru" || len(a.reviews) != 3 || a.paneList.Len() != 9 {
		t.Fatalf("all rows %+v pin %s", a.paneList.Items(), a.pin)
	}
	if items := a.paneList.Items(); items[len(items)-3].ID != "project:" || !strings.Contains(items[len(items)-3].Label, "unassociated") || items[len(items)-1].ID != panes[2].rowID() {
		t.Fatalf("unassociated is not last: %+v", items)
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
	t.Parallel()
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
		if err := a.r.OpenPage("work"); err != nil {
			t.Fatal(err)
		}
		if err := a.r.SetFocus("ochazuke"); err != nil {
			t.Fatal(err)
		}
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	path := seeded(t)
	a := openAt(t, path)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	b := openAt(t, path)
	if b.pin != "matomeru" || b.m.Title != "📌 matomeru" {
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
	t.Parallel()
	a, _ := cliApp(t)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	executeA(t, a, "task", "new", "work", ochazuke.Arguments{"title": "outside", "project": "ochazuke"})
	rows, err := a.work.Tasks("ochazuke")
	if err != nil || len(rows) != 1 || rows[0].Title != "outside" {
		t.Fatalf("outside task %+v %v", rows, err)
	}
	// The task is shown where it was made, the pin unchanged: the tasks
	// follow the project, not the pin (2026-10-26).
	if a.taskList.Len() != 1 || a.pin != "matomeru" {
		t.Fatalf("outside task: rows %+v pin %q", a.taskList.Items(), a.pin)
	}
	mustCLI(t, a, "task", "associate", "--target", rows[0].ID, "--project", "matomeru")
	got, err := a.work.Task(rows[0].ID)
	if err != nil || got.ProjectID != "matomeru" {
		t.Fatalf("association %+v %v", got, err)
	}
}

func TestProjectNameFallsBackToFolderThenUntitled(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		p    work.Project
		want string
	}{
		{work.Project{Name: "named", DefaultTree: "/src/other"}, "named"},
		{work.Project{DefaultTree: "/src/deflt", Trees: []work.Tree{{Path: "/src/first"}, {Path: "/src/deflt"}}}, "deflt"},
		{work.Project{Trees: []work.Tree{{Path: "/src/first"}}}, "first"},
		{work.Project{}, "untitled"},
	} {
		if got := projectName(c.p); got != c.want {
			t.Fatalf("%+v: got %q want %q", c.p, got, c.want)
		}
	}
	a := open(t)
	if err := a.work.PutProject(work.Project{ID: "nameless", DefaultTree: "/src/folder", Trees: []work.Tree{{Kind: "dir", Path: "/src/folder"}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	if got := a.ownerLabel("nameless"); got != "folder" {
		t.Fatalf("owner label %q", got)
	}
	found := false
	for _, item := range a.projectList.Items() {
		found = found || item.ID == "nameless" && item.Label == "◆ folder"
	}
	if !found {
		t.Fatalf("project rows %+v", a.projectList.Items())
	}
}

// titled seeds a long default directory under a fake home for matomeru.
func titled(t *testing.T) *App {
	t.Helper()
	a := open(t)
	a.home = func() (string, error) { return "/home/u", nil }
	if _, err := a.work.AddTree("matomeru", work.Tree{Kind: "dir", Path: "/home/u/proj/clients/acme/very-long-checkout-name/matomeru"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	return a
}

// titleRow renders the app at width and returns its first row, trimmed.
func titleRow(a *App, width int) string {
	a.m.Width, a.m.Height = width, 24
	return strings.TrimSpace(strings.SplitN(ansi.Strip(frameView(a).Content), "\n", 2)[0])
}

func TestTitleLeadsWithPin(t *testing.T) {
	t.Parallel()
	a := titled(t)
	const dir = "~/proj/clients/acme/very-long-checkout-name/matomeru"
	for _, c := range []struct {
		pin  string
		all  bool
		want string
	}{
		{"", false, "◆ Matomeru · no pin"},
		{"", true, "◆ Matomeru · no pin · all"},
		{"matomeru", false, "◆ 📌 matomeru  " + dir},
		{"matomeru", true, "◆ 📌 matomeru · all  " + dir},
	} {
		if a.pin != c.pin {
			executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": c.pin})
		}
		if a.all != c.all {
			executeA(t, a, "scope", "all", "work", nil)
		}
		row := titleRow(a, 120)
		if !strings.HasPrefix(row, c.want) || !strings.HasSuffix(row, "NORMAL") {
			t.Fatalf("pin %q all %v at 120: %q want %q", c.pin, c.all, row, c.want)
		}
		t.Logf("120: %s", row)
	}
	row := titleRow(a, 80)
	if !strings.HasPrefix(row, "◆ 📌 matomeru · all") || strings.Contains(row, "~") || strings.Contains(row, "…") || !strings.HasSuffix(row, "NORMAL") {
		t.Fatalf("at 80 the name must survive and the path go whole: %q", row)
	}
	t.Logf("80: %s", row)
	executeA(t, a, "scope", "all", "work", nil)
	// 90: the badge is padded to the widest base mode's name (ochazuke
	// v0.55 mode-badge.width widest), so the whole path needs more than 80.
	if row := titleRow(a, 90); !strings.HasPrefix(row, "◆ 📌 matomeru  ~/proj") || strings.Contains(row, "…") {
		t.Fatalf("a path that fits stays: %q", row)
	}
}

func TestTitleFollowsNameAndDefaultDirectory(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.home = func() (string, error) { return "/home/u", nil }
	if err := a.work.PutProject(work.Project{ID: "nameless", Created: 9}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "nameless"})
	if a.m.Title != "📌 untitled" {
		t.Fatalf("pathless %q", a.m.Title)
	}
	if _, err := a.work.AddTree("nameless", work.Tree{Kind: "dir", Path: "/home/u/src/folder"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	if a.m.Title != "📌 folder  ~/src/folder" {
		t.Fatalf("folder name %q", a.m.Title)
	}
	if _, err := a.work.AddTree("nameless", work.Tree{Kind: "dir", Path: "/elsewhere/other"}); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "default", "nameless", ochazuke.Arguments{"path": "/elsewhere/other"})
	if a.m.Title != "📌 other  /elsewhere/other" {
		t.Fatalf("default directory change %q", a.m.Title)
	}
	executeA(t, a, "project", "edit", "nameless", ochazuke.Arguments{"name": "renamed"})
	if a.m.Title != "📌 renamed  /elsewhere/other" {
		t.Fatalf("rename %q", a.m.Title)
	}
}

// "No pin" (owner, 2026-10-26): on Work, selecting a project row outside
// the pin shows its tasks without pinning it.
func TestWorkShowsAnUnpinnedProjectsTasksWithoutPinningIt(t *testing.T) {
	t.Parallel()
	a := pinned(t)
	if err := a.work.PutTask(work.Task{ID: "o1", ProjectID: "ochazuke", Title: "tag a release"}); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := a.projectList.Select("ochazuke"); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus("ochazuke"); err != nil {
		t.Fatal(err)
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	f := frameAt(a, 120)
	if a.pin != "matomeru" || !strings.Contains(f, "tag a release") || strings.Contains(f, "outside the pin") {
		t.Fatalf("pin %q:\n%s", a.pin, f)
	}
}

// Owner decision (2026-10-26): every project draws a glyph. One without a
// stored glyph (a new project) draws ◆, the stored record keeps its empty
// glyph, a stored glyph is kept, and every project row's name cell starts
// with a glyph and a space, so the names line up.
func TestEveryProjectDrawsAGlyph(t *testing.T) {
	t.Parallel()
	a := open(t)
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "new", Args: ochazuke.Arguments{"name": "fresh"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutProject(work.Project{ID: "starred", Name: "starred", Theme: work.Theme{Glyph: "★"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	var fresh work.Project
	for _, p := range a.projects {
		if p.Name == "fresh" {
			fresh = p
		}
	}
	if fresh.ID == "" || fresh.Theme.Glyph != "" {
		t.Fatalf("fresh project %+v: the stored glyph must stay empty", fresh)
	}
	names := map[string]string{}
	for _, item := range a.projectList.Items() {
		if item.Noun != "project" {
			continue
		}
		names[item.ID] = item.Cells[0]
		glyph, _, ok := strings.Cut(item.Cells[0], " ")
		if !ok || ansi.StringWidth(glyph) != 1 {
			t.Errorf("row %s name cell %q does not start with a one-cell glyph", item.ID, item.Cells[0])
		}
	}
	if names[fresh.ID] != "◆ fresh" || names["starred"] != "★ starred" {
		t.Fatalf("name cells %+v", names)
	}
	if got := projectLabel(fresh); got != "◆ fresh" {
		t.Fatalf("label %q", got)
	}
}
