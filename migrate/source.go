package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// v1Tables are the tables Import reads, in the order it maps them.
var v1Tables = []string{"projects", "tasks", "notes", "directories", "relations", "schedule_defs", "reminder_defs"}

// source is an open, read-only v1 database.
type source struct {
	db   *sql.DB
	path string
	// snapshot is the temporary directory holding a copy of the file, when
	// the file is in WAL mode without its -wal and -shm beside it: opening
	// it in place would make SQLite create them next to the owner's file.
	snapshot string
}

// sourceDSN is the DSN of path opened read-only: mode=ro for SQLite and
// query_only on every connection, so no statement through it can write.
func sourceDSN(path string) string {
	u := url.URL{Path: path}
	return "file:" + u.EscapedPath() + "?mode=ro&_pragma=query_only(1)"
}

// openSource opens a v1 database strictly read-only. It never writes to,
// locks for writing, checkpoints or creates a file beside path.
func openSource(path string) (*source, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("migrate: source %s: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("migrate: source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("migrate: source %s is not a file", abs)
	}
	src := &source{path: abs}
	open := abs
	wal, err := isWAL(abs)
	if err != nil {
		return nil, err
	}
	if wal && !(exists(abs+"-wal") && exists(abs+"-shm")) {
		dir, err := os.MkdirTemp("", "mtm-migrate-")
		if err != nil {
			return nil, fmt.Errorf("migrate: snapshot dir: %w", err)
		}
		src.snapshot = dir
		open = filepath.Join(dir, filepath.Base(abs))
		for _, suffix := range []string{"", "-wal"} {
			if suffix != "" && !exists(abs+suffix) {
				continue
			}
			if err := copyFile(abs+suffix, open+suffix); err != nil {
				src.Close()
				return nil, err
			}
		}
	}
	db, err := sql.Open("sqlite", sourceDSN(open))
	if err != nil {
		src.Close()
		return nil, fmt.Errorf("migrate: open %s: %w", abs, err)
	}
	src.db = db
	if err := db.Ping(); err != nil {
		src.Close()
		return nil, fmt.Errorf("migrate: open %s: %w", abs, err)
	}
	return src, nil
}

// Close closes the database and removes a snapshot copy.
func (s *source) Close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.snapshot != "" {
		os.RemoveAll(s.snapshot)
	}
	return err
}

// isWAL reads the SQLite header: write version 2 is WAL mode.
func isWAL(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("migrate: source: %w", err)
	}
	defer f.Close()
	var head [20]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false, fmt.Errorf("migrate: source %s is not a SQLite database", path)
	}
	if !bytes.HasPrefix(head[:], []byte("SQLite format 3\x00")) {
		return false, fmt.Errorf("migrate: source %s is not a SQLite database", path)
	}
	return head[18] == 2, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("migrate: snapshot: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("migrate: snapshot: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("migrate: snapshot: %w", err)
	}
	return out.Close()
}

// row is one v1 row: the table's key and the row's JSON object.
type row struct {
	id string
	m  map[string]any
}

// tables lists the tables that exist in the source.
func (s *source) tables(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return nil, fmt.Errorf("migrate: read tables of %s: %w", s.path, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// read returns a table's rows ordered by key. A row whose JSON is not an
// object is skipped with a warning and counted on c.
func (s *source) read(ctx context.Context, table string, rep *reporter, c *Counts) ([]row, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, row FROM "+table+" ORDER BY id")
	if err != nil {
		rep.warn("table %s unreadable (%v); skipped", table, err)
		return nil, nil
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var id sql.NullString
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", table, err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil || m == nil {
			c.Total++
			c.Skipped++
			rep.warn("%s %q: malformed row skipped", table, id.String)
			continue
		}
		out = append(out, row{id: id.String, m: m})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: read %s: %w", table, err)
	}
	return out, nil
}
