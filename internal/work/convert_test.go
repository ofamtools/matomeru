package work_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

type fixture struct {
	t  *testing.T
	db *store.Store
	w  *work.Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contextitems.Open(db); err != nil {
		t.Fatal(err)
	}
	if _, err := actionitems.Open(db); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Open(db, nil); err != nil {
		t.Fatal(err)
	}
	return fixture{t, db, w}
}

func (f fixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

// raw writes a row as JSON straight into a table.
func (f fixture) raw(table, id string, row any) {
	f.t.Helper()
	f.must(f.db.Tx([]string{table}, func(tx *store.Tx) error { return tx.Put(table, id, row) }))
}

// get reads one row as a generic map ("" fields absent read as "").
func (f fixture) get(table, id string) map[string]any {
	f.t.Helper()
	row := map[string]any{}
	if err := f.db.Get(table, id, &row); err != nil {
		f.t.Fatalf("%s/%s: %v", table, id, err)
	}
	return row
}

func (f fixture) gone(table, id string) {
	f.t.Helper()
	var row map[string]any
	if err := f.db.Get(table, id, &row); !errors.Is(err, store.ErrNotFound) {
		f.t.Fatalf("%s/%s still stored (err %v)", table, id, err)
	}
}

// snapshot is every row of every declared table, decoded, for comparing
// a database before and after.
func (f fixture) snapshot() map[string]map[string]any {
	f.t.Helper()
	tables, err := f.db.Tables()
	f.must(err)
	out := map[string]map[string]any{}
	for _, table := range tables {
		rows := map[string]any{}
		f.must(f.db.View([]string{table.Name}, func(tx *store.Tx) error {
			entries, err := tx.Entries(table.Name)
			for _, e := range entries {
				var v any
				if err := json.Unmarshal(e.Row, &v); err != nil {
					return err
				}
				rows[e.ID] = v
			}
			return err
		}))
		out[table.Name] = rows
	}
	return out
}

func owner(row map[string]any) [2]string {
	p, _ := row["project_id"].(string)
	t, _ := row["task_id"].(string)
	return [2]string{p, t}
}

// seedDemote is two projects: "parent" (one task, a directory) and
// "child" below it, with nested tasks, a note, a pane, a session, a
// context item, an inbox item, directories (one the parent's too) and the
// pin.
func seedDemote(f fixture) {
	f.must(f.w.PutProject(work.Project{ID: "parent", Name: "Parent", Status: "open", Created: 100, Trees: []work.Tree{{Kind: "directory", Path: "/src/shared"}}, DefaultTree: "/src/shared"}))
	f.must(f.w.PutProject(work.Project{ID: "child", Name: "Child", Status: "open", Created: 200, Trees: []work.Tree{{Kind: "directory", Path: "/src/child"}, {Kind: "directory", Path: "/src/shared/"}}, DefaultTree: "/src/child"}))
	f.must(f.w.PutTask(work.Task{ID: "p1", ProjectID: "parent", Title: "parent's", Position: 4}))
	f.must(f.w.PutTask(work.Task{ID: "c1", ProjectID: "child", Title: "one", Position: 1}))
	f.must(f.w.PutTask(work.Task{ID: "c2", ProjectID: "child", Title: "two", Position: 2}))
	f.must(f.w.PutTask(work.Task{ID: "c1a", ProjectID: "child", ParentID: "c1", Title: "one a", Position: 1}))
	f.must(f.w.PutTask(work.Task{ID: "legacy", ParentID: "c1a", Title: "no project of its own", Position: 1}))
	f.must(f.w.PutNote(work.Note{ID: "n-project", ProjectID: "child", Text: "on the project"}))
	f.must(f.w.PutNote(work.Note{ID: "n-task", ProjectID: "child", TaskID: "c2", Text: "on a task"}))
	f.must(f.w.SetPaneOwner(work.Assignment{Window: "w1", Session: "s1", ProjectID: "child"}))
	f.must(f.w.SetSessionOwner(work.SessionOwner{ID: "s1", Name: "one", ProjectID: "child"}))
	f.raw("context_items", "ctx", map[string]any{"id": "ctx", "kind": "link", "text": "https://x", "project_id": "child", "task_id": ""})
	f.raw("action_items", "act", map[string]any{"id": "act", "service": "manual", "title": "look", "project_id": "child"})
	f.must(f.w.SetPin("child"))
	f.must(f.w.PutTask(work.Task{ID: "other", ProjectID: "parent", ParentID: "p1", Title: "untouched"}))
}

func TestDemoteProjectMovesEverythingThenDeletesIt(t *testing.T) {
	f := newFixture(t)
	seedDemote(f)
	conv, err := f.w.DemoteProject("child", "")
	f.must(err)
	if conv.Project != "parent" || conv.From != "child" || conv.Task == "" {
		t.Fatalf("conversion = %+v", conv)
	}
	f.gone("projects", "child")
	task, err := f.w.Task(conv.Task)
	f.must(err)
	if task.Title != "Child" || task.ProjectID != "parent" || task.ParentID != "" || task.Position != 5 {
		t.Fatalf("new task = %+v", task)
	}
	for id, parent := range map[string]string{"c1": conv.Task, "c2": conv.Task, "c1a": "c1", "legacy": "c1a"} {
		got, err := f.w.Task(id)
		f.must(err)
		if got.ParentID != parent {
			t.Fatalf("%s parent = %q, want %q", id, got.ParentID, parent)
		}
		if id != "legacy" && got.ProjectID != "parent" {
			t.Fatalf("%s project = %q", id, got.ProjectID)
		}
	}
	c1, _ := f.w.Task("c1")
	c2, _ := f.w.Task("c2")
	if c1.Position >= c2.Position {
		t.Fatalf("order lost: c1 %d, c2 %d", c1.Position, c2.Position)
	}
	want := [2]string{"parent", conv.Task}
	for _, r := range [][2]string{{"notes", "n-project"}, {"assignments", "w1"}, {"session_owners", "s1"}, {"context_items", "ctx"}, {"action_items", "act"}} {
		if got := owner(f.get(r[0], r[1])); got != want {
			t.Fatalf("%s owner = %v, want %v", r, got, want)
		}
	}
	if got := owner(f.get("notes", "n-task")); got != [2]string{"parent", "c2"} {
		t.Fatalf("task note owner = %v", got)
	}
	pane, err := f.w.PaneOwner("w2", "s1") // no assignment: inherits the session
	f.must(err)
	if pane.ProjectID != "parent" || pane.TaskID != conv.Task {
		t.Fatalf("inherited owner = %+v", pane)
	}
	p, err := f.w.Project("parent")
	f.must(err)
	var paths []string
	for _, tr := range p.Trees {
		paths = append(paths, tr.Path)
	}
	if !slices.Equal(paths, []string{"/src/shared", "/src/child"}) || p.DefaultTree != "/src/shared" {
		t.Fatalf("parent dirs = %v default %q", paths, p.DefaultTree)
	}
	pin, err := f.w.Pin()
	f.must(err)
	at, err := f.w.PinnedAt()
	f.must(err)
	if _, ok := at["child"]; pin != "parent" || ok || at["parent"] == 0 {
		t.Fatalf("pin = %q, pinned_at %v", pin, at)
	}
}

func TestDemoteFirstProjectRefused(t *testing.T) {
	f := newFixture(t)
	seedDemote(f)
	before := f.snapshot()
	if _, err := f.w.DemoteProject("parent", ""); !errors.Is(err, work.ErrNoProjectAbove) {
		t.Fatalf("err = %v, want ErrNoProjectAbove", err)
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("a refused demote changed the store")
	}
}

func TestConversionRollsBackOnFailure(t *testing.T) {
	f := newFixture(t)
	seedDemote(f)
	before := f.snapshot()
	for _, fail := range []string{"projects", "action_items", "assignments"} {
		work.SetConvertFault(func(table, id string) error {
			if table == fail {
				return errors.New("forced")
			}
			return nil
		})
		if _, err := f.w.DemoteProject("child", ""); err == nil {
			t.Fatalf("demote with %s failing succeeded", fail)
		}
		work.SetConvertFault(nil)
		if !reflect.DeepEqual(before, f.snapshot()) {
			t.Fatalf("a failed conversion (%s) wrote rows", fail)
		}
	}
	// A promote failing at its last step (deleting the task) writes nothing.
	work.SetConvertFault(func(table, id string) error {
		if table == "tasks" && id == "c1" {
			return errors.New("forced")
		}
		return nil
	})
	defer work.SetConvertFault(nil)
	if _, err := f.w.PromoteTask("c1"); err == nil {
		t.Fatal("promote with its delete failing succeeded")
	}
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("a failed promote wrote rows")
	}
}

func TestDemoteUndoRedoRestoresExactly(t *testing.T) {
	f := newFixture(t)
	seedDemote(f)
	before := f.snapshot()
	conv, err := f.w.DemoteProject("child", "")
	f.must(err)
	after := f.snapshot()
	f.must(f.w.Undo(conv))
	if got := f.snapshot(); !reflect.DeepEqual(before, got) {
		t.Fatalf("undo did not restore the store:\nbefore %v\ngot    %v", before, got)
	}
	f.must(f.w.Redo(conv))
	if got := f.snapshot(); !reflect.DeepEqual(after, got) {
		t.Fatal("redo did not write the conversion again")
	}
	f.must(f.w.Undo(conv))
	if got := owner(f.get("assignments", "w1")); got != [2]string{"child", ""} {
		t.Fatalf("pane owner after undo = %v", got)
	}
}

func TestUndoKeepsAnUnrelatedLaterEdit(t *testing.T) {
	f := newFixture(t)
	seedDemote(f)
	conv, err := f.w.DemoteProject("child", "")
	f.must(err)
	n := f.get("notes", "n-project")
	n["text"] = "edited after"
	f.raw("notes", "n-project", n)
	f.must(f.w.Undo(conv))
	got := f.get("notes", "n-project")
	if got["text"] != "edited after" || owner(got) != [2]string{"child", ""} {
		t.Fatalf("note after undo = %v", got)
	}
}

// seedPromote is a project with a top task "t" (a subtask tree under it)
// and the rows a task can own, with directories in every place the store
// keeps one.
func seedPromote(f fixture) {
	f.must(f.w.PutProject(work.Project{ID: "a", Name: "A", Created: 100, Trees: []work.Tree{{Kind: "directory", Path: "/src/a"}}, DefaultTree: "/src/a"}))
	f.must(f.w.PutProject(work.Project{ID: "b", Name: "B", Created: 300}))
	f.must(f.w.PutTask(work.Task{ID: "t", ProjectID: "a", Title: "Big task", Position: 1}))
	f.must(f.w.PutTask(work.Task{ID: "t1", ProjectID: "a", ParentID: "t", Title: "sub one", Position: 1}))
	f.must(f.w.PutTask(work.Task{ID: "t2", ProjectID: "a", ParentID: "t", Title: "sub two", Position: 2}))
	f.must(f.w.PutTask(work.Task{ID: "t1x", ProjectID: "a", ParentID: "t1", Title: "deep", Position: 1}))
	f.must(f.w.PutTask(work.Task{ID: "stay", ProjectID: "a", Title: "sibling", Position: 2}))
	f.must(f.w.PutNote(work.Note{ID: "n", ProjectID: "a", TaskID: "t", Text: "task note"}))
	f.must(f.w.PutNote(work.Note{ID: "n1", ProjectID: "a", TaskID: "t1", Text: "sub note"}))
	f.must(f.w.PutNote(work.Note{ID: "na", ProjectID: "a", Text: "project note"}))
	f.must(f.w.SetPaneOwner(work.Assignment{Window: "w1", Session: "s1", ProjectID: "a", TaskID: "t"}))
	f.must(f.w.PutPaneRecipe(work.PaneRecipe{Window: "w1", Cwd: "/work/pane", ProjectID: "a", Updated: 50}))
	f.must(f.w.PutPaneRecipe(work.PaneRecipe{Window: "w2", Cwd: "/work/sub-pane", ProjectID: "a", TaskID: "t1", Updated: 60}))
	_, err := f.w.PutSavedPane(work.SavedPane{ID: "sv", PaneRecipe: work.PaneRecipe{Window: "w9", Cwd: "/work/saved", ProjectID: "a", TaskID: "t"}})
	f.must(err)
	f.must(f.w.PutVimSession(work.VimSession{ID: "vim", ProjectID: "a", TaskID: "t", Cwd: "/work/vim/"}))
	f.raw("context_items", "cdir", map[string]any{"id": "cdir", "kind": "dir", "text": "/work/ctx-dir", "project_id": "a", "task_id": "t", "sources": []map[string]any{{"kind": "screen", "cwd": "/work/seen"}}})
	f.raw("context_items", "cfile", map[string]any{"id": "cfile", "kind": "file", "text": "/work/ctx-dir/main.go", "project_id": "a", "task_id": "t"})
	f.raw("action_items", "act", map[string]any{"id": "act", "service": "manual", "title": "do", "project_id": "a", "task_id": "t"})
	f.raw("history", "h1", map[string]any{"id": "h1", "kind": "turn", "at": 10, "task_id": "t", "project_id": "a", "cwd": "/work/turn", "summary": "", "detail": "", "provenance": map[string]any{"at": 10}})
	f.raw("history", "h2", map[string]any{"id": "h2", "kind": "turn", "at": 11, "cwd": "/work/pane", "summary": "", "detail": "", "provenance": map[string]any{"task_id": "t", "at": 11}})
}

func TestPromoteTaskBecomesProjectWithItsDirectories(t *testing.T) {
	f := newFixture(t)
	seedPromote(f)
	conv, err := f.w.PromoteTask("t")
	f.must(err)
	f.gone("tasks", "t")
	p, err := f.w.Project(conv.Project)
	f.must(err)
	if p.Name != "Big task" || p.Status != "open" {
		t.Fatalf("project = %+v", p)
	}
	projects, err := f.w.Projects()
	f.must(err)
	var order []string
	for _, q := range projects {
		order = append(order, q.ID)
	}
	if !slices.Equal(order, []string{"a", conv.Project, "b"}) {
		t.Fatalf("project order = %v", order)
	}
	var paths []string
	for _, tr := range p.Trees {
		paths = append(paths, tr.Path)
	}
	wantDirs := []string{"/work/pane", "/work/sub-pane", "/work/saved", "/work/vim", "/work/ctx-dir", "/work/seen", "/work/turn"}
	if !slices.Equal(paths, wantDirs) || p.DefaultTree != "/work/pane" {
		t.Fatalf("dirs = %v default %q, want %v", paths, p.DefaultTree, wantDirs)
	}
	if !slices.Equal(conv.Directories, wantDirs) {
		t.Fatalf("conversion dirs = %v", conv.Directories)
	}
	a, _ := f.w.Project("a")
	if len(a.Trees) != 1 || a.DefaultTree != "/src/a" {
		t.Fatalf("source project dirs changed: %+v", a)
	}
	for id, parent := range map[string]string{"t1": "", "t2": "", "t1x": "t1"} {
		got, _ := f.w.Task(id)
		if got.ProjectID != conv.Project || got.ParentID != parent {
			t.Fatalf("%s = %+v", id, got)
		}
	}
	if got, _ := f.w.Task("stay"); got.ProjectID != "a" {
		t.Fatalf("sibling moved: %+v", got)
	}
	alone := [2]string{conv.Project, ""}
	for _, r := range [][2]string{{"notes", "n"}, {"assignments", "w1"}, {"saved_panes", "sv"}, {"vim_sessions", "vim"}, {"context_items", "cdir"}, {"context_items", "cfile"}, {"action_items", "act"}} {
		if got := owner(f.get(r[0], r[1])); got != alone {
			t.Fatalf("%v owner = %v, want %v", r, got, alone)
		}
	}
	if got := owner(f.get("notes", "n1")); got != [2]string{conv.Project, "t1"} {
		t.Fatalf("subtask note = %v", got)
	}
	if got := owner(f.get("notes", "na")); got != [2]string{"a", ""} {
		t.Fatalf("project note moved: %v", got)
	}
	if got := owner(f.get("history", "h1")); got != [2]string{"a", "t"} {
		t.Fatalf("journal entry rewritten: %v", got)
	}
}

func TestPromoteWithoutDirectoriesAndDeepTaskRefused(t *testing.T) {
	f := newFixture(t)
	f.must(f.w.PutProject(work.Project{ID: "a", Created: 1, Trees: []work.Tree{{Path: "/src/a"}}}))
	f.must(f.w.PutTask(work.Task{ID: "t", ProjectID: "a", Title: "bare"}))
	f.must(f.w.PutTask(work.Task{ID: "deep", ProjectID: "a", ParentID: "t"}))
	if _, err := f.w.PromoteTask("deep"); !errors.Is(err, work.ErrNotTopTask) {
		t.Fatalf("deep promote err = %v", err)
	}
	conv, err := f.w.PromoteTask("t")
	f.must(err)
	p, _ := f.w.Project(conv.Project)
	if len(p.Trees) != 0 || p.DefaultTree != "" {
		t.Fatalf("promoted project copied directories: %+v", p)
	}
}

func TestPromoteUndoRedoRestoresExactly(t *testing.T) {
	f := newFixture(t)
	seedPromote(f)
	before := f.snapshot()
	conv, err := f.w.PromoteTask("t")
	f.must(err)
	after := f.snapshot()
	f.must(f.w.Undo(conv))
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("undo did not restore the store")
	}
	f.must(f.w.Redo(conv))
	if !reflect.DeepEqual(after, f.snapshot()) {
		t.Fatal("redo did not write the conversion again")
	}
}

func TestPromoteAdjacentCreationTimesStillListsAfter(t *testing.T) {
	f := newFixture(t)
	f.must(f.w.PutProject(work.Project{ID: "5", Created: 100}))
	f.must(f.w.PutProject(work.Project{ID: "e", Created: 101}))
	f.must(f.w.PutTask(work.Task{ID: "t", ProjectID: "5"}))
	conv, err := f.w.PromoteTask("t")
	f.must(err)
	projects, _ := f.w.Projects()
	var order []string
	for _, q := range projects {
		order = append(order, q.ID)
	}
	if !slices.Equal(order, []string{"5", conv.Project, "e"}) {
		t.Fatalf("order = %v", order)
	}
}
