package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
)

// seedConvert gives the seeded matomeru project a note, a pane, a
// subtask and directories (one ochazuke has too).
func seedConvert(t *testing.T, a *App) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := a.work.AddTree("ochazuke", work.Tree{Kind: "directory", Path: "/src/shared"})
	must(err)
	_, err = a.work.AddTree("matomeru", work.Tree{Kind: "directory", Path: "/src/matomeru"})
	must(err)
	_, err = a.work.AddTree("matomeru", work.Tree{Kind: "directory", Path: "/src/shared"})
	must(err)
	must(a.work.PutNote(work.Note{ID: "note", ProjectID: "matomeru", Text: "project note"}))
	must(a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "s1", ProjectID: "matomeru"}))
	must(a.work.PutTask(work.Task{ID: "t1a", ProjectID: "matomeru", ParentID: "t1", Title: "sub of the brief", Position: 1, Status: work.StatusTodo}))
	must(a.refreshProjects())
}

func paneOwnerOf(t *testing.T, a *App, window string) [2]string {
	t.Helper()
	o, err := a.work.PaneOwner(window, "")
	if err != nil {
		t.Fatal(err)
	}
	return [2]string{o.ProjectID, o.TaskID}
}

func noteOwnerOf(t *testing.T, a *App, id string) [2]string {
	t.Helper()
	notes, err := a.work.Notes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.ID == id {
			return [2]string{n.ProjectID, n.TaskID}
		}
	}
	t.Fatalf("note %s gone", id)
	return [2]string{}
}

// tab on a project row of the all view makes it a task of the project
// above: its tasks nest under the new task, its note and pane follow,
// its directories join; u puts it all back with the same ids, ctrl+r
// does it again.
func TestOutlineTabMakesAProjectATaskOfTheOneAbove(t *testing.T) {
	t.Parallel()
	a := open(t)
	seedConvert(t, a)
	press(t, a, "T")
	focusRow(t, a, projectNode("matomeru"))
	before := frameAt(a, 100)
	t.Logf("before tab, 100 columns:\n%s", before)
	press(t, a, "tab")
	after := frameAt(a, 100)
	t.Logf("after tab, 100 columns:\n%s", after)
	if _, err := a.work.Project("matomeru"); err == nil {
		t.Fatal("the project is still stored")
	}
	if roots := rootRows(a); !slices.Equal(roots, []string{projectNode("ochazuke"), projectNode("")}) {
		t.Fatalf("roots %v", roots)
	}
	made := taskTitled(tasksOf(t, a, "ochazuke"), "matomeru")
	if made.ID == "" || made.ParentID != "" {
		t.Fatalf("no task matomeru under ochazuke: %+v", tasksOf(t, a, "ochazuke"))
	}
	if a.r.Focus != outlineNode(made.ID) {
		t.Fatalf("focus %s, want the new task", a.r.Focus)
	}
	for id, parent := range map[string]string{"t1": made.ID, "t2": made.ID, "t1a": "t1"} {
		if got := stored(t, a, id); got.ProjectID != "ochazuke" || got.ParentID != parent {
			t.Fatalf("%s = %+v", id, got)
		}
	}
	tree := a.r.Tree(outlineTree)
	if rootOf(tree, outlineNode("t1a")) != projectNode("ochazuke") {
		t.Fatal("the subtask is not drawn under ochazuke")
	}
	want := [2]string{"ochazuke", made.ID}
	if got := noteOwnerOf(t, a, "note"); got != want {
		t.Fatalf("note owner %v", got)
	}
	if got := paneOwnerOf(t, a, "w1"); got != want {
		t.Fatalf("pane owner %v", got)
	}
	p, _ := a.work.Project("ochazuke")
	if len(p.Trees) != 2 || p.DefaultTree != "/src/shared" {
		t.Fatalf("ochazuke dirs %+v default %q", p.Trees, p.DefaultTree)
	}
	if !strings.Contains(after, "[ ] matomeru") || !strings.Contains(after, "[ ] sub of the brief") {
		t.Fatalf("frame after tab lacks the new rows:\n%s", after)
	}

	press(t, a, "u")
	if _, err := a.work.Project("matomeru"); err != nil {
		t.Fatalf("u did not restore the project: %v", err)
	}
	if _, err := a.work.Task(made.ID); err == nil {
		t.Fatal("u kept the new task")
	}
	for _, id := range []string{"t1", "t2"} {
		if got := stored(t, a, id); got.ProjectID != "matomeru" || got.ParentID != "" {
			t.Fatalf("u: %s = %+v", id, got)
		}
	}
	if got := noteOwnerOf(t, a, "note"); got != [2]string{"matomeru", ""} {
		t.Fatalf("u: note owner %v", got)
	}
	if got := paneOwnerOf(t, a, "w1"); got != [2]string{"matomeru", ""} {
		t.Fatalf("u: pane owner %v", got)
	}
	if p, _ := a.work.Project("ochazuke"); len(p.Trees) != 1 {
		t.Fatalf("u: ochazuke dirs %+v", p.Trees)
	}
	if a.r.Focus != projectNode("matomeru") {
		t.Fatalf("u: focus %s", a.r.Focus)
	}
	if got := frameAt(a, 100); got != before {
		t.Fatalf("u: frame differs from before:\n%s", got)
	}
	press(t, a, "ctrl+r")
	if _, err := a.work.Project("matomeru"); err == nil {
		t.Fatal("ctrl+r kept the project")
	}
	if got := paneOwnerOf(t, a, "w1"); got != want {
		t.Fatalf("ctrl+r: pane owner %v, want %v (the same task id)", got, want)
	}
}

// On the pin's outline tab makes the pinned project a task of the one
// above: the pin and the outline follow it there.
func TestPinnedOutlineTabMovesThePin(t *testing.T) {
	t.Parallel()
	a := pinned(t)
	press(t, a, "T", "k")
	if a.r.Focus != projectNode("matomeru") {
		t.Fatalf("focus %s", a.r.Focus)
	}
	press(t, a, "tab")
	if a.pin != "ochazuke" {
		t.Fatalf("pin %q", a.pin)
	}
	if roots := rootRows(a); !slices.Equal(roots, []string{projectNode("ochazuke")}) {
		t.Fatalf("roots %v", roots)
	}
	press(t, a, "u")
	if a.pin != "matomeru" {
		t.Fatalf("u: pin %q", a.pin)
	}
	if roots := rootRows(a); !slices.Equal(roots, []string{projectNode("matomeru")}) {
		t.Fatalf("u: roots %v", roots)
	}
}

// shift+tab on a task directly under its project makes it a project
// listed right after it; its subtask becomes the project's task, a pane
// it owns follows with the pane's directory; u undoes it.
func TestOutlineShiftTabMakesATopTaskAProject(t *testing.T) {
	t.Parallel()
	a := open(t)
	seedConvert(t, a)
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w2", ProjectID: "matomeru", TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutPaneRecipe(work.PaneRecipe{Window: "w2", Cwd: "/src/brief"}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "T")
	focusRow(t, a, outlineNode("t1"))
	t.Logf("before shift+tab, 100 columns:\n%s", frameAt(a, 100))
	press(t, a, "shift+tab")
	t.Logf("after shift+tab, 100 columns:\n%s", frameAt(a, 100))
	if _, err := a.work.Task("t1"); err == nil {
		t.Fatal("the task is still stored")
	}
	var made work.Project
	for _, p := range newProjects(t, a) {
		made = p
	}
	if made.Name != "write the brief" || made.DefaultTree != "/src/brief" || len(made.Trees) != 1 {
		t.Fatalf("new project %+v", made)
	}
	if roots := rootRows(a); !slices.Equal(roots, []string{projectNode("ochazuke"), projectNode("matomeru"), projectNode(made.ID), projectNode("")}) {
		t.Fatalf("roots %v", roots)
	}
	if a.r.Focus != projectNode(made.ID) {
		t.Fatalf("focus %s", a.r.Focus)
	}
	if got := stored(t, a, "t1a"); got.ProjectID != made.ID || got.ParentID != "" {
		t.Fatalf("subtask %+v", got)
	}
	if got := paneOwnerOf(t, a, "w2"); got != [2]string{made.ID, ""} {
		t.Fatalf("pane owner %v", got)
	}
	if p, _ := a.work.Project("matomeru"); len(p.Trees) != 2 {
		t.Fatalf("matomeru dirs changed: %+v", p.Trees)
	}
	press(t, a, "u")
	if got := stored(t, a, "t1"); got.ProjectID != "matomeru" || got.Title != "write the brief" {
		t.Fatalf("u: t1 %+v", got)
	}
	if got := stored(t, a, "t1a"); got.ProjectID != "matomeru" || got.ParentID != "t1" {
		t.Fatalf("u: t1a %+v", got)
	}
	if got := paneOwnerOf(t, a, "w2"); got != [2]string{"matomeru", "t1"} {
		t.Fatalf("u: pane owner %v", got)
	}
	if len(newProjects(t, a)) != 0 {
		t.Fatal("u kept the new project")
	}
	if a.r.Focus != outlineNode("t1") {
		t.Fatalf("u: focus %s", a.r.Focus)
	}
	press(t, a, "ctrl+r")
	if _, err := a.work.Project(made.ID); err != nil {
		t.Fatalf("ctrl+r: the same project id is not back: %v", err)
	}
	// A deeper task still outdents one level.
	press(t, a, "u")
	focusRow(t, a, outlineNode("t1a"))
	press(t, a, "shift+tab")
	if got := stored(t, a, "t1a"); got.ProjectID != "matomeru" || got.ParentID != "" {
		t.Fatalf("deep shift+tab: %+v", got)
	}
}

// J and K stay refused on a project row, and ? help still lists them.
func TestOutlineProjectRefusalsStayInHelp(t *testing.T) {
	t.Parallel()
	a := allOutline(t, projectNode("matomeru"))
	refused(t, a, "J", "creation order")
	refused(t, a, "K", "creation order")
	press(t, a, "?")
	help := frameAt(a, 160)
	if !strings.Contains(help, "creation order") || !strings.Contains(help, "outline-project.demote") && !strings.Contains(help, "make task") {
		t.Fatalf("help lacks the refusal or the demote row:\n%s", help)
	}
}

// project make-task and task make-project from the CLI.
func TestConversionsFromTheCLI(t *testing.T) {
	t.Parallel()
	a := open(t)
	if _, errOut, err := cli(a, "", "project", "make-task", "--target", "ochazuke"); err == nil || !strings.Contains(errOut+err.Error(), "no project above") {
		t.Fatalf("first project: err %v %s", err, errOut)
	}
	mustCLI(t, a, "project", "make-task", "--target", "matomeru")
	made := taskTitled(tasksOf(t, a, "ochazuke"), "matomeru")
	if made.ID == "" {
		t.Fatal("make-task wrote no task")
	}
	out := mustCLI(t, a, "task", "make-project", "--target", made.ID)
	if !strings.Contains(out, "promote") {
		t.Fatalf("make-project printed %q", out)
	}
	var names []string
	for _, p := range newProjects(t, a) {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"matomeru"}) {
		t.Fatalf("projects after the round trip %v", names)
	}
	if got := stored(t, a, "t1"); got.ProjectID == "ochazuke" || got.ParentID != "" {
		t.Fatalf("t1 after the round trip %+v", got)
	}
}
