package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

// TestExplicitSubmitNoPrematureWrites verifies that editing fields in New and
// Edit forms does not write to SQLite until ctrl+s or navigating to the
// [ Submit ] row and pressing enter.
func TestExplicitSubmitNoPrematureWrites(t *testing.T) {
	a, d := demo(t)
	d.Keys("2")
	task := titled(t, a, data.KindTask, "ochazuke-v2")

	// 1. New record: editing fields does NOT create a record until submit.
	d.Keys("n")
	if rf := a.activeRecordForm(); a.Modal() != "form" || rf == nil || !rf.Model().SubmitRow || rf.Model().SubmitLabel != "Create task" {
		t.Fatalf("new task form = modal:%q form:%v", a.Modal(), rf)
	}
	d.Keys("i")
	d.Type("Drafted task")
	d.Keys("enter")
	if _, err := a.Store.GetByField(data.KindTask, "title", "Drafted task"); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("new record was prematurely written to SQLite: err=%v", err)
	}
	// Navigate to the focusable [ Submit ] row with G and press enter.
	d.Keys("G")
	if fm := a.activeRecordForm().Model(); !fm.OnSubmitRow() {
		t.Fatalf("G did not focus Submit row: activeIdx=%d specs=%d", fm.ActiveIdx, len(fm.Specs))
	}
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("enter on Submit row did not close modal: status=%q", a.Status())
	}
	created, err := a.Store.GetByField(data.KindTask, "title", "Drafted task")
	if err != nil || created == nil {
		t.Fatalf("task was not created on Submit row enter: %v", err)
	}
	if !strings.Contains(a.Status(), `created task "Drafted task"`) {
		t.Fatalf("status = %q, want created task confirmation", a.Status())
	}

	// 2. Edit record: changing multiple fields stages them without writing until ctrl+s.
	d.Keys("g", "g", "e")
	if rf := a.activeRecordForm(); a.Modal() != "form" || rf == nil || !rf.Model().SubmitRow || rf.Model().SubmitLabel != "Save task" {
		t.Fatalf("edit task form = modal:%q form:%v", a.Modal(), rf)
	}
	d.Keys("i")
	d.Type(" updated")
	d.Keys("enter", "j", "j", "l")
	cur, _ := a.Store.Get(task.Ref())
	if cur.Text("title") != "ochazuke-v2" || cur.Text("workStatus") != "active" {
		t.Fatalf("edit prematurely wrote to SQLite: title=%q status=%q", cur.Text("title"), cur.Text("workStatus"))
	}
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not close modal: status=%q", a.Status())
	}
	cur, _ = a.Store.Get(task.Ref())
	if cur.Text("title") != "ochazuke-v2 updated" || cur.Text("workStatus") != "blocked" {
		t.Fatalf("after ctrl+s: title=%q status=%q", cur.Text("title"), cur.Text("workStatus"))
	}
	// Undoing reverts the entire atomic patch in one step.
	d.Keys("u")
	cur, _ = a.Store.Get(task.Ref())
	if cur.Text("title") != "ochazuke-v2" || cur.Text("workStatus") != "active" {
		t.Fatalf("u did not undo atomic edit: title=%q status=%q", cur.Text("title"), cur.Text("workStatus"))
	}
}

// TestSubmitDirectlyFromFormEditMode verifies that ctrl+s works even while
// actively typing in ModeFormEdit, and that u undoes the submission in one step.
func TestSubmitDirectlyFromFormEditMode(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "n", "i")
	if a.Mode() != ModeFormEdit {
		t.Fatalf("mode = %q, want %q", a.Mode(), ModeFormEdit)
	}
	d.Type("Direct submit task")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s in ModeFormEdit left modal open: %q status=%q", a.Modal(), a.Status())
	}
	rec, err := a.Store.GetByField(data.KindTask, "title", "Direct submit task")
	if err != nil || rec == nil {
		t.Fatalf("record not created from ModeFormEdit ctrl+s: %v", err)
	}
	d.Keys("u")
	if _, err := a.Store.Get(rec.Ref()); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("u did not undo direct submit: err=%v", err)
	}
}

// TestFormDraftsLifecycle verifies auto-save on cancel, draft badges,
// [ / ] cycling, D discarding, and draft cleanup on submit.
func TestFormDraftsLifecycle(t *testing.T) {
	a, d := demo(t)
	d.Keys("2")

	// Save first draft on esc.
	d.Keys("n", "i")
	d.Type("First draft title")
	d.Keys("enter", "esc")
	if a.Modal() != "" {
		t.Fatalf("esc did not close form: %q", a.Modal())
	}
	if !strings.Contains(a.Status(), "draft saved") {
		t.Fatalf("status = %q, want draft saved", a.Status())
	}
	if _, err := a.Store.GetByField(data.KindTask, "title", "First draft title"); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("esc created record instead of saving draft: %v", err)
	}

	// Reopen: starts fresh with 1 draft badge.
	d.Keys("n")
	if fm := a.activeRecordForm().Model(); fm.ActiveDraftIndex() != -1 || fm.DraftCount() != 1 {
		t.Fatalf("reopened form draft state: idx=%d count=%d", fm.ActiveDraftIndex(), fm.DraftCount())
	}
	if frame := d.Frame(); !strings.Contains(frame, "1 draft(s) · [ restores") {
		t.Fatalf("missing draft count badge:\n%s", frame)
	}

	// Save a second draft.
	d.Keys("i")
	d.Type("Second draft title")
	d.Keys("enter", "esc")

	// Reopen and type live edits, then cycle drafts with [ and ].
	d.Keys("n", "i")
	d.Type("Live working title")
	d.Keys("enter")

	// First [ loads newest draft (Second draft title).
	d.Keys("[")
	if fm := a.activeRecordForm().Model(); fm.ActiveDraftIndex() != 0 || fm.Values["title"] != "Second draft title" {
		t.Fatalf("after [: idx=%d title=%v", fm.ActiveDraftIndex(), fm.Values["title"])
	}
	if frame := d.Frame(); !strings.Contains(frame, "draft 1/2 · [ / ] · D discard") {
		t.Fatalf("missing active draft badge:\n%s", frame)
	}

	// Second [ loads older draft (First draft title).
	d.Keys("[")
	if fm := a.activeRecordForm().Model(); fm.ActiveDraftIndex() != 1 || fm.Values["title"] != "First draft title" {
		t.Fatalf("after second [: idx=%d title=%v", fm.ActiveDraftIndex(), fm.Values["title"])
	}

	// ] steps back toward newer draft, then live edits.
	d.Keys("]")
	if fm := a.activeRecordForm().Model(); fm.ActiveDraftIndex() != 0 || fm.Values["title"] != "Second draft title" {
		t.Fatalf("after ]: idx=%d title=%v", fm.ActiveDraftIndex(), fm.Values["title"])
	}
	d.Keys("]")
	if fm := a.activeRecordForm().Model(); fm.ActiveDraftIndex() != -1 || fm.Values["title"] != "Live working title" {
		t.Fatalf("after second ]: idx=%d title=%v", fm.ActiveDraftIndex(), fm.Values["title"])
	}

	// Discard the newest draft with [ then D.
	d.Keys("[", "D")
	if !strings.Contains(a.Status(), "discarded draft") {
		t.Fatalf("status after D = %q, want discarded draft", a.Status())
	}
	if fm := a.activeRecordForm().Model(); fm.DraftCount() != 1 || fm.Values["title"] != "First draft title" {
		t.Fatalf("after D: count=%d title=%v", fm.DraftCount(), fm.Values["title"])
	}

	// Submitting the remaining loaded draft removes it from DraftStore.
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s on restored draft failed: status=%q", a.Status())
	}
	if _, err := a.Store.GetByField(data.KindTask, "title", "First draft title"); err != nil {
		t.Fatalf("restored draft was not created on submit: %v", err)
	}
	if remaining := a.cfg.drafts.List("new:task"); len(remaining) != 0 {
		t.Fatalf("draft was not removed on submit: %d remaining", len(remaining))
	}
}

// TestFormDraftsPersistAcrossRestart verifies that drafts.json in ConfigDir
// survives creating a brand-new App instance.
func TestFormDraftsPersistAcrossRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "matomeru-next")
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}

	a1, err := New(store, Options{ConfigDir: dir, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	d1 := NewDriver(a1, 120, 36)
	d1.Keys("2", "n", "i")
	d1.Type("Persisted draft")
	d1.Keys("enter", "esc")
	a1.Close()

	if _, err := os.Stat(filepath.Join(dir, draftsFileName)); err != nil {
		t.Fatalf("drafts.json was not written: %v", err)
	}

	// Restart a brand-new App pointing at the same ConfigDir.
	a2, err := New(store, Options{ConfigDir: dir, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a2.Close() })
	d2 := NewDriver(a2, 120, 36)
	d2.Keys("2", "n")
	if a2.activeRecordForm().Model().DraftCount() != 1 {
		t.Fatalf("restarted app loaded %d drafts, want 1", a2.activeRecordForm().Model().DraftCount())
	}
	d2.Keys("[")
	if got := a2.activeRecordForm().Model().Values["title"]; got != "Persisted draft" {
		t.Fatalf("restored title after restart = %v, want %q", got, "Persisted draft")
	}
}

// TestDraftKeysDoNotSwitchSpaces verifies that [ and ] in ModeForm never leak
// to Normal mode's space switching.
func TestDraftKeysDoNotSwitchSpaces(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "n")
	if a.Space() != SpaceWork || a.Modal() != "form" {
		t.Fatalf("setup: space=%q modal=%q", a.Space(), a.Modal())
	}
	d.Keys("[", "]", "[", "]")
	if a.Space() != SpaceWork || a.Modal() != "form" {
		t.Fatalf("after [ / ] in ModeForm: space=%q modal=%q, want %q/%q", a.Space(), a.Modal(), SpaceWork, "form")
	}
}
