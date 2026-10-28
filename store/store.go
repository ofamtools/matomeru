package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when Get cannot find the requested row.
var ErrNotFound = sql.ErrNoRows

var tableName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Table describes ownership and the version of a unit's JSON row format.
// Key names the logical key field; Put receives the storage key explicitly.
type Table struct {
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	Key        string `json:"key"`
	Version    int    `json:"version"`
	AppendOnly bool   `json:"append_only"`
}

type Store struct {
	db *sql.DB
}

// Tx grants access only to the tables named in Store.Tx.
// Use its methods, not Store methods, inside a transaction callback.
type Tx struct {
	tx       *sql.Tx
	tables   map[string]Table
	readOnly bool
}

// Open opens a SQLite file, creating its parent directory if necessary.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: database path is required")
	}
	dsn := "file::memory:"
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("store path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			return nil, fmt.Errorf("store directory: %w", err)
		}
		dsn = (&url.URL{Scheme: "file", Path: absolute}).String()
	}
	dsn += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS __deck_tables (
		name TEXT PRIMARY KEY,
		owner TEXT NOT NULL,
		key TEXT NOT NULL,
		version INTEGER NOT NULL CHECK (version > 0),
		append_only INTEGER NOT NULL CHECK (append_only IN (0, 1))
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize store: %w", err)
	}
	return &Store{db: db}, nil
}

// OpenReadOnly opens an existing SQLite file for reading only (mode=ro):
// it creates no directory, no file and no metadata table, and every write
// fails. A reader that must not change the owner's database (an inventory)
// opens it so.
func OpenReadOnly(path string) (*Store, error) {
	if path == "" || path == ":memory:" {
		return nil, errors.New("store: a read-only store needs a database file")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store path: %w", err)
	}
	if _, err := os.Stat(absolute); err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute}).String() + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open store: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// TableInfo is a declared table and how many rows it holds.
type TableInfo struct {
	Table
	Rows int `json:"rows"`
}

// Tables is every declared table, by name, with its row count, read in one
// read transaction; it writes nothing.
func (s *Store) Tables() ([]TableInfo, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT name, owner, key, version, append_only FROM __deck_tables ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store tables: %w", err)
	}
	var out []TableInfo
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Name, &t.Owner, &t.Key, &t.Version, &t.AppendOnly); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := tx.QueryRow(`SELECT count(*) FROM ` + quote(out[i].Name)).Scan(&out[i].Rows); err != nil {
			return nil, fmt.Errorf("count %s: %w", out[i].Name, err)
		}
	}
	return out, nil
}

// Declare creates a table or accepts an identical existing declaration.
// Changing its owner, key, or append-only policy is refused; a version bump
// goes through Migrate first. Redeclaration never silently changes persisted
// metadata.
func (s *Store) Declare(table Table) error {
	if !tableName.MatchString(table.Name) || strings.HasPrefix(table.Name, "sqlite_") {
		return fmt.Errorf("store: invalid table name %q", table.Name)
	}
	if table.Owner == "" || table.Key == "" || table.Version < 1 {
		return errors.New("store: table owner, key, and positive version are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownerAllowed(tx, table); err != nil {
		return err
	}
	existing, err := readTable(tx.QueryRow(`SELECT name, owner, key, version, append_only FROM __deck_tables WHERE name = ?`, table.Name))
	if err == nil {
		if existing != table {
			return fmt.Errorf("store: table %q declaration conflicts with persisted metadata", table.Name)
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	name := quote(table.Name)
	if _, err := tx.Exec(`CREATE TABLE ` + name + ` (id TEXT PRIMARY KEY NOT NULL, row TEXT NOT NULL CHECK (json_valid(row)))`); err != nil {
		return fmt.Errorf("declare %s: %w", table.Name, err)
	}
	if table.AppendOnly {
		// Enforce immutability at the database boundary, including INSERT OR REPLACE.
		for _, operation := range []string{"UPDATE", "DELETE", "INSERT"} {
			guard := ""
			if operation == "INSERT" {
				guard = ` WHEN EXISTS (SELECT 1 FROM ` + name + ` WHERE id = NEW.id)`
			}
			trigger := quote("__deck_" + table.Name + "_" + strings.ToLower(operation))
			if _, err := tx.Exec(`CREATE TRIGGER ` + trigger + ` BEFORE ` + operation + ` ON ` + name + guard + ` BEGIN SELECT RAISE(ABORT, 'append-only table'); END`); err != nil {
				return fmt.Errorf("protect %s: %w", table.Name, err)
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO __deck_tables (name, owner, key, version, append_only) VALUES (?, ?, ?, ?, ?)`, table.Name, table.Owner, table.Key, table.Version, table.AppendOnly); err != nil {
		return err
	}
	return tx.Commit()
}

// Tx commits all callback writes together, or rolls them back on error or panic.
// Every listed table must already be declared; omitted tables are inaccessible.
// It begins with the write lock (BEGIN IMMEDIATE), so writers queue behind busy_timeout.
func (s *Store) Tx(tables []string, fn func(*Tx) error) error { return s.run(tables, false, fn) }

// View runs fn in a deferred read transaction. In WAL mode a reader takes no
// write lock: it never waits behind a writer in another process and never holds
// one back, so observations from the TUI or CLI cannot starve each other.
// Put and Delete fail inside a View.
func (s *Store) View(tables []string, fn func(*Tx) error) error { return s.run(tables, true, fn) }

func (s *Store) run(tables []string, readOnly bool, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scope := &Tx{tx: tx, tables: make(map[string]Table, len(tables)), readOnly: readOnly}
	for _, name := range tables {
		table, err := readTable(tx.QueryRow(`SELECT name, owner, key, version, append_only FROM __deck_tables WHERE name = ?`, name))
		if err != nil {
			return fmt.Errorf("store: table %q is unavailable: %w", name, err)
		}
		scope.tables[name] = table
	}
	if err := fn(scope); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Get(table, id string, dst any) error {
	if err := s.declared(table); err != nil {
		return err
	}
	return decodeRow(s.db.QueryRow(`SELECT row FROM `+quote(table)+` WHERE id = ?`, id), dst)
}

// All returns rows in insertion order. Updating a row preserves its position.
func (s *Store) All(table string) ([]json.RawMessage, error) {
	if err := s.declared(table); err != nil {
		return nil, err
	}
	return readRows(s.db.Query(`SELECT row FROM ` + quote(table) + ` ORDER BY rowid`))
}

func (s *Store) declared(table string) error {
	var name string
	if err := s.db.QueryRow(`SELECT name FROM __deck_tables WHERE name = ?`, table).Scan(&name); err != nil {
		return fmt.Errorf("store: table %q is unavailable: %w", table, err)
	}
	return nil
}

func (t *Tx) Get(table, id string, dst any) error {
	if _, err := t.allowed(table); err != nil {
		return err
	}
	return decodeRow(t.tx.QueryRow(`SELECT row FROM `+quote(table)+` WHERE id = ?`, id), dst)
}

func (t *Tx) All(table string) ([]json.RawMessage, error) {
	if _, err := t.allowed(table); err != nil {
		return nil, err
	}
	return readRows(t.tx.Query(`SELECT row FROM ` + quote(table) + ` ORDER BY rowid`))
}

// Entry is one stored row with the id it is stored under (a row's own
// key field can be absent, as in a singleton row).
type Entry struct {
	ID  string
	Row json.RawMessage
}

// Entries returns a table's rows with their ids, in insertion order.
func (t *Tx) Entries(table string) ([]Entry, error) {
	if _, err := t.allowed(table); err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(`SELECT id, row FROM ` + quote(table) + ` ORDER BY rowid`)
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

// Raw is a row's stored JSON, nil when the table holds no row under id.
func (t *Tx) Raw(table, id string) (json.RawMessage, error) {
	if _, err := t.allowed(table); err != nil {
		return nil, err
	}
	var data []byte
	err := t.tx.QueryRow(`SELECT row FROM `+quote(table)+` WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return json.RawMessage(data), err
}

// Declared reports whether a table has been declared.
func (s *Store) Declared(table string) bool { return s.declared(table) == nil }

func (t *Tx) Put(table, id string, row any) error {
	if t.readOnly {
		return errors.New("store: put inside a read-only view")
	}
	definition, err := t.allowed(table)
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("store: row id is required")
	}
	data, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode %s row: %w", table, err)
	}
	query := `INSERT INTO ` + quote(table) + ` (id, row) VALUES (?, ?)`
	if !definition.AppendOnly {
		query += ` ON CONFLICT(id) DO UPDATE SET row = excluded.row`
	}
	if _, err := t.tx.Exec(query, id, string(data)); err != nil {
		return fmt.Errorf("put %s/%s: %w", table, id, err)
	}
	return nil
}

func (t *Tx) Delete(table, id string) error {
	if t.readOnly {
		return errors.New("store: delete inside a read-only view")
	}
	definition, err := t.allowed(table)
	if err != nil {
		return err
	}
	if definition.AppendOnly {
		return fmt.Errorf("store: table %q is append-only", table)
	}
	_, err = t.tx.Exec(`DELETE FROM `+quote(table)+` WHERE id = ?`, id)
	return err
}

func (t *Tx) allowed(name string) (Table, error) {
	table, ok := t.tables[name]
	if !ok {
		return Table{}, fmt.Errorf("store: table %q is outside transaction scope", name)
	}
	return table, nil
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func readTable(row *sql.Row) (Table, error) {
	var table Table
	err := row.Scan(&table.Name, &table.Owner, &table.Key, &table.Version, &table.AppendOnly)
	return table, err
}

func decodeRow(row *sql.Row, dst any) error {
	var data []byte
	if err := row.Scan(&data); err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func readRows(rows *sql.Rows, err error) ([]json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]json.RawMessage, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		result = append(result, json.RawMessage(data))
	}
	return result, rows.Err()
}
