package ptyhost

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const deadline = 10 * time.Second

func newHost(t *testing.T) *LocalHost {
	t.Helper()
	h := &LocalHost{Env: []string{"PS1=$ ", "ENV=", "HISTFILE=/dev/null"}}
	t.Cleanup(h.CloseAll)
	return h
}

// waitFrame polls the session until ok accepts its frame or the deadline
// passes.
func waitFrame(t *testing.T, h Host, id string, what string, ok func(Frame) bool) Frame {
	t.Helper()
	end := time.Now().Add(deadline)
	var f Frame
	for time.Now().Before(end) {
		f, _ = h.Snapshot(id)
		if ok(f) {
			return f
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: timed out; screen:\n%s", what, strings.Join(f.Plain, "\n"))
	return f
}

func hasLine(lines []string, s string) bool {
	return slices.ContainsFunc(lines, func(l string) bool { return strings.TrimSpace(l) == s })
}

func TestOutputExitAndChanges(t *testing.T) {
	h := newHost(t)
	changes := h.Changes()
	if err := h.Open("p", `printf "hello-pty\n"`, t.TempDir(), 40, 5); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-changes:
		if id != "p" {
			t.Fatalf("change for %q", id)
		}
	case <-time.After(deadline):
		t.Fatal("no change notification")
	}
	f := waitFrame(t, h, "p", "hello-pty and exit", func(f Frame) bool { return hasLine(f.Plain, "hello-pty") && f.Exited })
	if f.Plain[0] != "hello-pty" || f.ExitCode != 0 || f.PID <= 0 || f.Cols != 40 || f.Rows != 5 || len(f.Lines) != 5 {
		t.Fatalf("frame = %+v", f)
	}
	if err := h.Open("p", "true", "", 10, 2); err == nil {
		t.Fatal("reopening an open id succeeded")
	}
	if err := h.Write("nope", []byte("x")); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Write unknown = %v", err)
	}
	if _, ok := h.Snapshot("nope"); ok {
		t.Fatal("Snapshot unknown reported ok")
	}
}

func TestWriteEchoAndResize(t *testing.T) {
	h := newHost(t)
	if err := h.Open("sh", "/bin/sh", "", 60, 10); err != nil {
		t.Fatal(err)
	}
	if err := h.Write("sh", []byte("echo hi-$((1+2))\r")); err != nil {
		t.Fatal(err)
	}
	f0 := waitFrame(t, h, "sh", "echo", func(f Frame) bool { return hasLine(f.Plain, "hi-3") })
	if err := h.Resize("sh", 100, 30); err != nil {
		t.Fatal(err)
	}
	if err := h.Write("sh", []byte("stty size\r")); err != nil {
		t.Fatal(err)
	}
	f := waitFrame(t, h, "sh", "stty size", func(f Frame) bool { return hasLine(f.Plain, "30 100") })
	if f.Cols != 100 || f.Rows != 30 || len(f.Plain) != 30 || f.Revision <= f0.Revision {
		t.Fatalf("after resize: %dx%d, %d rows, rev %d -> %d", f.Cols, f.Rows, len(f.Plain), f0.Revision, f.Revision)
	}
}

func TestScrollbackKeepsScrolledLines(t *testing.T) {
	h := newHost(t)
	if err := h.Open("seq", "seq 1 200", "", 20, 10); err != nil {
		t.Fatal(err)
	}
	f := waitFrame(t, h, "seq", "seq done", func(f Frame) bool { return f.Exited && hasLine(f.History(), "200") })
	if len(f.Scrollback) < 190 || f.Scrollback[0] != "1" || f.Scrollback[99] != "100" {
		t.Fatalf("scrollback has %d lines, starts %q", len(f.Scrollback), f.Scrollback[:min(3, len(f.Scrollback))])
	}
}

func TestCloseAllReapsEverything(t *testing.T) {
	before := runtime.NumGoroutine()
	h := &LocalHost{}
	var pids []int
	for i := range 3 {
		id := fmt.Sprint("s", i)
		// A background child holds the PTY open too: Close must kill the
		// whole process group for the reader to finish.
		if err := h.Open(id, "sleep 60 & wait", "", 20, 5); err != nil {
			t.Fatal(err)
		}
		f, _ := h.Snapshot(id)
		pids = append(pids, f.PID)
	}
	h.CloseAll()
	if _, ok := h.Snapshot("s0"); ok {
		t.Fatal("session survived CloseAll")
	}
	end := time.Now().Add(deadline)
	for runtime.NumGoroutine() > before && time.Now().Before(end) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines: %d before, %d after", before, n)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("pid %d still exists: %v", pid, err)
		}
	}
}

func TestEncode(t *testing.T) {
	for key, want := range map[string]string{
		"a": "a", "é": "é", "space": " ", "enter": "\r", "tab": "\t", "backspace": "\x7f",
		"esc": "\x1b", "up": "\x1b[A", "ctrl+c": "\x03", "ctrl+[": "\x1b", "alt+x": "\x1bx",
		"ctrl+left": "\x1b[1;5D", "delete": "\x1b[3~", "f1": "\x1bOP",
	} {
		if got, ok := Encode(key, false); !ok || string(got) != want {
			t.Errorf("Encode(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
	if got, _ := Encode("up", true); string(got) != "\x1bOA" {
		t.Errorf("app cursor up = %q", got)
	}
	if _, ok := Encode("hyper+q", false); ok {
		t.Error("Encode(hyper+q) succeeded")
	}
}

// fakeHost records writes and serves one fixed frame.
type fakeHost struct {
	frame  Frame
	writes map[string][]byte
}

func (f *fakeHost) Open(string, string, string, int, int) error { return nil }
func (f *fakeHost) Write(id string, b []byte) error {
	if f.writes == nil {
		f.writes = map[string][]byte{}
	}
	f.writes[id] = append(f.writes[id], b...)
	return nil
}
func (f *fakeHost) Resize(string, int, int) error { return nil }
func (f *fakeHost) Snapshot(string) (Frame, bool) { return f.frame, true }
func (f *fakeHost) Close(string) error            { return nil }
func (f *fakeHost) CloseAll()                     {}

func keys(c *Controller, id string, ks ...string) Action {
	var a Action
	for _, k := range ks {
		a = c.Key(id, k)
	}
	return a
}

func TestPrefixKeys(t *testing.T) {
	fh := &fakeHost{}
	c := NewController(fh)
	c.LookPath = func(name string) (string, error) {
		if name == "vim" {
			return "/usr/bin/vim", nil
		}
		return "", errors.New("not found")
	}
	if a := c.Key("p", "x"); !reflect.DeepEqual(a, ActionSent{Data: []byte("x")}) {
		t.Fatalf("x = %#v", a)
	}
	if a := c.Key("p", "ctrl+a"); a != (ActionMode{Mode: ModePrefix}) {
		t.Fatalf("ctrl+a = %#v", a)
	}
	if a := c.Key("p", "t"); a != (ActionSpawn{}) {
		t.Fatalf("ctrl+a t = %#v", a)
	}
	if a := keys(c, "p", "ctrl+a", "v"); a != (ActionSpawn{Command: "vim"}) {
		t.Fatalf("ctrl+a v = %#v", a)
	}
	keys(c, "p", "ctrl+a", "ctrl+a")
	if got := string(fh.writes["p"]); got != "x\x01" || c.Mode("p") != ModePassthrough {
		t.Fatalf("writes %q, mode %v", got, c.Mode("p"))
	}
}

func TestCopyMode(t *testing.T) {
	fh := &fakeHost{frame: Frame{
		Plain: []string{"alpha", "beta", "gamma", "", ""},
		Lines: []string{"\x1b[1malpha\x1b[m", "beta", "gamma", "", ""},
		Rows:  5, Cols: 20,
	}}
	for i := 1; i <= 10; i++ {
		fh.frame.Scrollback = append(fh.frame.Scrollback, fmt.Sprint("line ", i))
	}
	fh.frame.Cursor.Y = 2
	c := NewController(fh)
	if a := keys(c, "p", "ctrl+a", "["); a != (ActionMode{Mode: ModeCopy}) {
		t.Fatalf("ctrl+a [ = %#v", a)
	}
	view := func() CopyView {
		t.Helper()
		v, ok := c.Copy("p")
		if !ok {
			t.Fatal("not in copy mode")
		}
		return v
	}
	if v := view(); v.Cursor != 12 || v.Top != 10 || len(v.Lines) != 15 || v.Styled[10] != "\x1b[1malpha\x1b[m" {
		t.Fatalf("entered at cursor %d top %d, %d lines", v.Cursor, v.Top, len(v.Lines))
	}
	steps := []struct {
		keys        []string
		cursor, top int
	}{
		{[]string{"k"}, 11, 10},
		{[]string{"j"}, 12, 10},
		{[]string{"g", "g"}, 0, 0},
		{[]string{"G"}, 14, 10},
		{[]string{"ctrl+u"}, 12, 10},
		{[]string{"/", "l", "i", "n", "e", "space", "7", "enter"}, 6, 6},
		{[]string{"/", "L", "I", "N", "E", "backspace", "backspace", "backspace", "backspace", "l", "i", "n", "e", "enter"}, 7, 6},
		{[]string{"N"}, 6, 6},
	}
	for _, s := range steps {
		keys(c, "p", s.keys...)
		if v := view(); v.Cursor != s.cursor || v.Top != s.top {
			t.Fatalf("after %v: cursor %d top %d, want %d %d", s.keys, v.Cursor, v.Top, s.cursor, s.top)
		}
	}
	if v := view(); v.Search != "line" || len(v.Matches) != 10 {
		t.Fatalf("search %q matches %v", v.Search, v.Matches)
	}
	if a := keys(c, "p", "v", "j", "y"); a != (ActionYank{Text: "line 7\nline 8"}) {
		t.Fatalf("yank = %#v", a)
	}
	if c.Mode("p") != ModePassthrough {
		t.Fatal("yank did not leave copy mode")
	}
	if a := keys(c, "p", "ctrl+a", "[", "k", "y"); a != (ActionYank{Text: "beta"}) {
		t.Fatalf("line yank = %#v", a)
	}
	if a := keys(c, "p", "ctrl+a", "[", "q"); a != (ActionMode{Mode: ModePassthrough}) || len(fh.writes["p"]) != 0 {
		t.Fatalf("q = %#v, writes %q", a, fh.writes["p"])
	}
}

// OpenWithEnv names the pane and its task to the program; an empty value is
// left out, and the extra variables win over the host's own.
func TestOpenWithEnv(t *testing.T) {
	h := newHost(t)
	h.Env = []string{"MTM_PANE_ID=from-host"}
	env := map[string]string{"MTM_PANE_ID": "w7", "MTM_TASK_ID": "t1", "MTM_EMPTY": ""}
	if err := h.OpenWithEnv("p", `printf "pane=%s task=%s empty=%s\n" "$MTM_PANE_ID" "$MTM_TASK_ID" "${MTM_EMPTY-unset}"`, "", 60, 5, env); err != nil {
		t.Fatal(err)
	}
	waitFrame(t, h, "p", "the environment", func(f Frame) bool { return hasLine(f.Plain, "pane=w7 task=t1 empty=unset") })
	var _ EnvOpener = h
}
