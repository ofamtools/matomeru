// Package backup writes a zip of the installed OFAM binaries, the Matomeru
// config and the Matomeru data, with a MANIFEST.md at the root of the zip that
// lists the version and commit of every binary in it. Standard library only.
//
// A backup is taken before an install or update replaces anything, so the
// manifest answers "what exactly was here?" without unpacking the zip.
package backup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	// ManifestName is the manifest's path inside every backup zip.
	ManifestName = "MANIFEST.md"
	// ArchivePrefix starts the default file name of every backup zip.
	ArchivePrefix = "ofam-backup-"

	unknownVersion = "unknown"
	noCommit       = "—"
	stampLayout    = "20060102-150405"
	timeLayout     = "2006-01-02 15:04:05"
)

// BinaryMeta is what a backup records about one binary.
type BinaryMeta struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Module     string    `json:"module,omitempty"`
	Version    string    `json:"version"`
	GoVersion  string    `json:"goVersion,omitempty"`
	CommitSHA  string    `json:"commitSHA"`
	CommitTime string    `json:"commitTime,omitempty"`
	Dirty      bool      `json:"dirty"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"modTime"`
	SHA256     string    `json:"sha256"`
}

// FileMeta is what a backup records about one config or data file.
type FileMeta struct {
	Archive string    `json:"archive"` // the path inside the zip
	Source  string    `json:"source"`  // where it was read from
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	SHA256  string    `json:"sha256"`
	Note    string    `json:"note,omitempty"`
}

// Options says what to back up and where to write the zip. Empty paths take
// the defaults under the user's home directory.
type Options struct {
	BinDir    string   // default ~/bin
	ConfigDir string   // default ~/.config/matomeru
	DBPath    string   // default ~/.local/share/matomeru/mtm.db; drafts/ and attachments/ are read beside it
	OutPath   string   // the zip to write; wins over OutDir
	OutDir    string   // where the default-named zip goes; default the home directory
	BinsOnly  bool     // skip the config and the data
	Only      []string // back up only these binaries (by name); empty means every managed binary
	// DBDriver names a registered database/sql SQLite driver. When it is
	// registered the database is snapshotted with VACUUM INTO, which is safe
	// while mtm is running; otherwise the database file is copied together
	// with its -wal and -shm files.
	DBDriver string
	Now      func() time.Time // default time.Now
}

// Report describes a finished backup.
type Report struct {
	ZipPath   string       `json:"zipPath"`
	ZipSize   int64        `json:"zipSize"`
	CreatedAt time.Time    `json:"createdAt"`
	Hostname  string       `json:"hostname"`
	OS        string       `json:"os"`
	Arch      string       `json:"arch"`
	BinDir    string       `json:"binDir"`
	ConfigDir string       `json:"configDir,omitempty"`
	DBPath    string       `json:"dbPath,omitempty"`
	Binaries  []BinaryMeta `json:"binaries"`
	Files     []FileMeta   `json:"files"`
	Skipped   []string     `json:"skipped,omitempty"`
	Manifest  string       `json:"-"`
}

// Archive is one existing backup zip, as List reports it.
type Archive struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// pseudoVersionRe matches the tail of a Go pseudo-version: a 14-digit commit
// time and the first 12 hex digits of the commit.
var pseudoVersionRe = regexp.MustCompile(`[.-](\d{14})-([0-9a-f]{12})(\+dirty)?$`)

// Managed reports whether name is a binary this family installs: mtm, o, and
// every mtm-* and o-* tool. Leftovers of earlier manual backups (*.bak-*) are
// not.
func Managed(name string) bool {
	if strings.Contains(name, ".bak") {
		return false
	}
	return name == "mtm" || name == "o" || strings.HasPrefix(name, "mtm-") || strings.HasPrefix(name, "o-")
}

// InspectBinary reads everything a manifest row needs from the file at path.
// A file that is not a Go binary (a script, a foreign executable) still gets
// its size, time and hash, with the version "unknown".
func InspectBinary(path string) BinaryMeta {
	m := describeBinary(path)
	if sum, err := hashFile(path); err == nil {
		m.SHA256 = sum
	}
	return m
}

// describeBinary is InspectBinary without the hash, which Create computes
// while it streams the file into the zip.
func describeBinary(path string) BinaryMeta {
	m := BinaryMeta{Name: filepath.Base(path), Path: path, Version: unknownVersion, CommitSHA: noCommit}
	if st, err := os.Stat(path); err == nil {
		m.Size, m.ModTime = st.Size(), st.ModTime().UTC()
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return m
	}
	m.Module, m.GoVersion = info.Main.Path, info.GoVersion
	if m.Module == "" {
		m.Module = info.Path
	}
	if v := info.Main.Version; v != "" {
		m.Version = v
	}
	m.Dirty = strings.HasSuffix(m.Version, "+dirty")
	if sub := pseudoVersionRe.FindStringSubmatch(m.Version); sub != nil {
		m.CommitSHA = sub[2]
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				m.CommitSHA = s.Value
			}
		case "vcs.time":
			m.CommitTime = s.Value
		case "vcs.modified":
			m.Dirty = m.Dirty || s.Value == "true"
		}
	}
	return m
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// source is one file on its way into the zip.
type source struct {
	archive string // the path inside the zip
	path    string // the file read
	origin  string // the path shown as the source, when it is not the file read
	note    string
}

// withDefaults fills the empty paths of opts.
func withDefaults(opts Options) (Options, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.BinDir != "" && opts.OutPath != "" && (opts.BinsOnly || (opts.ConfigDir != "" && opts.DBPath != "")) {
		return opts, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return opts, fmt.Errorf("backup: no home directory for the default paths: %w", err)
	}
	if opts.BinDir == "" {
		opts.BinDir = filepath.Join(home, "bin")
	}
	if opts.ConfigDir == "" {
		opts.ConfigDir = filepath.Join(home, ".config", "matomeru")
	}
	if opts.DBPath == "" {
		opts.DBPath = filepath.Join(home, ".local", "share", "matomeru", "mtm.db")
	}
	if opts.OutPath == "" {
		dir := opts.OutDir
		if dir == "" {
			dir = home
		}
		opts.OutPath = filepath.Join(dir, ArchivePrefix+opts.Now().Format(stampLayout)+".zip")
	}
	return opts, nil
}

// Create writes the backup zip and reports what went into it. The zip appears
// at its final path only once it is complete; an existing file there is never
// overwritten.
func Create(opts Options) (Report, error) {
	opts, err := withDefaults(opts)
	if err != nil {
		return Report{}, err
	}
	if _, err := os.Lstat(opts.OutPath); err == nil {
		return Report{}, fmt.Errorf("backup: %s already exists", opts.OutPath)
	}
	host, _ := os.Hostname()
	rep := Report{
		ZipPath: opts.OutPath, CreatedAt: opts.Now(), Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH,
		BinDir: opts.BinDir, Binaries: []BinaryMeta{}, Files: []FileMeta{},
	}

	bins, err := binarySources(opts, &rep)
	if err != nil {
		return rep, err
	}
	var files []source
	if !opts.BinsOnly {
		rep.ConfigDir, rep.DBPath = opts.ConfigDir, opts.DBPath
		scratch, err := os.MkdirTemp("", "ofam-backup-db-")
		if err != nil {
			return rep, fmt.Errorf("backup: %w", err)
		}
		defer os.RemoveAll(scratch)
		if files, err = dataSources(opts, scratch, &rep); err != nil {
			return rep, err
		}
	}
	if len(bins)+len(files) == 0 {
		return rep, fmt.Errorf("backup: nothing to back up (no managed binaries in %s)", opts.BinDir)
	}
	if err := writeZip(opts.OutPath, bins, files, &rep); err != nil {
		return rep, err
	}
	if st, err := os.Stat(opts.OutPath); err == nil {
		rep.ZipSize = st.Size()
	}
	return rep, nil
}

// binarySources lists the managed binaries of the bin directory, in name
// order. Symlinks are named in the report and left out: the zip holds files
// an install replaces, not pointers to other checkouts.
func binarySources(opts Options, rep *Report) ([]source, error) {
	entries, err := os.ReadDir(opts.BinDir)
	if errors.Is(err, fs.ErrNotExist) {
		rep.Skipped = append(rep.Skipped, opts.BinDir+": no such directory")
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	var out []source
	found := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !wanted(name, opts.Only) {
			continue
		}
		found[name] = true
		path := filepath.Join(opts.BinDir, name)
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: symlink to %s", path, target))
		case e.Type().IsRegular():
			out = append(out, source{archive: "bin/" + name, path: path})
		}
	}
	for _, name := range opts.Only {
		if !found[name] {
			rep.Skipped = append(rep.Skipped, filepath.Join(opts.BinDir, name)+": not installed")
		}
	}
	return out, nil
}

func wanted(name string, only []string) bool {
	if len(only) > 0 {
		return slices.Contains(only, name)
	}
	return Managed(name)
}

// dataSources lists the config files, the database snapshot, and the drafts
// and attachments kept beside the database.
func dataSources(opts Options, scratch string, rep *Report) ([]source, error) {
	out, err := treeSources(opts.ConfigDir, "config", rep)
	if err != nil {
		return nil, err
	}
	db, err := databaseSources(opts, scratch, rep)
	if err != nil {
		return nil, err
	}
	out = append(out, db...)
	for _, sub := range []string{"drafts", "attachments"} {
		more, err := treeSources(filepath.Join(filepath.Dir(opts.DBPath), sub), "data/"+sub, rep)
		if err != nil {
			return nil, err
		}
		out = append(out, more...)
	}
	return out, nil
}

// treeSources lists every regular file under root, in path order. A missing
// root is noted and skipped; earlier backup zips are never nested.
func treeSources(root, prefix string, rep *Report) ([]source, error) {
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		rep.Skipped = append(rep.Skipped, root+": not present")
		return nil, nil
	}
	var out []source
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || strings.Contains(d.Name(), ArchivePrefix) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, source{archive: prefix + "/" + filepath.ToSlash(rel), path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	return out, nil
}

// databaseSources gives the database as one consistent file when a SQLite
// driver is registered, and as the raw file with its WAL companions otherwise.
func databaseSources(opts Options, scratch string, rep *Report) ([]source, error) {
	if _, err := os.Stat(opts.DBPath); errors.Is(err, fs.ErrNotExist) {
		rep.Skipped = append(rep.Skipped, opts.DBPath+": not present")
		return nil, nil
	}
	name := filepath.Base(opts.DBPath)
	if opts.DBDriver != "" && slices.Contains(sql.Drivers(), opts.DBDriver) {
		snap := filepath.Join(scratch, name)
		if err := vacuumInto(opts.DBDriver, opts.DBPath, snap); err != nil {
			return nil, fmt.Errorf("backup: snapshot %s: %w", opts.DBPath, err)
		}
		return []source{{archive: "data/" + name, path: snap, origin: opts.DBPath, note: "consistent snapshot (VACUUM INTO)"}}, nil
	}
	out := []source{{archive: "data/" + name, path: opts.DBPath, note: "raw copy; restore together with its -wal and -shm files"}}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(opts.DBPath + suffix); err == nil {
			out = append(out, source{archive: "data/" + name + suffix, path: opts.DBPath + suffix, note: "raw copy"})
		}
	}
	return out, nil
}

// vacuumInto writes a consistent copy of the database at src to dst. It reads
// through SQLite, so it is safe while another process writes to the database.
func vacuumInto(driver, src, dst string) error {
	db, err := sql.Open(driver, src)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=10000"); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dst, "'", "''")+"'")
	return err
}

// writeZip streams every source into a temporary zip beside out, appends the
// manifest, and renames the finished zip into place.
func writeZip(out string, bins, files []source, rep *Report) (err error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), "."+ArchivePrefix+"*.zip.tmp")
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	zw := zip.NewWriter(tmp)
	for _, s := range bins {
		m := describeBinary(s.path)
		if m.Size, m.SHA256, err = addFile(zw, s); err != nil {
			return fmt.Errorf("backup: %s: %w", s.path, err)
		}
		rep.Binaries = append(rep.Binaries, m)
	}
	for _, s := range files {
		m := FileMeta{Archive: s.archive, Source: s.path, Note: s.note}
		if s.origin != "" {
			m.Source = s.origin
		}
		if st, serr := os.Stat(s.path); serr == nil {
			m.ModTime = st.ModTime().UTC()
		}
		if m.Size, m.SHA256, err = addFile(zw, s); err != nil {
			return fmt.Errorf("backup: %s: %w", s.path, err)
		}
		rep.Files = append(rep.Files, m)
	}
	rep.Manifest = Manifest(*rep)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: rep.CreatedAt})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if _, err = io.WriteString(w, rep.Manifest); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err = zw.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err = os.Rename(tmp.Name(), out); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// addFile copies one file into the zip, keeping its mode and time, and returns
// the size and SHA-256 of the bytes that went in.
func addFile(zw *zip.Writer, s source) (int64, string, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	hdr, err := zip.FileInfoHeader(st)
	if err != nil {
		return 0, "", err
	}
	hdr.Name, hdr.Method = s.archive, zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// Manifest renders the MANIFEST.md of a backup.
func Manifest(rep Report) string {
	var b strings.Builder
	zipName := filepath.Base(rep.ZipPath)
	fmt.Fprintf(&b, "# OFAM backup manifest\n\n")
	fmt.Fprintf(&b, "- **Archive:** `%s`\n", zipName)
	fmt.Fprintf(&b, "- **Created (UTC):** %s\n", rep.CreatedAt.UTC().Format(timeLayout))
	fmt.Fprintf(&b, "- **Created (local):** %s\n", rep.CreatedAt.Format(timeLayout+" -0700 MST"))
	fmt.Fprintf(&b, "- **Host:** %s\n", rep.Hostname)
	fmt.Fprintf(&b, "- **OS/arch:** %s/%s\n", rep.OS, rep.Arch)
	fmt.Fprintf(&b, "- **Bin dir:** `%s`\n", rep.BinDir)
	if rep.ConfigDir != "" {
		fmt.Fprintf(&b, "- **Config dir:** `%s`\n", rep.ConfigDir)
	}
	if rep.DBPath != "" {
		fmt.Fprintf(&b, "- **Database:** `%s`\n", rep.DBPath)
	}

	fmt.Fprintf(&b, "\n## Binaries (%d)\n\n", len(rep.Binaries))
	if len(rep.Binaries) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| Binary | Version | Commit SHA | Module | Go | Modified (UTC) | Size | SHA-256 |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|\n")
		for _, m := range rep.Binaries {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s | %s | `%s` |\n",
				cell(m.Name), cell(m.Version), cell(commitCell(m)), cell(dash(m.Module)), cell(dash(m.GoVersion)),
				m.ModTime.UTC().Format(timeLayout), HumanSize(m.Size), m.SHA256)
		}
		b.WriteString("\nIn the zip each binary is `bin/<Binary>`. A commit marked *(dirty)* was built from a tree with uncommitted changes.\n")
	}

	fmt.Fprintf(&b, "\n## Config and data files (%d)\n\n", len(rep.Files))
	if len(rep.Files) == 0 {
		b.WriteString("None (a binaries-only backup, or nothing was present).\n")
	} else {
		b.WriteString("| File in zip | Source | Modified (UTC) | Size | SHA-256 | Note |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, m := range rep.Files {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | `%s` | %s |\n",
				cell(m.Archive), cell(m.Source), m.ModTime.UTC().Format(timeLayout), HumanSize(m.Size), m.SHA256, cell(m.Note))
		}
	}

	if len(rep.Skipped) > 0 {
		b.WriteString("\n## Not in this backup\n\n")
		for _, s := range rep.Skipped {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	b.WriteString(restoreInstructions(rep, zipName))
	return b.String()
}

func commitCell(m BinaryMeta) string {
	if m.CommitSHA == noCommit || m.CommitSHA == "" {
		return noCommit
	}
	out := "`" + m.CommitSHA + "`"
	if m.Dirty {
		out += " *(dirty)*"
	}
	return out
}

func dash(s string) string {
	if s == "" {
		return noCommit
	}
	return s
}

// cell makes text safe inside a Markdown table cell.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

// HumanSize prints a byte count the way a person reads it.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// restoreInstructions is the copy-pasteable end of the manifest.
func restoreInstructions(rep Report, zipName string) string {
	var b strings.Builder
	zipPath := shellQuote(rep.ZipPath)
	b.WriteString("\n## Restore instructions\n\n")
	b.WriteString("Read this manifest again at any time:\n\n```sh\n")
	fmt.Fprintf(&b, "mtm backup inspect %s    # or: unzip -p %s %s\n```\n", zipPath, zipPath, ManifestName)
	if len(rep.Binaries) > 0 {
		first := rep.Binaries[0].Name
		b.WriteString("\nRestore every binary (each file is replaced atomically, so running programs are not disturbed):\n\n```sh\n")
		b.WriteString("tmp=$(mktemp -d)\n")
		fmt.Fprintf(&b, "unzip -o %s 'bin/*' -d \"$tmp\"\n", zipPath)
		fmt.Fprintf(&b, "for f in \"$tmp\"/bin/*; do install -m 0755 \"$f\" %s/.restore.$$ && mv %s/.restore.$$ %s/\"$(basename \"$f\")\"; done\n",
			shellQuote(rep.BinDir), shellQuote(rep.BinDir), shellQuote(rep.BinDir))
		b.WriteString("rm -rf \"$tmp\"\n")
		b.WriteString("mtm daemon upgrade --json    # only when mtm itself was restored\n```\n")
		b.WriteString("\nRestore one binary:\n\n```sh\n")
		target := shellQuote(filepath.Join(rep.BinDir, first))
		fmt.Fprintf(&b, "unzip -p %s %s > %s.restore && chmod 0755 %s.restore && mv %s.restore %s\n```\n",
			zipPath, shellQuote("bin/"+first), target, target, target, target)
	}
	if rep.ConfigDir != "" && hasPrefix(rep.Files, "config/") {
		b.WriteString("\nRestore the config:\n\n```sh\n")
		b.WriteString("tmp=$(mktemp -d)\n")
		fmt.Fprintf(&b, "unzip -o %s 'config/*' -d \"$tmp\"\n", zipPath)
		fmt.Fprintf(&b, "mkdir -p %s && cp -Rp \"$tmp\"/config/. %s/\n", shellQuote(rep.ConfigDir), shellQuote(rep.ConfigDir))
		b.WriteString("rm -rf \"$tmp\"\n```\n")
	}
	if rep.DBPath != "" && hasPrefix(rep.Files, "data/") {
		dataDir := shellQuote(filepath.Dir(rep.DBPath))
		db := shellQuote(rep.DBPath)
		b.WriteString("\nRestore the database, drafts and attachments. Quit the mtm TUI first; this replaces the live data:\n\n```sh\n")
		b.WriteString("mtm daemon stop\n")
		b.WriteString("tmp=$(mktemp -d)\n")
		fmt.Fprintf(&b, "unzip -o %s 'data/*' -d \"$tmp\"\n", zipPath)
		fmt.Fprintf(&b, "cp -p %s %s.before-restore 2>/dev/null; rm -f %s-wal %s-shm\n", db, db, db, db)
		fmt.Fprintf(&b, "mkdir -p %s && cp -Rp \"$tmp\"/data/. %s/\n", dataDir, dataDir)
		b.WriteString("rm -rf \"$tmp\"\n```\n")
	}
	fmt.Fprintf(&b, "\nVerify a restored file against the SHA-256 column: `shasum -a 256 FILE`. Archive: `%s`.\n", zipName)
	return b.String()
}

func hasPrefix(files []FileMeta, prefix string) bool {
	return slices.ContainsFunc(files, func(m FileMeta) bool { return strings.HasPrefix(m.Archive, prefix) })
}

// InspectZip returns the MANIFEST.md of an existing backup zip.
func InspectZip(zipPath string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	defer zr.Close()
	f, err := zr.Open(ManifestName)
	if err != nil {
		return "", fmt.Errorf("backup: %s has no %s: it is not an mtm backup", zipPath, ManifestName)
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	return string(body), nil
}

// List returns the backup zips in dir, oldest first. The default dir is the
// home directory, where Create writes by default.
func List(dir string) ([]Archive, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("backup: %w", err)
		}
		dir = home
	}
	paths, err := filepath.Glob(filepath.Join(dir, ArchivePrefix+"*.zip"))
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	sort.Strings(paths)
	out := []Archive{}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		out = append(out, Archive{Name: filepath.Base(p), Path: p, Size: st.Size(), ModTime: st.ModTime().UTC()})
	}
	return out, nil
}
