package locate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/harness"
)

// fakeSys is a process table.
type fakeSys struct {
	procs map[int]Proc
	env   map[int]map[string]string
	calls int
}

func (f *fakeSys) Procs(pids []int, ttys []string) map[int]Proc {
	f.calls++
	out := map[int]Proc{}
	for pid, p := range f.procs {
		for _, want := range pids {
			if pid == want {
				out[pid] = p
			}
		}
		for _, t := range ttys {
			if p.TTY == t {
				out[pid] = p
			}
		}
	}
	return out
}

func (f *fakeSys) Env(pids []int, names []string) map[int]map[string]string {
	out := map[int]map[string]string{}
	for _, pid := range pids {
		out[pid] = f.env[pid]
	}
	return out
}

const start = "Wed Oct 28 08:01:05 2026"

// claudeWorld is a Claude home with the built-in claude source pointed at
// it: sessions/<pid>.json records and projects/<slug>/<id>.jsonl
// transcripts.
type claudeWorld struct {
	t    *testing.T
	home string
	sys  *fakeSys
	l    *Locator
}

func newClaudeWorld(t *testing.T) *claudeWorld {
	home := t.TempDir()
	for _, d := range []string{"sessions", "projects"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w := &claudeWorld{t: t, home: home, sys: &fakeSys{procs: map[int]Proc{}, env: map[int]map[string]string{}}}
	env := func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return home
		}
		return ""
	}
	w.l = New(harness.Builtins(), w.sys, env, t.TempDir())
	return w
}

// process is a running claude: its sessions file, its process table row
// and the window its environment names ("" none).
func (w *claudeWorld) process(pid int, session, cwd, window string) {
	w.t.Helper()
	b, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": session, "cwd": cwd, "kind": "bg", "procStart": start})
	if err := os.WriteFile(filepath.Join(w.home, "sessions", strings.TrimSpace(itoa(pid))+".json"), b, 0o644); err != nil {
		w.t.Fatal(err)
	}
	w.sys.procs[pid] = Proc{Start: start}
	if window != "" {
		w.sys.env[pid] = map[string]string{"TUIOS_WINDOW_ID": window}
	}
}

func (w *claudeWorld) transcript(cwd, session string) string {
	w.t.Helper()
	dir := filepath.Join(w.home, "projects", cwdSlug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	p := filepath.Join(dir, session+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return p
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestClaudeRecords(t *testing.T) {
	pane := Pane{ID: "w1", Harness: "claude-code", Session: "stale", Cwd: "/w/hub"}
	other := Pane{ID: "w2", Cwd: "/w/hub"}
	for _, tc := range []struct {
		name  string
		setup func(w *claudeWorld)
		panes []Pane
		want  string // the session found for w1, "" none
		note  bool
	}{
		{"the pane's process names the live session", func(w *claudeWorld) {
			w.transcript("/w/hub", "stale")
			w.transcript("/w/hub", "live")
			w.process(10, "live", "/w/hub", "w1")
		}, []Pane{pane}, "live", false},
		{"an attaching client: the folder's one unheld session", func(w *claudeWorld) {
			w.process(10, "client", "/w/hub", "w1") // no transcript
			w.process(20, "bg", "/w/hub", "gone-window")
			w.transcript("/w/hub", "bg")
		}, []Pane{pane, other}, "bg", false},
		{"two unheld sessions in the folder: a note", func(w *claudeWorld) {
			w.process(20, "one", "/w/hub", "")
			w.process(21, "two", "/w/hub", "")
			w.transcript("/w/hub", "one")
			w.transcript("/w/hub", "two")
		}, []Pane{pane}, "", true},
		{"a reused pid is not trusted", func(w *claudeWorld) {
			w.process(10, "live", "/w/hub", "w1")
			w.transcript("/w/hub", "live")
			w.sys.procs[10] = Proc{Start: "Thu Oct 29 01:00:00 2026"}
		}, []Pane{pane}, "", false},
		{"a process held by another listed pane is never taken", func(w *claudeWorld) {
			w.process(20, "theirs", "/w/hub", "w2")
			w.transcript("/w/hub", "theirs")
		}, []Pane{pane, other}, "", false},
		{"another unresolved Claude pane in the folder: nothing", func(w *claudeWorld) {
			w.process(20, "bg", "/w/hub", "")
			w.transcript("/w/hub", "bg")
		}, []Pane{pane, {ID: "w2", Harness: "claude-code", Session: "also-stale", Cwd: "/w/hub"}}, "", false},
		{"a reported id with a transcript stands", func(w *claudeWorld) {
			w.transcript("/w/hub", "stale")
			w.process(20, "bg", "/w/hub", "")
			w.transcript("/w/hub", "bg")
		}, []Pane{pane}, "", false},
		{"another folder's session is not the pane's", func(w *claudeWorld) {
			w.process(20, "bg", "/w/elsewhere", "")
			w.transcript("/w/elsewhere", "bg")
		}, []Pane{pane}, "", false},
		{"a session another pane reports is not taken", func(w *claudeWorld) {
			w.process(20, "bg", "/w/hub", "")
			w.transcript("/w/hub", "bg")
		}, []Pane{pane, {ID: "w2", Session: "bg", Cwd: "/w/hub"}}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newClaudeWorld(t)
			tc.setup(w)
			f := w.l.Locate(tc.panes)["w1"]
			if f.Session != tc.want || (f.Note != "") != tc.note {
				t.Fatalf("got %+v, want session %q note %v", f, tc.want, tc.note)
			}
			if tc.want != "" && (f.Harness != "claude" || filepath.Base(f.Path) != tc.want+".jsonl") {
				t.Fatalf("got %+v", f)
			}
		})
	}
}

// The record is read on every listing: a session the process moves to
// is found at once; the process table is asked once per CacheFor.
func TestClaudeRecordFollowsTheProcess(t *testing.T) {
	w := newClaudeWorld(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	w.l.SetClock(func() time.Time { return now })
	w.transcript("/w/hub", "first")
	w.process(10, "first", "/w/hub", "w1")
	panes := []Pane{{ID: "w1", Harness: "claude-code", Session: "first", Cwd: "/w/hub"}}
	if f := w.l.Locate(panes)["w1"]; f.Session != "first" {
		t.Fatalf("got %+v", f)
	}
	w.transcript("/w/hub", "second")
	w.process(10, "second", "/w/hub", "w1")
	if f := w.l.Locate(panes)["w1"]; f.Session != "second" {
		t.Fatalf("after the move got %+v", f)
	}
	if w.sys.calls != 1 {
		t.Fatalf("ps ran %d times within CacheFor", w.sys.calls)
	}
	now = now.Add(CacheFor)
	w.l.Locate(panes)
	if w.sys.calls != 2 {
		t.Fatalf("ps ran %d times after CacheFor", w.sys.calls)
	}
	w.l.Forget()
	w.l.Locate(panes)
	if w.sys.calls != 3 {
		t.Fatalf("ps ran %d times after Forget", w.sys.calls)
	}
}

// omp writes terminal-sessions/<tty>: the folder, then the session file.
func TestOmpTerminalRecords(t *testing.T) {
	agent := t.TempDir()
	logDir := filepath.Join(agent, "sessions", "-hub")
	for _, d := range []string{filepath.Join(agent, "terminal-sessions"), logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(logDir, "2026-10-28T08-16-14-387Z_01a18194-ca33-77d9-ba40-c003af2c542b.jsonl")
	if err := os.WriteFile(log, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	crumb := "/w/hub\n" + log + "\ncwdstat 1 2\n"
	if err := os.WriteFile(filepath.Join(agent, "terminal-sessions", "ttys041"), []byte(crumb), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := "/w/hub\n" + filepath.Join(logDir, "gone_01a1.jsonl") + "\n"
	if err := os.WriteFile(filepath.Join(agent, "terminal-sessions", "ttys042"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		if k == "PI_CODING_AGENT_DIR" {
			return agent
		}
		return ""
	}
	sys := &fakeSys{procs: map[int]Proc{
		50: {Start: start, TTY: "ttys041", Args: []string{"bun", "/home/u/.bun/bin/omp"}},
		51: {Start: start, TTY: "ttys042", Args: []string{"bun", "/home/u/.bun/bin/omp"}},
		52: {Start: start, TTY: "ttys043", Args: []string{"-zsh"}},
	}}
	if err := os.WriteFile(filepath.Join(agent, "terminal-sessions", "ttys043"), []byte(crumb), 0o644); err != nil {
		t.Fatal(err)
	}
	l := New(harness.Builtins(), sys, env, t.TempDir())
	got := l.Locate([]Pane{
		{ID: "omp", Cwd: "/w/hub", TTY: "/dev/ttys041"},
		{ID: "gone", Cwd: "/w/hub", TTY: "/dev/ttys042"},  // the file it names is gone
		{ID: "shell", Cwd: "/w/hub", TTY: "/dev/ttys043"}, // omp left the terminal
	})
	if f := got["omp"]; f.Harness != "omp" || f.Session != "01a18194-ca33-77d9-ba40-c003af2c542b" || f.Path != log {
		t.Fatalf("omp pane: %+v", f)
	}
	for _, id := range []string{"gone", "shell"} {
		if f, ok := got[id]; ok {
			t.Fatalf("%s pane: %+v", id, f)
		}
	}
}

func TestExpand(t *testing.T) {
	l := New(nil, nil, func(k string) string {
		if k == "SET" {
			return "/set"
		}
		return ""
	}, "/home/u")
	for in, want := range map[string]string{
		"${SET:-~/x}/a":   "/set/a",
		"${UNSET:-~/x}/a": "/home/u/x/a",
		"~/a/{tty}":       "/home/u/a/ttys1",
		"/a/{cwd_slug}":   "/a/{cwd_slug}",
	} {
		if got := l.expand(in, map[string]string{"tty": "ttys1"}); got != want {
			t.Fatalf("expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseProcs(t *testing.T) {
	out := parseProcs("41099 ttys005  Wed Oct 28 08:01:05 2026     claude --dangerously-skip-permissions\n25170 ??  Wed Oct 28 07:17:43 2026  /x/2.1.312 --session-id a\n")
	if p := out[41099]; p.TTY != "ttys005" || p.Start != start || p.Args[0] != "claude" {
		t.Fatalf("%+v", p)
	}
	if p := out[25170]; p.TTY != "" || p.Start != "Wed Oct 28 07:17:43 2026" || len(p.Args) != 2 {
		t.Fatalf("%+v", p)
	}
	if env := pick(strings.Fields("claude -p TUIOS_WINDOW_ID=a HOME=/h TUIOS_WINDOW_ID=b"), []string{"TUIOS_WINDOW_ID"}); env["TUIOS_WINDOW_ID"] != "b" {
		t.Fatalf("%v", env)
	}
}
