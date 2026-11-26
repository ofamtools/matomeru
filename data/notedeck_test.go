package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoteDeckStoreLifecycleAndCompilation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note-deck.json")
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

	st := NewNoteDeckStore(path)
	p1, err := st.AddPage("Show Page Ledger", "mtm://task/abc123", "work", "work.table", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateBody(p1.ID, "Keep borderless margin clean.", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	p2, err := st.AddPage("Pane Terminal", "mtm://pane/p1", "panes", "site.screen", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateBody(p2.ID, "Stay live on ctrl+a P.", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if marked, err := st.ToggleMark(p2.ID); err != nil || !marked {
		t.Fatalf("ToggleMark p2 = %v, %v", marked, err)
	}

	// Reload from disk verifies atomic JSON persistence.
	reloaded := NewNoteDeckStore(path)
	if reloaded.Len() != 2 || reloaded.MarkedCount() != 1 {
		t.Fatalf("reloaded len=%d marked=%d, want 2/1", reloaded.Len(), reloaded.MarkedCount())
	}

	// CompileMarkedMarkdown selects marked pages only.
	md, count := reloaded.CompileMarkedMarkdown(p1.ID)
	if count != 1 || !strings.Contains(md, "## Pane Terminal") || !strings.Contains(md, "[mtm://pane/p1](mtm://pane/p1)") || strings.Contains(md, "Show Page Ledger") {
		t.Fatalf("compiled marked markdown mismatch (count=%d):\n%s", count, md)
	}

	// MarkAll marks all pages.
	if err := reloaded.MarkAll(true); err != nil {
		t.Fatal(err)
	}
	allMD, allCount := reloaded.CompileMarkedMarkdown("")
	if allCount != 2 || !strings.Contains(allMD, "Show Page Ledger") || !strings.Contains(allMD, "Pane Terminal") {
		t.Fatalf("compiled all markdown mismatch (count=%d):\n%s", allCount, allMD)
	}
}
