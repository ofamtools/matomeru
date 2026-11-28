package backup

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func sum(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

// copyTestBinary puts the running test binary, a real Go executable, at dst.
func copyTestBinary(t *testing.T, dst string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, body, 0o755); err != nil {
		t.Fatal(err)
	}
}

func zipEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(body)
	}
	return out
}

func TestInspectBinaryReadsGoBuildInfo(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := InspectBinary(self)
	if m.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", m.GoVersion, runtime.Version())
	}
	if !strings.Contains(m.Module, "matomeru") {
		t.Errorf("Module = %q, want the matomeru module", m.Module)
	}
	if m.Name != filepath.Base(self) || m.Size == 0 || m.ModTime.IsZero() || len(m.SHA256) != 64 {
		t.Errorf("incomplete metadata: %+v", m)
	}
	if m.Version == "" || m.CommitSHA == "" {
		t.Errorf("Version and CommitSHA must never be empty: %+v", m)
	}
}

func TestInspectBinaryNonGoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "o-script")
	write(t, path, "#!/bin/sh\necho hi\n", 0o755)
	m := InspectBinary(path)
	if m.Version != "unknown" || m.CommitSHA != "—" || m.Dirty || m.GoVersion != "" {
		t.Errorf("non-Go file: %+v", m)
	}
	if m.Size != 18 || m.SHA256 != sum("#!/bin/sh\necho hi\n") {
		t.Errorf("size/hash: %+v", m)
	}
}

func TestPseudoVersionCommit(t *testing.T) {
	for version, want := range map[string]string{
		"v0.0.0-20261120123456-abcdef123456":         "abcdef123456",
		"v1.2.4-0.20261120123456-0123456789ab+dirty": "0123456789ab",
		"v1.53.0":    "",
		"(devel)":    "",
		"v1.0.0-rc1": "",
	} {
		got := ""
		if sub := pseudoVersionRe.FindStringSubmatch(version); sub != nil {
			got = sub[2]
		}
		if got != want {
			t.Errorf("%s: commit %q, want %q", version, got, want)
		}
	}
}

func TestManaged(t *testing.T) {
	for name, want := range map[string]bool{
		"mtm": true, "mtm-hook": true, "o": true, "o-lane": true, "mtm-inbox-notify": true,
		"mtm.bak-20261102-152821": false, "o-lane.bak-1": false, "git": false, "open": false, "mtmx": false,
	} {
		if got := Managed(name); got != want {
			t.Errorf("Managed(%q) = %v, want %v", name, got, want)
		}
	}
}

// fixture lays out a bin dir, a config dir and a data dir under one root.
func fixture(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	copyTestBinary(t, filepath.Join(bin, "mtm"))
	write(t, filepath.Join(bin, "o-script"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(bin, "mtm.bak-20261102"), "old", 0o755)
	write(t, filepath.Join(bin, "unrelated"), "x", 0o755)
	if err := os.Symlink("o-script", filepath.Join(bin, "o-link")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "config", "settings.json"), `{"a":1}`, 0o644)
	write(t, filepath.Join(root, "config", "nested", "themes.json"), `{}`, 0o644)
	write(t, filepath.Join(root, "data", "mtm.db"), "db-bytes", 0o644)
	write(t, filepath.Join(root, "data", "mtm.db-wal"), "wal-bytes", 0o644)
	write(t, filepath.Join(root, "data", "drafts", "d1.md"), "draft", 0o644)
	write(t, filepath.Join(root, "data", "attachments", "a", "pic.png"), "png", 0o644)
	write(t, filepath.Join(root, "data", "mtm.log"), "not backed up", 0o644)
	return Options{
		BinDir:    bin,
		ConfigDir: filepath.Join(root, "config"),
		DBPath:    filepath.Join(root, "data", "mtm.db"),
		OutDir:    filepath.Join(root, "out"),
		Now:       func() time.Time { return time.Date(2026, 11, 27, 9, 30, 5, 0, time.UTC) },
	}
}

func TestCreateWritesZipWithManifest(t *testing.T) {
	opts := fixture(t)
	rep, err := Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(opts.OutDir, "ofam-backup-20261127-093005.zip")
	if rep.ZipPath != want || rep.ZipSize == 0 {
		t.Fatalf("zip path %q size %d, want %q", rep.ZipPath, rep.ZipSize, want)
	}
	got := zipEntries(t, rep.ZipPath)
	for name, body := range map[string]string{
		"bin/o-script":               "#!/bin/sh\n",
		"config/settings.json":       `{"a":1}`,
		"config/nested/themes.json":  `{}`,
		"data/mtm.db":                "db-bytes",
		"data/mtm.db-wal":            "wal-bytes",
		"data/drafts/d1.md":          "draft",
		"data/attachments/a/pic.png": "png",
	} {
		if got[name] != body {
			t.Errorf("zip %s = %q, want %q", name, got[name], body)
		}
	}
	for _, absent := range []string{"bin/mtm.bak-20261102", "bin/unrelated", "bin/o-link", "data/mtm.log"} {
		if _, ok := got[absent]; ok {
			t.Errorf("zip must not hold %s", absent)
		}
	}
	if len(got) != 9 { // 2 binaries, 2 config, 2 db, 1 draft, 1 attachment, the manifest
		t.Errorf("zip holds %d entries, want 9: %v", len(got), keys(got))
	}

	manifest := got[ManifestName]
	if manifest == "" || manifest != rep.Manifest {
		t.Fatalf("manifest missing or differs from the report")
	}
	self := InspectBinary(filepath.Join(opts.BinDir, "mtm"))
	for _, frag := range []string{
		"# OFAM backup manifest",
		"ofam-backup-20261127-093005.zip",
		"2026-11-27 09:30:05",
		runtime.GOOS + "/" + runtime.GOARCH,
		"| Binary | Version | Commit SHA | Module | Go | Modified (UTC) | Size | SHA-256 |",
		"| `mtm` | " + self.Version + " | ",
		runtime.Version(),
		self.SHA256,
		"| `o-script` | unknown | — |",
		sum("#!/bin/sh\n"),
		"| `config/settings.json` |",
		sum(`{"a":1}`),
		"## Not in this backup",
		"symlink to o-script",
		"## Restore instructions",
		"mtm backup inspect",
		"unzip -o",
		"mtm daemon stop",
	} {
		if !strings.Contains(manifest, frag) {
			t.Errorf("manifest lacks %q\n%s", frag, manifest)
		}
	}
	if len(rep.Binaries) != 2 || rep.Binaries[0].Name != "mtm" || rep.Binaries[0].SHA256 != self.SHA256 || rep.Binaries[0].GoVersion != runtime.Version() {
		t.Errorf("report binaries: %+v", rep.Binaries)
	}

	// The mode survives, so a restored binary is still executable.
	zr, err := zip.OpenReader(rep.ZipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "bin/mtm" && f.Mode().Perm()&0o100 == 0 {
			t.Errorf("bin/mtm lost its executable bit: %v", f.Mode())
		}
	}
	// Nothing temporary is left beside the zip.
	left, _ := os.ReadDir(opts.OutDir)
	if len(left) != 1 {
		t.Errorf("out dir holds %d entries, want only the zip", len(left))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCreateBinsOnlyAndOnly(t *testing.T) {
	opts := fixture(t)
	opts.BinsOnly = true
	opts.Only = []string{"o-script", "o-absent"}
	opts.OutPath = filepath.Join(t.TempDir(), "one.zip")
	rep, err := Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	got := zipEntries(t, rep.ZipPath)
	if len(got) != 2 || got["bin/o-script"] == "" || got[ManifestName] == "" {
		t.Fatalf("zip entries: %v", keys(got))
	}
	if len(rep.Files) != 0 || strings.Contains(rep.Manifest, "mtm daemon stop") {
		t.Errorf("a binaries-only backup must not describe data: %+v", rep.Files)
	}
	if !strings.Contains(rep.Manifest, "o-absent: not installed") {
		t.Errorf("manifest must name the missing binary:\n%s", rep.Manifest)
	}
}

func TestCreateNeverOverwritesAndFailsWhenEmpty(t *testing.T) {
	opts := fixture(t)
	opts.OutPath = filepath.Join(t.TempDir(), "keep.zip")
	write(t, opts.OutPath, "precious", 0o644)
	if _, err := Create(opts); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already exists", err)
	}
	if body, _ := os.ReadFile(opts.OutPath); string(body) != "precious" {
		t.Fatalf("existing file was changed: %q", body)
	}

	empty := Options{BinDir: t.TempDir(), BinsOnly: true, OutPath: filepath.Join(t.TempDir(), "none.zip")}
	if _, err := Create(empty); err == nil || !strings.Contains(err.Error(), "nothing to back up") {
		t.Fatalf("err = %v, want nothing to back up", err)
	}
	if _, err := os.Stat(empty.OutPath); err == nil {
		t.Fatal("an empty backup must not leave a zip")
	}
}

func TestInspectZipAndList(t *testing.T) {
	opts := fixture(t)
	rep, err := Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := InspectZip(rep.ZipPath)
	if err != nil || manifest != rep.Manifest {
		t.Fatalf("InspectZip: %v", err)
	}

	plain := filepath.Join(opts.OutDir, "plain.zip")
	f, err := os.Create(plain)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	if _, err := zw.Create("x"); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	f.Close()
	if _, err := InspectZip(plain); err == nil || !strings.Contains(err.Error(), "not an mtm backup") {
		t.Fatalf("err = %v, want not an mtm backup", err)
	}

	list, err := List(opts.OutDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != rep.ZipPath || list[0].Size != rep.ZipSize {
		t.Fatalf("List = %+v", list)
	}
	if none, err := List(t.TempDir()); err != nil || len(none) != 0 {
		t.Fatalf("empty dir: %v %v", none, err)
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
