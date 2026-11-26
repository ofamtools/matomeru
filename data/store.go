package data

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
	"ofam.dev/ochazuke/core"
)

// ErrExists reports a create whose kind and ID are already taken.
var ErrExists = errors.New("data: already exists")

// WorkspaceRef is the default holder of pins and marks: a "pinned" or
// "marked" relation runs from it to the pinned or marked record.
var WorkspaceRef = core.Ref{Kind: KindWorkspace, ID: "default"}

const ddl = `
CREATE TABLE IF NOT EXISTS records (
	seq            INTEGER PRIMARY KEY AUTOINCREMENT,
	kind           TEXT NOT NULL,
	id             TEXT NOT NULL,
	fields         TEXT NOT NULL DEFAULT '{}',
	read_only      TEXT NOT NULL DEFAULT '{}',
	archive_status TEXT NOT NULL DEFAULT 'active',
	star_status    TEXT NOT NULL DEFAULT 'unstarred',
	change_set     INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL,
	archived_at    INTEGER,
	snoozed_until  INTEGER,
	UNIQUE (kind, id)
);
CREATE INDEX IF NOT EXISTS records_kind_archive ON records (kind, archive_status, created_at);
CREATE INDEX IF NOT EXISTS records_kind_star ON records (kind, star_status, updated_at DESC);
CREATE INDEX IF NOT EXISTS records_change_set ON records (change_set);

CREATE TABLE IF NOT EXISTS local_overrides (
	kind  TEXT NOT NULL,
	id    TEXT NOT NULL,
	field TEXT NOT NULL,
	value TEXT NOT NULL,
	PRIMARY KEY (kind, id, field)
);

CREATE TABLE IF NOT EXISTS relations (
	id         TEXT PRIMARY KEY,
	from_kind  TEXT NOT NULL,
	from_id    TEXT NOT NULL,
	kind       TEXT NOT NULL,
	to_kind    TEXT NOT NULL,
	to_id      TEXT NOT NULL,
	position   INTEGER NOT NULL DEFAULT 0,
	meta       TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL,
	UNIQUE (from_kind, from_id, kind, to_kind, to_id)
);
CREATE INDEX IF NOT EXISTS relations_from ON relations (from_kind, from_id, kind, position);
CREATE INDEX IF NOT EXISTS relations_to ON relations (to_kind, to_id, kind, position);

CREATE TABLE IF NOT EXISTS change_sets (
	number     INTEGER PRIMARY KEY,
	actor      TEXT NOT NULL,
	action     TEXT NOT NULL,
	summary    TEXT NOT NULL DEFAULT '',
	undo_of    INTEGER NOT NULL DEFAULT 0,
	changes    TEXT NOT NULL,
	conflicts  TEXT NOT NULL DEFAULT '[]',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS change_sets_undo_of ON change_sets (undo_of);
CREATE TRIGGER IF NOT EXISTS change_sets_no_update BEFORE UPDATE ON change_sets
BEGIN SELECT RAISE(ABORT, 'change sets are immutable'); END;
CREATE TRIGGER IF NOT EXISTS change_sets_no_delete BEFORE DELETE ON change_sets
BEGIN SELECT RAISE(ABORT, 'change sets are immutable'); END;

CREATE TABLE IF NOT EXISTS change_targets (
	change_set INTEGER NOT NULL,
	kind       TEXT NOT NULL,
	id         TEXT NOT NULL,
	PRIMARY KEY (change_set, kind, id)
);
CREATE INDEX IF NOT EXISTS change_targets_ref ON change_targets (kind, id, change_set);
`

// Store is the SQLite store behind the Data API. It is safe for concurrent
// use; writes are serialized.
type Store struct {
	db        *sql.DB
	reg       *Registry
	pres      *PresentationRegistry
	dsn       string
	filesRoot string

	wmu  sync.Mutex
	last time.Time
}

// Open opens (and creates if needed) a store. dsn is a file path, a
// "file:" URI, or ":memory:". Files are opened in WAL mode.
func Open(dsn string) (*Store, error) {
	memory := dsn == "" || dsn == ":memory:" || strings.Contains(dsn, "mode=memory")
	open := dsn
	switch {
	case dsn == "":
		open = ":memory:"
	case !memory:
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		open += sep + "_pragma=busy_timeout(5000)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", open)
	if err != nil {
		return nil, fmt.Errorf("data: open %s: %w", dsn, err)
	}
	if memory {
		// One connection: every query must see the same in-memory database.
		db.SetMaxOpenConns(1)
	} else {
		// Bound connection pool and SQLite memory footprint; NORMAL sync avoids
		// per-write full disk fsync stalls while preserving WAL crash safety.
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(2)
		db.SetConnMaxIdleTime(2 * time.Minute)
		if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA temp_store=MEMORY; PRAGMA cache_size=-8000;"); err != nil {
			db.Close()
			return nil, fmt.Errorf("data: enable WAL on %s: %w", dsn, err)
		}
	}
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("data: create tables in %s: %w", dsn, err)
	}
	// A database made before snoozing gets the column added.
	var hasSnooze int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('records') WHERE name = 'snoozed_until'").Scan(&hasSnooze); err != nil {
		db.Close()
		return nil, fmt.Errorf("data: read columns of %s: %w", dsn, err)
	}
	if hasSnooze == 0 {
		if _, err := db.Exec("ALTER TABLE records ADD COLUMN snoozed_until INTEGER"); err != nil {
			db.Close()
			return nil, fmt.Errorf("data: add snoozed_until to %s: %w", dsn, err)
		}
	}
	s := &Store{db: db, reg: DefaultSchemas(), dsn: dsn}
	if err := s.ensureUniqueIndexes(); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureQueryIndexes(db); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DBPath returns the DSN or path the store was opened with.
func (s *Store) DBPath() string { return s.dsn }

// FilesRoot returns the canonical managed files directory for this store.
func (s *Store) FilesRoot() string {
	if s.filesRoot != "" {
		return s.filesRoot
	}
	return FilesRoot(s.dsn)
}

// SetFilesRoot overrides the managed files directory for this store.
func (s *Store) SetFilesRoot(dir string) { s.filesRoot = dir }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Registry returns the store's schema registry (DefaultSchemas unless
// replaced). The store uses it for field defaults and typed reads only: a
// kind without a schema is stored and read all the same.
func (s *Store) Registry() *Registry { return s.reg }

// SetRegistry replaces the schema registry and indexes its unique fields.
func (s *Store) SetRegistry(r *Registry) {
	s.reg = r
	_ = s.ensureUniqueIndexes() // an index only speeds GetByField up
}

// JournalMode reports the database's journal mode ("wal" for files).
func (s *Store) JournalMode() (string, error) {
	var mode string
	err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	return mode, err
}

// querier is the part of *sql.DB and *sql.Tx the store reads through.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// --- encoding ---------------------------------------------------------------

func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("data: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// normalize turns v into the form it has when read back from the store, so a
// written value and a stored value compare equal.
func normalize(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("data: value is not JSON: %w", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func equalJSON(a, b any) bool { return toJSON(a) == toJSON(b) }

func unixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromNano(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// coerce gives schema-declared integer fields an int64 value after a JSON read.
func (s *Store) coerce(rec *Record) {
	schema, ok := s.reg.Get(rec.Kind)
	if !ok {
		return
	}
	for _, f := range schema.Fields {
		if f.Type != core.ParamInt {
			continue
		}
		for _, m := range []map[string]any{rec.Fields, rec.Local} {
			if x, ok := m[f.Name].(float64); ok && x == math.Trunc(x) {
				m[f.Name] = int64(x)
			}
		}
	}
}

const recordCols = "kind, id, fields, read_only, archive_status, star_status, change_set, created_at, updated_at, archived_at, snoozed_until"

func scanRecord(sc interface{ Scan(...any) error }, extraColumns ...any) (Record, error) {
	var (
		rec              Record
		fields, readOnly string
		created, updated int64
		archived         sql.NullInt64
		snoozed          sql.NullInt64
	)
	columns := []any{&rec.Kind, &rec.ID, &fields, &readOnly, &rec.ArchiveStatus, &rec.StarStatus, &rec.ChangeSetNumber, &created, &updated, &archived, &snoozed}
	err := sc.Scan(append(columns, extraColumns...)...)
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal([]byte(fields), &rec.Fields); err != nil {
		return rec, fmt.Errorf("data: fields of %s:%s: %w", rec.Kind, rec.ID, err)
	}
	if rec.Fields == nil {
		rec.Fields = map[string]any{}
	}
	if readOnly != "" && readOnly != "{}" {
		if err := json.Unmarshal([]byte(readOnly), &rec.ReadOnly); err != nil {
			return rec, fmt.Errorf("data: read-only set of %s:%s: %w", rec.Kind, rec.ID, err)
		}
	}
	rec.CreatedAt, rec.UpdatedAt = fromNano(created), fromNano(updated)
	if archived.Valid {
		t := fromNano(archived.Int64)
		rec.ArchivedAt = &t
	}
	if snoozed.Valid {
		t := fromNano(snoozed.Int64)
		rec.SnoozedUntil = &t
	}
	return rec, nil
}

// loadLocal reads local overrides: of one record, of one kind (id ""), or
// all (kind "").
func loadLocal(q querier, kind, id string) (map[core.Ref]map[string]any, error) {
	query, args := "SELECT kind, id, field, value FROM local_overrides", []any{}
	switch {
	case kind != "" && id != "":
		query, args = query+" WHERE kind = ? AND id = ?", []any{kind, id}
	case kind != "":
		query, args = query+" WHERE kind = ?", []any{kind}
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.Ref]map[string]any{}
	for rows.Next() {
		var ref core.Ref
		var field, value string
		if err := rows.Scan(&ref.Kind, &ref.ID, &field, &value); err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			return nil, fmt.Errorf("data: local override %s.%s: %w", RefText(ref), field, err)
		}
		if out[ref] == nil {
			out[ref] = map[string]any{}
		}
		out[ref][field] = v
	}
	return out, rows.Err()
}

func (s *Store) loadRecord(q querier, ref core.Ref) (*Record, error) {
	rec, err := scanRecord(q.QueryRow("SELECT "+recordCols+" FROM records WHERE kind = ? AND id = ?", ref.Kind, ref.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, RefText(ref))
	}
	if err != nil {
		return nil, err
	}
	local, err := loadLocal(q, ref.Kind, ref.ID)
	if err != nil {
		return nil, err
	}
	rec.Local = local[ref]
	s.coerce(&rec)
	return &rec, nil
}

const relationCols = "id, from_kind, from_id, kind, to_kind, to_id, position, meta, created_at"

func scanRelation(sc interface{ Scan(...any) error }) (Relation, error) {
	var rel Relation
	var meta string
	var created int64
	err := sc.Scan(&rel.ID, &rel.From.Kind, &rel.From.ID, &rel.Kind, &rel.To.Kind, &rel.To.ID, &rel.Position, &meta, &created)
	if err != nil {
		return rel, err
	}
	if meta != "" && meta != "{}" && meta != "null" {
		if err := json.Unmarshal([]byte(meta), &rel.Meta); err != nil {
			return rel, fmt.Errorf("data: meta of relation %s: %w", rel.ID, err)
		}
	}
	rel.CreatedAt = fromNano(created)
	return rel, nil
}

func queryRelations(q querier, where string, args ...any) ([]Relation, error) {
	rows, err := q.Query("SELECT "+relationCols+" FROM relations WHERE "+where+" ORDER BY position, created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		rel, err := scanRelation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

func relationsOf(q querier, ref core.Ref, relKind string, dir RelationDir) ([]Relation, error) {
	var conds []string
	var args []any
	if dir == Outgoing || dir == Both {
		conds, args = append(conds, "(from_kind = ? AND from_id = ?)"), append(args, ref.Kind, ref.ID)
	}
	if dir == Incoming || dir == Both {
		conds, args = append(conds, "(to_kind = ? AND to_id = ?)"), append(args, ref.Kind, ref.ID)
	}
	where := "(" + strings.Join(conds, " OR ") + ")"
	if relKind != "" {
		where, args = where+" AND kind = ?", append(args, relKind)
	}
	return queryRelations(q, where, args...)
}

func findRelation(q querier, from core.Ref, kind string, to core.Ref) (*Relation, error) {
	rels, err := queryRelations(q, "from_kind = ? AND from_id = ? AND kind = ? AND to_kind = ? AND to_id = ?", from.Kind, from.ID, kind, to.Kind, to.ID)
	if err != nil || len(rels) == 0 {
		return nil, err
	}
	return &rels[0], nil
}

// --- the one write path -----------------------------------------------------

// txn is one write: every verb expresses itself as FieldChanges and applies
// them here, so the change set is exactly what happened and can be inverted.
type txn struct {
	s              *Store
	tx             *sql.Tx
	num            int64
	now            time.Time
	check          bool // undo and redo: a change's Old is what we expect to find
	changes        []FieldChange
	conflicts      []Conflict
	touched        map[core.Ref]bool // true when the record's content changed
	explicitReadAt map[core.Ref]bool // true when caller explicitly wrote readAt
}

func (t *txn) touch(ref core.Ref, content bool) { t.touched[ref] = t.touched[ref] || content }

func (t *txn) conflict(c Conflict) { t.conflicts = append(t.conflicts, c) }

// apply performs one change. It fills Old from the current state, skips a
// change that changes nothing, and in check mode records a conflict when the
// current state is not the one the change expected. It never refuses a
// change whose target exists.
func (t *txn) apply(ch FieldChange) error {
	switch ch.Op {
	case OpCreate:
		return t.applyCreate(ch)
	case OpDelete:
		return t.applyDelete(ch)
	case OpField, OpLocal, OpStatus:
		return t.applyValue(ch)
	case OpRelate:
		return t.applyRelate(ch)
	case OpUnrelate:
		return t.applyUnrelate(ch)
	}
	return fmt.Errorf("data: unknown change op %q", ch.Op)
}

func (t *txn) applyCreate(ch FieldChange) error {
	rec, err := decodeAs[Record](ch.New)
	if err != nil {
		return fmt.Errorf("data: create %s: %w", RefText(ch.Ref), err)
	}
	if _, err := t.s.loadRecord(t.tx, ch.Ref); err == nil {
		if t.check {
			t.conflict(Conflict{Kind: ConflictMissing, Ref: ch.Ref, Message: "already exists; left as it is"})
			return nil
		}
		return fmt.Errorf("%w: %s", ErrExists, RefText(ch.Ref))
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	rec.Kind, rec.ID = ch.Ref.Kind, ch.Ref.ID
	rec.ChangeSetNumber, rec.UpdatedAt = t.num, t.now
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = t.now
	}
	if rec.ArchiveStatus == "" {
		rec.ArchiveStatus = ArchiveActive
	}
	if rec.StarStatus == "" {
		rec.StarStatus = StarUnstarred
	}
	if rec.Fields == nil {
		rec.Fields = map[string]any{}
	}
	var archived any
	if rec.ArchivedAt != nil {
		archived = rec.ArchivedAt.UnixNano()
	}
	var snoozed any
	if rec.SnoozedUntil != nil {
		snoozed = rec.SnoozedUntil.UnixNano()
	}
	readOnly := rec.ReadOnly
	if readOnly == nil {
		readOnly = map[string]bool{}
	}
	if err := t.checkUnique(rec.Ref(), rec.Kind, rec.Fields, nil); err != nil {
		return err
	}
	_, err = t.tx.Exec("INSERT INTO records ("+recordCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		rec.Kind, rec.ID, toJSON(rec.Fields), toJSON(readOnly), rec.ArchiveStatus, rec.StarStatus, t.num, unixNano(rec.CreatedAt), unixNano(t.now), archived, snoozed)
	if err != nil {
		return err
	}
	for field, v := range rec.Local {
		if _, err := t.tx.Exec("INSERT INTO local_overrides (kind, id, field, value) VALUES (?, ?, ?, ?)", rec.Kind, rec.ID, field, toJSON(v)); err != nil {
			return err
		}
	}
	ch.Old, ch.HadOld, ch.New, ch.HasNew = nil, false, rec, true
	t.changes = append(t.changes, ch)
	t.touch(ch.Ref, true)
	return nil
}

func (t *txn) applyDelete(ch FieldChange) error {
	rec, err := t.s.loadRecord(t.tx, ch.Ref)
	if errors.Is(err, ErrNotFound) && t.check {
		t.conflict(Conflict{Kind: ConflictMissing, Ref: ch.Ref, Message: "already gone"})
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := t.tx.Exec("DELETE FROM records WHERE kind = ? AND id = ?", ch.Ref.Kind, ch.Ref.ID); err != nil {
		return err
	}
	if _, err := t.tx.Exec("DELETE FROM local_overrides WHERE kind = ? AND id = ?", ch.Ref.Kind, ch.Ref.ID); err != nil {
		return err
	}
	ch.Old, ch.HadOld, ch.New, ch.HasNew = *rec, true, nil, false
	t.changes = append(t.changes, ch)
	delete(t.touched, ch.Ref)
	return nil
}

func (t *txn) applyValue(ch FieldChange) error {
	rec, err := t.s.loadRecord(t.tx, ch.Ref)
	if errors.Is(err, ErrNotFound) && t.check {
		t.conflict(Conflict{Kind: ConflictMissing, Ref: ch.Ref, Field: ch.Field, Message: "record is gone; change skipped"})
		return nil
	}
	if err != nil {
		return err
	}
	t.noteFieldWrite(ch)
	var cur any
	var had bool
	switch ch.Op {
	case OpField:
		cur, had = rec.Fields[ch.Field]
	case OpLocal:
		cur, had = rec.Local[ch.Field]
	case OpStatus:
		had, ch.HasNew = true, true
		switch ch.Field {
		case ColArchiveStatus:
			cur = rec.ArchiveStatus
		case ColStarStatus:
			cur = rec.StarStatus
		case ColSnoozedUntil:
			// Unlike the two statuses, a snooze may be absent.
			had, ch.HasNew = rec.SnoozedUntil != nil, ch.New != nil && Text(ch.New) != ""
			if had {
				cur = rec.SnoozedUntil.Format(TimeFormat)
			}
			if ch.HasNew {
				until, err := time.Parse(TimeFormat, Text(ch.New))
				if err != nil {
					return fmt.Errorf("data: %s must be a time like %s: %w", ColSnoozedUntil, TimeFormat, err)
				}
				ch.New = until.UTC().Format(TimeFormat)
			}
		default:
			return fmt.Errorf("data: %q is not a universal status column", ch.Field)
		}
	}
	if !ch.HasNew {
		ch.New = nil
	}
	if t.check && (had != ch.HadOld || !equalJSON(cur, ch.Old)) {
		t.conflict(Conflict{Kind: ConflictUndo, Ref: ch.Ref, Field: ch.Field, Expected: ch.Old, Actual: cur,
			Message: "value changed since; overwritten"})
	}
	ch.Old, ch.HadOld = cur, had
	if had == ch.HasNew && equalJSON(cur, ch.New) {
		return nil
	}
	switch ch.Op {
	case OpField:
		if ch.HasNew {
			rec.Fields[ch.Field] = ch.New
			if err := t.checkUnique(ch.Ref, rec.Kind, rec.Fields, []string{ch.Field}); err != nil {
				return err
			}
		} else {
			delete(rec.Fields, ch.Field)
		}
		_, err = t.tx.Exec("UPDATE records SET fields = ? WHERE kind = ? AND id = ?", toJSON(rec.Fields), ch.Ref.Kind, ch.Ref.ID)
	case OpLocal:
		if ch.HasNew {
			_, err = t.tx.Exec("INSERT INTO local_overrides (kind, id, field, value) VALUES (?, ?, ?, ?) ON CONFLICT (kind, id, field) DO UPDATE SET value = excluded.value",
				ch.Ref.Kind, ch.Ref.ID, ch.Field, toJSON(ch.New))
		} else {
			_, err = t.tx.Exec("DELETE FROM local_overrides WHERE kind = ? AND id = ? AND field = ?", ch.Ref.Kind, ch.Ref.ID, ch.Field)
		}
	case OpStatus:
		value := Text(ch.New)
		if ch.Field == ColStarStatus {
			_, err = t.tx.Exec("UPDATE records SET star_status = ? WHERE kind = ? AND id = ?", value, ch.Ref.Kind, ch.Ref.ID)
			break
		}
		if ch.Field == ColSnoozedUntil {
			var until any
			if ch.HasNew {
				parsed, _ := time.Parse(TimeFormat, value) // checked above
				until = parsed.UnixNano()
			}
			_, err = t.tx.Exec("UPDATE records SET snoozed_until = ? WHERE kind = ? AND id = ?", until, ch.Ref.Kind, ch.Ref.ID)
			break
		}
		// The archive status and its time are written together, so they never drift.
		var at any
		if value == ArchiveArchived {
			at = t.now.UnixNano()
		}
		_, err = t.tx.Exec("UPDATE records SET archive_status = ?, archived_at = ? WHERE kind = ? AND id = ?", value, at, ch.Ref.Kind, ch.Ref.ID)
	}
	if err != nil {
		return err
	}
	t.changes = append(t.changes, ch)
	t.touch(ch.Ref, ch.Op == OpField && !isBookkeepingField(ch.Field))
	return nil
}

func (t *txn) applyRelate(ch FieldChange) error {
	rel, err := decodeAs[Relation](ch.New)
	if err != nil {
		return fmt.Errorf("data: relate: %w", err)
	}
	if existing, err := findRelation(t.tx, rel.From, rel.Kind, rel.To); err != nil || existing != nil {
		return err // already related: nothing to do
	}
	if rel.ID == "" {
		rel.ID = newID()
	}
	if rel.CreatedAt.IsZero() {
		rel.CreatedAt = t.now
	}
	if rel.Position == 0 {
		var maxPos sql.NullInt64
		err := t.tx.QueryRow("SELECT MAX(position) FROM relations WHERE from_kind = ? AND from_id = ? AND kind = ?", rel.From.Kind, rel.From.ID, rel.Kind).Scan(&maxPos)
		if err != nil {
			return err
		}
		rel.Position = int(maxPos.Int64) + 1
	}
	meta := rel.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	_, err = t.tx.Exec("INSERT INTO relations ("+relationCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		rel.ID, rel.From.Kind, rel.From.ID, rel.Kind, rel.To.Kind, rel.To.ID, rel.Position, toJSON(meta), unixNano(rel.CreatedAt))
	if err != nil {
		return err
	}
	ch.Ref, ch.Old, ch.HadOld, ch.New, ch.HasNew = rel.From, nil, false, rel, true
	t.changes = append(t.changes, ch)
	return nil
}

func (t *txn) applyUnrelate(ch FieldChange) error {
	want, err := decodeAs[Relation](ch.Old)
	if err != nil {
		return fmt.Errorf("data: unrelate: %w", err)
	}
	rels, err := queryRelations(t.tx, "id = ?", want.ID)
	if err != nil {
		return err
	}
	if len(rels) == 0 {
		if t.check {
			t.conflict(Conflict{Kind: ConflictMissing, Ref: want.From, Field: want.Kind, Message: "relation is already gone"})
		}
		return nil
	}
	if _, err := t.tx.Exec("DELETE FROM relations WHERE id = ?", want.ID); err != nil {
		return err
	}
	ch.Ref, ch.Old, ch.HadOld, ch.New, ch.HasNew = rels[0].From, rels[0], true, nil, false
	t.changes = append(t.changes, ch)
	return nil
}

// write runs build in one transaction and records what it applied as one
// immutable change set. It returns nil when nothing changed and no conflict
// was found.
func (s *Store) write(actor core.Actor, action, summary string, undoOf int64, check bool, build func(t *txn) error) (*ChangeSet, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	now := time.Now().UTC().Round(0)
	if !now.After(s.last) {
		now = s.last.Add(time.Nanosecond) // every write has its own, later time
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	t := &txn{s: s, tx: tx, now: now, check: check, touched: map[core.Ref]bool{}}
	if err := tx.QueryRow("SELECT COALESCE(MAX(number), 0) + 1 FROM change_sets").Scan(&t.num); err != nil {
		return nil, err
	}
	if err := build(t); err != nil {
		return nil, err
	}
	if len(t.changes) == 0 && len(t.conflicts) == 0 {
		return nil, nil
	}
	if err := t.flushTouched(actor); err != nil {
		return nil, err
	}
	cs := &ChangeSet{Number: t.num, Actor: actor, Action: action, Summary: summary, UndoOf: undoOf,
		Changes: t.changes, Conflicts: t.conflicts, CreatedAt: now}
	if cs.Changes == nil {
		cs.Changes = []FieldChange{}
	}
	if cs.Conflicts == nil {
		cs.Conflicts = []Conflict{}
	}
	_, err = tx.Exec("INSERT INTO change_sets (number, actor, action, summary, undo_of, changes, conflicts, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		cs.Number, toJSON(cs.Actor), cs.Action, cs.Summary, cs.UndoOf, toJSON(cs.Changes), toJSON(cs.Conflicts), now.UnixNano())
	if err != nil {
		return nil, err
	}
	for _, target := range cs.Targets() {
		if _, err := tx.Exec("INSERT OR IGNORE INTO change_targets (change_set, kind, id) VALUES (?, ?, ?)", target.ChangeSet, target.Ref.Kind, target.Ref.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.last = now
	return cs, nil
}

// --- records ----------------------------------------------------------------

// CreateInput describes a record to create. ID is generated when empty.
type CreateInput struct {
	Kind     string
	ID       string
	Fields   map[string]any
	ReadOnly map[string]bool
}

// PatchInput describes a patch. A key set to nil is deleted. A WriteTime
// value becomes the write's timestamp. IfRevision, when positive, names the
// revision (ChangeSetNumber) the writer last saw.
type PatchInput struct {
	Ref        core.Ref
	Fields     map[string]any
	IfRevision int64
	Summary    string
}

// Get returns the record with the given identity. It resolves by ID alone:
// an archived record, or a record under an archived parent, is still found.
func (s *Store) Get(ref core.Ref) (*Record, error) { return s.loadRecord(s.db, ref) }

// Create stores a new record. Schema defaults fill the fields left out.
func (s *Store) Create(in CreateInput, actor core.Actor) (*Record, *ChangeSet, error) {
	if in.Kind == "" {
		return nil, nil, errors.New("data: create needs a kind")
	}
	ref := core.Ref{Kind: in.Kind, ID: in.ID}
	if ref.ID == "" {
		ref.ID = newID()
	}
	cs, err := s.write(actor, "create", "create "+RefText(ref), 0, false, func(t *txn) error {
		return t.createRecord(ref, in, actor)
	})
	if err != nil {
		return nil, nil, err
	}
	rec, err := s.Get(ref)
	return rec, cs, err
}

// value turns a written field value into its stored form: WriteTime becomes
// the write's timestamp, and an empty reference becomes nil (no reference).
func (t *txn) value(v any) (any, error) {
	if _, ok := v.(WriteTime); ok {
		return t.now.Format(TimeFormat), nil
	}
	nv, err := normalize(v)
	if err != nil {
		return nil, err
	}
	if m, ok := nv.(map[string]any); ok && len(m) == 2 {
		_, hasKind := m["kind"]
		if id, hasID := m["id"]; hasKind && hasID && id == "" {
			return nil, nil
		}
	}
	return nv, nil
}

// Patch changes fields of a record and never refuses over a stale revision:
// when IfRevision is positive and is not the record's current
// ChangeSetNumber, the write is applied and a Conflict is recorded on the
// returned change set. A read-only field is written as a local override, and
// archiveStatus, starStatus and snoozedUntil are written as the status
// columns they are. The change set is nil when the patch changed nothing.
func (s *Store) Patch(in PatchInput, actor core.Actor) (*Record, *ChangeSet, error) {
	summary := in.Summary
	if summary == "" {
		summary = "patch " + RefText(in.Ref) + ": " + strings.Join(in.keys(), ", ")
	}
	cs, err := s.write(actor, "patch", summary, 0, false, func(t *txn) error { return t.patch(in) })
	if err != nil {
		return nil, nil, err
	}
	rec, err := s.Get(in.Ref)
	return rec, cs, err
}

// PatchMany applies multiple record patches with Patch's semantics in one
// transaction and one immutable change set, so a single Undo takes all of
// them back together and a failing patch writes none of them.
func (s *Store) PatchMany(patches []PatchInput, action, summary string, actor core.Actor) (*ChangeSet, error) {
	if len(patches) == 0 {
		return nil, nil
	}
	if action == "" {
		action = "patch.batch"
	}
	if summary == "" {
		summary = fmt.Sprintf("patch %d records", len(patches))
	}
	return s.write(actor, action, summary, 0, false, func(t *txn) error {
		for _, in := range patches {
			if err := t.patch(in); err != nil {
				return err
			}
		}
		return nil
	})
}

// keys returns the patched field names in sorted order.
func (in PatchInput) keys() []string {
	keys := slices.Collect(maps.Keys(in.Fields))
	slices.Sort(keys)
	return keys
}

// patch applies one PatchInput inside the transaction: the IfRevision
// check, then each field as a field, local override or status change.
func (t *txn) patch(in PatchInput) error {
	rec, err := t.s.loadRecord(t.tx, in.Ref)
	if err != nil {
		return err
	}
	keys := in.keys()
	if in.IfRevision > 0 && in.IfRevision != rec.ChangeSetNumber {
		t.conflict(Conflict{Kind: ConflictRevision, Ref: in.Ref, Fields: keys, IfRevision: in.IfRevision, Revision: rec.ChangeSetNumber,
			Message: fmt.Sprintf("written over revision %d; the writer had seen revision %d", rec.ChangeSetNumber, in.IfRevision)})
	}
	for _, k := range keys {
		nv, err := t.value(in.Fields[k])
		if err != nil {
			return fmt.Errorf("field %q: %w", k, err)
		}
		ch := FieldChange{Ref: in.Ref, Op: OpField, Field: k, New: nv, HasNew: nv != nil}
		switch {
		case k == ColSnoozedUntil:
			ch.Op = OpStatus
		case k == ColArchiveStatus || k == ColStarStatus:
			if nv == nil {
				nv = map[string]any{ColArchiveStatus: ArchiveActive, ColStarStatus: StarUnstarred}[k]
			}
			ch.Op, ch.New = OpStatus, Text(nv)
		case rec.ReadOnly[k]:
			ch.Op = OpLocal
		}
		if err := t.apply(ch); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a record, its local overrides and its relations in one
// change set, so one undo brings all of it back.
func (s *Store) Delete(ref core.Ref, actor core.Actor) (*ChangeSet, error) {
	return s.write(actor, "delete", "delete "+RefText(ref), 0, false, func(t *txn) error {
		if _, err := s.loadRecord(t.tx, ref); err != nil {
			return err
		}
		rels, err := relationsOf(t.tx, ref, "", Both)
		if err != nil {
			return err
		}
		for _, rel := range rels {
			if err := t.apply(FieldChange{Ref: rel.From, Op: OpUnrelate, Old: rel}); err != nil {
				return err
			}
		}
		return t.apply(FieldChange{Ref: ref, Op: OpDelete})
	})
}

// change applies one change to an existing record as its own change set.
func (s *Store) change(action string, ch FieldChange, actor core.Actor) (*Record, *ChangeSet, error) {
	summary := action + " " + RefText(ch.Ref)
	if ch.Field != "" && ch.Op != OpStatus {
		summary += ": " + ch.Field
	}
	cs, err := s.write(actor, action, summary, 0, false, func(t *txn) error {
		if _, err := s.loadRecord(t.tx, ch.Ref); err != nil {
			return err
		}
		return t.apply(ch)
	})
	if err != nil {
		return nil, nil, err
	}
	rec, err := s.Get(ch.Ref)
	return rec, cs, err
}

func (s *Store) setStatus(action, column, value string, ref core.Ref, actor core.Actor) (*Record, *ChangeSet, error) {
	return s.change(action, FieldChange{Ref: ref, Op: OpStatus, Field: column, New: value, HasNew: true}, actor)
}

// Archive sets ArchiveStatus to archived and ArchivedAt together.
func (s *Store) Archive(ref core.Ref, actor core.Actor) (*Record, *ChangeSet, error) {
	return s.setStatus("archive", ColArchiveStatus, ArchiveArchived, ref, actor)
}

// Restore is the opposite of Archive.
func (s *Store) Restore(ref core.Ref, actor core.Actor) (*Record, *ChangeSet, error) {
	return s.setStatus("restore", ColArchiveStatus, ArchiveActive, ref, actor)
}

// Star sets StarStatus to starred.
func (s *Store) Star(ref core.Ref, actor core.Actor) (*Record, *ChangeSet, error) {
	return s.setStatus("star", ColStarStatus, StarStarred, ref, actor)
}

// Unstar is the opposite of Star.
func (s *Store) Unstar(ref core.Ref, actor core.Actor) (*Record, *ChangeSet, error) {
	return s.setStatus("unstar", ColStarStatus, StarUnstarred, ref, actor)
}

// Snooze hides a record until the given time. It is written like a status:
// the record's fields and UpdatedAt do not move, and one undo takes it back.
func (s *Store) Snooze(ref core.Ref, until time.Time, actor core.Actor) (*Record, *ChangeSet, error) {
	if until.IsZero() {
		return nil, nil, errors.New("data: snooze needs a time")
	}
	return s.change("snooze", FieldChange{Ref: ref, Op: OpStatus, Field: ColSnoozedUntil, New: until.UTC().Format(TimeFormat), HasNew: true}, actor)
}

// Unsnooze is the opposite of Snooze.
func (s *Store) Unsnooze(ref core.Ref, actor core.Actor) (*Record, *ChangeSet, error) {
	return s.change("unsnooze", FieldChange{Ref: ref, Op: OpStatus, Field: ColSnoozedUntil}, actor)
}

// RecordPaneInput records that input was sent to a pane. Only the owner's
// own prompt moves promptedAt, which orders the pane rail; input from an
// agent or the system is kept apart in lastAgentInputAt and lastAgentBy, so
// an agent's send-text never reorders the rail.
func (s *Store) RecordPaneInput(pane core.Ref, actor core.Actor) (*Record, *ChangeSet, error) {
	fields := map[string]any{"promptedAt": WriteTime{}}
	if actor.Kind != "user" {
		by := actor.ID
		if by == "" {
			by = actor.Label
		}
		if by == "" {
			by = actor.Kind
		}
		fields = map[string]any{"lastAgentInputAt": WriteTime{}, "lastAgentBy": by}
	}
	return s.Patch(PatchInput{Ref: pane, Fields: fields, Summary: "input to " + RefText(pane)}, actor)
}

// SetLocalOverride lays a local value over a field without changing the
// record's source Fields.
func (s *Store) SetLocalOverride(ref core.Ref, field string, value any, actor core.Actor) (*Record, *ChangeSet, error) {
	nv, err := normalize(value)
	if err != nil {
		return nil, nil, err
	}
	return s.change("local.set", FieldChange{Ref: ref, Op: OpLocal, Field: field, New: nv, HasNew: nv != nil}, actor)
}

// ClearLocalOverride removes a local override, showing the source value again.
func (s *Store) ClearLocalOverride(ref core.Ref, field string, actor core.Actor) (*Record, *ChangeSet, error) {
	return s.change("local.clear", FieldChange{Ref: ref, Op: OpLocal, Field: field}, actor)
}

// --- relations --------------------------------------------------------------

// RelateInput describes a relation to add. Position 0 appends after the
// last relation of the same kind from the same record.
type RelateInput struct {
	From     core.Ref
	Kind     string
	To       core.Ref
	Position int
	Meta     map[string]any
}

// Relate adds a relation. Neither end has to be a stored record. Relating
// two things that are already related returns the existing relation and a
// nil change set.
func (s *Store) Relate(in RelateInput, actor core.Actor) (*Relation, *ChangeSet, error) {
	if in.Kind == "" || in.From.Kind == "" || in.To.Kind == "" {
		return nil, nil, errors.New("data: relate needs from, kind and to")
	}
	meta, err := normalize(in.Meta)
	if err != nil {
		return nil, nil, err
	}
	rel := Relation{From: in.From, Kind: in.Kind, To: in.To, Position: in.Position}
	rel.Meta, _ = meta.(map[string]any)
	summary := fmt.Sprintf("relate %s -%s-> %s", RefText(in.From), in.Kind, RefText(in.To))
	cs, err := s.write(actor, "relate", summary, 0, false, func(t *txn) error {
		return t.apply(FieldChange{Ref: in.From, Op: OpRelate, New: rel})
	})
	if err != nil {
		return nil, nil, err
	}
	stored, err := findRelation(s.db, in.From, in.Kind, in.To)
	if err == nil && stored == nil {
		err = fmt.Errorf("%w: relation", ErrNotFound)
	}
	return stored, cs, err
}

// Unrelate removes a relation. Removing one that does not exist changes
// nothing and returns a nil change set.
func (s *Store) Unrelate(from core.Ref, kind string, to core.Ref, actor core.Actor) (*ChangeSet, error) {
	summary := fmt.Sprintf("unrelate %s -%s-> %s", RefText(from), kind, RefText(to))
	return s.write(actor, "unrelate", summary, 0, false, func(t *txn) error {
		rel, err := findRelation(t.tx, from, kind, to)
		if err != nil || rel == nil {
			return err
		}
		return t.apply(FieldChange{Ref: from, Op: OpUnrelate, Old: *rel})
	})
}

// Related returns the relations on ref. relKind "" means every kind.
func (s *Store) Related(ref core.Ref, relKind string, dir RelationDir) ([]Relation, error) {
	return relationsOf(s.db, ref, relKind, dir)
}

// RelatedRecords returns the stored records at the other end of ref's
// relations, in relation order. Ends that are not stored records (a tag, the
// workspace) are left out; read them from Related.
func (s *Store) RelatedRecords(ref core.Ref, relKind string, dir RelationDir) ([]Record, error) {
	rels, err := s.Related(ref, relKind, dir)
	if err != nil {
		return nil, err
	}
	seen := map[core.Ref]bool{}
	var out []Record
	for _, rel := range rels {
		other := rel.Other(ref)
		if seen[other] {
			continue
		}
		seen[other] = true
		rec, err := s.Get(other)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, nil
}

// Pin pins any record (a "pinned" relation from WorkspaceRef).
func (s *Store) Pin(ref core.Ref, actor core.Actor) (*ChangeSet, error) {
	_, cs, err := s.Relate(RelateInput{From: WorkspaceRef, Kind: RelPinned, To: ref}, actor)
	return cs, err
}

// Unpin is the opposite of Pin.
func (s *Store) Unpin(ref core.Ref, actor core.Actor) (*ChangeSet, error) {
	return s.Unrelate(WorkspaceRef, RelPinned, ref, actor)
}

// Mark marks any record (a "marked" relation from WorkspaceRef).
func (s *Store) Mark(ref core.Ref, actor core.Actor) (*ChangeSet, error) {
	_, cs, err := s.Relate(RelateInput{From: WorkspaceRef, Kind: RelMarked, To: ref}, actor)
	return cs, err
}

// Unmark is the opposite of Mark.
func (s *Store) Unmark(ref core.Ref, actor core.Actor) (*ChangeSet, error) {
	return s.Unrelate(WorkspaceRef, RelMarked, ref, actor)
}

// Tag tags any record (a "tag" relation from the record to the tag).
func (s *Store) Tag(ref core.Ref, tag string, actor core.Actor) (*ChangeSet, error) {
	_, cs, err := s.Relate(RelateInput{From: ref, Kind: RelTag, To: core.Ref{Kind: KindTag, ID: tag}}, actor)
	return cs, err
}

// Untag is the opposite of Tag.
func (s *Store) Untag(ref core.Ref, tag string, actor core.Actor) (*ChangeSet, error) {
	return s.Unrelate(ref, RelTag, core.Ref{Kind: KindTag, ID: tag}, actor)
}

// IsPinned reports whether ref is pinned.
func (s *Store) IsPinned(ref core.Ref) (bool, error) {
	rel, err := findRelation(s.db, WorkspaceRef, RelPinned, ref)
	return rel != nil, err
}

// IsMarked reports whether ref is marked.
func (s *Store) IsMarked(ref core.Ref) (bool, error) {
	rel, err := findRelation(s.db, WorkspaceRef, RelMarked, ref)
	return rel != nil, err
}

// Tags returns the tags on ref, in the order they were added.
func (s *Store) Tags(ref core.Ref) ([]string, error) {
	rels, err := s.Related(ref, RelTag, Outgoing)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rels))
	for i, rel := range rels {
		out[i] = rel.To.ID
	}
	return out, nil
}

// --- history, undo, redo ----------------------------------------------------

const changeSetCols = "c.number, c.actor, c.action, c.summary, c.undo_of, c.changes, c.conflicts, c.created_at"

func scanChangeSet(sc interface{ Scan(...any) error }) (ChangeSet, error) {
	var cs ChangeSet
	var actor, changes, conflicts string
	var created int64
	if err := sc.Scan(&cs.Number, &actor, &cs.Action, &cs.Summary, &cs.UndoOf, &changes, &conflicts, &created); err != nil {
		return cs, err
	}
	cs.CreatedAt = fromNano(created)
	for _, part := range []struct {
		src string
		dst any
	}{{actor, &cs.Actor}, {changes, &cs.Changes}, {conflicts, &cs.Conflicts}} {
		if err := json.Unmarshal([]byte(part.src), part.dst); err != nil {
			return cs, fmt.Errorf("data: change set %d: %w", cs.Number, err)
		}
	}
	return cs, nil
}

// ChangeSet returns one change set by number.
func (s *Store) ChangeSet(number int64) (*ChangeSet, error) {
	cs, err := scanChangeSet(s.db.QueryRow("SELECT "+changeSetCols+" FROM change_sets c WHERE c.number = ?", number))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: change set %d", ErrNotFound, number)
	}
	if err != nil {
		return nil, err
	}
	return &cs, nil
}

// LatestChangeSetNumber returns the number of the newest change set, or 0
// when nothing was written yet. A watcher compares it to tell that the store
// changed.
func (s *Store) LatestChangeSetNumber() (int64, error) {
	var n int64
	err := s.db.QueryRow("SELECT COALESCE(MAX(number), 0) FROM change_sets").Scan(&n)
	return n, err
}

// History returns change sets, newest first: all of them (ref nil), the ones
// that touched a noun (ref with an empty ID) or the ones that touched one
// record. limit <= 0 means no limit.
func (s *Store) History(ref *core.Ref, limit int) ([]ChangeSet, error) {
	query, args := "SELECT "+changeSetCols+" FROM change_sets c", []any{}
	switch {
	case ref != nil && ref.ID != "":
		query += " JOIN change_targets t ON t.change_set = c.number WHERE t.kind = ? AND t.id = ?"
		args = append(args, ref.Kind, ref.ID)
	case ref != nil:
		query += " WHERE c.number IN (SELECT change_set FROM change_targets WHERE kind = ?)"
		args = append(args, ref.Kind)
	}
	if limit <= 0 {
		limit = -1
	}
	query, args = query+" ORDER BY c.number DESC LIMIT ?", append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeSet
	for rows.Next() {
		cs, err := scanChangeSet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// invert returns the changes that take back changes, last change first.
func invert(changes []FieldChange) []FieldChange {
	out := make([]FieldChange, 0, len(changes))
	for i := len(changes) - 1; i >= 0; i-- {
		ch := changes[i]
		inv := FieldChange{Ref: ch.Ref, Field: ch.Field, Old: ch.New, HadOld: ch.HasNew, New: ch.Old, HasNew: ch.HadOld}
		switch ch.Op {
		case OpCreate:
			inv.Op = OpDelete
		case OpDelete:
			inv.Op = OpCreate
		case OpRelate:
			inv.Op = OpUnrelate
		case OpUnrelate:
			inv.Op = OpRelate
		default:
			inv.Op = ch.Op
		}
		out = append(out, inv)
	}
	return out
}

func (s *Store) revert(action string, target *ChangeSet, actor core.Actor) (*ChangeSet, error) {
	summary := fmt.Sprintf("%s #%d: %s", action, target.Number, target.Summary)
	return s.write(actor, action, summary, target.Number, true, func(t *txn) error {
		for _, ch := range invert(target.Changes) {
			if err := t.apply(ch); err != nil {
				return err
			}
		}
		return nil
	})
}

// Undo takes back any change set by writing a compensating one whose UndoOf
// names it. It works for every verb, because every verb is recorded as the
// same reversible changes. A value changed since is still put back, and the
// overwrite is recorded as a Conflict. The result is nil when there was
// nothing left to take back.
func (s *Store) Undo(changeSetNum int64, actor core.Actor) (*ChangeSet, error) {
	target, err := s.ChangeSet(changeSetNum)
	if err != nil {
		return nil, err
	}
	return s.revert("undo", target, actor)
}

// undoState reads the newest change sets (up to a bound) and reports, for
// each one that was undone, how many of its undos have not been redone.
func (s *Store) undoState() ([]ChangeSet, map[int64]int, map[int64]bool, error) {
	history, err := s.History(nil, 2000)
	if err != nil {
		return nil, nil, nil, err
	}
	undoTarget := map[int64]int64{} // undo change set -> the change set it undid
	for _, cs := range history {
		if cs.Action == "undo" {
			undoTarget[cs.Number] = cs.UndoOf
		}
	}
	undone := map[int64]int{}    // change set -> undos not redone
	redone := map[int64]bool{}   // undo change set -> a redo took it back
	for _, cs := range history { // newest first
		switch cs.Action {
		case "undo":
			undone[cs.UndoOf]++
		case "redo":
			if target, ok := undoTarget[cs.UndoOf]; ok && !redone[cs.UndoOf] {
				redone[cs.UndoOf] = true
				undone[target]--
			}
		}
	}
	return history, undone, redone, nil
}

func sameActor(a, b core.Actor) bool { return a.Kind == b.Kind && a.ID == b.ID }

// UndoLast takes back the actor's newest change set that is not itself an
// undo or redo and is not already undone. Other actors' writes are left
// alone. It reports ErrNotFound when the actor has nothing left to undo.
func (s *Store) UndoLast(actor core.Actor) (*ChangeSet, error) {
	history, undone, _, err := s.undoState()
	if err != nil {
		return nil, err
	}
	for _, cs := range history {
		if cs.Action == "undo" || cs.Action == "redo" || !sameActor(cs.Actor, actor) || undone[cs.Number] > 0 {
			continue
		}
		return s.Undo(cs.Number, actor)
	}
	return nil, fmt.Errorf("%w: nothing to undo", ErrNotFound)
}

// RedoLast re-applies the actor's newest undo that has not been redone. A
// newer write by the actor ends the redo chain, as in an editor.
func (s *Store) RedoLast(actor core.Actor) (*ChangeSet, error) {
	history, _, redone, err := s.undoState()
	if err != nil {
		return nil, err
	}
	for _, cs := range history {
		if !sameActor(cs.Actor, actor) {
			continue
		}
		switch {
		case cs.Action == "redo", cs.Action == "undo" && redone[cs.Number]:
			continue
		case cs.Action == "undo":
			return s.Redo(cs.Number, actor)
		}
		break // a newer write of the actor's own
	}
	return nil, fmt.Errorf("%w: nothing to redo", ErrNotFound)
}

// Redo re-applies an undone change set by undoing its undo. changeSetNum is
// either the undo's own number or the number of the change set it undid (its
// latest undo is used).
func (s *Store) Redo(changeSetNum int64, actor core.Actor) (*ChangeSet, error) {
	target, err := s.ChangeSet(changeSetNum)
	if err != nil {
		return nil, err
	}
	if target.Action != "undo" {
		undo, err := scanChangeSet(s.db.QueryRow("SELECT "+changeSetCols+" FROM change_sets c WHERE c.undo_of = ? AND c.action = 'undo' ORDER BY c.number DESC LIMIT 1", changeSetNum))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: change set %d has no undo to redo", ErrNotFound, changeSetNum)
		}
		if err != nil {
			return nil, err
		}
		target = &undo
	}
	return s.revert("redo", target, actor)
}

// --- natural keys -----------------------------------------------------------

// plainField reports whether name can be written into a JSON path as is.
func plainField(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

func plainKind(kind string) string {
	var b strings.Builder
	for _, r := range kind {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// ensureUniqueIndexes creates one expression index per unique field of the
// registry's schemas, so GetByField does not scan the kind.
func (s *Store) ensureUniqueIndexes() error {
	for _, schema := range s.reg.List() {
		for _, field := range schema.UniqueFields {
			if !plainField(field) || strings.ContainsAny(schema.Kind, "'\"") {
				continue
			}
			stmt := fmt.Sprintf("CREATE INDEX IF NOT EXISTS records_by_%s_%s ON records (json_extract(fields, '$.%s')) WHERE kind = '%s'",
				plainKind(schema.Kind), field, field, schema.Kind)
			if _, err := s.db.Exec(stmt); err != nil {
				return fmt.Errorf("data: index %s.%s: %w", schema.Kind, field, err)
			}
		}
	}
	return nil
}

func byField(q querier, kind, field, value string, scopeField string, scope *core.Ref) ([]Record, error) {
	if !plainField(field) || (scopeField != "" && !plainField(scopeField)) {
		return nil, fmt.Errorf("data: %q is not a field name GetByField can look up", field)
	}
	query := "SELECT " + recordCols + " FROM records WHERE kind = ? AND json_extract(fields, '$." + field + "') = ?"
	args := []any{kind, value}
	if scopeField != "" {
		if scope == nil {
			query += " AND json_extract(fields, '$." + scopeField + ".id') IS NULL"
		} else {
			query += " AND json_extract(fields, '$." + scopeField + ".kind') = ? AND json_extract(fields, '$." + scopeField + ".id') = ?"
			args = append(args, scope.Kind, scope.ID)
		}
	}
	rows, err := q.Query(query+" ORDER BY created_at, seq", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) finish(q querier, recs []Record, what string) (*Record, error) {
	if len(recs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	// Prefer a record that is not archived; among those, the oldest.
	pick := recs[0]
	for _, rec := range recs {
		if !rec.IsArchived() {
			pick = rec
			break
		}
	}
	return s.loadRecord(q, pick.Ref())
}

// GetByField finds a record of kind by the text value of one of its fields:
// an extension by name, a page by slug. IDs stay opaque and stable; this is
// the lookup by natural key. When several records share the value, the
// oldest unarchived one is returned. The fields a schema lists in
// UniqueFields are indexed.
func (s *Store) GetByField(kind, field, value string) (*Record, error) {
	recs, err := byField(s.db, kind, field, value, "", nil)
	if err != nil {
		return nil, err
	}
	return s.finish(s.db, recs, kind+" with "+field+" "+value)
}

// GetByFieldIn is GetByField within one scope: the record whose scopeField
// refers to scope (a page by slug within its pane).
func (s *Store) GetByFieldIn(kind, field, value, scopeField string, scope core.Ref) (*Record, error) {
	recs, err := byField(s.db, kind, field, value, scopeField, &scope)
	if err != nil {
		return nil, err
	}
	return s.finish(s.db, recs, kind+" with "+field+" "+value+" in "+RefText(scope))
}

// checkUnique records a conflict when a write gives a record a value of one
// of its schema's unique fields that another unarchived record already has.
// only narrows the check to the fields just written (nil checks them all).
// The write goes ahead either way.
func (t *txn) checkUnique(ref core.Ref, kind string, fields map[string]any, only []string) error {
	schema, ok := t.s.reg.Get(kind)
	if !ok || len(schema.UniqueFields) == 0 {
		return nil
	}
	var scope *core.Ref
	if schema.UniqueScope != "" {
		if r, ok := AsRef(fields[schema.UniqueScope]); ok {
			scope = &r
		}
	}
	for _, field := range schema.UniqueFields {
		if only != nil && !slices.Contains(only, field) && !slices.Contains(only, schema.UniqueScope) {
			continue
		}
		value, isText := fields[field].(string)
		if !isText || value == "" || !plainField(field) {
			continue
		}
		others, err := byField(t.tx, kind, field, value, schema.UniqueScope, scope)
		if err != nil {
			return err
		}
		for _, other := range others {
			if other.Ref() == ref || other.IsArchived() {
				continue
			}
			t.conflict(Conflict{Kind: ConflictUnique, Ref: ref, Field: field, Actual: value,
				Message: fmt.Sprintf("%s %q is already used by %s; written anyway", field, value, RefText(other.Ref()))})
			break
		}
	}
	return nil
}
