package data

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Generic Preset kinds — intentionally NOT prefixed with "filter" so any form,
// query, model picker, or view can share one standardized Preset abstraction.
const (
	PresetKindQuery = "query"
	PresetKindForm  = "form"
	PresetKindModel = "model"
	PresetKindTable = "table"
)

// Preset is a standardized, reusable named configuration of arbitrary Values
// scoped by Kind (e.g. "query", "form", "model") and Scope (e.g. noun kind or form ID).
type Preset struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Scope     string         `json:"scope,omitempty"`
	Default   bool           `json:"default,omitempty"`
	Values    map[string]any `json:"values"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

// PresetStore manages generic named Presets with atomic JSON file persistence.
type PresetStore struct {
	mu      sync.Mutex
	path    string
	presets []Preset
}

// NewPresetStore loads presets from path (if non-empty and present).
func NewPresetStore(path string) *PresetStore {
	s := &PresetStore{path: strings.TrimSpace(path)}
	if s.path != "" {
		if b, err := os.ReadFile(s.path); err == nil && len(b) > 0 {
			var list []Preset
			if json.Unmarshal(b, &list) == nil {
				s.presets = list
			}
		}
	}
	return s
}

// Save creates or updates a preset matching (kind, scope, name).
func (s *PresetStore) Save(p Preset, now time.Time) (Preset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Name = strings.TrimSpace(p.Name)
	p.Kind = strings.TrimSpace(p.Kind)
	p.Scope = strings.TrimSpace(p.Scope)
	if p.Name == "" {
		return Preset{}, errors.New("preset name is required")
	}
	if p.Kind == "" {
		p.Kind = PresetKindQuery
	}
	if p.ID == "" {
		p.ID = newID()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	p.UpdatedAt = now.UTC()
	if p.Values == nil {
		p.Values = map[string]any{}
	}
	if p.Default {
		for i := range s.presets {
			if s.presets[i].Kind == p.Kind && s.presets[i].Scope == p.Scope {
				s.presets[i].Default = false
			}
		}
	}
	replaced := false
	for i := range s.presets {
		if strings.EqualFold(s.presets[i].Name, p.Name) && s.presets[i].Kind == p.Kind && s.presets[i].Scope == p.Scope {
			p.ID = s.presets[i].ID
			s.presets[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		s.presets = append(s.presets, p)
	}
	if err := s.persistLocked(); err != nil {
		return Preset{}, err
	}
	return p, nil
}

// List returns presets filtered by optional kind and scope (empty matches all).
func (s *PresetStore) List(kind, scope string) []Preset {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Preset
	for _, p := range s.presets {
		if kind != "" && p.Kind != kind {
			continue
		}
		if scope != "" && p.Scope != "" && p.Scope != scope {
			continue
		}
		out = append(out, clonePreset(p))
	}
	return out
}

// Default returns the default preset for (kind, scope), if any.
func (s *PresetStore) Default(kind, scope string) (Preset, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.presets {
		if p.Default && p.Kind == kind && p.Scope == scope {
			return clonePreset(p), true
		}
	}
	return Preset{}, false
}

// Delete removes the preset with id or matching (kind, scope, name).
func (s *PresetStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := slices.IndexFunc(s.presets, func(p Preset) bool { return p.ID == id })
	if idx < 0 {
		return ErrNotFound
	}
	s.presets = slices.Delete(s.presets, idx, idx+1)
	return s.persistLocked()
}

func clonePreset(p Preset) Preset {
	cp := p
	if p.Values != nil {
		b, _ := json.Marshal(p.Values)
		_ = json.Unmarshal(b, &cp.Values)
	}
	return cp
}

func (s *PresetStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.presets, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".presets-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, s.path)
}
