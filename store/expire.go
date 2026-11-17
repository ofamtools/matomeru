package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
)

// Expiry (data-retention plan, lane R9): the one sanctioned way rows leave
// an append-only table. An append-only table's DELETE guard (guardDelete)
// refuses a row unless the deleting transaction holds a row of
// __deck_expiry for the table that the row falls under (older than its
// cutoff, or below its count bound, and never among the newest rows). A
// row of __deck_expiry can only be written while the function
// deck_expiry_token answers 1 for its token, and only this package
// registers that function, for the tokens of the Expire calls running in
// this process. So, with the rule in the file itself:
//
//   - a plain DELETE (this build, an older build, a sqlite3 shell) is
//     refused: the transaction holds no expiry row;
//   - writing an expiry row from a sqlite3 shell or an older build fails
//     (no such function: deck_expiry_token);
//   - UPDATE stays refused for every row, as before;
//   - Expire writes its row, deletes, removes its row and commits in one
//     transaction, so no other connection ever sees the row.
//
// Only dropping the guard trigger itself gets around it, as before.

// expiryTable holds the expiry grants of the running transaction (never a
// committed row).
const expiryTable = "__deck_expiry"

// expiryFunction is the SQL function the grant's insert guard asks.
const expiryFunction = "deck_expiry_token"

// ExpireBatch is how many rows, in insertion order, one expiry transaction
// examines; ExpirePause the wait between two of them, so a writer in
// another process (waiting in its busy handler) gets the lock between
// batches. A batch of 1000 holds the write lock about 20 ms on the owner's
// journal (rows of about 1 KB: the delete guard parses each row's time,
// and the write-count trigger bumps once per row).
const (
	ExpireBatch = 1000
	ExpirePause = 25 * time.Millisecond
)

// tokens are the grants of the Expire calls running in this process.
var tokens sync.Map

func init() {
	sqlite.MustRegisterScalarFunction(expiryFunction, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		token, _ := args[0].(string)
		if _, ok := tokens.Load(token); ok && token != "" {
			return int64(1), nil
		}
		return int64(0), nil
	})
}

// expirySchema creates the grant table and its insert guard; both are
// idempotent.
var expirySchema = []string{
	`CREATE TABLE IF NOT EXISTS ` + expiryTable + ` (
		name TEXT PRIMARY KEY,
		token TEXT NOT NULL,
		field TEXT NOT NULL,
		before INTEGER NOT NULL,
		below INTEGER NOT NULL,
		upto INTEGER NOT NULL
	)`,
	`CREATE TRIGGER IF NOT EXISTS __deck_expiry_grant BEFORE INSERT ON ` + expiryTable +
		` WHEN ` + expiryFunction + `(NEW.token) IS NOT 1 BEGIN SELECT RAISE(ABORT, 'expiry is the store''s own'); END`,
	`CREATE TRIGGER IF NOT EXISTS __deck_expiry_update BEFORE UPDATE ON ` + expiryTable +
		` BEGIN SELECT RAISE(ABORT, 'expiry is the store''s own'); END`,
}

// guardName is an append-only table's guard trigger for an operation.
func guardName(table, operation string) string {
	return "__deck_" + table + "_" + strings.ToLower(operation)
}

// guardDelete is an append-only table's DELETE guard: it refuses a row the
// transaction's expiry grant does not cover.
func guardDelete(table string) string {
	literal := "'" + strings.ReplaceAll(table, "'", "''") + "'"
	return `CREATE TRIGGER ` + quote(guardName(table, "DELETE")) + ` BEFORE DELETE ON ` + quote(table) +
		` WHEN NOT EXISTS (SELECT 1 FROM ` + expiryTable + ` e WHERE e.name = ` + literal + ` AND OLD.rowid < e.upto` +
		` AND ((e.before > 0 AND json_extract(OLD.row, e.field) < e.before) OR OLD.rowid < e.below))` +
		` BEGIN SELECT RAISE(ABORT, 'append-only table'); END`
}

// ensureExpiry creates the grant table and gives every append-only table
// the expiry-aware DELETE guard (a file declared before it has the guard
// that refuses every row). It reads first and writes only when a guard
// differs, so an ordinary start takes no write lock.
func ensureExpiry(db *sql.DB) error {
	for _, stmt := range expirySchema {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	tables, err := names(db, `SELECT d.name FROM __deck_tables d JOIN sqlite_master m ON m.type = 'table' AND m.name = d.name WHERE d.append_only = 1`)
	if err != nil {
		return err
	}
	var stale []string
	for _, t := range tables {
		var text string
		err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, guardName(t, "DELETE")).Scan(&text)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if text != guardDelete(t) {
			stale = append(stale, t)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range stale {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + quote(guardName(t, "DELETE"))); err != nil {
			return err
		}
		if _, err := tx.Exec(guardDelete(t)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Expiry is what Expire removes from an append-only table. A row goes when
// its Field (a JSON number, such as Unix milliseconds) is below Before, or
// when it is older (by insertion) than the newest Keep rows; it never goes
// when it is among the newest MinKeep rows, and the newest row always stays
// (a later insert then never reuses an expired row's sequence, so an
// After cursor keeps working). Before 0 and Keep 0 remove nothing.
type Expiry struct {
	Field   string
	Before  int64
	Keep    int
	MinKeep int
	// Batch is the rows one transaction examines (0: ExpireBatch); Pause
	// the wait between transactions (0: ExpirePause; negative: none).
	Batch int
	Pause time.Duration
}

// Expired is what an expiry finds (ExpireCount: the table's rows and bytes,
// the id and document lengths, an estimate of the file's share; the rows
// and bytes it would remove and the range of their Field) or did (Expire:
// the rows it removed, its transactions, the longest write-lock hold, the
// time it took).
type Expired struct {
	Rows        int           `json:"rows"`
	Bytes       int64         `json:"bytes"`
	Remove      int           `json:"remove"`
	RemoveBytes int64         `json:"remove_bytes"`
	Oldest      int64         `json:"oldest,omitempty"`
	Newest      int64         `json:"newest,omitempty"`
	Batches     int           `json:"batches,omitempty"`
	LongestHold time.Duration `json:"longest_hold,omitempty"`
	Took        time.Duration `json:"took,omitempty"`
}

func (e Expiry) off() bool { return e.Before <= 0 && e.Keep <= 0 }

// condition is the rows the expiry removes, between two sequences;
// arguments: field, before, before, field-free bounds below and upto.
const expiryCondition = `rowid < ? AND ((? > 0 AND json_extract(row, ?) < ?) OR rowid < ?)`

// bounds reads, in q's transaction or connection, the sequence below which
// rows go by count (0: no count limit) and the sequence from which rows
// always stay (the newest max(MinKeep, 1)).
func (e Expiry) bounds(q interface {
	QueryRow(string, ...any) *sql.Row
}, table string) (below, upto int64, err error) {
	nth := func(n int) (int64, error) {
		var seq int64
		err := q.QueryRow(`SELECT rowid FROM `+quote(table)+` ORDER BY rowid DESC LIMIT 1 OFFSET ?`, n-1).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return seq, err
	}
	if e.Keep > 0 {
		if below, err = nth(e.Keep); err != nil {
			return 0, 0, err
		}
	}
	upto, err = nth(max(e.MinKeep, 1))
	return below, upto, err
}

// ExpireCount is what Expire would remove now, read in a read transaction:
// it writes nothing.
func (s *Store) ExpireCount(table string, e Expiry) (Expired, error) {
	var out Expired
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	name := quote(table)
	if err := tx.QueryRow(`SELECT count(*), coalesce(sum(length(id) + length(row)), 0) FROM `+name).Scan(&out.Rows, &out.Bytes); err != nil {
		return out, err
	}
	if e.off() {
		return out, nil
	}
	below, upto, err := e.bounds(tx, table)
	if err != nil {
		return out, err
	}
	var oldest, newest sql.NullInt64
	err = tx.QueryRow(`SELECT count(*), coalesce(sum(length(id) + length(row)), 0), min(json_extract(row, ?)), max(json_extract(row, ?)) FROM `+name+` WHERE `+expiryCondition,
		"$."+e.Field, "$."+e.Field, upto, e.Before, "$."+e.Field, e.Before, below).Scan(&out.Remove, &out.RemoveBytes, &oldest, &newest)
	out.Oldest, out.Newest = oldest.Int64, newest.Int64
	return out, err
}

// Expire removes the rows e names from an append-only table, oldest first,
// in transactions of e.Batch rows each (each one a write lock of a few
// tens of milliseconds, a pause between them), and stops at the first
// batch that removes nothing: rows are inserted in time order, so what
// follows is newer (a late row older than the cutoff waits for a later
// run). It is the only way rows leave such a table (see guardDelete). It
// answers Remove, Batches, LongestHold and Took; ExpireCount, read before
// without the lock, gives the bytes and the time range.
func (s *Store) Expire(table string, e Expiry) (out Expired, err error) {
	start := time.Now()
	defer func() { out.Took = time.Since(start) }()
	if e.off() {
		return out, nil
	}
	batch := orDefault(e.Batch, ExpireBatch)
	pause := e.Pause
	if pause == 0 {
		pause = ExpirePause
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return out, err
	}
	grant := hex.EncodeToString(token[:])
	tokens.Store(grant, true)
	defer tokens.Delete(grant)
	name := quote(table)
	field := "$." + e.Field
	var cursor int64
	for {
		var held time.Duration
		removed, end, err := func() (int, int64, error) {
			tx, err := s.db.Begin() // BEGIN IMMEDIATE: the write lock from here
			if err != nil {
				return 0, 0, err
			}
			locked := time.Now()
			defer func() { held = time.Since(locked) }()
			defer tx.Rollback()
			below, upto, err := e.bounds(tx, table)
			if err != nil {
				return 0, 0, err
			}
			var end sql.NullInt64
			if err := tx.QueryRow(`SELECT max(rowid) FROM (SELECT rowid FROM `+name+` WHERE rowid > ? ORDER BY rowid LIMIT ?)`, cursor, batch).Scan(&end); err != nil || !end.Valid {
				return 0, 0, err
			}
			if _, err := tx.Exec(`INSERT INTO `+expiryTable+` (name, token, field, before, below, upto) VALUES (?, ?, ?, ?, ?, ?)`, table, grant, field, e.Before, below, upto); err != nil {
				return 0, 0, err
			}
			res, err := tx.Exec(`DELETE FROM `+name+` WHERE rowid > ? AND rowid <= ? AND `+expiryCondition, cursor, end.Int64, upto, e.Before, field, e.Before, below)
			if err != nil {
				return 0, 0, err
			}
			n, _ := res.RowsAffected()
			if _, err := tx.Exec(`DELETE FROM `+expiryTable+` WHERE name = ?`, table); err != nil {
				return 0, 0, err
			}
			if n == 0 {
				return 0, end.Int64, nil // rolled back: nothing written
			}
			return int(n), end.Int64, tx.Commit()
		}()
		if err != nil {
			return out, err
		}
		if removed == 0 {
			return out, nil
		}
		out.Batches++
		out.LongestHold = max(out.LongestHold, held)
		out.Remove += removed
		s.expired(table)
		cursor = end
		if pause > 0 {
			time.Sleep(pause)
		}
	}
}

// expired tells this process's Version counter of a batch's commit.
func (s *Store) expired(table string) { s.noteWrites(map[string]bool{table: true}) }

// orDefault is n, or def when n is not positive.
func orDefault(n, def int) int {
	if n > 0 {
		return n
	}
	return def
}
