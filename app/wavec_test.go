package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func outlineTitles(t *testing.T, s *data.Store) []string {
	t.Helper()
	nodes, err := s.TaskTree(false)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(ns []data.TaskNode)
	walk = func(ns []data.TaskNode) {
		for _, n := range ns {
			out = append(out, strings.Repeat("  ", n.Depth)+n.Record.Text("title"))
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

func TestOutlineReorderIndentOutdentStatusAndUndo(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seedActor := core.Actor{Kind: "system", ID: "seed"}
	t1, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "First", "workStatus": "todo"}}, seedActor)
	t2, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Second", "workStatus": "todo"}}, seedActor)
	t3, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Third", "workStatus": "todo"}}, seedActor)

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// Open outline: First is selected.
	d.Keys("T")
	if a.Modal() != modalOutline || a.outlineSel != t1.ID {
		t.Fatalf("modal=%q sel=%q, want outline/%s", a.Modal(), a.outlineSel, t1.ID)
	}

	// 2J moves First down 2 spots -> [Second, Third, First].
	d.Keys("2", "J")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "Second|Third|First" {
		t.Fatalf("after 2J: %s", got)
	}
	// u cleanly undoes 2J -> [First, Second, Third].
	d.Keys("u")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "First|Second|Third" {
		t.Fatalf("after undo 2J: %s", got)
	}

	// J moves First down 1 spot -> [Second, First, Third].
	d.Keys("J")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "Second|First|Third" {
		t.Fatalf("after J: %s", got)
	}
	// K moves First back up -> [First, Second, Third].
	d.Keys("K")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "First|Second|Third" {
		t.Fatalf("after K: %s", got)
	}

	// Move cursor to Second, indent under First (>).
	d.Keys("j", ">")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "First|  Second|Third" {
		t.Fatalf("after >: %s", got)
	}
	if rec, _ := store.Get(t2.Ref()); rec.Text("parent") != "task:"+t1.ID {
		t.Fatalf("parent after > = %q", rec.Text("parent"))
	}

	// Outdent Second back to root (<).
	d.Keys("<")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "First|Second|Third" {
		t.Fatalf("after <: %s", got)
	}
	// Undo outdent -> back under First.
	d.Keys("u")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "First|  Second|Third" {
		t.Fatalf("after undo <: %s", got)
	}
	// Undo indent -> back to root.
	d.Keys("u")
	if got := strings.Join(outlineTitles(t, store), "|"); got != "First|Second|Third" {
		t.Fatalf("after undo >: %s", got)
	}

	// space cycles workStatus: todo -> active -> blocked -> done -> todo.
	for _, want := range []string{"active", "blocked", "done", "todo"} {
		d.Keys("space")
		rec, _ := store.Get(t2.Ref())
		if got := rec.Text("workStatus"); got != want {
			t.Fatalf("workStatus after space = %q, want %q", got, want)
		}
	}
	// Undo brings workStatus back from todo to done.
	d.Keys("u")
	if rec, _ := store.Get(t2.Ref()); rec.Text("workStatus") != "done" {
		t.Fatalf("workStatus after undo = %q, want done", rec.Text("workStatus"))
	}

	// i enters in-place inline edit at the start of the title (no modal form).
	d.Keys("i")
	if a.Modal() != modalOutline || a.Mode() != ModeOutlineEdit || a.outlineEdit == nil {
		t.Fatalf("after i: modal=%q mode=%q edit=%v, want outline/outline-edit", a.Modal(), a.Mode(), a.outlineEdit != nil)
	}
	if a.outlineEdit.line.Cursor != 0 {
		t.Fatalf("cursor after i = %d, want 0", a.outlineEdit.line.Cursor)
	}
	d.Keys("ctrl+k")
	d.Type("Second Renamed")
	d.Keys("enter")
	if rec, _ := store.Get(t2.Ref()); rec.Text("title") != "Second Renamed" {
		t.Fatalf("title after inline rename = %q, want Second Renamed", rec.Text("title"))
	}
	_ = t3
}

func TestGlobalRecentsPicker(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now().UTC()
	seed := core.Actor{Kind: "system", ID: "seed"}
	task, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Old Task"}}, seed)
	note, _, _ := store.Create(data.CreateInput{Kind: data.KindNote, Fields: map[string]any{"title": "Middle Note", "body": "hello"}}, seed)
	doc, _, _ := store.Create(data.CreateInput{Kind: data.KindDocument, Fields: map[string]any{"title": "Newest Doc"}}, seed)
	// Update Old Task so it becomes the newest record overall.
	_, _, _ = store.Patch(data.PatchInput{Ref: task.Ref(), Fields: map[string]any{"title": "Updated Task"}}, seed)
	_ = note

	a, err := New(store, Options{Now: func() time.Time { return now.Add(2 * time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	d.Keys("g", "r")
	if a.Modal() != modalRecents {
		t.Fatalf("modal = %q, want %q", a.Modal(), modalRecents)
	}
	pm := a.pickers[modalRecents]
	if pm == nil || len(pm.Items) < 3 {
		t.Fatalf("recents items = %v", pm)
	}
	if pm.Items[0].Ref == nil || *pm.Items[0].Ref != task.Ref() {
		t.Fatalf("first recent = %+v, want %v", pm.Items[0], task.Ref())
	}
	if pm.Items[1].Ref == nil || *pm.Items[1].Ref != doc.Ref() {
		t.Fatalf("second recent = %+v, want %v", pm.Items[1], doc.Ref())
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Updated Task") || !strings.Contains(frame, "Task") {
		t.Fatalf("recents frame missing title/noun badge:\n%s", frame)
	}

	// Pressing enter opens the newest record's Show page.
	d.Keys("enter")
	if shown, ok := a.Showing(); !ok || shown != task.Ref() {
		t.Fatalf("showing = %v (ok=%v), want %v", shown, ok, task.Ref())
	}
}

func TestManagedFileSyncFromTUI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mtm.db")
	store, err := data.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Create a document with a body and verify syncManagedFile writes under <dir>/files/documents/.
	doc, _, err := store.Create(data.CreateInput{Kind: data.KindDocument, Fields: map[string]any{"title": "Design Spec", "body": "# Spec\n"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	a.syncManagedFile(doc)
	got, _ := store.Get(doc.Ref())
	p := got.Text("path")
	if p == "" || !data.IsManagedPath(store.FilesRoot(), p) {
		t.Fatalf("managed path = %q, root = %q", p, store.FilesRoot())
	}
	raw, err := os.ReadFile(p)
	if err != nil || string(raw) != "# Spec\n" {
		t.Fatalf("managed file content = %q err=%v", string(raw), err)
	}

	// External path is preserved untouched.
	extPath := filepath.Join(dir, "external.md")
	extDoc, _, err := store.Create(data.CreateInput{Kind: data.KindDocument, Fields: map[string]any{"title": "External", "filePath": extPath, "body": "inline"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	a.syncManagedFile(extDoc)
	gotExt, _ := store.Get(extDoc.Ref())
	if gotExt.Text("filePath") != extPath {
		t.Fatalf("external filePath overwritten: %q", gotExt.Text("filePath"))
	}
}
