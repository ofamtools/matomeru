package main

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/store"
)

// The database noun (data-retention plan, lane R4): mtm's database file,
// its size, the tables' shares, and its copies. database backup is the one
// way a copy is made (store.BackupCopy: the migrations call the same
// function), into backups/ beside the file, and the retention row
// database-copies keeps them: the newest 5, none older than 14 days, never
// the newest one. The row also rules the copies made beside the file
// before this (mtm.db.bak-*): nothing is moved.

const (
	databaseNoun = "database"
	// databaseCopiesRow is the retention row over the copies.
	databaseCopiesRow = "database-copies"
	// databaseCopyUnit joins a copy with its -wal, -shm or -journal file
	// (the first capture is the copy's name).
	databaseCopyUnit = `^(.+?)(?:-wal|-shm|-journal)?$`
)

var databaseCopyUnitRE = regexp.MustCompile(databaseCopyUnit)

// databaseCopiesRetention is the row: both places copies live, the unit,
// and its defaults (settings retention.database-copies.keep and .days).
func databaseCopiesRetention(dataPath string) ochazuke.RetentionRow {
	copies := filepath.Base(dataPath) + ".bak-*"
	return ochazuke.RetentionRow{
		Name:        databaseCopiesRow,
		Description: "Copies of mtm's database (database backup, the migrations, a release's copy): the newest 5, none older than 14 days, never the newest one",
		Paths:       []string{"{data}/" + store.BackupsDir + "/" + copies, "{data}/" + copies},
		Unit:        databaseCopyUnit,
		Keep:        5,
		Days:        14,
		MinKeep:     1,
	}
}

// defineDatabase declares the noun, its verbs (local: none needs the
// daemon) and, for a database file, the retention row; {data} is the
// file's folder.
func (a *App) defineDatabase() {
	n := a.r.Define(databaseNoun).About("mtm's database file: its size, its tables' shares, its copies; a consistent copy on request")
	a.localVerb(n, databaseNoun, "show", a.databaseShow, "Show the database file: path, size (with its -wal and -shm), each table's share, and its copies with their sizes and times, and what the database-copies rule would remove now (read-only)").Fetch()
	a.localVerb(n, databaseNoun, "backup", a.databaseBackup, "Write a consistent copy of the database (VACUUM INTO) into backups/ beside it, named mtm.db.bak-<UTC time>[-label], then apply the database-copies retention rule as after a write (retention-rule apply --after-write: a first large removal waits for you)").
		Params(map[string]ochazuke.Parameter{"label": {Type: "string", Description: "A word for the copy's name (pre-v0.51.0); characters a file name cannot hold become -"}}).Positional("label")
	a.localVerb(n, databaseNoun, "tables", a.storeTables, "List every declared table with its owner, key, version, append-only flag and row count (read-only; store tables)").Rows(storeTableNoun)
	if a.dataPath == "" || a.dataPath == ":memory:" {
		return
	}
	if err := a.r.DeclareRetention(databaseCopiesRetention(a.dataPath)); err != nil {
		a.r.UI().Notify("database: " + err.Error())
	}
}

// databaseFile is one file of the database or of a copy.
type databaseFile struct {
	Path  string    `json:"path"`
	Bytes int64     `json:"bytes"`
	Time  time.Time `json:"mtime"`
}

// databaseCopy is one copy: its files (a copy with -wal and -shm is one
// copy), their bytes, its newest file's time.
type databaseCopy struct {
	Name  string         `json:"name"`
	Bytes int64          `json:"bytes"`
	Time  time.Time      `json:"mtime"`
	Files []databaseFile `json:"files"`
}

// databaseTable is one table's or index's bytes and share of the file.
type databaseTable struct {
	Name    string  `json:"name"`
	Bytes   int64   `json:"bytes"`
	Percent float64 `json:"percent"`
}

// databaseInfo is database show's answer.
type databaseInfo struct {
	Path        string          `json:"path"`
	Bytes       int64           `json:"bytes"`
	Files       []databaseFile  `json:"files"`
	Tables      []databaseTable `json:"tables,omitempty"`
	Copies      []databaseCopy  `json:"copies"`
	CopiesBytes int64           `json:"copies_bytes"`
	Rule        ochazuke.Thing  `json:"rule,omitempty"`
}

// databaseShow is database.show: the rule's plan is read on the loop (the
// runtime's), the files and the table sizes off it. It writes nothing.
func (a *App) databaseShow(ctx ochazuke.Ctx) ochazuke.Result {
	var rule ochazuke.Thing
	if v, err := ctx.Runtime.ExecuteAt(ctx.Context, ochazuke.Request{Noun: ochazuke.RetentionNoun, Verb: "get", Args: ochazuke.Arguments{"name": databaseCopiesRow}}); err == nil {
		rule, _ = ochazuke.ThingOf(v)
	}
	path, db := a.dataPath, a.db
	return ochazuke.Pending(func(context.Context) (any, error) {
		return readDatabase(path, db, rule)
	})
}

// readDatabase reads the file's sizes, its tables' shares (a read-only
// connection, else db) and the copies in both places.
func readDatabase(path string, db *store.Store, rule ochazuke.Thing) (databaseInfo, error) {
	info := databaseInfo{Path: path, Files: []databaseFile{}, Copies: []databaseCopy{}, Rule: rule}
	if path == "" || path == ":memory:" {
		return info, nil // in memory: no file, no copies
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if f, ok := statFile(p); ok {
			info.Files = append(info.Files, f)
			info.Bytes += f.Bytes
		}
	}
	info.Tables = tableShares(path, db)
	base := filepath.Base(path) + ".bak-*"
	dir := filepath.Dir(path)
	info.Copies = databaseCopies(filepath.Join(dir, store.BackupsDir, base), filepath.Join(dir, base))
	for _, c := range info.Copies {
		info.CopiesBytes += c.Bytes
	}
	return info, nil
}

func statFile(path string) (databaseFile, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return databaseFile{}, false
	}
	return databaseFile{Path: path, Bytes: fi.Size(), Time: fi.ModTime()}, true
}

// tableShares is each table's and index's bytes, largest first; nil when
// the sizes cannot be read.
func tableShares(path string, db *store.Store) []databaseTable {
	sizes, err := func() (map[string]int64, error) {
		if ro, err := store.OpenReadOnly(path); err == nil {
			defer ro.Close()
			return ro.TableBytes()
		}
		return db.TableBytes()
	}()
	if err != nil {
		return nil
	}
	var total int64
	for _, n := range sizes {
		total += n
	}
	out := make([]databaseTable, 0, len(sizes))
	for name, n := range sizes {
		if n == 0 {
			continue
		}
		out = append(out, databaseTable{Name: name, Bytes: n, Percent: float64(n*1000/max(total, 1)) / 10})
	}
	slices.SortFunc(out, func(x, y databaseTable) int {
		return cmp.Or(cmp.Compare(y.Bytes, x.Bytes), strings.Compare(x.Name, y.Name))
	})
	return out
}

// databaseCopies lists the copies the globs match, newest first, each
// copy's files joined by the row's unit.
func databaseCopies(patterns ...string) []databaseCopy {
	byName := map[string]*databaseCopy{}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			f, ok := statFile(m)
			if !ok {
				continue
			}
			name := filepath.Join(filepath.Dir(m), databaseCopyUnitRE.FindStringSubmatch(filepath.Base(m))[1])
			c := byName[name]
			if c == nil {
				c = &databaseCopy{Name: name}
				byName[name] = c
			}
			c.Files = append(c.Files, f)
			c.Bytes += f.Bytes
			if f.Time.After(c.Time) {
				c.Time = f.Time
			}
		}
	}
	out := make([]databaseCopy, 0, len(byName))
	for _, c := range byName {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(x, y databaseCopy) int {
		if c := y.Time.Compare(x.Time); c != 0 {
			return c
		}
		return strings.Compare(y.Name, x.Name)
	})
	return out
}

// databaseBackupAnswer is database backup's answer: the copy, and what the
// rule did after it.
type databaseBackupAnswer struct {
	Path  string                 `json:"path"`
	Bytes int64                  `json:"bytes"`
	Rule  *ochazuke.RetentionRun `json:"rule,omitempty"`
}

// databaseBackup is database.backup: the copy (store.BackupCopy, the
// function the migrations call), then the database-copies row applied as
// its automatic run (retention-rule apply {after-write}: a first large removal
// waits for the owner, retention.first-run-ask).
func (a *App) databaseBackup(r *ochazuke.Runtime, c ochazuke.Context, args ochazuke.Arguments) (any, error) {
	path, err := a.db.BackupCopy(copyLabel(ochazuke.StringArg(args, "label", "")), a.now())
	if err != nil {
		return nil, err
	}
	answer := databaseBackupAnswer{Path: path}
	if f, ok := statFile(path); ok {
		answer.Bytes = f.Bytes
	}
	if !slices.ContainsFunc(r.RetentionRows(), func(row ochazuke.RetentionRow) bool { return row.Name == databaseCopiesRow }) {
		return answer, nil
	}
	v, err := r.ExecuteAt(c, ochazuke.Request{Noun: ochazuke.RetentionNoun, Verb: "apply", Args: ochazuke.Arguments{"name": databaseCopiesRow, "after-write": true}})
	if runs, ok := v.([]ochazuke.RetentionRun); err == nil && ok && len(runs) == 1 {
		answer.Rule = &runs[0]
	}
	return answer, nil
}

// copyLabel keeps a label's letters, digits, dots and dashes; anything
// else becomes - (a label is free text, a file name is not).
func copyLabel(label string) string {
	return strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(label, "-"), "-")
}
