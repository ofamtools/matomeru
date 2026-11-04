package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// withKid is the project outline of matomeru with a subtask "kid" under
// t1, tree.delete-children set to mode, the focus on t1.
func withKid(t *testing.T, mode string) *App {
	t.Helper()
	a := open(t)
	if err := a.r.Settings().Set("tree.delete-children", mode); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutTask(work.Task{ID: "kid", ProjectID: "matomeru", ParentID: "t1", Title: "kid", Position: 1, Status: work.StatusActive}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "j", "O")
	focusRow(t, a, outlineNode("t1"))
	return a
}

// agree checks the outline's task rows are exactly the project's stored
// tasks, each under the parent it is stored with.
func agree(t *testing.T, a *App, when string) {
	t.Helper()
	var rows []string
	for _, n := range a.r.Tree(outlineTree).Nodes() {
		if n.Parent == "" {
			continue
		}
		parent := taskOf(n.Parent)
		if strings.HasPrefix(n.Parent, projectNodePrefix) {
			parent = ""
		}
		rows = append(rows, taskOf(n.ID)+"<"+parent)
	}
	var stored []string
	for _, x := range tasksOf(t, a, "matomeru") {
		stored = append(stored, x.ID+"<"+x.ParentID)
	}
	slices.Sort(rows)
	slices.Sort(stored)
	if !slices.Equal(rows, stored) {
		t.Fatalf("%s: screen %v, store %v", when, rows, stored)
	}
}

// Each value of tree.delete-children through the store, and u: promote
// moves the kid up beside the deleted row, orphan to the project's level
// (a task stays under a project), subtree deletes it; the screen and the
// store agree each time.
func TestOutlineDeleteChildrenSetting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode string
		kid  string // the kid's stored parent after d d, "-" for gone
	}{{"promote", ""}, {"orphan", ""}, {"subtree", "-"}} {
		a := withKid(t, tc.mode)
		press(t, a, "d", "d")
		if _, err := a.work.Task("t1"); err == nil {
			t.Fatalf("%s: d d kept t1", tc.mode)
		}
		kid, err := a.work.Task("kid")
		switch {
		case tc.kid == "-" && err == nil:
			t.Fatalf("%s: the kid survived the subtree delete: %+v", tc.mode, kid)
		case tc.kid != "-" && (err != nil || kid.ParentID != tc.kid || kid.ProjectID != "matomeru" || kid.Status != work.StatusActive):
			t.Fatalf("%s: kid %+v %v", tc.mode, kid, err)
		}
		agree(t, a, tc.mode+" after d d")
		press(t, a, "u")
		back, err := a.work.Task("t1")
		if err != nil || back.Title != "write the brief" {
			t.Fatalf("%s: u did not restore t1: %+v %v", tc.mode, back, err)
		}
		kid, err = a.work.Task("kid")
		if err != nil || kid.ParentID != "t1" || kid.Status != work.StatusActive {
			t.Fatalf("%s: after u the kid is %+v %v", tc.mode, kid, err)
		}
		agree(t, a, tc.mode+" after u")
	}
}

// A move refused in a delete plan stops it: nothing is deleted, the rows
// are the store's, and u has nothing to undo. The kid would go to the top
// level, which a task may not.
func TestOutlineRefusedOrphanMoveStopsTheDelete(t *testing.T) {
	t.Parallel()
	a := withKid(t, "orphan")
	a.outline.tree.OrphanParent = func(ochazuke.TreeNode) string { return "" }
	a.m.Status = ""
	a.Update(keyMsg("d")) // the refusal is the key's error: press would fail on it
	a.Update(keyMsg("d"))
	if !strings.Contains(a.m.Status, "A task stays under a project") {
		t.Fatalf("status %q does not say why", a.m.Status)
	}
	if _, err := a.work.Task("t1"); err != nil {
		t.Fatalf("the delete went through: %v", err)
	}
	if kid, _ := a.work.Task("kid"); kid.ParentID != "t1" {
		t.Fatalf("kid %+v", kid)
	}
	agree(t, a, "after the refused delete")
	press(t, a, "u")
	if _, err := a.work.Task("t1"); err != nil {
		t.Fatalf("u deleted t1: %v", err)
	}
	agree(t, a, "after u")
}
