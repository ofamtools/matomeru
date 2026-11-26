package data

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	actionLogCapacity     = 500
	actionLogCompactAbove = 2000
)

// ActionLogEntry describes an executed command and its outcome.
type ActionLogEntry struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Command string    `json:"command"`
	Target  string    `json:"target"`
	Space   string    `json:"space"`
	Source  string    `json:"source"`
	Outcome string    `json:"outcome"`
	Detail  string    `json:"detail"`
}

// ActionLogStore retains the last 500 entries and optionally persists them as JSONL.
type ActionLogStore struct {
	mu           sync.Mutex
	path         string
	ring         [actionLogCapacity]ActionLogEntry
	next         int
	count        int
	lines        int
	needsNewline bool
}

// NewActionLogStore restores the retained entries from filePath, when present.
func NewActionLogStore(filePath string) *ActionLogStore {
	store := &ActionLogStore{path: filePath}
	if filePath == "" {
		return store
	}
	f, err := os.Open(filePath)
	if err != nil {
		return store
	}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			store.lines++
			store.needsNewline = line[len(line)-1] != '\n'
			var entry *ActionLogEntry
			if json.Unmarshal(line, &entry) == nil && entry != nil {
				store.push(*entry)
			}
		}
		if err != nil {
			break
		}
	}
	_ = f.Close()
	if store.lines > actionLogCompactAbove {
		_ = store.replaceFile(store.count)
	}
	return store
}

// Record keeps entry in memory even when its best-effort disk write fails.
func (s *ActionLogStore) Record(entry ActionLogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.ID == "" {
		entry.ID = newID()
	}
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	s.push(entry)
	if s.path == "" {
		return
	}
	s.appendFile(entry)
	if s.lines > actionLogCompactAbove {
		_ = s.replaceFile(s.count)
	}
}

// List returns a snapshot in newest-first recording order. A nonpositive
// limit returns every retained entry.
func (s *ActionLogStore) List(limit int) []ActionLogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > s.count {
		limit = s.count
	}
	entries := make([]ActionLogEntry, limit)
	for i := range entries {
		entries[i] = s.newest(i)
	}
	return entries
}

// Clear discards the entries after successfully clearing persistent storage.
func (s *ActionLogStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if err := s.replaceFile(0); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	clear(s.ring[:])
	s.next, s.count, s.lines = 0, 0, 0
	s.needsNewline = false
	return nil
}

func (s *ActionLogStore) push(entry ActionLogEntry) {
	s.ring[s.next] = entry
	s.next = (s.next + 1) % len(s.ring)
	s.count = min(s.count+1, len(s.ring))
}

func (s *ActionLogStore) newest(offset int) ActionLogEntry {
	return s.ring[(s.next+len(s.ring)-1-offset)%len(s.ring)]
}

func (s *ActionLogStore) appendFile(entry ActionLogEntry) {
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')
	const flags = os.O_CREATE | os.O_APPEND | os.O_WRONLY
	f, err := os.OpenFile(s.path, flags, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
			return
		}
		f, err = os.OpenFile(s.path, flags, 0o600)
	}
	if err != nil {
		return
	}
	// Separate an unterminated line left by a previous writer before appending.
	data := line
	if s.needsNewline {
		data = append([]byte{'\n'}, line...)
	}
	n, _ := f.Write(data)
	_ = f.Close()
	if n == 0 {
		return
	}
	s.needsNewline = data[n-1] != '\n'
	if n > len(data)-len(line) {
		s.lines++
	}
}

// replaceFile atomically rewrites the newest count entries in recording order.
func (s *ActionLogStore) replaceFile(count int) error {
	f, err := os.CreateTemp(filepath.Dir(s.path), ".action-log-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	writer := bufio.NewWriter(f)
	encoder := json.NewEncoder(writer)
	for i := count - 1; i >= 0; i-- {
		if err := encoder.Encode(s.newest(i)); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.lines = count
	s.needsNewline = false
	return nil
}
