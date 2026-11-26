package data

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActionLogRing(t *testing.T) {
	store := NewActionLogStore("")
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for i := range actionLogCapacity + 3 {
		store.Record(ActionLogEntry{ID: fmt.Sprint(i), At: at, Command: "example.open"})
	}
	got := store.List(0)
	if len(got) != actionLogCapacity {
		t.Fatalf("retained = %d, want %d", len(got), actionLogCapacity)
	}
	if got[0].ID != "502" || got[len(got)-1].ID != "3" || got[0].At != at {
		t.Fatalf("unexpected newest/oldest entries: %+v / %+v", got[0], got[len(got)-1])
	}
	got[0].ID = "changed"
	if limited := store.List(1); len(limited) != 1 || limited[0].ID != "502" {
		t.Fatalf("limited snapshot = %+v", limited)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	store.Record(ActionLogEntry{Command: "example.refresh"})
	if entry := store.List(1)[0]; entry.ID == "" || entry.At.IsZero() || entry.Command != "example.refresh" {
		t.Fatalf("recorded entry = %+v", entry)
	}
}

func TestActionLogPersistentRotationAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "action-log.jsonl")
	store := NewActionLogStore(path)
	for i := range actionLogCompactAbove + 2 {
		store.Record(ActionLogEntry{
			ID: fmt.Sprint(i), At: time.Date(2026, 11, 26, 10, 0, i, 0, time.UTC),
			Command: "example.open", Target: "item:one", Space: "dashboard", Source: "binding",
			Outcome: "ok", Detail: "Line one\nLine two",
		})
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(contents, []byte{'\n'}); n != actionLogCapacity+1 {
		t.Fatalf("compacted log has %d lines, want %d", n, actionLogCapacity+1)
	}
	reopened := NewActionLogStore(path)
	got := reopened.List(0)
	if len(got) != actionLogCapacity || got[0].ID != "2001" || got[len(got)-1].ID != "1502" {
		t.Fatalf("unexpected persisted ring order (%d entries)", len(got))
	}
	if got[0] != store.List(1)[0] {
		t.Fatalf("reopened entry lost fields: %+v", got[0])
	}
	if err := reopened.Clear(); err != nil {
		t.Fatal(err)
	}
	if len(reopened.List(0)) != 0 || len(NewActionLogStore(path).List(0)) != 0 {
		t.Fatal("clear did not reset memory and persisted entries")
	}
	reopened.Record(ActionLogEntry{ID: "after-clear"})
	got = NewActionLogStore(path).List(0)
	if len(got) != 1 || got[0].ID != "after-clear" {
		t.Fatalf("log after clear and append = %+v", got)
	}
}

func TestActionLogRecoversAcrossBrokenLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action-log.jsonl")
	large := strings.Repeat("x", 96*1024)
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	content.WriteString(large + "\n")
	if err := encoder.Encode(ActionLogEntry{ID: "large", Detail: large}); err != nil {
		t.Fatal(err)
	}
	content.WriteString("{\"id\":\"truncated")
	if err := os.WriteFile(path, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewActionLogStore(path)
	store.Record(ActionLogEntry{ID: "after-fragment"})
	got := NewActionLogStore(path).List(0)
	if len(got) != 2 || got[0].ID != "after-fragment" || got[1].ID != "large" || got[1].Detail != large {
		t.Fatalf("expected both valid entries, got %d entries", len(got))
	}
}

func TestActionLogConcurrentSnapshots(t *testing.T) {
	store := NewActionLogStore("")
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			for j := range 30 {
				store.Record(ActionLogEntry{ID: fmt.Sprintf("%d-%d", i, j)})
				_ = store.List(5)
			}
		})
	}
	workers.Wait()
	if got := store.List(0); len(got) != 240 {
		t.Fatalf("concurrent records = %d, want 240", len(got))
	}
}
