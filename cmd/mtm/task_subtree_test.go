package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
)

// addSubtree puts kid (with grand under it) under t1 in matomeru.
func addSubtree(t *testing.T, w *work.Store) {
	t.Helper()
	for _, task := range []work.Task{
		{ID: "kid", ProjectID: "matomeru", ParentID: "t1", Title: "kid", Status: work.StatusTodo},
		{ID: "grand", ProjectID: "matomeru", ParentID: "kid", Title: "grand", Status: work.StatusTodo},
	} {
		if err := w.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
}

func projectsOf(t *testing.T, a *App, ids ...string) string {
	t.Helper()
	var out []string
	for _, id := range ids {
		out = append(out, stored(t, a, id).ProjectID)
	}
	return strings.Join(out, " ")
}

// task.move (m's verb) takes the subtree to the project in one write; one
// undo takes every task back; set parent= into another project does too.
func TestTaskMoveTakesTheSubtreeAndOneUndoRestoresIt(t *testing.T) {
	t.Parallel()
	a := open(t)
	addSubtree(t, a.work)
	executeA(t, a, "task", "move", "", ochazuke.Arguments{"id": "t1", "to": "ochazuke"})
	if got := projectsOf(t, a, "t1", "kid", "grand", "t2"); got != "ochazuke ochazuke ochazuke matomeru" {
		t.Fatalf("move: %s", got)
	}
	if g := stored(t, a, "grand"); g.ParentID != "kid" {
		t.Fatalf("grand's parent: %+v", g)
	}
	executeA(t, a, "history", "undo", "", nil)
	if got := projectsOf(t, a, "t1", "kid", "grand"); got != "matomeru matomeru matomeru" {
		t.Fatalf("one undo: %s", got)
	}
	// A move of a subtask: it leaves its parent, its subtask with it.
	executeA(t, a, "task", "move", "", ochazuke.Arguments{"id": "kid", "to": "ochazuke"})
	if got := projectsOf(t, a, "t1", "kid", "grand"); got != "matomeru ochazuke ochazuke" || stored(t, a, "kid").ParentID != "" {
		t.Fatalf("move kid: %s %+v", got, stored(t, a, "kid"))
	}
	// Under a parent in matomeru again: its project follows, grand too.
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "kid", "prop": "parent", "value": "t2"})
	if got := projectsOf(t, a, "kid", "grand"); got != "matomeru matomeru" {
		t.Fatalf("set parent: %s", got)
	}
	// Record first: a move to no project leaves the subtree unassociated.
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t2", "prop": "project", "value": ""})
	if got := projectsOf(t, a, "t2", "kid", "grand"); got != "  " {
		t.Fatalf("unassociate: %q", got)
	}
}

// From the CLI on a temp database: task move takes the subtree.
func TestCLITaskMoveTakesTheSubtree(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	addSubtree(t, w)
	db.Close()
	var out, errOut bytes.Buffer
	args := []string{"--data", path, "--socket", filepath.Join(t.TempDir(), "none.sock")}
	if err := run(append(args, "task", "move", "--id", "t1", "--to", "ochazuke"), strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("task move: %v\n%s", err, errOut.String())
	}
	a := openAt(t, path)
	if got := projectsOf(t, a, "t1", "kid", "grand", "t2"); got != "ochazuke ochazuke ochazuke matomeru" {
		t.Fatalf("cli move: %s", got)
	}
}
