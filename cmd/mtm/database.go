package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/retain"

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
	a.localVerb(n, databaseNoun, "compact", a.databaseCompact, "Give the file's free pages (the space removed rows left, such as expired activity) back to the disk: the first time a copy (database backup pre-compact), then one VACUUM that rewrites the whole file and switches it to incremental auto-vacuum (about half a second after the activity rule, other mtm processes' writes wait for it); afterwards short incremental steps").History(ochazuke.HistoryChange)
	a.localVerb(n, databaseNoun, "tables", a.storeTables, "List every declared table with its owner, key, version, append-only flag and row count (read-only; store tables)").Rows(storeTableNoun)
	if a.dataPath == "" || a.dataPath == ":memory:" {
		return
	}
	if err := a.r.DeclareRetention(databaseCopiesRetention(a.dataPath), a.activityRetention()); err != nil {
		a.r.UI().Notify("database: " + err.Error())
	}
	if err := a.r.DeclareSetting(ochazuke.Setting{Name: activityCompactSetting, Type: ochazuke.SettingBoolean, Default: true, Section: "data",
		Description: "After the activity rule removes entries, give their space back to the disk (database compact: the first time a copy and one whole rewrite of the file, then short steps); off, the space stays inside the file and later writes reuse it"}); err != nil {
		a.r.UI().Notify("database: " + err.Error())
	}
}

// The activity row (data-retention plan, lane R9): the history journal's
// entries (what the agents and you did: turns, reviews and the context
// picker read it) older than 7 days go, through the store's one expiry
// path (store.Expire; the table stays append-only for everything else).
// The owner asked for the clearing itself, so the row is declared approved
// with his words: its first automatic run removes at once.
const (
	activityRetentionRow   = "activity"
	activityCompactSetting = "retention.activity.compact"
	activityDecision       = `owner 2026-11-17: "clear up activity history that is older than 7 days old"`
)

func (a *App) activityRetention() ochazuke.RetentionRow {
	return ochazuke.RetentionRow{
		Name:        activityRetentionRow,
		Description: "Activity history (the journal turns, reviews and the context picker read): entries older than 7 days, never the newest",
		Days:        7,
		MinKeep:     1,
		Approved:    activityDecision,
		Expire:      a.expireActivity,
	}
}

// expireActivity is the activity row's cut: a count (the dry run, a read)
// or the removal; after a removal, with retention.activity.compact on, the
// file gives the space back (databaseCompact's routine).
func (a *App) expireActivity(cut ochazuke.RetentionCut) (ochazuke.RetentionCount, error) {
	if a.hist == nil {
		return ochazuke.RetentionCount{}, nil // no journal here: nothing to remove
	}
	var before int64
	if !cut.Before.IsZero() {
		before = cut.Before.UnixMilli()
	}
	a.hist.SetHorizon(before)
	got, err := a.hist.Expire(before, cut.Keep, cut.MinKeep, cut.DryRun)
	out := ochazuke.RetentionCount{Count: got.Rows, Bytes: got.Bytes, Remove: got.Remove, RemoveBytes: got.RemoveBytes}
	if err != nil || got.Remove == 0 {
		return out, err
	}
	if cut.DryRun {
		out.What = fmt.Sprintf("%d activity entries from %s to %s", got.Remove, dayOf(got.Oldest), dayOf(got.Newest))
		return out, nil
	}
	out.What = fmt.Sprintf("%d activity entries in %d batches (longest write lock %s, %s)", got.Remove, got.Batches, got.LongestHold.Round(time.Millisecond), got.Took.Round(time.Millisecond))
	if a.r.Settings().Bool(activityCompactSetting) {
		done, err := a.compact("pre-compact")
		if err != nil {
			return out, err
		}
		out.What += "; " + done.line()
	}
	return out, nil
}

func dayOf(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.DateOnly) }

// compacted is database compact's answer: the copy made before a switch,
// and what the store did.
type compacted struct {
	store.Compacted
	Copy string `json:"copy,omitempty"`
}

func (c compacted) line() string {
	what := "file " + retain.Size(c.Before) + " -> " + retain.Size(c.After)
	if c.Switched {
		what += " (switched to incremental auto-vacuum, copy " + filepath.Base(c.Copy) + ")"
	}
	return what
}

// compact is database compact's routine: a copy first when the file is
// still to be switched (the one whole rewrite), then store.Compact.
func (a *App) compact(label string) (compacted, error) {
	var out compacted
	if a.dataPath == "" || a.dataPath == ":memory:" {
		return out, nil
	}
	sp, err := a.db.Space()
	if err != nil {
		return out, err
	}
	if sp.FreePages == 0 {
		out.Before, out.After = sp.Pages*sp.PageSize, sp.Pages*sp.PageSize
		return out, nil
	}
	if sp.AutoVacuum != "incremental" {
		if out.Copy, err = a.db.BackupCopy(label, a.now()); err != nil {
			return out, err
		}
	}
	out.Compacted, err = a.db.Compact()
	return out, err
}

// databaseCompact is database.compact.
func (a *App) databaseCompact(r *ochazuke.Runtime, c ochazuke.Context, args ochazuke.Arguments) (any, error) {
	return a.compact("pre-compact")
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
	// Space is the file's pages: free pages are space removed rows left
	// inside it (database compact gives it back).
	Space *store.Space `json:"space,omitempty"`
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
	if sp, err := db.Space(); err == nil {
		info.Space = &sp
	}
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
