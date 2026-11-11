package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
)

// directoryRecord is the single saved list. Depth zero is an ordinary folder;
// a positive depth makes it a base to scan. Sources never require filesystem IO.
type directoryRecord struct {
	Path        string `json:"path"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Depth       int    `json:"depth,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	Source      string `json:"source,omitempty"`
}

type directoryState struct {
	ID           string `json:"id"`
	ProbePending bool   `json:"probe_pending,omitempty"`
	ConfigHash   string `json:"config_hash,omitempty"`
}

const directoryStateID = "list"

// userDirectories is shared by the noun, page, scanning and every path source.
// Project relations from older databases participate without duplicating records.
// MTM_PROJECT_BASE_DIRS replaces scan bases, not explicitly saved ordinary folders.
func (a *App) userDirectories() ([]directoryRecord, error) {
	raws, err := a.db.All("directories")
	if err != nil {
		return nil, err
	}
	byPath := map[string]directoryRecord{}
	for _, raw := range raws {
		var row directoryRecord
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		row.Path = a.expandPath(row.Path)
		if row.Source == "" {
			row.Source = "saved"
		}
		byPath[row.Path] = row
	}
	projects, err := a.work.Projects()
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		for _, tree := range p.Trees {
			path := a.expandPath(tree.Path)
			if _, ok := byPath[path]; !ok {
				byPath[path] = directoryRecord{Path: path, Source: "project"}
			}
		}
	}
	if value, override := a.opts.lookup("MTM_PROJECT_BASE_DIRS"); override {
		for path, row := range byPath {
			if row.Depth > 0 {
				delete(byPath, path)
			}
		}
		for _, path := range strings.Split(value, string(os.PathListSeparator)) {
			if path == "" {
				continue
			}
			path = a.expandPath(path)
			row := byPath[path]
			row.Path, row.Depth, row.Source, row.Archived = path, 1, "variable", false
			byPath[path] = row
		}
	}
	out := make([]directoryRecord, 0, len(byPath))
	for _, row := range byPath {
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b directoryRecord) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// initializeDirectoryList never touches a user's folders. An existing database
// without dirs.json gets a pending one-time probe, performed by dirs.refresh.
func (a *App) initializeDirectoryList(existing bool) (bool, error) {
	var state directoryState
	if err := a.db.Get("directory_state", directoryStateID, &state); err == nil {
		return state.ProbePending, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	data, err := os.ReadFile(a.dirsConfigPath())
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return false, err
	}
	var config directoryConfig
	if !missing {
		if err := json.Unmarshal(data, &config); err != nil {
			return false, err
		}
	}
	state = directoryState{ID: directoryStateID, ProbePending: existing && missing}
	if !missing {
		state.ConfigHash = directoryConfigHash(data)
	}
	err = a.db.Tx([]string{"directories", "directory_state"}, func(tx *store.Tx) error {
		for _, base := range config.Bases {
			if base.Depth == 0 {
				base.Depth = 1
			}
			if base.Depth < 0 {
				return errors.New("project base depth must be positive")
			}
			path := a.expandPath(base.Path)
			var row directoryRecord
			if err := tx.Get("directories", path, &row); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			row.Path, row.Depth, row.Source = path, base.Depth, "file"
			if err := tx.Put("directories", path, row); err != nil {
				return err
			}
		}
		return tx.Put("directory_state", state.ID, state)
	})
	return state.ProbePending, err
}

// importDirectoryBases is the explicit config.reload compatibility path. Once
// imported, runtime consumers only read userDirectories, never dirs.json.
func (a *App) importDirectoryBases() error {
	data, err := os.ReadFile(a.dirsConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var config directoryConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	digest := directoryConfigHash(data)
	for _, base := range config.Bases {
		if base.Depth < 0 {
			return errors.New("project base depth must be positive")
		}
	}
	return a.db.Tx([]string{"directories", "directory_state"}, func(tx *store.Tx) error {
		var state directoryState
		if err := tx.Get("directory_state", directoryStateID, &state); err != nil {
			return err
		}
		if state.ConfigHash == digest {
			return nil
		}
		raws, err := tx.All("directories")
		if err != nil {
			return err
		}
		for _, raw := range raws {
			var row directoryRecord
			if err := json.Unmarshal(raw, &row); err != nil {
				return err
			}
			if row.Source == "file" {
				row.Depth = 0
				row.Source = "saved"
				if err := tx.Put("directories", row.Path, row); err != nil {
					return err
				}
			}
		}
		for _, base := range config.Bases {
			path := a.expandPath(base.Path)
			var row directoryRecord
			if err := tx.Get("directories", path, &row); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			row.Path, row.Depth, row.Source, row.Archived = path, max(1, base.Depth), "file", false
			if err := tx.Put("directories", path, row); err != nil {
				return err
			}
		}
		state.ConfigHash, state.ProbePending = digest, false
		return tx.Put("directory_state", directoryStateID, state)
	})
}

func directoryConfigHash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func (a *App) putDirectoryRecord(row directoryRecord) error {
	if row.Depth < 0 {
		return errors.New("depth must be zero (directory) or positive (scan base)")
	}
	row.Path = a.expandPath(row.Path)
	err := a.db.Tx([]string{"directories"}, func(tx *store.Tx) error { return tx.Put("directories", row.Path, row) })
	if err == nil {
		a.r.Changed(work.DirectoryNoun)
	}
	return err
}

func directoryLabel(row directoryRecord) string {
	if row.Label != "" {
		return row.Label
	}
	return row.Path
}

func (a *App) savedDirectory(path string) (directoryRecord, error) {
	var row directoryRecord
	path = a.expandPath(path)
	err := a.db.Get("directories", path, &row)
	if errors.Is(err, store.ErrNotFound) {
		return directoryRecord{Path: path}, nil
	}
	return row, err
}

func (a *App) directoryRecordChanged(path string) {
	a.directories.Ready = false
	a.directories.Query = "\x00"
	a.refreshDirectoryRows()
	if a.directoryList != nil && a.r.Settings().Bool(settingDirectorySelect) {
		_ = a.directoryList.Select("directory:" + a.dirKey(path))
	}
	a.refreshPathChoosers()
}

func directorySource(row directoryRecord) string {
	if row.Depth > 0 {
		return fmt.Sprintf("%s · depth %d", row.Source, row.Depth)
	}
	return row.Source
}

func (a *App) configureDirectorySettings(existing bool) error {
	if err := a.declareSettings(); err != nil {
		return err
	}
	pending, err := a.initializeDirectoryList(existing)
	a.directories.ProbePending = pending
	return err
}

func (a *App) startDirectoryMigration() error {
	if !a.directories.ProbePending {
		return nil
	}
	return a.loadDirectories()
}

func (a *App) observeDirectorySettings() {
	for _, name := range []string{settingDirectoryManaged, settingDirectoryStatus} {
		a.r.Settings().Observe(name, func(string, any) { a.refreshDirectoryRows() })
	}
}
