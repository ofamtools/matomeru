package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
)

// realCopyNames are the names (after mtm.db.bak-) of the database copies
// the owner had on 2026-11-17: hand-made labels, -pre-vX.Y.Z release
// copies, the migrations' UTC stamps, some with their -wal and -shm.
var realCopyNames = []string{
	"2026-11-08-v080",
	"2026-11-08-v080-shm",
	"2026-11-08-v080-wal",
	"2026-11-09-v090",
	"2026-11-09-v090-shm",
	"2026-11-09-v090-wal",
	"2026-11-10-follow",
	"2026-11-10-follow-shm",
	"2026-11-10-follow-wal",
	"2026-11-10-relations",
	"2026-11-10-relations-shm",
	"2026-11-10-relations-wal",
	"2026-11-10-v0130",
	"2026-11-10-v0130-shm",
	"2026-11-10-v0130-wal",
	"2026-11-10-v0140",
	"2026-11-10-v0140-shm",
	"2026-11-10-v0140-wal",
	"2026-11-10-v0150",
	"2026-11-10-v0150-shm",
	"2026-11-10-v0150-wal",
	"2026-11-10-v0160",
	"2026-11-10-v0160-shm",
	"2026-11-10-v0160-wal",
	"2026-11-10-v0170",
	"2026-11-10-v0170-shm",
	"2026-11-10-v0170-wal",
	"2026-11-10-v099",
	"2026-11-11-v0180",
	"2026-11-11-v0190",
	"2026-11-11-v0200",
	"2026-11-12-v0220",
	"2026-11-13-before-action-items-wipe",
	"2026-11-13-focus",
	"2026-11-13-needsyou",
	"2026-11-13-ptc",
	"2026-11-13-rail",
	"2026-11-13-railtriage",
	"2026-11-13-v0210",
	"2026-11-13-v0260",
	"2026-11-13-v0270",
	"2026-11-13-v0280",
	"2026-11-13-v0290",
	"2026-11-13-v0300",
	"20261028-project-indent",
	"20261110T102203Z",
	"20261116-1611-pre-v0.39.0",
	"20261116-1801-pre-v0.40.0",
	"20261116-1957-pre-v0.41.0",
	"20261117T000737-pre-v0.42.0",
	"20261117T005811Z",
	"20261117T044739-pre-v0.45.0",
	"20261117T071800-pre-v0.49.0",
	"20261117T094845Z-changes",
	"animation-20261028-050227",
}

// touchCopies makes empty files named name in dir, each an hour older than
// the one before it (the first is the newest).
func touchCopies(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(i+1) * time.Hour)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDatabaseCopiesUnit: the row's unit joins each real copy with its -wal
// and -shm, so the 55 files the owner had are 37 copies, in both the old
// place (beside the file) and backups/.
func TestDatabaseCopiesUnit(t *testing.T) {
	t.Parallel()
	app := openWith(t, Options{Data: seeded(t)})
	dir := filepath.Dir(app.dataPath)
	var old, moved []string
	for _, n := range realCopyNames {
		old = append(old, "mtm.db.bak-"+n)
		moved = append(moved, "mtm.db.bak-moved-"+n)
	}
	touchCopies(t, dir, old...)
	touchCopies(t, filepath.Join(dir, "backups"), moved...)
	copies := databaseCopies(filepath.Join(dir, "backups", "mtm.db.bak-*"), filepath.Join(dir, "mtm.db.bak-*"))
	if len(copies) != 2*37 {
		t.Fatalf("55 files in each place are 37 copies each: %d", len(copies))
	}
	for _, c := range copies {
		if strings.HasSuffix(c.Name, "-wal") || strings.HasSuffix(c.Name, "-shm") || (strings.HasSuffix(c.Name, "2026-11-08-v080") && len(c.Files) != 3) {
			t.Fatalf("a copy's -wal and -shm are part of it: %+v", c)
		}
	}
	rule := ruleThing(t, app)
	if rule["count"] != float64(2*37) || rule["error"] != "" {
		t.Fatalf("the rule counts the same copies: %v", rule)
	}
}

func ruleThing(t *testing.T, app *App) map[string]any {
	t.Helper()
	var rule map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, app, "retention-rule", "get", databaseCopiesRow, "--json")), &rule); err != nil {
		t.Fatal(err)
	}
	return rule
}

// TestDatabaseBackupKeepsFive: database backup writes into backups/ and
// applies the rule; one case per keep value. keep 5: with five copies
// there before, the sixth makes the oldest go (beside the file, -wal and
// -shm with it). keep 0: no count limit, all six stay (all are younger
// than 14 days).
func TestDatabaseBackupKeepsFive(t *testing.T) {
	t.Parallel()
	for _, keep := range []int{5, 0} {
		app := openWith(t, Options{Data: seeded(t)})
		dir := filepath.Dir(app.dataPath)
		touchCopies(t, filepath.Join(dir, "backups"), "mtm.db.bak-20261116T010000Z-a", "mtm.db.bak-20261116T000000Z-b", "mtm.db.bak-20261115T000000Z-c")
		touchCopies(t, dir, "mtm.db.bak-20261114-pre-v0.1.0", "mtm.db.bak-20261113-pre-v0.0.9", "mtm.db.bak-20261113-pre-v0.0.9-wal", "mtm.db.bak-20261113-pre-v0.0.9-shm")
		// The four files beside the file are older than the three in backups/.
		for i, n := range []string{"mtm.db.bak-20261114-pre-v0.1.0", "mtm.db.bak-20261113-pre-v0.0.9", "mtm.db.bak-20261113-pre-v0.0.9-wal", "mtm.db.bak-20261113-pre-v0.0.9-shm"} {
			at := time.Now().Add(-time.Duration(10+min(i, 1)) * time.Hour)
			if err := os.Chtimes(filepath.Join(dir, n), at, at); err != nil {
				t.Fatal(err)
			}
		}
		var asked []string
		app.r.SetRetentionRemover(func(p string) error { asked = append(asked, filepath.Base(p)); return nil })
		if err := app.r.Settings().Set("retention.database-copies.keep", keep); err != nil {
			t.Fatal(err)
		}
		var got struct {
			Path string                 `json:"path"`
			Rule *ochazuke.RetentionRun `json:"rule"`
		}
		if err := json.Unmarshal([]byte(mustCLI(t, app, "database", "backup", "pre v0.51.0", "--json")), &got); err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(got.Path) != filepath.Join(dir, "backups") || !strings.HasSuffix(got.Path, "-pre-v0.51.0") {
			t.Fatalf("the copy lands in backups/ with its label: %s", got.Path)
		}
		if _, err := os.Stat(got.Path); err != nil {
			t.Fatal(err)
		}
		slices.Sort(asked)
		want := []string{"mtm.db.bak-20261113-pre-v0.0.9", "mtm.db.bak-20261113-pre-v0.0.9-shm", "mtm.db.bak-20261113-pre-v0.0.9-wal"}
		if keep == 0 {
			want = nil
		}
		if !slices.Equal(asked, want) || got.Rule == nil || got.Rule.Held {
			t.Fatalf("keep %d: removed %v, rule %+v", keep, asked, got.Rule)
		}
	}
}
