package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

// A file from before 5.2 holds session names where the id belongs. The dry
// run says what would change and changes nothing; the open rewrites a name
// one session held to its id (and a window's saved session id wins),
// re-keys a session owner stored by its name, keeps a name several
// sessions held and one no record knows, backs the file up first, keeps
// every owner, and a second open changes nothing.
func TestSessionNamesBecomeIDs(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old := map[string]int{"assignments": 1, "session_owners": 1, "saved_panes": 1, copiesTable: 2}
	rows := map[string]map[string]any{
		"assignments/w1":                {"window": "w1", "session": "solo", "project_id": "p1", "task_id": ""},
		"assignments/w2":                {"window": "w2", "session": "twice", "project_id": "p2", "task_id": ""},
		"assignments/w3":                {"window": "w3", "session": "twice", "project_id": "", "task_id": ""},
		"assignments/w4":                {"window": "w4", "session": "nobody", "project_id": "p4", "task_id": ""},
		"assignments/w5":                {"window": "w5", "session": "s-solo", "project_id": "p5", "task_id": ""},
		"session_owners/s-solo":         {"id": "s-solo", "name": "solo", "project_id": "p1"},
		"session_owners/s-old":          {"id": "s-old", "name": "twice", "project_id": "p2"},
		"session_owners/twice":          {"id": "", "name": "twice", "project_id": "p3"},
		"session_owners/uniq":           {"id": "", "name": "uniq", "project_id": "p6"},
		copiesTable + "/session|s-uniq": {"id": "session|s-uniq", "noun": "session", "key": "s-uniq", "props": map[string]any{"name": "uniq"}},
		"saved_panes/v1":                {"id": "v1", "window": "w3", "session": "twice", "session_id": "s-new"},
		copiesTable + "/session|s-new":  {"id": "session|s-new", "noun": "session", "key": "s-new", "props": map[string]any{"name": "twice"}},
		copiesTable + "/pane|w1":        {"id": "pane|w1", "noun": "pane", "key": "w1", "props": map[string]any{"session": "solo"}},
	}
	for table, v := range old {
		key := "id"
		if table == "assignments" {
			key = "window"
		}
		if err := db.Declare(store.Table{Name: table, Owner: "work", Key: key, Version: v}); err != nil {
			t.Fatal(err)
		}
	}
	err = db.Tx([]string{"assignments", "session_owners", "saved_panes", copiesTable}, func(tx *store.Tx) error {
		for key, row := range rows {
			table, id := filepath.Dir(key), filepath.Base(key)
			if err := tx.Put(table, id, row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSessionIDs(db)
	if err != nil {
		t.Fatal(err)
	}
	counts := plan.Counts()
	if counts["assignments"] != [2]int{2, 2} || counts["session_owners"] != [2]int{1, 1} || counts[copiesTable] != [2]int{1, 0} {
		t.Fatalf("dry run %v:\n%s", counts, plan)
	}
	if v, _ := db.TableVersion("assignments"); v != 1 {
		t.Fatalf("the dry run moved the version to %d", v)
	}
	db.Close()
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if baks, _ := filepath.Glob(path + ".bak-*"); len(baks) != 1 {
		t.Fatalf("one backup beside the file: %v", baks)
	}
	session := func(window string) string {
		var a Assignment
		if err := db.Get("assignments", window, &a); err != nil {
			t.Fatal(err)
		}
		return a.Session
	}
	for window, want := range map[string]string{"w1": "s-solo", "w2": "twice", "w3": "s-new", "w4": "nobody", "w5": "s-solo"} {
		if got := session(window); got != want {
			t.Errorf("%s: session %q, want %q", window, got, want)
		}
	}
	for window, project := range map[string]string{"w1": "p1", "w2": "p2", "w4": "p4", "w5": "p5"} {
		if o, _ := w.PaneAssignment(window); o.ProjectID != project {
			t.Errorf("%s: owner %+v, want %s", window, o, project)
		}
	}
	if o, _ := w.SessionOwner("s-uniq"); o.ProjectID != "p6" || o.Name != "uniq" {
		t.Errorf("an owner keyed by its name is keyed by its id: %+v", o)
	}
	if again, _ := PlanSessionIDs(db); len(again.Changes) != 0 || len(again.Done) != 4 {
		t.Fatalf("a second run: %s", again)
	}
}
