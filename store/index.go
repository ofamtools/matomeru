package store

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// Index creates, when it is not there yet, an index over JSON fields of a
// declared table: one expression per field, in order, so a query that
// matches the leading fields (Match) reads the index instead of scanning
// every row. It is idempotent, changes no row and is kept outside
// __deck_tables, so table declarations stay byte-identical and a file
// written before the index existed opens unchanged (and an older binary
// opens a file that has it). name is the index's own name (a word; it is
// prefixed with the table's); a field is a JSON path without the "$."
// ("from", "data.kind").
func (s *Store) Index(table, name string, fields ...string) error {
	if err := s.declared(table); err != nil {
		return err
	}
	if !tableName.MatchString(name) {
		return fmt.Errorf("store: invalid index name %q", name)
	}
	if len(fields) == 0 {
		return fmt.Errorf("store: index %s.%s needs a field", table, name)
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		expr, err := fieldExpr(f)
		if err != nil {
			return err
		}
		parts[i] = expr
	}
	_, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS ` + quote("idx_"+table+"_"+name) + ` ON ` + quote(table) + ` (` + strings.Join(parts, ", ") + `)`)
	if err != nil {
		return fmt.Errorf("store: index %s.%s: %w", table, name, err)
	}
	return nil
}

// fieldExpr is the indexed expression of a JSON field. The path is a
// literal in the text, not a bound argument: SQLite uses an expression
// index only when the query repeats the expression exactly.
func fieldExpr(field string) (string, error) {
	if !fieldName.MatchString(field) {
		return "", fmt.Errorf("store: invalid field %q", field)
	}
	return `json_extract(row, '$.` + field + `')`, nil
}

// matchWhere is the WHERE clause and the arguments of the matches. A prefix
// match is a range over the same expression, so an index serves it too.
func matchWhere(matches []JSONMatch) (string, []any, error) {
	where, args := ` WHERE 1=1`, []any(nil)
	for _, m := range matches {
		expr, err := fieldExpr(m.Field)
		if err != nil {
			return "", nil, err
		}
		if !m.Prefix {
			where += ` AND ` + expr + ` = ?`
			args = append(args, m.Value)
			continue
		}
		where += ` AND ` + expr + ` >= ?`
		args = append(args, m.Value)
		if upper, ok := prefixEnd(m.Value); ok {
			where += ` AND ` + expr + ` < ?`
			args = append(args, upper)
		}
	}
	return where, args, nil
}

// prefixEnd is the smallest string above every string that starts with p
// (false when none: p empty or all 0xff bytes).
func prefixEnd(p string) (string, bool) {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

// Match returns the rows whose JSON fields equal (or, with Prefix, start
// with) every match, in insertion order. With an Index over the leading
// fields it reads only those rows.
func (s *Store) Match(table string, matches ...JSONMatch) ([]json.RawMessage, error) {
	if err := s.declared(table); err != nil {
		return nil, err
	}
	where, args, err := matchWhere(matches)
	if err != nil {
		return nil, err
	}
	return readRows(s.db.Query(`SELECT row FROM `+quote(table)+where+` ORDER BY rowid`, args...))
}

// Match is Store.Match inside a transaction.
func (t *Tx) Match(table string, matches ...JSONMatch) ([]json.RawMessage, error) {
	if _, err := t.allowed(table); err != nil {
		return nil, err
	}
	where, args, err := matchWhere(matches)
	if err != nil {
		return nil, err
	}
	return readRows(t.tx.Query(`SELECT row FROM `+quote(table)+where+` ORDER BY rowid`, args...))
}

// MatchEntries is Tx.Match with the id each row is stored under.
func (t *Tx) MatchEntries(table string, matches ...JSONMatch) ([]Entry, error) {
	if _, err := t.allowed(table); err != nil {
		return nil, err
	}
	where, args, err := matchWhere(matches)
	if err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(`SELECT id, row FROM `+quote(table)+where+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Entry, 0)
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		out = append(out, Entry{ID: id, Row: json.RawMessage(data)})
	}
	return out, rows.Err()
}

// plan is the query plan of a Match, for tests and diagnostics.
func (s *Store) plan(table string, matches ...JSONMatch) (string, error) {
	where, args, err := matchWhere(matches)
	if err != nil {
		return "", err
	}
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT row FROM `+quote(table)+where+` ORDER BY rowid`, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return "", err
		}
		out = append(out, detail)
	}
	return strings.Join(out, "; "), rows.Err()
}

// TableVersion is the declared version of a table (false when it is not
// declared), so a unit can tell a file it has yet to migrate.
func (s *Store) TableVersion(table string) (int, bool) {
	var v int
	if err := s.db.QueryRow(`SELECT version FROM __deck_tables WHERE name = ?`, table).Scan(&v); err != nil {
		return 0, false
	}
	return v, true
}

// Backup writes a consistent copy of the database to dest (VACUUM INTO: a
// whole file, WAL included, safe while others read). It refuses a dest that
// exists, and an in-memory database has nothing to copy. A unit calls it
// before a migration that rewrites rows for good.
func (s *Store) Backup(dest string) error {
	if s.path == "" || s.path == ":memory:" {
		return fmt.Errorf("store: an in-memory database has no file to back up")
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("store: backup %s exists", dest)
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("store: backup: %w", err)
	}
	return nil
}

// Path is the file the store was opened on (":memory:" for none).
func (s *Store) Path() string { return s.path }
