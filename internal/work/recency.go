package work

import (
	"errors"
	"path/filepath"

	"github.com/unixsuperhero/matomeru/internal/store"
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
