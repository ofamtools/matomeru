package work

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

// Legacy statuses read normalized ("open" is todo) and are written back on
// the next change; an unknown status is kept; SetStatus takes the four.
func TestStatusesLegacyAndUnknown(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for id, status := range map[string]string{"open": "open", "act": "active", "odd": "skipped", "none": ""} {
		if err := w.PutTask(Task{ID: id, ProjectID: "p", Title: id, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	raw := func(id string) string {
		var row map[string]any
		if err := db.Get("tasks", id, &row); err != nil {
			t.Fatal(err)
		}
		s, _ := row["status"].(string)
		return s
	}
	want := map[string]string{"open": StatusTodo, "act": StatusActive, "odd": "skipped", "none": StatusTodo}
	tasks, err := w.Tasks("p")
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Status != want[task.ID] {
			t.Fatalf("%s reads %q, want %q", task.ID, task.Status, want[task.ID])
		}
	}
	if got, _ := w.Task("open"); got.Status != StatusTodo || raw("open") != "open" {
		t.Fatalf("open reads %q, stored %q", got.Status, raw("open"))
	}
	if _, err := w.Retitle("open", "renamed"); err != nil {
		t.Fatal(err)
	}
	if raw("open") != StatusTodo {
		t.Fatalf("open was not written back as todo: %q", raw("open"))
	}
	if _, err := w.Retitle("odd", "renamed"); err != nil || raw("odd") != "skipped" {
		t.Fatalf("unknown status not kept: %q %v", raw("odd"), err)
	}
	for _, s := range w.StatusNames() {
		if got, err := w.SetStatus("act", s); err != nil || got.Status != s || raw("act") != s {
			t.Fatalf("SetStatus %s: %+v %v", s, got, err)
		}
	}
	if _, err := w.SetStatus("act", StatusDone); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SetStatus("act", "open"); err == nil {
		t.Fatal("SetStatus took a legacy value")
	}
	if got, _ := w.ToggleDone("act"); got.Status != StatusTodo {
		t.Fatalf("toggle from done: %q", got.Status)
	}
	if got, _ := w.ToggleDone("act"); got.Status != StatusDone {
		t.Fatalf("toggle to done: %q", got.Status)
	}
	glyphs := map[string]string{"todo": "[ ]", "open": "[ ]", "active": "[>]", "waiting": "[~]", "blocked": "[!]", "done": "[x]", "cancelled": "[-]", "skipped": "[?]"}
	for s, g := range glyphs {
		if w.StatusGlyph(s) != g {
			t.Fatalf("glyph %s = %s", s, w.StatusGlyph(s))
		}
	}
	if !(w.StatusRank("active") < w.StatusRank("todo") && w.StatusRank("todo") < w.StatusRank("blocked") && w.StatusRank("blocked") < w.StatusRank("skipped") && w.StatusRank("skipped") < w.StatusRank("done")) {
		t.Fatal("rank order")
	}
	// The table is data: a row of the setting is a status, with its glyph.
	rows, err := ParseStatuses(`[{"name":"todo","glyph":" "},{"name":"done","glyph":"x","closed":true},{"name":"later","glyph":"L"}]`)
	if err != nil {
		t.Fatal(err)
	}
	w.SetStatusSource(func() []StatusDef { return rows })
	if got, err := w.SetStatus("act", "later"); err != nil || got.Status != "later" || w.StatusGlyph("later") != "[L]" {
		t.Fatalf("custom status: %+v %v", got, err)
	}
	if _, err := w.SetStatus("act", "active"); err == nil || w.StatusClosed("active") || !w.StatusClosed("done") {
		t.Fatalf("a status the table lacks was taken: %v", err)
	}
	for _, bad := range []string{`[{"name":"todo"}]`, `[{"name":"todo","glyph":"ab"},{"name":"done"}]`, `[{"name":"todo"},{"name":"todo"},{"name":"done"}]`} {
		if _, err := ParseStatuses(bad); err == nil {
			t.Fatalf("%s was accepted", bad)
		}
	}
	w.SetStatusSource(nil)
	sub, err := w.AddSubtask("p", "act", "child")
	if err != nil || sub.ParentID != "act" || sub.Position != 1 || sub.Status != StatusTodo {
		t.Fatalf("subtask %+v %v", sub, err)
	}
}
