// Package locate finds the session an agent in a pane writes now from the
// harness's own records (harness.SessionSource), one mechanism for every
// harness: which records exist, how a record is tied to a pane and where
// its transcript is are the harness's data.
//
// tuios reports the session id it first heard for a pane, which goes stale
// when the agent moves to another session (/clear, a resume, a fork) or
// when the pane's agent only attaches to a session another process runs
// (Claude Code's background daemon). A record ties a session to a pane in
// one of two ways:
//
//   - held: the record names a running process (pid, checked against its
//     start time so a reused pid is not trusted) whose environment names
//     the pane's window, or the record is named by the pane's terminal
//     ({tty}) and the harness's program runs on that terminal;
//   - the folder's one: when the pane holds no record with a transcript
//     and the id tuios reports has none either, the one running process's
//     record that no listed pane holds, started in the pane's folder, with
//     a transcript. Two or more is a note, never a guess; another pane of
//     the harness in the folder that is unresolved too means nothing is
//     taken.
//
// A record whose transcript does not exist is never taken.
package locate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ofam.dev/matomeru/internal/harness"
)

// Pane is what the locator needs of a listed pane.
type Pane struct {
	ID      string // the window id
	Harness string // the harness id tuios reports ("" none)
	Session string // the session id tuios reports ("" none)
	Cwd     string
	TTY     string // the pane's terminal, /dev/ttys041
}

// Found is what the locator found for a pane.
type Found struct {
	Harness string // the harness whose record it is (its name)
	Session string // the session the agent writes now
	Path    string // its transcript
	Note    string // why nothing was taken, for the person ("" none)
}

// Proc is a process from the process table.
type Proc struct {
	Start string   // ps's lstart in UTC
	TTY   string   // its terminal's name (ttys041), "" none
	Args  []string // the first words of its command line
}

// System reads the process table: Procs the processes with pids or on
// ttys (names, ttys041), Env the environments of pids (only the names
// asked). A process that is gone is left out.
type System interface {
	Procs(pids []int, ttys []string) map[int]Proc
	Env(pids []int, names []string) map[int]map[string]string
}

// CacheFor is how long a read of the process table stands: a listing
// comes every second or two, ps need not run for each.
const CacheFor = 5 * time.Second

// Locator locates panes' sessions. Safe for concurrent use.
type Locator struct {
	mu      sync.Mutex
	sources []source
	sys     System
	getenv  func(string) string
	home    string
	now     func() time.Time
	cache   map[string]cached
}

type source struct {
	name string
	harness.SessionSource
	pattern *regexp.Regexp
}

type cached struct {
	at    time.Time
	procs map[int]Proc
	env   map[int]map[string]string
}

// New is a locator over the harnesses' session sources, reading the
// process table through sys. getenv and home expand the sources' paths
// (nil: os.Getenv, os.UserHomeDir).
func New(hs []harness.Harness, sys System, getenv func(string) string, home string) *Locator {
	if getenv == nil {
		getenv = os.Getenv
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	l := &Locator{sys: sys, getenv: getenv, home: home, now: time.Now}
	l.Set(hs)
	return l
}

// Set replaces the harnesses (a reloaded harness file).
func (l *Locator) Set(hs []harness.Harness) {
	var out []source
	for _, h := range hs {
		if h.Session == nil {
			continue
		}
		s := source{name: h.Name, SessionSource: *h.Session}
		if s.SessionPattern != "" {
			s.pattern, _ = regexp.Compile(s.SessionPattern) // validated with the harness
		}
		out = append(out, s)
	}
	l.mu.Lock()
	l.sources = out
	l.mu.Unlock()
}

// SetClock sets the clock the process table's cache goes by.
func (l *Locator) SetClock(now func() time.Time) {
	l.mu.Lock()
	l.now = now
	l.mu.Unlock()
}

// Forget drops the cached process table (a refresh asks ps again).
func (l *Locator) Forget() {
	l.mu.Lock()
	l.cache = nil
	l.mu.Unlock()
}

// record is one record read, tied or not to a pane.
type record struct {
	src     *source
	held    string // the pane it is tied to, "" none
	session string
	cwd     string
	path    string // its transcript, "" when it does not exist
}

// Locate is what the harnesses' records say of each pane, by window id;
// a pane with nothing found is left out.
func (l *Locator) Locate(panes []Pane) map[string]Found {
	l.mu.Lock()
	sources := slices.Clone(l.sources)
	l.mu.Unlock()
	if len(sources) == 0 || len(panes) == 0 {
		return nil
	}
	listed := map[string]bool{}
	for _, p := range panes {
		listed[p.ID] = true
	}
	var recs []record
	for i := range sources {
		recs = append(recs, l.records(&sources[i], panes, listed)...)
	}
	out := map[string]Found{}
	for _, p := range panes {
		if f, ok := l.locate(p, panes, recs, sources); ok {
			out[p.ID] = f
		}
	}
	return out
}

// locate is one pane's answer.
func (l *Locator) locate(p Pane, panes []Pane, recs []record, sources []source) (Found, bool) {
	// Held: the pane's own records with a transcript, of a harness that
	// covers what tuios reports (or of any, when it reports none).
	held := unique(recs, func(r record) bool {
		return r.held == p.ID && r.path != "" && (p.Harness == "" || r.src.Covers(r.src.name, p.Harness))
	})
	if len(held) == 1 {
		return found(held[0]), true
	}
	if len(held) > 1 {
		return Found{Note: note(p.Cwd, held, l.home)}, true
	}
	for i := range sources {
		src := &sources[i]
		if src.PID == "" || !src.Covers(src.name, p.Harness) || l.resolved(src, p, recs) {
			continue
		}
		cands := l.folderCandidates(src, p, panes, recs)
		if len(cands) == 0 {
			continue
		}
		for _, o := range panes {
			if o.ID != p.ID && src.Covers(src.name, o.Harness) && sameDir(o.Cwd, p.Cwd) && !l.resolved(src, o, recs) {
				return Found{}, false // another pane here could own it as well
			}
		}
		if len(cands) > 1 {
			return Found{Note: note(p.Cwd, cands, l.home)}, true
		}
		return found(cands[0]), true
	}
	return Found{}, false
}

// resolved reports whether p's session is known for src: a record it
// holds has a transcript, or the id tuios reports has one.
func (l *Locator) resolved(src *source, p Pane, recs []record) bool {
	if slices.ContainsFunc(recs, func(r record) bool { return r.src == src && r.held == p.ID && r.path != "" }) {
		return true
	}
	if p.Session == "" || src.Transcript == "" {
		return false
	}
	return regular(l.expand(src.Transcript, map[string]string{"session": p.Session, "cwd": p.Cwd, "cwd_slug": cwdSlug(p.Cwd)}))
}

// folderCandidates are src's records no listed pane holds, of a process
// that started in p's folder, with a transcript, whose session no other
// pane reports.
func (l *Locator) folderCandidates(src *source, p Pane, panes []Pane, recs []record) []record {
	return unique(recs, func(r record) bool {
		if r.src != src || r.held != "" || r.path == "" || !sameDir(r.cwd, p.Cwd) {
			return false
		}
		return !slices.ContainsFunc(panes, func(o Pane) bool { return o.ID != p.ID && o.Session == r.session })
	})
}

// records reads src's records, checked against the process table.
func (l *Locator) records(src *source, panes []Pane, listed map[string]bool) []record {
	type raw struct {
		fields map[string]string
		held   string
		tty    string
	}
	var raws []raw
	if strings.Contains(src.Files, "{tty}") {
		for _, p := range panes {
			tty := ttyName(p.TTY)
			if tty == "" {
				continue
			}
			for _, path := range l.glob(src.Files, map[string]string{"tty": tty}) {
				if f, ok := readRecord(path, src.Format); ok {
					raws = append(raws, raw{fields: f, held: p.ID, tty: tty})
				}
			}
		}
	} else {
		for _, path := range l.glob(src.Files, nil) {
			if f, ok := readRecord(path, src.Format); ok {
				raws = append(raws, raw{fields: f})
			}
		}
	}
	if len(raws) == 0 {
		return nil
	}
	// The process table, once for the source.
	var pids []int
	var ttys []string
	for _, r := range raws {
		if src.PID != "" {
			if pid, err := strconv.Atoi(r.fields[src.PID]); err == nil && pid > 0 {
				pids = append(pids, pid)
			}
		}
		if src.Process != "" && r.tty != "" {
			ttys = append(ttys, r.tty)
		}
	}
	procs, env := l.table(pids, ttys, src.Env)
	var out []record
	for _, r := range raws {
		held := r.held
		if src.PID != "" {
			pid, _ := strconv.Atoi(r.fields[src.PID])
			pr, ok := procs[pid]
			if !ok || src.Start == "" || words(pr.Start) != words(r.fields[src.Start]) {
				continue // gone, or the pid is another process now
			}
			for _, name := range src.Env {
				if w := env[pid][name]; w != "" && listed[w] {
					held = w
					break
				}
			}
		}
		if src.Process != "" && r.tty != "" && !runsOn(procs, r.tty, src.Process) {
			continue // the program left the terminal: the record is old
		}
		rec := record{src: src, held: held, session: r.fields[src.Session], cwd: r.fields[src.Cwd]}
		if src.Path != "" {
			rec.path = r.fields[src.Path]
		} else {
			rec.path = l.expand(src.Transcript, map[string]string{"session": rec.session, "cwd": rec.cwd, "cwd_slug": cwdSlug(rec.cwd)})
		}
		if rec.session == "" && src.pattern != nil {
			if m := src.pattern.FindStringSubmatch(filepath.Base(rec.path)); len(m) > 1 {
				rec.session = m[1]
			}
		}
		if rec.session == "" || !regular(rec.path) {
			rec.path = ""
		}
		out = append(out, rec)
	}
	return out
}

// table is the process table for pids and ttys and the environment
// names of pids, cached for CacheFor.
func (l *Locator) table(pids []int, ttys, names []string) (map[int]Proc, map[int]map[string]string) {
	if l.sys == nil || len(pids) == 0 && len(ttys) == 0 {
		return nil, nil
	}
	slices.Sort(pids)
	slices.Sort(ttys)
	key := fmt.Sprint(pids, ttys, names)
	l.mu.Lock()
	now := l.now()
	if c, ok := l.cache[key]; ok && now.Sub(c.at) < CacheFor {
		l.mu.Unlock()
		return c.procs, c.env
	}
	l.mu.Unlock()
	procs := l.sys.Procs(pids, ttys)
	var env map[int]map[string]string
	if len(pids) > 0 && len(names) > 0 {
		var live []int
		for _, pid := range pids {
			if _, ok := procs[pid]; ok {
				live = append(live, pid)
			}
		}
		if len(live) > 0 {
			env = l.sys.Env(live, names)
		}
	}
	l.mu.Lock()
	if l.cache == nil {
		l.cache = map[string]cached{}
	}
	l.cache[key] = cached{at: now, procs: procs, env: env}
	l.mu.Unlock()
	return procs, env
}

// glob expands pattern's variables and globs it; at most 512 records.
func (l *Locator) glob(pattern string, vars map[string]string) []string {
	matches, _ := filepath.Glob(l.expand(pattern, vars))
	if len(matches) > 512 {
		matches = matches[:512]
	}
	return matches
}

var envVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expand writes s with ${VAR:-default}, {name} from vars, and a leading
// ~ as the home directory.
func (l *Locator) expand(s string, vars map[string]string) string {
	s = envVar.ReplaceAllStringFunc(s, func(m string) string {
		g := envVar.FindStringSubmatch(m)
		if v := l.getenv(g[1]); v != "" {
			return v
		}
		return g[2]
	})
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	if s == "~" || strings.HasPrefix(s, "~/") {
		s = l.home + s[1:]
	}
	return s
}

// readRecord reads one record's fields: a JSON object's string and number
// values by key, or a text file's lines by number from 1. At most 64 KiB.
func readRecord(path, format string) (map[string]string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	out := map[string]string{}
	if format == harness.SessionLines {
		for i, line := range strings.Split(string(b), "\n") {
			out[strconv.Itoa(i+1)] = strings.TrimSpace(line)
		}
		return out, true
	}
	var obj map[string]any
	if json.Unmarshal(b, &obj) != nil {
		return nil, false
	}
	for k, v := range obj {
		switch v := v.(type) {
		case string:
			out[k] = v
		case float64:
			out[k] = strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return out, true
}

// runsOn reports whether program runs on tty: a word among the first two
// of a command line there, by base name.
func runsOn(procs map[int]Proc, tty, program string) bool {
	for _, pr := range procs {
		if pr.TTY != tty {
			continue
		}
		for i, a := range pr.Args {
			if i > 1 {
				break
			}
			if filepath.Base(a) == program {
				return true
			}
		}
	}
	return false
}

// unique are the records keep passes, one per transcript.
func unique(recs []record, keep func(record) bool) []record {
	var out []record
	for _, r := range recs {
		if keep(r) && !slices.ContainsFunc(out, func(o record) bool { return o.path == r.path }) {
			out = append(out, r)
		}
	}
	return out
}

func found(r record) Found {
	return Found{Harness: r.src.name, Session: r.session, Path: r.path}
}

// note says that several sessions could be the pane's.
func note(cwd string, recs []record, home string) string {
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = short(r.session)
	}
	where := cwd
	if home != "" && (cwd == home || strings.HasPrefix(cwd, home+"/")) {
		where = "~" + cwd[len(home):]
	}
	return fmt.Sprintf("%d live sessions in %s could be this pane's (%s); not guessing which", len(recs), where, strings.Join(ids, ", "))
}

func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}

func words(s string) string { return strings.Join(strings.Fields(s), " ") }

func sameDir(a, b string) bool {
	return a != "" && b != "" && filepath.Clean(a) == filepath.Clean(b)
}

func regular(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

var notWordChar = regexp.MustCompile(`[^A-Za-z0-9]`)

// cwdSlug is the folder with every character but letters and digits as
// "-" (/Users/me/proj → -Users-me-proj).
func cwdSlug(cwd string) string { return notWordChar.ReplaceAllString(cwd, "-") }

// ttyName is a terminal's name as records use it: /dev/ttys041 → ttys041.
func ttyName(tty string) string {
	tty = strings.TrimPrefix(tty, "/dev/")
	if tty == "" || tty == "??" || strings.Contains(tty, "..") {
		return ""
	}
	return strings.ReplaceAll(tty, "/", "-")
}
