package data

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestManagedFilesBoundary(t *testing.T) {
	t.Setenv("MTM_FILES_DIR", "")
	if got := FilesRoot(":memory:"); got != "" {
		t.Fatalf("FilesRoot(:memory:) = %q, want empty", got)
	}
	dbPath := filepath.Join(t.TempDir(), "state", "mtm.db")
	wantRoot := filepath.Join(filepath.Dir(dbPath), "files")
	if got := FilesRoot(dbPath); got != wantRoot {
		t.Fatalf("FilesRoot(%q) = %q, want %q", dbPath, got, wantRoot)
	}

	custom := filepath.Join(t.TempDir(), "override-files")
	t.Setenv("MTM_FILES_DIR", custom)
	if got := FilesRoot(dbPath); got != custom {
		t.Fatalf("MTM_FILES_DIR override = %q, want %q", got, custom)
	}
	t.Setenv("MTM_FILES_DIR", "")

	p, err := WriteManagedFile(wantRoot, CategoryDocuments, "doc123", "Hello, World! / Plan", "md", "# Hello\n")
	if err != nil {
		t.Fatalf("WriteManagedFile: %v", err)
	}
	if !IsManagedPath(wantRoot, p) {
		t.Fatalf("IsManagedPath(%q, %q) = false", wantRoot, p)
	}
	if IsManagedPath(wantRoot, "/tmp/external.md") {
		t.Fatalf("external path reported as managed")
	}
	if !strings.HasSuffix(p, filepath.Join(CategoryDocuments, "doc123-hello-world-plan.md")) {
		t.Fatalf("unexpected managed path %q", p)
	}
	raw, err := os.ReadFile(p)
	if err != nil || string(raw) != "# Hello\n" {
		t.Fatalf("read back = %q, err=%v", string(raw), err)
	}
}

func TestTaskTreeOrderAndPatchManyUndo(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor := core.Actor{Kind: "user", ID: "owner"}

	a, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Alpha"}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Beta"}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := s.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Gamma"}}, actor)
	if err != nil {
		t.Fatal(err)
	}

	// Reorder to Gamma, Alpha, Beta in one undoable batch.
	cs, err := s.PatchMany([]PatchInput{
		{Ref: c.Ref(), Fields: map[string]any{"order": int64(1)}},
		{Ref: a.Ref(), Fields: map[string]any{"order": int64(2)}},
		{Ref: b.Ref(), Fields: map[string]any{"order": int64(3)}},
	}, "outline.reorder", "reorder roots", actor)
	if err != nil || cs == nil {
		t.Fatalf("PatchMany: cs=%v err=%v", cs, err)
	}
	tree, err := s.TaskTree(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 3 || tree[0].Record.ID != c.ID || tree[1].Record.ID != a.ID || tree[2].Record.ID != b.ID {
		t.Fatalf("ordered tree = %v %v %v", tree[0].Record.Text("title"), tree[1].Record.Text("title"), tree[2].Record.Text("title"))
	}

	// One undo restores original creation order.
	if _, err := s.UndoLast(actor); err != nil {
		t.Fatal(err)
	}
	tree, err = s.TaskTree(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 3 || tree[0].Record.ID != a.ID || tree[1].Record.ID != b.ID || tree[2].Record.ID != c.ID {
		t.Fatalf("after undo tree = %v %v %v", tree[0].Record.Text("title"), tree[1].Record.Text("title"), tree[2].Record.Text("title"))
	}
}
