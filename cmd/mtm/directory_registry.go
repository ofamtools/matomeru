package main

import (
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
	Candidate   bool   `json:"candidate,omitempty"`
}

type directoryState struct {
	ID           string `json:"id"`
	ProbePending bool   `json:"probe_pending,omitempty"`
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

// initializeDirectoryList imports legacy configuration only for populated data.
// The marker belongs to this store: a fresh --data never inherits shared config.
func (a *App) initializeDirectoryList() (bool, error) {
	var state directoryState
	if err := a.db.Get("directory_state", directoryStateID, &state); err == nil {
		return state.ProbePending, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	existing := false
	for _, table := range []string{"projects", "directories"} {
		rows, err := a.db.All(table)
		if err != nil {
			return false, err
		}
		existing = existing || len(rows) > 0
	}
	var config directoryConfig
	state = directoryState{ID: directoryStateID}
	if existing {
		data, err := os.ReadFile(a.dirsConfigPath())
		if errors.Is(err, os.ErrNotExist) {
			state.ProbePending = true
		} else if err != nil {
			return false, err
		} else if err := json.Unmarshal(data, &config); err != nil {
			return false, err
		}
	}
	err := a.db.Tx([]string{"directories", "directory_state"}, func(tx *store.Tx) error {
		for _, base := range config.Bases {
			if base.Depth == 0 {
				base.Depth = 1
			}
			if base.Depth < 0 {
				return errors.New("project base depth must be positive")
			}
			path := a.expandPath(base.Path)
			row := map[string]any{}
			if err := tx.Get("directories", path, &row); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			row["path"], row["depth"], row["source"] = path, base.Depth, "file"
			if err := tx.Put("directories", path, row); err != nil {
				return err
			}
		}
		return tx.Put("directory_state", state.ID, state)
	})
	return state.ProbePending, err
}

// saveDirectoryCandidates records discoveries with the selected data store.
// Completion owns these writes; going through CRUD would schedule another scan.
func (a *App) saveDirectoryCandidates(loaded directoryCache) error {
	records, err := a.userDirectories()
	if err != nil {
		return err
	}
	saved := map[string]string{}
	for _, record := range records {
		saved[a.dirKey(record.Path)] = record.Path
	}
	err = a.db.Tx([]string{"directories"}, func(tx *store.Tx) error {
		for _, row := range loaded.Rows {
			if loaded.Unavailable[row.Path] != "" {
				continue
			}
			path := saved[row.Path]
			if path == "" {
				path = row.Path
			}
			meta := map[string]any{}
			err := tx.Get("directories", path, &meta)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			old, _ := meta["candidate"].(bool)
			archived, _ := meta["archived"].(bool)
			if archived || old == row.Candidate {
				continue
			}
			if len(meta) == 0 {
				meta["path"], meta["source"] = path, "scan"
			}
			meta["candidate"] = row.Candidate
			if err := tx.Put("directories", path, meta); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		a.r.Changed(work.DirectoryNoun)
	}
	return err
}

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

func (a *App) configureDirectorySettings() error {
	if err := a.declareSettings(); err != nil {
		return err
	}
	pending, err := a.initializeDirectoryList()
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
