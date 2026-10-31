package work

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

func TestVimLegacyOwnerAndUnknownFieldsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := store.Table{Name: "vim_sessions", Owner: "vim", Key: "id", Version: 1}
	if err := db.Declare(legacy); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"id":"legacy","name":"old editor","path":"/old/session.vim","task_id":"task","cwd":"/repo","session":"arbitrary","window_id":"pane","state":"closed","created":12,"updated":13,"provenance":{"source":"old-mtm"},"former_project_id":"deleted"}`)
	if err := db.Tx([]string{"vim_sessions"}, func(tx *store.Tx) error { return tx.Put("vim_sessions", "legacy", raw) }); err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.PutTask(Task{ID: "parent", ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	if err := w.PutTask(Task{ID: "task", ParentID: "parent"}); err != nil {
		t.Fatal(err)
	}
	row, err := w.VimSession("legacy")
	if err != nil {
		t.Fatal(err)
	}
	project, err := w.VimProject(row)
	if err != nil || project != "project" {
		t.Fatalf("task owner: %q, %v", project, err)
	}
	if row.OwnedPath != "" || row.Listen != "" {
		t.Fatalf("legacy file must not become owned: %+v", row)
	}
	row.WindowID, row.Listen = "replacement", "/tmp/new-socket"
	if err := w.PutVimSession(row); err != nil {
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
	rows, err := w.VimSessions()
	if err != nil || len(rows) != 1 {
		t.Fatalf("sessions: %+v, %v", rows, err)
	}
	var fields map[string]json.RawMessage
	if err := db.Get("vim_sessions", "legacy", &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["provenance"]) != `{"source":"old-mtm"}` || string(fields["former_project_id"]) != `"deleted"` {
		t.Fatalf("legacy fields lost: %s", fields)
	}
	if rows[0].WindowID != "replacement" || rows[0].Created != 12 || rows[0].Path != "/old/session.vim" {
		t.Fatalf("legacy changed: %+v", rows[0])
	}
	if err := w.ForgetVimSession("legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.VimSession("legacy"); err != store.ErrNotFound {
		t.Fatalf("forget: %v", err)
	}
}
