// Package draft stores editor contents separately from saved domain records.
package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Store struct{ Dir string }
type Entry struct {
	ID   string
	Data json.RawMessage
}

// New uses the directory containing the database, not the database filename.
func New(dataDir string) *Store { return &Store{Dir: filepath.Join(dataDir, "drafts")} }

var nounName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var recordID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (s *Store) path(noun, id string) (string, error) {
	if !nounName.MatchString(noun) || !recordID.MatchString(id) {
		return "", fmt.Errorf("invalid draft noun or id: %q/%q", noun, id)
	}
	return filepath.Join(s.Dir, noun+"-"+id+".json"), nil
}

// Save replaces a whole JSON draft atomically; a failed write keeps the old file.
func (s *Store) Save(noun, id string, value any) error {
	path, err := s.path(noun, id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.Dir, ".draft-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Load returns os.ErrNotExist when there is no draft.
func (s *Store) Load(noun, id string, dst any) error {
	path, err := s.path(noun, id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func (s *Store) List(noun string) ([]Entry, error) {
	if !nounName.MatchString(noun) {
		return nil, fmt.Errorf("invalid draft noun %q", noun)
	}
	files, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, file := range files {
		name := file.Name()
		if file.IsDir() || !strings.HasPrefix(name, noun+"-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, noun+"-"), ".json")
		var data json.RawMessage
		if err := s.Load(noun, id, &data); err != nil {
			return nil, err
		}
		entries = append(entries, Entry{ID: id, Data: data})
	}
	return entries, nil
}

func (s *Store) Clear(noun, id string) error {
	path, err := s.path(noun, id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
