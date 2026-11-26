package data

import (
	"cmp"
	"errors"
	"maps"
	"math"
	"slices"
	"sync"
	"time"
)

// PathSortMode selects the ordering of path history.
type PathSortMode string

const (
	PathSortRecent   PathSortMode = "recent"
	PathSortFrequent PathSortMode = "frequent"
	PathSortFrecency PathSortMode = "frecency"
	PathSortName     PathSortMode = "name"
)

// PathEntry records how often and how recently a path was picked.
type PathEntry struct {
	Path       string    `json:"path"`
	IsDir      bool      `json:"isDir"`
	Count      int       `json:"count"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	Frecency   float64   `json:"frecency"`
}

// Score weights the use count by time since the last pick, with a 72-hour half-life.
func Score(count int, lastUsed, now time.Time) float64 {
	ageHours := max(0, now.Sub(lastUsed).Hours())
	return float64(count) * math.Pow(0.5, ageHours/72)
}

// SortPathEntries returns a scored and sorted copy of entries.
func SortPathEntries(entries []PathEntry, mode PathSortMode, now time.Time) []PathEntry {
	out := slices.Clone(entries)
	for i := range out {
		out[i].Frecency = Score(out[i].Count, out[i].LastUsedAt, now)
	}
	slices.SortStableFunc(out, func(a, b PathEntry) int {
		switch mode {
		case PathSortName:
			return cmp.Compare(a.Path, b.Path)
		case PathSortFrequent:
			return cmp.Or(cmp.Compare(b.Count, a.Count), b.LastUsedAt.Compare(a.LastUsedAt), cmp.Compare(a.Path, b.Path))
		case PathSortFrecency:
			return cmp.Or(cmp.Compare(b.Frecency, a.Frecency), b.LastUsedAt.Compare(a.LastUsedAt), cmp.Compare(a.Path, b.Path))
		default:
			return cmp.Or(b.LastUsedAt.Compare(a.LastUsedAt), cmp.Compare(b.Count, a.Count), cmp.Compare(a.Path, b.Path))
		}
	})
	return out
}

// FrecencyStore keeps a concurrent-safe snapshot of path usage.
type FrecencyStore struct {
	mu      sync.RWMutex
	file    string
	entries map[string]PathEntry
	loadErr error
}

// NewFrecencyStore reads path history from filePath. An empty filePath keeps it in memory.
func NewFrecencyStore(filePath string) *FrecencyStore {
	s := &FrecencyStore{file: filePath}
	s.load()
	return s
}

func (s *FrecencyStore) load() {
	entries := map[string]PathEntry{}
	s.loadErr = readPathIndex(s.file, &entries)
	if s.loadErr == nil {
		s.entries = entries
	}
}

// Record adds one use of path and preserves the latest observation's timestamp and kind.
func (s *FrecencyStore) Record(path string, isDir bool, now time.Time) error {
	if path == "" {
		return errors.New("data: path history requires a path")
	}
	return s.update(func(entries map[string]PathEntry) bool {
		entry := entries[path]
		entry.Path = path
		if entry.Count == 0 || !now.Before(entry.LastUsedAt) {
			entry.IsDir = isDir
			entry.LastUsedAt = now.UTC()
		}
		entry.Count++
		entries[path] = entry
		return true
	})
}

// List returns a history snapshot sorted at now.
func (s *FrecencyStore) List(mode PathSortMode, now time.Time) []PathEntry {
	s.mu.RLock()
	entries := slices.Collect(maps.Values(s.entries))
	s.mu.RUnlock()
	return SortPathEntries(entries, mode, now)
}

// Remove removes path from history.
func (s *FrecencyStore) Remove(path string) error {
	return s.update(func(entries map[string]PathEntry) bool {
		_, exists := entries[path]
		delete(entries, path)
		return exists
	})
}

func (s *FrecencyStore) update(edit func(map[string]PathEntry) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		s.load()
		if s.loadErr != nil {
			return s.loadErr
		}
	}
	entries := make(map[string]PathEntry, len(s.entries))
	maps.Copy(entries, s.entries)
	if !edit(entries) {
		return nil
	}
	if err := writePathIndex(s.file, entries); err != nil {
		return err
	}
	s.entries = entries
	return nil
}
