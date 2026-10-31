package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

func TestDeleteOrphansWithoutLosingRecords(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(w.PutProject(Project{ID: "a"}))
	must(w.PutTask(Task{ID: "task", ProjectID: "a"}))
	must(w.PutTask(Task{ID: "child", ParentID: "task", Title: "keep me"}))
	must(w.PutNote(Note{ID: "note", TaskID: "task", Text: "keep text"}))
	must(w.SetPaneOwner(Assignment{Window: "pane", TaskID: "task"}))
	must(w.SetSessionOwner(SessionOwner{ID: "session", ProjectID: "a"}))
	must(w.SetPin("a"))
	_, err = w.AddReview(Review{ProjectID: "a", TaskID: "task", Session: "session", Window: "pane", Output: "history"})
	must(err)
	want := DeleteCounts{Tasks: 1, Notes: 1, Assignments: 1, Reviews: 1}
	impact, err := w.DeletionImpact("task", "task")
	must(err)
	if impact != want {
		t.Fatalf("impact = %+v, want %+v", impact, want)
	}
	_, err = w.Task("task")
	must(err)
	got, err := w.Delete("task", "task")
	must(err)
	if got != want {
		t.Fatalf("deleted impact = %+v", got)
	}
	note, err := w.Note("note")
	must(err)
	if note.TaskID != "" || note.FormerTaskID != "task" || note.ProjectID != "a" || note.Text != "keep text" {
		t.Fatalf("note = %+v", note)
	}
	child, err := w.Task("child")
	must(err)
	if child.ParentID != "" || child.FormerTaskID != "task" || child.ProjectID != "a" || child.Title != "keep me" {
		t.Fatalf("child = %+v", child)
	}
	want = DeleteCounts{Tasks: 1, Notes: 1, Assignments: 1, Sessions: 1, Reviews: 1}
	got, err = w.Delete("project", "a")
	must(err)
	if got != want {
		t.Fatalf("project impact = %+v", got)
	}
	note, err = w.Note("note")
	must(err)
	if note.ProjectID != "" || note.FormerProjectID != "a" || note.FormerTaskID != "task" {
		t.Fatalf("orphan = %+v", note)
	}
	owner, err := w.PaneOwner("pane", "session")
	must(err)
	if owner.ProjectID != "" || owner.TaskID != "" || owner.FormerProjectID != "a" || owner.FormerTaskID != "task" {
		t.Fatalf("pane = %+v", owner)
	}
	session, err := w.SessionOwner("session")
	must(err)
	if session.ProjectID != "" || session.FormerProjectID != "a" {
		t.Fatalf("session = %+v", session)
	}
	pin, err := w.Pin()
	must(err)
	if pin != "" {
		t.Fatalf("pin = %q", pin)
	}
	reviews, err := w.Reviews()
	must(err)
	if len(reviews) != 1 || reviews[0].FormerProjectID != "a" || reviews[0].FormerTaskID != "task" || reviews[0].Output != "history" {
		t.Fatalf("reviews = %+v", reviews)
	}
}

func TestDeleteOrphansSavedLaunchesAndVimSessions(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutProject(Project{ID: "project"}); err != nil {
		t.Fatal(err)
	}
	if err := w.PutTask(Task{ID: "task", ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	tables := []string{"saved_panes", "pane_recipes", "vim_sessions"}
	if err := db.Tx(tables, func(tx *store.Tx) error {
		for _, table := range tables {
			if err := tx.Put(table, "record", map[string]any{
				"id": "record", "window": "record", "task_id": "task",
				"path": "/keep/session.vim", "command": []string{"sh", "-i"},
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	counts, err := w.Delete("task", "task")
	if err != nil || counts.Saved != 1 || counts.Vim != 1 {
		t.Fatalf("task deletion: %+v, %v", counts, err)
	}
	counts, err = w.Delete("project", "project")
	if err != nil || counts.Saved != 1 || counts.Vim != 1 {
		t.Fatalf("project deletion: %+v, %v", counts, err)
	}
	for _, table := range tables {
		var row struct {
			ProjectID       string   `json:"project_id"`
			TaskID          string   `json:"task_id"`
			FormerProjectID string   `json:"former_project_id"`
			FormerTaskID    string   `json:"former_task_id"`
			Path            string   `json:"path"`
			Command         []string `json:"command"`
		}
		if err := db.Get(table, "record", &row); err != nil {
			t.Fatal(err)
		}
		if row.ProjectID != "" || row.TaskID != "" || row.FormerProjectID != "project" || row.FormerTaskID != "task" ||
			row.Path != "/keep/session.vim" || len(row.Command) != 2 || row.Command[0] != "sh" || row.Command[1] != "-i" {
			t.Fatalf("%s lost record or ownership history: %+v", table, row)
		}
	}
}
