package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var extensionName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ExtensionDir is the directory under mtm's data directory that holds one
// database per extension.
const ExtensionDir = "ext"

// ValidExtensionName reports whether name may name an extension database:
// lowercase letters, digits and single dashes between them, at most 64 bytes.
func ValidExtensionName(name string) bool {
	return len(name) <= 64 && extensionName.MatchString(name)
}

// ExtensionPath is the file OpenExtension opens for name under dataDir,
// <dataDir>/ext/<name>.db. mtm removes it to purge an extension's data.
func ExtensionPath(dataDir, name string) (string, error) {
	if dataDir == "" {
		return "", errors.New("store: the data directory is required")
	}
	if !ValidExtensionName(name) {
		return "", fmt.Errorf("store: invalid extension name %q (lowercase letters, digits and dashes)", name)
	}
	return filepath.Join(dataDir, ExtensionDir, name+".db"), nil
}

// OpenExtension opens (creating it if needed) the extension's own database,
// <dataDir>/ext/<name>.db, never mtm's file. The file is marked as the
// extension's on first open, and from then on:
//   - it opens only as that extension's file: another name, or a file that
//     already holds tables without the mark (mtm's database reached through a
//     link), is refused before anything is written to it;
//   - Declare accepts only tables whose Owner is the extension's name.
//
// Declare and Migrate work as on mtm's own file.
func OpenExtension(dataDir, name string) (*Store, error) {
	path, err := ExtensionPath(dataDir, name)
	if err != nil {
		return nil, err
	}
	s, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := s.claim(name); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) claim(name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	role := "ext:" + name
	current, marked, err := fileRole(tx)
	if err != nil {
		return err
	}
	if marked {
		if current != role {
			return fmt.Errorf("store: %s is not extension %q's database (it belongs to %q)", name+".db", name, current)
		}
		return tx.Commit()
	}
	var tables int
	if err := tx.QueryRow(`SELECT count(*) FROM __deck_tables`).Scan(&tables); err != nil {
		return err
	}
	if tables > 0 {
		return fmt.Errorf("store: %s already holds %d tables that are not extension %q's; an extension never opens mtm's database", name+".db", tables, name)
	}
	if _, err := tx.Exec(`CREATE TABLE __deck_file (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("mark extension database: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO __deck_file (key, value) VALUES ('role', ?)`, role); err != nil {
		return fmt.Errorf("mark extension database: %w", err)
	}
	return tx.Commit()
}

// fileRole reads the file's mark: "ext:<name>" for an extension database,
// unmarked for mtm's own.
func fileRole(tx *sql.Tx) (string, bool, error) {
	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = '__deck_file'`).Scan(&exists); err != nil {
		return "", false, err
	}
	if exists == 0 {
		return "", false, nil
	}
	var role string
	err := tx.QueryRow(`SELECT value FROM __deck_file WHERE key = 'role'`).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return role, err == nil, err
}

// ownerAllowed refuses a table an extension's file may not hold.
func ownerAllowed(tx *sql.Tx, table Table) error {
	role, marked, err := fileRole(tx)
	if err != nil || !marked {
		return err
	}
	if name, ok := strings.CutPrefix(role, "ext:"); ok && table.Owner != name {
		return fmt.Errorf("store: extension %q's database cannot hold table %q owned by %q", name, table.Name, table.Owner)
	}
	return nil
}
