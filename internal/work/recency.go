package work

import (
	"errors"
	"path/filepath"

	"github.com/unixsuperhero/matomeru/store"
)

// DirRecent records usage of a cleaned path, independently of project ownership.
type DirRecent struct {
	Path     string `json:"path"`
	LastUsed int64  `json:"last_used"`
	Uses     int64  `json:"uses"`
}

func (s *Store) TouchDir(path string) error {
	if path == "" {
		return nil
	}
	path = filepath.Clean(path)
	return s.db.Tx([]string{"dir_recent"}, func(tx *store.Tx) error {
		var row DirRecent
		if err := tx.Get("dir_recent", path, &row); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		row.Path, row.LastUsed, row.Uses = path, Now(), row.Uses+1
		return tx.Put("dir_recent", path, row)
	})
}

func (s *Store) RecentDirs() ([]DirRecent, error) {
	return all[DirRecent](s.db, "dir_recent")
}

// SeedDirs imports daemon activity once, without replacing recorded usage.
func (s *Store) SeedDirs(rows []DirRecent) error {
	return s.db.Tx([]string{"dir_recent"}, func(tx *store.Tx) error {
		for _, row := range rows {
			if row.Path == "" {
				continue
			}
			row.Path = filepath.Clean(row.Path)
			var existing DirRecent
			err := tx.Get("dir_recent", row.Path, &existing)
			if err == nil {
				continue
			}
			if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err := tx.Put("dir_recent", row.Path, row); err != nil {
				return err
			}
		}
		return nil
	})
}
