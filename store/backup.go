package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BackupsDir is the folder, beside the database file, every copy of it
// goes to (BackupCopy).
const BackupsDir = "backups"

// BackupCopyStamp is the UTC time form in a copy's name.
const BackupCopyStamp = "20060102T150405Z"

// BackupCopy writes a consistent copy of the database (Backup) into the
// backups folder beside the file, named <file>.bak-<UTC time>[-label] (a
// second copy in the same second gets -2, -3, ...), and answers its path.
// It is the one way mtm copies its database: a migration that rewrites rows
// for good, the database.backup verb and a release recipe all land their
// copies in one folder under one naming scheme, which the database-copies
// retention row rules.
func (s *Store) BackupCopy(label string, now time.Time) (string, error) {
	dir := filepath.Join(filepath.Dir(s.path), BackupsDir)
	if s.path != "" && s.path != ":memory:" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	name := filepath.Base(s.path) + ".bak-" + now.UTC().Format(BackupCopyStamp)
	if label != "" {
		name += "-" + label
	}
	dest := filepath.Join(dir, name)
	for i := 2; fileExists(dest); i++ {
		dest = filepath.Join(dir, fmt.Sprintf("%s-%d", name, i))
	}
	return dest, s.Backup(dest)
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TableBytes is the bytes each table and index of the file holds (SQLite's
// dbstat, one aggregate row per name), read in a read transaction; it
// writes nothing. A build without dbstat answers its error, and a caller
// leaves the shares out.
func (s *Store) TableBytes() (map[string]int64, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT name, pgsize FROM dbstat WHERE aggregate = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, rows.Err()
}
