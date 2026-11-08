package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// This file is daemon.import-state: copy one TUIOS server's saved sessions
// and history into another server's state folder, so the daemon that starts
// there restores them with their session and window IDs. It copies and never
// moves, writes only the target, and refuses (changing nothing) while a
// daemon runs on either side or when a saved file is of a schema this build
// does not read.

// DefaultServerName is the --from word for the user's own (unnamed) server.
const DefaultServerName = "default"

// MaxResurrectionVersion is the newest saved-session schema the daemon this
// mtm links restores (mtmd internal/session ResurrectionVersion). A file
// stamped higher is archived and skipped by that daemon, so the import
// refuses it up front. Older stamps, and none (version 0), load.
const MaxResurrectionVersion = 1

// Refusals are errors that wrap one of these, so a caller can tell them
// apart; none of them changes anything on disk.
var (
	ErrSourceLive = errors.New("the source server has a daemon running")
	ErrTargetLive = errors.New("the target server has a daemon running")
	ErrUnreadable = errors.New("saved state this build cannot read")
	ErrConflict   = errors.New("the target already holds saved state of the same names")
	ErrNothing    = errors.New("the source holds nothing to import")
	ErrSameServer = errors.New("the source and the target are the same server")
)

// ImportClass is one kind of saved file, in data: whether the import copies
// it, and why not when it does not. Order matters: the first match wins.
type ImportClass struct {
	Name    string
	Prefix  string // path under the sessions folder, "" for files at the top
	Suffix  string
	Copy    bool
	Why     string // for a class that is not copied
	Deep    bool   // matches at any depth when there is no Prefix
	Guard   bool   // user data: a file of the same name in the target refuses the import (unless Overwrite); otherwise the target's is archived and replaced
	Session bool   // a session file: validated, and what other classes follow
}

var importClasses = []ImportClass{
	{Name: "temp", Suffix: ".tmp", Deep: true, Why: "a write that never finished"},
	{Name: "archive", Prefix: "archive/", Why: "state the source daemon could not load"},
	{Name: "grants", Prefix: "grants/", Why: "the default grants in force, written again by the target daemon"},
	{Name: "push", Prefix: "push/", Why: "the source's web-push keys and subscriptions stay with it"},
	{Name: "history", Prefix: "scrollback/", Copy: true, Guard: true},
	{Name: "attention", Prefix: "attention/", Copy: true},
	{Name: "outbox", Prefix: "outbox/", Copy: true},
	{Name: "review", Prefix: "review/", Copy: true},
	{Name: "session", Suffix: ".json", Copy: true, Guard: true, Session: true},
}

// ImportOptions say what to copy from where to where. Base is the state home
// the target daemon uses (see StateHome); the zero value of every other field
// is the safe default.
type ImportOptions struct {
	From, To  string // server names: "" or DefaultServerName is the unnamed server
	Base      string // state home of the target (and of the source unless it is found elsewhere)
	Bases     []string
	DryRun    bool
	Overwrite bool // archive colliding target files first instead of refusing
	// Live reports whether a daemon runs on the server whose socket it is
	// given; nil is ServerLive.
	Live func(socket string) (bool, string)
	// Now stamps the archive folder an overwrite makes; nil is time.Now.
	Now func() time.Time
}

// ImportFile is one file of the plan.
type ImportFile struct {
	Path   string `json:"path"` // under the sessions folder
	Class  string `json:"class"`
	Bytes  int64  `json:"bytes"`
	Action string `json:"action"` // copy, replace, skip
	Why    string `json:"why,omitempty"`
}

// ImportWindow is a pane the import brings back (its ID is kept).
type ImportWindow struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Workspace int    `json:"workspace"`
}

// ImportSession is a saved session the import brings back (its ID is kept).
type ImportSession struct {
	Name    string         `json:"name"`
	ID      string         `json:"id,omitempty"`
	Version int            `json:"resurrection_version"`
	Windows []ImportWindow `json:"windows"`
}

// ImportReport is the plan, and after an apply, what was done.
type ImportReport struct {
	From       string          `json:"from"`
	To         string          `json:"to"`
	SourceDir  string          `json:"source_dir"`
	TargetDir  string          `json:"target_dir"`
	DryRun     bool            `json:"dry_run"`
	Copied     bool            `json:"copied"`
	Sessions   []ImportSession `json:"sessions"`
	Files      []ImportFile    `json:"files"`
	Archived   string          `json:"archived,omitempty"` // where colliding target files went
	Notes      []string        `json:"notes,omitempty"`
	CopiedFile int             `json:"copied_files"`
}

// ServerStateDir is the folder a server keeps its saved sessions in under
// the state home base: base/tuios/sessions for the unnamed server and
// base/tuios-servers/NAME/tuios/sessions for a named one.
func ServerStateDir(base, server string) string {
	if server != "" && server != DefaultServerName {
		base = filepath.Join(base, "tuios-servers", server)
	}
	return filepath.Join(base, "tuios", "sessions")
}

// StateHome is the state home a daemon started from this environment uses:
// XDG_STATE_HOME when set, else ~/Library/Application Support on macOS and
// ~/.local/state elsewhere (adrg/xdg, which the daemon resolves it with).
func StateHome(getenv func(string) string, home string) string {
	if v := getenv("XDG_STATE_HOME"); v != "" && filepath.IsAbs(v) {
		return v
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support")
	}
	return filepath.Join(home, ".local", "state")
}

// ServerLive reports whether a daemon runs on the server whose socket this
// is: something accepts a connection on it, or its pid file names a live
// process (a daemon that is starting or does not answer still writes to the
// state). The second result says how it knew.
func ServerLive(socket string) (bool, string) {
	if socket == "" {
		return false, ""
	}
	if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
		conn.Close()
		return true, "it answers on " + socket
	}
	data, err := os.ReadFile(socket + ".pid")
	if err != nil {
		return false, ""
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false, ""
	}
	if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
		return true, fmt.Sprintf("process %d from %s.pid is running", pid, socket)
	}
	return false, ""
}

func validServerName(s string) error {
	if s == "" || s == DefaultServerName {
		return nil
	}
	if len(s) > 14 {
		return fmt.Errorf("server name %q is longer than 14 characters", s)
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("server name %q may only contain letters, digits, '-' and '_'", s)
		}
	}
	return nil
}

func normalServer(s string) string {
	if s == DefaultServerName {
		return ""
	}
	return s
}

type srcFile struct {
	rel     string
	abs     string
	size    int64
	modTime time.Time
	class   ImportClass
}

// PlanImport inspects both servers and returns what ImportState would do. It
// writes nothing. A refusal is returned as an error with the report so far.
func PlanImport(o ImportOptions) (ImportReport, []srcFile, error) {
	rep := ImportReport{From: o.From, To: o.To, DryRun: o.DryRun}
	if err := validServerName(o.From); err != nil {
		return rep, nil, err
	}
	if err := validServerName(o.To); err != nil {
		return rep, nil, err
	}
	from, to := normalServer(o.From), normalServer(o.To)
	if from == to {
		return rep, nil, fmt.Errorf("%w (%s)", ErrSameServer, serverLabel(from))
	}
	live := o.Live
	if live == nil {
		live = ServerLive
	}
	bases := slices.Clone(o.Bases)
	bases = append([]string{o.Base}, bases...)
	srcDir := ServerStateDir(bases[0], from)
	for _, b := range bases {
		if d := ServerStateDir(b, from); isDir(d) {
			srcDir = d
			break
		}
	}
	rep.SourceDir, rep.TargetDir = srcDir, ServerStateDir(o.Base, to)

	if yes, how := live(SocketFor(from)); yes {
		return rep, nil, fmt.Errorf("%w (%s): stop it first (tuios kill-server%s); nothing was copied", ErrSourceLive, how, serverFlag(from))
	}
	if yes, how := live(SocketFor(to)); yes {
		return rep, nil, fmt.Errorf("%w (%s): stop it first (mtm daemon stop); nothing was copied", ErrTargetLive, how)
	}
	if !isDir(srcDir) {
		return rep, nil, fmt.Errorf("%w: %s does not exist", ErrNothing, srcDir)
	}

	var files []srcFile
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		f := srcFile{rel: rel, abs: path, size: info.Size(), modTime: info.ModTime()}
		f.class = classify(rel)
		if !info.Mode().IsRegular() {
			f.class = ImportClass{Name: "special", Why: "not a regular file"}
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return rep, nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	// Sessions first: they decide which history follows, and they are what
	// can make the whole import unreadable.
	saved := map[string]bool{}
	var unreadable []string
	for _, f := range files {
		if !f.class.Session {
			continue
		}
		s, err := readSession(f.abs, strings.TrimSuffix(f.rel, ".json"))
		if err != nil {
			unreadable = append(unreadable, err.Error())
			continue
		}
		if len(s.Windows) == 0 {
			continue // the daemon discards a session with no windows
		}
		saved[s.Name] = true
		rep.Sessions = append(rep.Sessions, s)
	}
	if len(unreadable) > 0 {
		return rep, nil, fmt.Errorf("%w: %s; nothing was copied", ErrUnreadable, strings.Join(unreadable, "; "))
	}

	var picked []srcFile
	for _, f := range files {
		item := ImportFile{Path: f.rel, Class: f.class.Name, Bytes: f.size, Action: "copy"}
		switch {
		case !f.class.Copy:
			item.Action, item.Why = "skip", f.class.Why
		case f.class.Session && !saved[strings.TrimSuffix(f.rel, ".json")]:
			item.Action, item.Why = "skip", "a session with no windows: nothing to restore"
		case f.class.Name == "history" && !saved[firstElem(strings.TrimPrefix(f.rel, "scrollback/"))]:
			item.Action, item.Why = "skip", "history of a session that is not imported"
		}
		if item.Action == "copy" {
			if _, err := os.Lstat(filepath.Join(rep.TargetDir, filepath.FromSlash(f.rel))); err == nil {
				item.Action = "replace"
			}
			picked = append(picked, f)
		}
		rep.Files = append(rep.Files, item)
	}
	if len(picked) == 0 {
		return rep, nil, fmt.Errorf("%w: %s", ErrNothing, srcDir)
	}
	if !o.Overwrite {
		var clash []string
		for _, f := range rep.Files {
			if f.Action == "replace" && classify(f.Path).Guard {
				clash = append(clash, f.Path)
			}
		}
		if len(clash) > 0 {
			return rep, nil, fmt.Errorf("%w: %s; nothing was copied (overwrite archives them first)", ErrConflict, summarize(clash, 5))
		}
	}
	rep.Notes = append(rep.Notes,
		"Processes do not survive: every pane restarts as a new shell with a new pid and pty. The layout, saved history, and the session and window IDs (and so mtm's associations) are kept.")
	return rep, picked, nil
}

// ImportState plans the import and, unless DryRun, performs it: each file is
// written beside its place and renamed in, the source is checked unchanged
// afterwards, and any failure removes what was written and restores what was
// archived.
func ImportState(o ImportOptions) (ImportReport, error) {
	rep, picked, err := PlanImport(o)
	if err != nil || o.DryRun {
		return rep, err
	}
	live := o.Live
	if live == nil {
		live = ServerLive
	}
	before := snapshot(picked)
	var created []string
	var moved [][2]string
	undo := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(moved[i][1], moved[i][0])
		}
	}
	fail := func(err error) (ImportReport, error) {
		undo()
		rep.Copied, rep.CopiedFile, rep.Archived = false, 0, ""
		return rep, fmt.Errorf("%w; nothing was left in %s", err, rep.TargetDir)
	}
	if yes, how := live(SocketFor(normalServer(o.To))); yes {
		return rep, fmt.Errorf("%w (%s); nothing was copied", ErrTargetLive, how)
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	archive := filepath.Join(rep.TargetDir, "archive", "import-"+now().UTC().Format("20060102T150405Z"))
	for _, f := range picked {
		dest := filepath.Join(rep.TargetDir, filepath.FromSlash(f.rel))
		if _, err := os.Lstat(dest); err == nil {
			held := filepath.Join(archive, filepath.FromSlash(f.rel))
			if err := os.MkdirAll(filepath.Dir(held), 0o700); err != nil {
				return fail(err)
			}
			if err := os.Rename(dest, held); err != nil {
				return fail(err)
			}
			moved = append(moved, [2]string{dest, held})
			rep.Archived = archive
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return fail(err)
		}
		if err := copyFileAtomic(f.abs, dest, f.modTime); err != nil {
			return fail(fmt.Errorf("copy %s: %w", f.rel, err))
		}
		created = append(created, dest)
	}
	if after := snapshot(picked); !slices.Equal(before, after) {
		return fail(fmt.Errorf("%w: the source changed while it was copied, so something wrote to it", ErrSourceLive))
	}
	rep.Copied, rep.CopiedFile = true, len(created)
	return rep, nil
}

func snapshot(files []srcFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.rel
		if info, err := os.Stat(f.abs); err == nil {
			out[i] += fmt.Sprintf("|%d|%d", info.Size(), info.ModTime().UnixNano())
		} else {
			out[i] += "|gone"
		}
	}
	return out
}

func copyFileAtomic(src, dest string, mod time.Time) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".import-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chtimes(tmp.Name(), mod, mod)
	return os.Rename(tmp.Name(), dest)
}

func classify(rel string) ImportClass {
	for _, c := range importClasses {
		switch {
		case c.Prefix != "" && !strings.HasPrefix(rel, c.Prefix):
		case c.Prefix == "" && !c.Deep && strings.Contains(rel, "/"):
		case !strings.HasSuffix(rel, c.Suffix):
		default:
			return c
		}
	}
	return ImportClass{Name: "other", Why: "not a kind of saved state the import knows"}
}

// readSession reads the part of a saved session the import needs and checks
// the schema is one the daemon restores.
func readSession(path, stem string) (ImportSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ImportSession{}, err
	}
	var raw struct {
		Name      string `json:"name"`
		SessionID string `json:"session_id"`
		Resurrect int    `json:"resurrection_version"`
		Windows   []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			CustomName string `json:"custom_name"`
			Workspace  int    `json:"workspace"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ImportSession{}, fmt.Errorf("%s.json is not readable JSON (%v)", stem, err)
	}
	if raw.Resurrect > MaxResurrectionVersion {
		return ImportSession{}, fmt.Errorf("%s.json is saved-state version %d, newer than the %d this daemon reads", stem, raw.Resurrect, MaxResurrectionVersion)
	}
	if raw.Name == "" || raw.Name != stem {
		return ImportSession{}, fmt.Errorf("%s.json names the session %q", stem, raw.Name)
	}
	s := ImportSession{Name: raw.Name, ID: raw.SessionID, Version: raw.Resurrect}
	for _, w := range raw.Windows {
		name := w.CustomName
		if name == "" {
			name = w.Title
		}
		s.Windows = append(s.Windows, ImportWindow{ID: w.ID, Name: name, Workspace: w.Workspace})
	}
	return s, nil
}

func firstElem(rel string) string {
	first, _, _ := strings.Cut(rel, "/")
	return first
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func serverLabel(s string) string {
	if s == "" {
		return DefaultServerName
	}
	return s
}

func serverFlag(s string) string {
	if s == "" {
		return ""
	}
	return " --server " + s
}

func summarize(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" and %d more", len(items)-n)
}
