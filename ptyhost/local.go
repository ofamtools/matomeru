package ptyhost

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/creack/pty"
	"ofam.dev/ochazuke/buffer"
	"tuios.dev/vt"
)

// DefaultHistory is the scrollback a LocalHost keeps per session when
// History is zero.
const DefaultHistory = 5000

// ErrUnknown is returned for an id with no open session.
var ErrUnknown = errors.New("ptyhost: unknown session")

// LocalHost runs sessions as local processes on real PTYs. The zero value is
// ready to use; set fields before the first Open.
type LocalHost struct {
	// History bounds each session's scrollback (DefaultHistory when zero).
	History int
	// Env is added to the parent's environment for every session, after
	// TERM=xterm-256color, so it can override it.
	Env []string
	// OnChange, when set, is called with a session's id after its output or
	// state changes. It runs on the session's reader or reaper goroutine and
	// must not block.
	OnChange func(id string)

	mu       sync.Mutex
	sessions map[string]*session
	changes  chan string
}

var _ Host = (*LocalHost)(nil)

// NewLocalHost returns a LocalHost with default settings.
func NewLocalHost() *LocalHost { return &LocalHost{} }

type session struct {
	id   string
	cmd  *exec.Cmd
	ptmx *os.File

	mu       sync.Mutex // guards everything below and emu
	emu      *vt.Emulator
	rev      uint64
	exited   bool
	code     int
	histMax  int
	histGen  uint64
	histLen  int
	hist     []string
	histOK   bool
	notified atomic.Bool // a change was sent on Changes and not yet read

	readDone chan struct{}
	waitDone chan struct{}
}

// Changes returns a channel that receives a session's id when its output or
// state changes. Sends never block the session: an id is sent once, and not
// again until Snapshot has read that session, so ids coalesce; an id that
// does not fit is retried on the next change.
func (h *LocalHost) Changes() <-chan string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.changesLocked()
}

func (h *LocalHost) changesLocked() chan string {
	if h.changes == nil {
		h.changes = make(chan string, 64)
	}
	return h.changes
}

// Open implements Host. An empty cmd runs $SHELL (or /bin/sh) with no
// arguments; any other cmd runs as /bin/sh -c cmd, so quoting, pipes and
// arguments work as in a shell.
func (h *LocalHost) Open(id, cmd, cwd string, cols, rows int) error {
	return h.OpenWithEnv(id, cmd, cwd, cols, rows, nil)
}

// OpenWithEnv is Open with variables added to the session's environment
// after the host's own Env, so they win: the app names the pane and its task
// (MTM_PANE_ID, MTM_TASK_ID) to the program it starts. A variable set to ""
// is left out.
func (h *LocalHost) OpenWithEnv(id, cmd, cwd string, cols, rows int, env map[string]string) error {
	cols, rows = max(cols, 1), max(rows, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sessions[id]; ok {
		return fmt.Errorf("ptyhost: session %q is already open", id)
	}
	c := command(cmd)
	c.Dir = cwd
	c.Env = append(withoutTERM(os.Environ()), "TERM=xterm-256color")
	c.Env = append(c.Env, h.Env...)
	for _, name := range slices.Sorted(maps.Keys(env)) {
		if env[name] != "" {
			c.Env = append(c.Env, name+"="+env[name])
		}
	}
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return fmt.Errorf("ptyhost: start %q: %w", c.Args, err)
	}
	emu := vt.NewEmulator(cols, rows)
	hist := h.History
	if hist <= 0 {
		hist = DefaultHistory
	}
	emu.SetScrollbackMaxLines(hist)
	s := &session{id: id, cmd: c, ptmx: ptmx, emu: emu, histMax: hist, readDone: make(chan struct{}), waitDone: make(chan struct{})}
	if h.sessions == nil {
		h.sessions = map[string]*session{}
	}
	h.sessions[id] = s
	h.changesLocked()
	go h.read(s)
	go h.reap(s)
	return nil
}

func command(cmd string) *exec.Cmd {
	if strings.TrimSpace(cmd) == "" {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		return exec.Command(sh)
	}
	return exec.Command("/bin/sh", "-c", cmd)
}

func withoutTERM(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "TERM=") {
			out = append(out, e)
		}
	}
	return out
}

// read feeds the program's output to the emulator and answers its replies.
func (h *LocalHost) read(s *session) {
	defer close(s.readDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.mu.Lock()
			_, _ = s.emu.Write(buf[:n])
			reply := s.emu.TakeResponses()
			s.rev++
			s.mu.Unlock()
			if len(reply) > 0 {
				_, _ = s.ptmx.Write(reply)
			}
			h.changed(s)
		}
		if err != nil {
			return
		}
	}
}

// reap waits for the program and records how it ended.
func (h *LocalHost) reap(s *session) {
	defer close(s.waitDone)
	err := s.cmd.Wait()
	code := 0
	if err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	s.mu.Lock()
	s.exited, s.code = true, code
	s.rev++
	s.mu.Unlock()
	h.changed(s)
}

func (h *LocalHost) changed(s *session) {
	if fn := h.OnChange; fn != nil {
		fn(s.id)
	}
	if !s.notified.CompareAndSwap(false, true) {
		return
	}
	h.mu.Lock()
	ch := h.changes
	h.mu.Unlock()
	select {
	case ch <- s.id:
	default:
		s.notified.Store(false)
	}
}

func (h *LocalHost) get(id string) (*session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[id]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknown, id)
	}
	return s, nil
}

// Write implements Host.
func (h *LocalHost) Write(id string, data []byte) error {
	s, err := h.get(id)
	if err != nil {
		return err
	}
	_, err = s.ptmx.Write(data)
	return err
}

// Resize implements Host.
func (h *LocalHost) Resize(id string, cols, rows int) error {
	s, err := h.get(id)
	if err != nil {
		return err
	}
	cols, rows = max(cols, 1), max(rows, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return err
	}
	if cols != s.emu.Width() || rows != s.emu.Height() {
		s.emu.Resize(cols, rows)
		s.rev++
	}
	return nil
}

// Snapshot implements Host.
func (h *LocalHost) Snapshot(id string) (Frame, bool) {
	s, err := h.get(id)
	if err != nil {
		return Frame{}, false
	}
	s.notified.Store(false)
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.emu
	w, ht := e.Width(), e.Height()
	f := Frame{
		ID:            id,
		Lines:         strings.Split(e.Render(), "\n"),
		Plain:         make([]string, ht),
		Scrollback:    s.history(),
		Cols:          w,
		Rows:          ht,
		Exited:        s.exited,
		ExitCode:      s.code,
		Title:         e.Titles().Title,
		AltScreen:     e.IsAltScreen(),
		AppCursorKeys: e.ApplicationCursorKeys(),
		Revision:      s.rev,
	}
	if s.cmd.Process != nil {
		f.PID = s.cmd.Process.Pid
	}
	var b strings.Builder
	for y := range ht {
		b.Reset()
		for x := range w {
			if c := e.CellAt(x, y); c != nil {
				b.WriteString(vt.CellText(c.Content))
			}
		}
		f.Plain[y] = strings.TrimRight(b.String(), " ")
	}
	pos := e.CursorPosition()
	f.Cursor = buffer.Cursor{X: pos.X, Y: pos.Y, Shape: cursorShape(e)}
	return f, true
}

// history returns the plain scrollback, rebuilt only when it changed. The
// caller holds s.mu.
func (s *session) history() []string {
	gen, n := s.emu.ScrollbackGeneration(), s.emu.ScrollbackLen()
	if s.histOK && gen == s.histGen && n == s.histLen {
		return s.hist
	}
	start := 0
	if s.histOK && n > s.histLen && n < s.histMax {
		// Only appended since the last read: keep what is cached.
		start = s.histLen
	} else {
		s.hist = nil
	}
	for i := start; i < n; i++ {
		s.hist = append(s.hist, strings.TrimRight(s.emu.ScrollbackLine(i).String(), " "))
	}
	// A fresh slice header per change, so a Frame handed out earlier never
	// sees lines appended after it.
	s.hist = s.hist[:n:n]
	s.histGen, s.histLen, s.histOK = gen, n, true
	return s.hist
}

func cursorShape(e *vt.Emulator) buffer.CursorShape {
	if e.IsCursorHidden() {
		return buffer.CursorHidden
	}
	switch st, _ := e.CursorStyle(); st {
	case vt.CursorBar:
		return buffer.CursorBar
	case vt.CursorUnderline:
		return buffer.CursorUnderline
	}
	return buffer.CursorBlock
}

// Close implements Host.
func (h *LocalHost) Close(id string) error {
	h.mu.Lock()
	s, ok := h.sessions[id]
	delete(h.sessions, id)
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknown, id)
	}
	s.stop()
	return nil
}

// CloseAll implements Host.
func (h *LocalHost) CloseAll() {
	h.mu.Lock()
	all := h.sessions
	h.sessions = nil
	h.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Go(s.stop)
	}
	wg.Wait()
}

// stop kills the process group (the PTY's session leader is its own group),
// reaps it, closes the PTY and waits for the reader.
func (s *session) stop() {
	if p := s.cmd.Process; p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
		_ = p.Kill()
	}
	<-s.waitDone
	_ = s.ptmx.Close()
	<-s.readDone
	s.mu.Lock()
	_ = s.emu.Close()
	s.mu.Unlock()
}
