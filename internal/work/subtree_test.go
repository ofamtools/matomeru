package work

import (
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/store"
)

// subtreeStore is a store with a task tree in project A: root over kid1
// (over grand) and kid2; other is a top task in B.
func subtreeStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Project{{ID: "A"}, {ID: "B"}} {
		if err := s.PutProject(p); err != nil {
			t.Fatal(err)
		}
	}
	for i, task := range []Task{
		{ID: "root", ProjectID: "A"},
		{ID: "kid1", ProjectID: "A", ParentID: "root"},
		{ID: "kid2", ProjectID: "A", ParentID: "root"},
		{ID: "grand", ProjectID: "A", ParentID: "kid1"},
		{ID: "other", ProjectID: "B"},
	} {
		task.Position = int64(i + 1)
		if err := s.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	return s, db
}

func projectsOf(t *testing.T, s *Store, ids ...string) []string {
	t.Helper()
	var out []string
	for _, id := range ids {
		task, err := s.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, task.ProjectID)
	}
	return out
}

func TestSubtreeIsTheTaskThenEachLevel(t *testing.T) {
	s, _ := subtreeStore(t)
	got, err := s.Subtree("root")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, task := range got {
		ids = append(ids, task.ID)
	}
	if !slices.Equal(ids, []string{"root", "kid1", "kid2", "grand"}) {
		t.Fatalf("subtree %v", ids)
	}
	if _, err := s.Subtree("nope"); err == nil {
		t.Fatal("subtree of a missing task")
	}
}

// A move takes the grandchildren; moving back (an undo's write) takes
// them back; parents stay.
func TestMoveSubtreeTakesGrandchildrenAndMovesBack(t *testing.T) {
	s, _ := subtreeStore(t)
	moved, moves, err := s.MoveSubtree("kid1", "B")
	if err != nil || moves != 1 || moved.ParentID != "" || moved.ProjectID != "B" {
		t.Fatalf("move kid1: %+v %d %v", moved, moves, err)
	}
	if got := projectsOf(t, s, "root", "kid1", "kid2", "grand"); !slices.Equal(got, []string{"A", "B", "A", "B"}) {
		t.Fatalf("after kid1 to B: %v", got)
	}
	if g, _ := s.Task("grand"); g.ParentID != "kid1" {
		t.Fatalf("grand's parent: %+v", g)
	}
	// The undo's write: the old parent and project, through PutTaskSubtree.
	kid1, _ := s.Task("kid1")
	kid1.ProjectID, kid1.ParentID = "A", "root"
	if n, err := s.PutTaskSubtree(kid1); err != nil || n != 1 {
		t.Fatalf("back: %d %v", n, err)
	}
	if got := projectsOf(t, s, "root", "kid1", "kid2", "grand"); !slices.Equal(got, []string{"A", "A", "A", "A"}) {
		t.Fatalf("after back: %v", got)
	}
	// Record first: a move to no project is unassociated, subtree too.
	if _, n, err := s.MoveSubtree("root", ""); err != nil || n != 3 {
		t.Fatalf("unassociate: %d %v", n, err)
	}
	if got := projectsOf(t, s, "root", "kid1", "kid2", "grand", "other"); !slices.Equal(got, []string{"", "", "", "", "B"}) {
		t.Fatalf("unassociated: %v", got)
	}
}

func TestAssociateTaskTakesTheSubtreeToTheTopLevel(t *testing.T) {
	s, _ := subtreeStore(t)
	got, err := s.AssociateTask("kid1", "A")
	if err != nil || got.ParentID != "" || got.ProjectID != "A" {
		t.Fatalf("associate within A: %+v %v", got, err)
	}
	if _, err := s.AssociateTask("root", "B"); err != nil {
		t.Fatal(err)
	}
	if got := projectsOf(t, s, "root", "kid1", "kid2", "grand"); !slices.Equal(got, []string{"B", "A", "B", "A"}) {
		t.Fatalf("associate root to B (kid1 left before): %v", got)
	}
}

// The repair gives every task its top task's project, writes nothing a
// second time, and runs quietly on open.
func TestRepairSubtreeProjectsIsIdempotent(t *testing.T) {
	s, db := subtreeStore(t)
	if n, err := s.RepairSubtreeProjects(); err != nil || n != 0 {
		t.Fatalf("clean store: %d %v", n, err)
	}
	// Strays a move before this lane left: a subtree in the old project,
	// a legacy subtask with no project, a missing parent, a loop.
	for _, task := range []Task{
		{ID: "kid2", ProjectID: "B", ParentID: "root"},
		{ID: "grand", ProjectID: "", ParentID: "kid1"},
		{ID: "orphan", ProjectID: "B", ParentID: "gone"},
		{ID: "loopA", ProjectID: "A", ParentID: "loopB"},
		{ID: "loopB", ProjectID: "B", ParentID: "loopA"},
	} {
		if err := s.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.Task("kid1")
	if n, err := s.RepairSubtreeProjects(); err != nil || n != 2 {
		t.Fatalf("repair: %d %v", n, err)
	}
	if got := projectsOf(t, s, "root", "kid1", "kid2", "grand", "orphan", "loopA", "loopB"); !slices.Equal(got, []string{"A", "A", "A", "A", "B", "A", "B"}) {
		t.Fatalf("repaired: %v", got)
	}
	if after, _ := s.Task("kid1"); after != before {
		t.Fatalf("a task that agreed was rewritten: %+v", after)
	}
	if n, err := s.RepairSubtreeProjects(); err != nil || n != 0 {
		t.Fatalf("second repair: %d %v", n, err)
	}
	// On open: a stray written behind the store's back rejoins its parent.
	if err := s.PutTask(Task{ID: "kid2", ProjectID: "B", ParentID: "root"}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := projectsOf(t, s2, "kid2"); got[0] != "A" {
		t.Fatalf("open repairs: %v", got)
	}
}
