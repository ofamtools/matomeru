package store

import (
	"database/sql"
	"strings"
)

// Write counts in SQLite (__deck_writes, WriteCount): each declared table
// has an AFTER INSERT, UPDATE and DELETE trigger that adds one to the
// table's row of __deck_writes in the writing statement itself. Every
// writer of the file moves the count, whatever it is: a process of an
// older build (which also bumps it from Go, counting a transaction twice,
// never missing one), Migrate's raw UPDATEs, a rewrite's writes into
// another table, a sqlite3 shell. The trigger writes only __deck_writes,
// which has no triggers, so it never recurses, and it runs in the
// statement's own transaction, so it takes no lock of its own.
//
// Declare creates a new table's triggers in the declaring transaction;
// Open checks every declared table has them (a file declared before they
// existed, a table an older build renamed) and adds the missing ones, with
// CREATE TRIGGER IF NOT EXISTS, so several processes opening at once agree.

// writeOps are the statements a write trigger follows.
var writeOps = []string{"insert", "update", "delete"}

// bumpWrites adds one to a table's write count.
const bumpWrites = `INSERT INTO __deck_writes (name, n) VALUES (?, 1) ON CONFLICT(name) DO UPDATE SET n = n + 1`

// writeTrigger is the name of table's write trigger after op. The prefix
// is not "__deck_" + a table name, so it never meets an append-only
// table's guard triggers.
func writeTrigger(table, op string) string { return "__deckcount_" + table + "_" + op }

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func createWriteTriggers(ex execer, table string) error {
	literal := "'" + strings.ReplaceAll(table, "'", "''") + "'"
	for _, op := range writeOps {
		if _, err := ex.Exec(`CREATE TRIGGER IF NOT EXISTS ` + quote(writeTrigger(table, op)) + ` AFTER ` + strings.ToUpper(op) + ` ON ` + quote(table) +
			` BEGIN INSERT INTO __deck_writes (name, n) VALUES (` + literal + `, 1) ON CONFLICT(name) DO UPDATE SET n = n + 1; END`); err != nil {
			return err
		}
	}
	return nil
}

func dropWriteTriggers(ex execer, table string) error {
	for _, op := range writeOps {
		if _, err := ex.Exec(`DROP TRIGGER IF EXISTS ` + quote(writeTrigger(table, op))); err != nil {
			return err
		}
	}
	return nil
}

// ensureWriteTriggers gives every declared table its write triggers and
// drops a write trigger that sits on another table than its name says (a
// table an older build renamed carries its old name's triggers). It reads
// first and writes only when something is missing or misplaced, so an
// ordinary start takes no write lock.
func ensureWriteTriggers(db *sql.DB) error {
	tables, err := names(db, `SELECT d.name FROM __deck_tables d JOIN sqlite_master m ON m.type = 'table' AND m.name = d.name`)
	if err != nil {
		return err
	}
	on := map[string]string{} // trigger → the table it sits on
	rows, err := db.Query(`SELECT name, tbl_name FROM sqlite_master WHERE type = 'trigger' AND name LIKE '__deckcount_%'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, table string
		if err := rows.Scan(&name, &table); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(name, "__deckcount_") {
			on[name] = table
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	want := map[string]string{}
	var missing []string
	for _, t := range tables {
		complete := true
		for _, op := range writeOps {
			want[writeTrigger(t, op)] = t
			complete = complete && on[writeTrigger(t, op)] == t
		}
		if !complete {
			missing = append(missing, t)
		}
	}
	var stale []string
	for name, table := range on {
		if want[name] != table {
			stale = append(stale, name)
		}
	}
	if len(missing) == 0 && len(stale) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range stale {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + quote(name)); err != nil {
			return err
		}
	}
	for _, t := range missing {
		if err := createWriteTriggers(tx, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func names(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
