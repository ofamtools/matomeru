package data

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestFrecencyScoreAndSort(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		age   time.Duration
		score float64
	}{
		{-time.Hour, 8},
		{0, 8},
		{72 * time.Hour, 4},
		{144 * time.Hour, 2},
	} {
		if got := Score(8, now.Add(-tc.age), now); got != tc.score {
			t.Errorf("score at %s = %g, want %g", tc.age, got, tc.score)
		}
	}
	entries := []PathEntry{
		{Path: "b", Count: 2, LastUsedAt: now},
		{Path: "a", Count: 2, LastUsedAt: now},
		{Path: "c", Count: 4, LastUsedAt: now.Add(-72 * time.Hour)},
		{Path: "e", Count: 4, LastUsedAt: now},
		{Path: "d", Count: 5, LastUsedAt: now.Add(-144 * time.Hour)},
		{Path: "f", Count: 12, LastUsedAt: now.Add(-144 * time.Hour)},
	}
	before := slices.Clone(entries)
	for _, tc := range []struct {
		mode PathSortMode
		want []string
	}{
		{PathSortRecent, []string{"e", "a", "b", "c", "f", "d"}},
		{PathSortFrequent, []string{"f", "d", "e", "c", "a", "b"}},
		{PathSortFrecency, []string{"e", "f", "a", "b", "c", "d"}},
		{PathSortName, []string{"a", "b", "c", "d", "e", "f"}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := SortPathEntries(entries, tc.mode, now)
			var paths []string
			for _, entry := range got {
				paths = append(paths, entry.Path)
				if want := Score(entry.Count, entry.LastUsedAt, now); entry.Frecency != want {
					t.Errorf("score for %s = %g, want %g", entry.Path, entry.Frecency, want)
				}
			}
			if !slices.Equal(paths, tc.want) || !slices.Equal(entries, before) {
				t.Fatalf("paths %v, want %v, input %+v", paths, tc.want, entries)
			}
		})
	}
}

func TestFrecencyStorePersistsConcurrentUses(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state", "frecency.json")
	store := NewFrecencyStore(file)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	const path = "/projects/current"
	if err := store.Record(path, true, now); err != nil {
		t.Fatal(err)
	}
	const earlier = 10
	var writes sync.WaitGroup
	for range earlier {
		writes.Go(func() {
			if err := store.Record(path, false, now.Add(-time.Hour)); err != nil {
				t.Error(err)
			}
		})
	}
	writes.Wait()
	reopened := NewFrecencyStore(file)
	entries := reopened.List(PathSortFrecency, now)
	want := []PathEntry{{Path: path, IsDir: true, Count: earlier + 1, LastUsedAt: now, Frecency: earlier + 1}}
	if !slices.Equal(entries, want) {
		t.Fatalf("restored history = %+v, want %+v", entries, want)
	}
	if err := reopened.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := NewFrecencyStore(file).List(PathSortRecent, now); len(got) != 0 {
		t.Fatalf("history after removal = %+v", got)
	}
}

func TestFrecencyStorePreservesHistoryOnWriteError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "frecency.json")
	store := NewFrecencyStore(file)
	now := time.Now().UTC()
	if err := store.Record("keep", false, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("failed", false, now); err == nil {
		t.Fatal("record succeeded with a directory at the state file")
	}
	if got := store.List(PathSortRecent, now); len(got) != 1 || got[0].Path != "keep" {
		t.Fatalf("history after failed write = %+v", got)
	}
}
