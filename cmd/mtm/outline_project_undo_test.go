package main

import (
	"reflect"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// Deleting an empty project's row in the all view (the tree's own delete,
// as an agent or the palette asks it) and undoing it brings the project
// back as it was: every stored field, and the note the delete detached is
// the project's again.
func TestOutlineUndoneProjectRowDeleteRestoresTheProject(t *testing.T) {
	t.Parallel()
	a := open(t)
	was := work.Project{
		ID: "empty", Name: "empty one", Status: "paused", Created: 1700000000,
		Theme:    work.Theme{Color: "palette.3", Glyph: "◇"},
		Repo:     "/tmp/empty-repo",
		Settings: work.ProjectSettings{Harness: "codex", Preset: "fast", Privacy: "family"},
		Origin:   "o-proj", Priority: "P1",
	}
	if err := a.work.PutProject(was); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutNote(work.Note{ID: "n-empty", Text: "about empty", ProjectID: "empty"}); err != nil {
		t.Fatal(err)
	}
	stored, err := a.work.Project("empty")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "T")
	focusRow(t, a, projectNode("empty"))
	if _, err := a.r.Execute(ochazuke.Request{Noun: "ochazuke.outline", Verb: "delete", Target: projectNode("empty")}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.work.Project("empty"); err == nil {
		t.Fatal("the delete kept the project")
	}
	press(t, a, "u")
	back, err := a.work.Project("empty")
	if err != nil {
		t.Fatalf("u did not bring the project back: %v", err)
	}
	back.Trees, back.DefaultTree, stored.Trees, stored.DefaultTree = nil, "", nil, ""
	if !reflect.DeepEqual(back, stored) {
		t.Fatalf("after u the project is\n %+v\nwant\n %+v", back, stored)
	}
	if n, err := a.work.Note("n-empty"); err != nil || n.ProjectID != "empty" || n.FormerProjectID != "" {
		t.Fatalf("after u the note is %+v %v", n, err)
	}
}
