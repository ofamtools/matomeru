package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

func TestEmptyRecordsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := w.AddProject("")
	if err != nil {
		t.Fatal(err)
	}
	task, err := w.AddTask("", "")
	if err != nil {
		t.Fatal(err)
	}
	named, err := w.AddTask(p.ID, "title")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Retitle(named.ID, ""); err != nil {
		t.Fatal(err)
	}
	task.FormerProjectID, task.FormerTaskID = "old-project", "old-task"
	if err := w.PutTask(task); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := w.Projects()
	if err != nil || len(projects) != 1 || projects[0].ID != p.ID || projects[0].Name != "" {
		t.Fatalf("projects: %+v, %v", projects, err)
	}
	got, err := w.Task(task.ID)
	if err != nil || got.ProjectID != "" || got.Title != "" || got.FormerProjectID != "old-project" || got.FormerTaskID != "old-task" {
		t.Fatalf("unassociated task: %+v, %v", got, err)
	}
	got, err = w.Task(named.ID)
	if err != nil || got.ProjectID != p.ID || got.Title != "" {
		t.Fatalf("retitled task: %+v, %v", got, err)
	}
}

func TestDirectoryRecencyAccumulatesCleanPaths(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	before := Now()
	for _, path := range []string{"/tmp/a/../repo", "/tmp/repo", ""} {
		if err := w.TouchDir(path); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := w.RecentDirs()
	if err != nil || len(rows) != 1 || rows[0].Path != "/tmp/repo" || rows[0].Uses != 2 || rows[0].LastUsed < before || rows[0].LastUsed > Now() {
		t.Fatalf("recency: %+v, %v", rows, err)
	}
}
