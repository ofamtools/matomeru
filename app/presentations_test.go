package app

import (
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
)

func TestPresentationsConfigAndRefCells(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	dir := t.TempDir()
	user := `// a JSONC comment
{"nouns": [{"kind": "task", "forms": {"chip": {"prefix": "→ "}}}]}`
	if err := os.WriteFile(filepath.Join(dir, presentationsFileName), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(store, Options{ConfigDir: dir})
	if err != nil || len(a.Warnings) != 0 {
		t.Fatalf("New: %v %v", err, a.Warnings)
	}
	defer a.Close()

	task := mkRec(t, a, data.KindTask, map[string]any{"title": "Ship zero-field"})
	dead := core.Ref{Kind: data.KindTask, ID: "2b161fe055c9"}
	panes := []data.Record{
		mkRec(t, a, data.KindPane, map[string]any{"title": "a", "task": task.Ref()}),
		mkRec(t, a, data.KindPane, map[string]any{"title": "b", "task": dead}),
	}
	schema := a.schema(data.KindPane)

	on := a.refCellLabels(schema, panes)
	if on[task.Ref()] != "→ Ship zero-field" || on[dead] != "(missing task)" {
		t.Fatalf("declarative labels = %v", on)
	}
	if !a.showOptions().DeclarativePresentations {
		t.Fatal("show options leave declarative presentations off by default")
	}

	settings, err := config.LoadSettings([]byte(`{"ui.declarativePresentations": false}`), DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.Settings = settings
	off := a.refCellLabels(schema, panes)
	if off[task.Ref()] != "Ship zero-field" || off[dead] != data.RefText(dead) || a.showOptions().DeclarativePresentations {
		t.Fatalf("opt-out labels = %v", off)
	}
}
