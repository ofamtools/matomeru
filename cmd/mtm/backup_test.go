package main

import (
	"archive/zip"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type backupReport struct {
	ZipPath  string `json:"zipPath"`
	ZipSize  int64  `json:"zipSize"`
	Binaries []struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		CommitSHA string `json:"commitSHA"`
		GoVersion string `json:"goVersion"`
		SHA256    string `json:"sha256"`
	} `json:"binaries"`
	Files []struct {
		Archive string `json:"archive"`
		Note    string `json:"note"`
	} `json:"files"`
}

// backupFixture gives a bin dir holding the running test binary as "mtm" and
// a script as "o-tool", a config dir, and a real store with one task.
func backupFixture(t *testing.T) (c cli, binDir, outDir string) {
	t.Helper()
	root := t.TempDir()
	binDir, outDir = filepath.Join(root, "bin"), filepath.Join(root, "out")
	cfg := filepath.Join(root, "config")
	for _, dir := range []string{binDir, cfg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "mtm"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "o-tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MTM_CONFIG_DIR", cfg)
	c = cli{t: t, db: filepath.Join(root, "data", "mtm.db")}
	c.ok("record", "create", "task", "--title", "Survives the backup")
	return c, binDir, outDir
}

func TestBackupCreateInspectList(t *testing.T) {
	c, binDir, outDir := backupFixture(t)

	rep := decode[backupReport](t, c.ok("backup", "--bin-dir", binDir, "--dir", outDir, "--json"))
	if filepath.Dir(rep.ZipPath) != outDir || !strings.HasPrefix(filepath.Base(rep.ZipPath), "ofam-backup-") || rep.ZipSize == 0 {
		t.Fatalf("zip path %q size %d", rep.ZipPath, rep.ZipSize)
	}
	if len(rep.Binaries) != 2 || rep.Binaries[0].Name != "mtm" || rep.Binaries[0].GoVersion != runtime.Version() ||
		len(rep.Binaries[0].SHA256) != 64 || rep.Binaries[1].Version != "unknown" {
		t.Fatalf("binaries: %+v", rep.Binaries)
	}

	// The database in the zip is a VACUUM INTO snapshot: one file, no WAL,
	// and it opens with the task in it.
	zr, err := zip.OpenReader(rep.ZipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"MANIFEST.md", "bin/mtm", "bin/o-tool", "config/settings.json", "data/mtm.db"} {
		if !names[want] {
			t.Errorf("zip lacks %s: %v", want, names)
		}
	}
	if names["data/mtm.db-wal"] {
		t.Error("a snapshot must not carry a WAL file")
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	src, err := zr.Open("data/mtm.db")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(restored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	src.Close()
	dst.Close()
	db, err := sql.Open(sqliteDriver, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("restored database: %q %v", check, err)
	}
	if got := (cli{t: t, db: restored}).ok("record", "list", "task"); !strings.Contains(got, "Survives the backup") {
		t.Fatalf("restored database lost the task: %s", got)
	}

	manifest := c.ok("backup", "inspect", rep.ZipPath)
	for _, frag := range []string{
		"| Binary | Version | Commit SHA | Module | Go | Modified (UTC) | Size | SHA-256 |",
		rep.Binaries[0].SHA256, runtime.Version(), "consistent snapshot (VACUUM INTO)", "## Restore instructions",
	} {
		if !strings.Contains(manifest, frag) {
			t.Errorf("inspect lacks %q\n%s", frag, manifest)
		}
	}

	list := decode[[]struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}](t, c.ok("backup", "list", "--dir", outDir, "--json"))
	if len(list) != 1 || list[0].Path != rep.ZipPath || list[0].Size != rep.ZipSize {
		t.Fatalf("list: %+v", list)
	}
	if table := c.ok("backup", "list", "--dir", outDir); !strings.Contains(table, rep.ZipPath) || !strings.Contains(table, "SIZE") {
		t.Fatalf("list table: %s", table)
	}
}

func TestBackupBinsOnlyTableAndUsage(t *testing.T) {
	c, binDir, outDir := backupFixture(t)
	out := filepath.Join(outDir, "pre-update.zip")
	table := c.ok("backup", "--bin-dir", binDir, "--out", out, "--bins-only", "--only", "mtm")
	for _, frag := range []string{"BINARY", "VERSION", "COMMIT", "mtm", runtime.Version(), "1 binaries and 0 config/data files", out} {
		if !strings.Contains(table, frag) {
			t.Errorf("summary lacks %q\n%s", frag, table)
		}
	}
	if strings.Contains(table, "o-tool") {
		t.Errorf("--only mtm backed up o-tool:\n%s", table)
	}
	if msg := c.fails("backup", "--bin-dir", binDir, "--out", out, "--bins-only"); !strings.Contains(msg, "already exists") {
		t.Errorf("overwrite: %s", msg)
	}
	if msg := c.fails("backup", "inspect"); !strings.Contains(msg, "usage: mtm backup") {
		t.Errorf("usage: %s", msg)
	}
	if msg := c.fails("backup", "inspect", filepath.Join(outDir, "absent.zip")); !strings.Contains(msg, "absent.zip") {
		t.Errorf("missing zip: %s", msg)
	}
	if got := c.ok("backup", "list", "--dir", t.TempDir()); !strings.Contains(got, "no backups") {
		t.Errorf("empty list: %s", got)
	}
}
