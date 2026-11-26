package data

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// NoteDeckPage is one saved page inside the persistent Floating Paged Note
// Composer deck (alt+n). Each page captures a focused item's title, deep link,
// user-written Markdown prose, and dispatch mark state.
type NoteDeckPage struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	DeepLink  string    `json:"deepLink"`
	Space     string    `json:"space,omitempty"`
	FocusID   string    `json:"focusId,omitempty"`
	Body      string    `json:"body"`
	Marked    bool      `json:"marked,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// NoteDeckStore persists a continuous list of note deck pages to JSON on disk
// (or keeps them in memory when filePath is empty).
type NoteDeckStore struct {
	mu       sync.RWMutex
	filePath string
	pages    []NoteDeckPage
}

// NewNoteDeckStore loads existing pages from filePath (ignoring missing file)
// and returns a ready NoteDeckStore.
func NewNoteDeckStore(filePath string) *NoteDeckStore {
	s := &NoteDeckStore{filePath: strings.TrimSpace(filePath)}
	if s.filePath != "" {
		if raw, err := os.ReadFile(s.filePath); err == nil && len(raw) > 0 {
			var pages []NoteDeckPage
			if json.Unmarshal(raw, &pages) == nil {
				s.pages = pages
			}
		}
	}
	return s
}

func newDeckPageID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Pages returns a defensive copy of all pages in chronological order.
func (s *NoteDeckStore) Pages() []NoteDeckPage {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]NoteDeckPage, len(s.pages))
	copy(out, s.pages)
	return out
}

// Len returns the number of pages in the deck.
func (s *NoteDeckStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.pages)
}

// AddPage appends a new page to the deck and persists it.
func (s *NoteDeckStore) AddPage(title, deepLink, space, focusID string, now time.Time) (NoteDeckPage, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled Note"
	}
	p := NoteDeckPage{
		ID:        newDeckPageID(),
		Title:     title,
		DeepLink:  strings.TrimSpace(deepLink),
		Space:     space,
		FocusID:   focusID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.pages = append(s.pages, p)
	err := s.saveLocked()
	s.mu.Unlock()
	return p, err
}

// UpdateBody updates a page's Markdown body text and persists it.
func (s *NoteDeckStore) UpdateBody(id, body string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.pages {
		if s.pages[i].ID == id {
			s.pages[i].Body = body
			s.pages[i].UpdatedAt = now
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// ToggleMark flips a page's marked state for batch dispatch and returns the new state.
func (s *NoteDeckStore) ToggleMark(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.pages {
		if s.pages[i].ID == id {
			s.pages[i].Marked = !s.pages[i].Marked
			return s.pages[i].Marked, s.saveLocked()
		}
	}
	return false, ErrNotFound
}

// MarkAll sets Marked on every page in the deck.
func (s *NoteDeckStore) MarkAll(marked bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.pages {
		s.pages[i].Marked = marked
	}
	return s.saveLocked()
}

// DeletePage removes a page by ID.
func (s *NoteDeckStore) DeletePage(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.pages {
		if s.pages[i].ID == id {
			s.pages = append(s.pages[:i], s.pages[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// MarkedCount returns how many pages are currently marked for dispatch.
func (s *NoteDeckStore) MarkedCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, p := range s.pages {
		if p.Marked {
			n++
		}
	}
	return n
}

// RenderPageMarkdown renders one note deck page as structured Markdown with
// section header, deep link underneath, and user prose below.
func RenderPageMarkdown(p NoteDeckPage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n", p.Title)
	if p.DeepLink != "" {
		fmt.Fprintf(&b, "\n[%s](%s)\n", p.DeepLink, p.DeepLink)
	}
	if body := strings.TrimSpace(p.Body); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	} else {
		b.WriteString("\n_(type notes below)_\n")
	}
	return b.String()
}

// CompileMarkedMarkdown compiles all marked pages (or fallbackID if none are
// marked) into a single Markdown prompt ready for agent dispatch.
func (s *NoteDeckStore) CompileMarkedMarkdown(fallbackID string) (string, int) {
	if s == nil {
		return "", 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var selected []NoteDeckPage
	for _, p := range s.pages {
		if p.Marked {
			selected = append(selected, p)
		}
	}
	if len(selected) == 0 && fallbackID != "" {
		for _, p := range s.pages {
			if p.ID == fallbackID {
				selected = append(selected, p)
				break
			}
		}
	}
	if len(selected) == 0 {
		return "", 0
	}
	parts := make([]string, 0, len(selected))
	for _, p := range selected {
		parts = append(parts, strings.TrimSpace(RenderPageMarkdown(p)))
	}
	return strings.Join(parts, "\n\n---\n\n"), len(selected)
}

func (s *NoteDeckStore) saveLocked() error {
	if s.filePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.pages, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}
