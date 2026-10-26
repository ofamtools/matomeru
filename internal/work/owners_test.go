package work

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

func TestOwnerImportAndExplicitUnassociation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range Tables {
		if err := db.Declare(table); err != nil {
			t.Fatal(err)
		}
	}
	legacy := json.RawMessage(`{"window":"legacy","session":"arbitrary","task_id":"child","project_id":"","assigned_at":123,"extension":"keep"}`)
	err = db.Tx([]string{"projects", "tasks", "active", "assignments", "reviews", "launch_state"}, func(tx *store.Tx) error {
		for _, p := range []Project{{ID: "A"}, {ID: "B"}} {
			if err := tx.Put("projects", p.ID, p); err != nil {
				return err
			}
		}
		for _, task := range []Task{{ID: "root", ProjectID: "A"}, {ID: "child", ParentID: "root"}} {
			if err := tx.Put("tasks", task.ID, task); err != nil {
				return err
			}
		}
		if err := tx.Put("active", "singleton", map[string]any{"task_id": "child", "tree": "keep"}); err != nil {
			return err
		}
		if err := tx.Put("launch_state", "last", map[string]string{"project_id": "B"}); err != nil {
			return err
		}
		if err := tx.Put("assignments", "legacy", legacy); err != nil {
			return err
		}
		for _, rv := range []Review{{ID: "r1", Window: "legacy", ProjectID: "B"}, {ID: "r2", Window: "imported", ProjectID: "B", Session: "random"}} {
			if err := tx.Put("reviews", rv.ID, rv); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if pin, err := w.Pin(); err != nil || pin != "A" {
		t.Fatalf("imported pin %q %v", pin, err)
	}
	var raw json.RawMessage
	if err := db.Get("assignments", "legacy", &raw); err != nil || string(raw) != string(legacy) {
		t.Fatalf("legacy changed: %s %v", raw, err)
	}
	if got, err := w.PaneOwner("legacy", ""); err != nil || got.ProjectID != "A" || got.TaskID != "child" {
		t.Fatalf("legacy owner %+v %v", got, err)
	}
	if got, err := w.PaneOwner("imported", ""); err != nil || got.ProjectID != "B" {
		t.Fatalf("import %+v %v", got, err)
	}
	if err := w.SetSessionOwner(SessionOwner{ID: "session-id", Name: "arbitrary", ProjectID: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPaneOwner(Assignment{Window: "explicit", ProjectID: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPaneOwner(Assignment{Window: "scratch"}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetSessionOwner(SessionOwner{ID: "session-id", Name: "renamed", ProjectID: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPaneOwner(Assignment{Window: "imported"}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPin(""); err != nil {
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
	if pin, err := w.Pin(); err != nil || pin != "" {
		t.Fatalf("unpin did not survive %q %v", pin, err)
	}
	for window, want := range map[string]string{"inherited": "B", "explicit": "A", "scratch": "", "imported": ""} {
		got, err := w.PaneOwner(window, "session-id")
		if err != nil || got.ProjectID != want {
			t.Errorf("%s owner %+v %v; want %s", window, got, err, want)
		}
	}
	var active map[string]any
	if err := db.Get("active", "singleton", &active); err != nil || active["tree"] != "keep" || active["task_id"] != "" {
		t.Fatalf("active %+v %v", active, err)
	}
}

func TestLastLaunchSeedsPinOnlyOnce(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range Tables {
		if err := db.Declare(table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Tx([]string{"projects", "launch_state"}, func(tx *store.Tx) error {
		if err := tx.Put("projects", "A", Project{ID: "A"}); err != nil {
			return err
		}
		return tx.Put("launch_state", "last", map[string]string{"project_id": "A"})
	}); err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if pin, _ := w.Pin(); pin != "A" {
		t.Fatalf("seed %q", pin)
	}
	if err := w.SetPin(""); err != nil {
		t.Fatal(err)
	}
	w, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if pin, _ := w.Pin(); pin != "" {
		t.Fatalf("reseeded %q", pin)
	}
}

func TestSetPinRecordsWhenEachProjectWasPinned(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if at, err := w.PinnedAt(); err != nil || len(at) != 0 {
		t.Fatalf("fresh %v %v", at, err)
	}
	for _, id := range []string{"A", "B", "", "A"} {
		if err := w.SetPin(id); err != nil {
			t.Fatal(err)
		}
	}
	at, err := w.PinnedAt()
	if err != nil || len(at) != 2 || at["A"] < at["B"] || at["B"] == 0 {
		t.Fatalf("pinned at %v %v", at, err)
	}
	if pin, _ := w.Pin(); pin != "A" {
		t.Fatalf("pin %q", pin)
	}
}
