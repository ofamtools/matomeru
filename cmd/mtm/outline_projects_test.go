package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// allOutline is the seeded app's T outline with no pin (the all view),
// the focus on row.
func allOutline(t *testing.T, row string) *App {
	t.Helper()
	a := open(t)
	press(t, a, "T")
	if a.r.TopModal() != outlineModal || !a.outline.scope.all {
		t.Fatalf("T opened %q, scope %+v", a.r.TopModal(), a.outline.scope)
	}
	focusRow(t, a, row)
	return a
}

// focusRow selects and focuses one of the outline's rows.
func focusRow(t *testing.T, a *App, row string) {
	t.Helper()
	if err := a.r.Tree(outlineTree).Select(row); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
}

// refused presses key and wants it refused: a message, nothing changed.
func refused(t *testing.T, a *App, key, want string) {
	t.Helper()
	a.m.Status = ""
	a.Update(keyMsg(key))
	if !strings.Contains(a.m.Status, want) {
		t.Fatalf("%s: status %q, want %q", key, a.m.Status, want)
	}
	a.m.Status = ""
}

// rootRows are the outline's top-level rows by id.
func rootRows(a *App) []string {
	var out []string
	for _, n := range a.r.Tree(outlineTree).Nodes() {
		if n.Parent == "" {
			out = append(out, n.ID)
		}
	}
	return out
}

// newProjects are the stored projects that are not seeded.
func newProjects(t *testing.T, a *App) []work.Project {
	t.Helper()
	all, err := a.work.Projects()
	if err != nil {
		t.Fatal(err)
	}
	var out []work.Project
	for _, p := range all {
		if p.ID != "ochazuke" && p.ID != "matomeru" {
			out = append(out, p)
		}
	}
	return out
}

// In the all view the top level is projects: o on a project row writes a
// new project at once (empty, owned by nothing), typing names it and its
// session name follows; u undoes the name, then the project, and ctrl+r
// brings both back.
func TestAllViewOAddsAProjectThatTypingNames(t *testing.T) {
	a := allOutline(t, projectNode("ochazuke"))
	press(t, a, "o")
	made := newProjects(t, a)
	if len(made) != 1 || made[0].Name != "" {
		t.Fatalf("o wrote %+v", made)
	}
	id := made[0].ID
	typePaused(t, a, "release train")
	if p, _ := a.work.Project(id); p.Name != "release train" || sessionFor(p) != "release-train" {
		t.Fatalf("typed name: %+v session %q", p, sessionFor(p))
	}
	press(t, a, "esc")
	if p, ok := a.project(id); !ok || projectName(p) != "release train" {
		t.Fatalf("Work page projects lack the new one: %+v", a.projects)
	}
	if row := a.r.Instances[a.r.Focus]; row == nil || row.Noun != outlineProjectNoun {
		t.Fatalf("new project row %s is %+v", a.r.Focus, row)
	}
	press(t, a, "u")
	if p, err := a.work.Project(id); err != nil || p.Name != "" {
		t.Fatalf("u after naming: %+v %v", p, err)
	}
	press(t, a, "u")
	if made := newProjects(t, a); len(made) != 0 {
		t.Fatalf("second u kept %+v", made)
	}
	press(t, a, "ctrl+r", "ctrl+r")
	if p, err := a.work.Project(id); err != nil || p.Name != "release train" {
		t.Fatalf("redo twice: %+v %v", p, err)
	}
	t.Logf("all view after a new project, 80 columns:\n%s", frameAt(a, 80))
}

// Typing on a project row renames the project through project.edit, as it
// is typed; u undoes the rename.
func TestAllViewTypingRenamesAProject(t *testing.T) {
	a := allOutline(t, projectNode("ochazuke"))
	press(t, a, "i", "end")
	typePaused(t, a, " two")
	if p, _ := a.work.Project("ochazuke"); p.Name != "ochazuke two" || sessionFor(p) != "ochazuke-two" {
		t.Fatalf("rename while typing: %+v", p)
	}
	press(t, a, "esc")
	if p, _ := a.project("ochazuke"); p.Name != "ochazuke two" || p.Theme.Glyph != "◆" {
		t.Fatalf("rename kept the rest? %+v", p)
	}
	press(t, a, "u")
	if p, _ := a.work.Project("ochazuke"); p.Name != "ochazuke" {
		t.Fatalf("u: %+v", p)
	}
}

// d d on a project row is project.delete: its confirm names what is
// orphaned, n keeps it, y deletes it and its tasks move to no project.
func TestAllViewDeletesAProjectThroughItsConfirm(t *testing.T) {
	a := allOutline(t, projectNode("matomeru"))
	press(t, a, "d", "d")
	if a.r.TopModal() != "confirm" {
		t.Fatalf("d d opened %q", a.r.TopModal())
	}
	if f := frameAt(a, 100); !strings.Contains(f, "Delete") || !strings.Contains(f, "2 tasks") {
		t.Fatalf("confirm:\n%s", f)
	}
	press(t, a, "n")
	if _, ok := a.project("matomeru"); !ok || a.r.TopModal() != outlineModal {
		t.Fatalf("n: modal %q", a.r.TopModal())
	}
	focusRow(t, a, projectNode("matomeru"))
	press(t, a, "d", "d", "y")
	if _, err := a.work.Project("matomeru"); err == nil {
		t.Fatal("y kept the project")
	}
	if a.r.TopModal() != outlineModal {
		t.Fatalf("after y: modal %q", a.r.TopModal())
	}
	for _, id := range []string{"t1", "t2"} {
		got := stored(t, a, id)
		if got.ProjectID != "" || got.FormerProjectID != "matomeru" {
			t.Fatalf("%s not orphaned: %+v", id, got)
		}
	}
	tree := a.r.Tree(outlineTree)
	if slices.Contains(rootRows(a), projectNode("matomeru")) || rootOf(tree, outlineNode("t1")) != projectNode("") {
		t.Fatalf("outline after delete: roots %v", rootRows(a))
	}
}

// Projects keep their creation order: J and K on a project row are
// refused, as is tab on the first project (no project above it); no
// project is not a project (i, d d, tab refused).
func TestOutlineRefusesChangingTheTopLevel(t *testing.T) {
	a := allOutline(t, projectNode("matomeru"))
	before := rootRows(a)
	refused(t, a, "J", "creation order")
	refused(t, a, "K", "creation order")
	focusRow(t, a, projectNode("ochazuke"))
	refused(t, a, "tab", "no project above it")
	focusRow(t, a, outlineNode("t1"))
	// In the open line shift+tab reaches the outline itself: the move is
	// not stored and the row goes back under its project.
	press(t, a, "i", "shift+tab")
	a.m.Status = ""
	press(t, a, "esc")
	if got := stored(t, a, "t1"); got.ProjectID != "matomeru" || rootOf(a.r.Tree(outlineTree), outlineNode("t1")) != projectNode("matomeru") {
		t.Fatalf("shift+tab in the line moved t1: %+v roots %v", got, rootRows(a))
	}
	focusRow(t, a, projectNode(""))
	refused(t, a, "tab", "top level")
	refused(t, a, "i", "not a project")
	refused(t, a, "d", "")
	refused(t, a, "d", "not a project")
	if after := rootRows(a); !slices.Equal(before, after) {
		t.Fatalf("roots changed: %v -> %v", before, after)
	}
	// x and s are a task's: on a project row they are refused.
	focusRow(t, a, projectNode("ochazuke"))
	refused(t, a, "x", "select a task")
	// A nested task still outdents as before.
	focusRow(t, a, outlineNode("t2"))
	press(t, a, "tab")
	if got := stored(t, a, "t2").ParentID; got != "t1" {
		t.Fatalf("tab nested t2 under %q", got)
	}
	press(t, a, "shift+tab")
	if got := stored(t, a, "t2"); got.ParentID != "" || got.ProjectID != "matomeru" {
		t.Fatalf("shift+tab: %+v", got)
	}
}

// n (the key map's new; a is retired) on a project row adds a task in
// that project.
func TestAllViewNAddsATaskInTheProject(t *testing.T) {
	a := allOutline(t, projectNode("ochazuke"))
	press(t, a, "n")
	typeText(t, a, "tag a release")
	press(t, a, "esc")
	got := taskTitled(tasksOf(t, a, "ochazuke"), "tag a release")
	if got.ID == "" || got.ParentID != "" {
		t.Fatalf("a under ochazuke: %+v", tasksOf(t, a, "ochazuke"))
	}
	// o on that task adds its sibling, in the same project.
	press(t, a, "o")
	typeText(t, a, "write notes")
	press(t, a, "esc")
	if got := taskTitled(tasksOf(t, a, "ochazuke"), "write notes"); got.ID == "" {
		t.Fatalf("o under the task: %+v", tasksOf(t, a, "ochazuke"))
	}
}

// Moving a task across a project boundary would refile it. ochazuke's
// outline moves (J/K swap siblings, tab/shift+tab nest under the sibling
// above or follow the parent) never leave the parent's children, so no
// key crosses a project boundary yet: a gap in the library.
func TestOutlineMovesATaskAcrossAProjectBoundary(t *testing.T) {
	t.Skip("gap: ochazuke's outline has no move across parents (J past the last sibling); see the report")
	a := allOutline(t, outlineNode("t2"))
	press(t, a, "J")
	if got := stored(t, a, "t2"); got.ProjectID != "" {
		t.Fatalf("J past matomeru's last task: %+v", got)
	}
}

// The pin's outline (T) and O on a project show the project as the root
// row with its tasks under it, and no other project; typing on it renames
// the project, u undoes it, and o on it adds a task.
func TestPinnedOutlineRootRenamesTheProject(t *testing.T) {
	a := pinned(t)
	press(t, a, "T")
	if a.r.Focus != outlineNode("t1") {
		t.Fatalf("focus %s", a.r.Focus)
	}
	if roots := rootRows(a); !slices.Equal(roots, []string{projectNode("matomeru")}) {
		t.Fatalf("roots %v", roots)
	}
	if f := frameAt(a, 80); !strings.Contains(f, "matomeru") || !strings.Contains(f, "top: the project") {
		t.Fatalf("root row or hint missing:\n%s", f)
	}
	press(t, a, "k")
	if row := a.r.Instances[a.r.Focus]; a.r.Focus != projectNode("matomeru") || row.Noun != outlineRootNoun {
		t.Fatalf("k: focus %s", a.r.Focus)
	}
	press(t, a, "i", "end")
	typeText(t, a, " app")
	press(t, a, "esc")
	if p, _ := a.work.Project("matomeru"); p.Name != "matomeru app" {
		t.Fatalf("rename: %+v", p)
	}
	press(t, a, "u")
	if p, _ := a.work.Project("matomeru"); p.Name != "matomeru" {
		t.Fatalf("u: %+v", p)
	}
	press(t, a, "o")
	typeText(t, a, "ship it")
	press(t, a, "esc")
	if got := taskTitled(tasksOf(t, a, "matomeru"), "ship it"); got.ID == "" || got.ParentID != "" {
		t.Fatalf("o on the root: %+v", tasksOf(t, a, "matomeru"))
	}
	press(t, a, "T")

	// O on a project row opens the same outline.
	b := open(t)
	press(t, b, "O")
	if roots := rootRows(b); !slices.Equal(roots, []string{projectNode("ochazuke")}) {
		t.Fatalf("O roots %v", roots)
	}
	// No pin, no project: the root is "no project", not editable.
	c := open(t)
	if err := c.work.PutTask(work.Task{ID: "loose", Title: "loose end", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	c.showOutline(outlineScope{}, "")
	if roots := rootRows(c); !slices.Equal(roots, []string{projectNode("")}) {
		t.Fatalf("no project roots %v", roots)
	}
	focusRow(t, c, projectNode(""))
	refused(t, c, "i", "not a project")
}
