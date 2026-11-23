package ptyhost

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ofam.dev/ochazuke/buffer"
)

// ExecFunc runs one command of the daemon's CLI and returns its standard
// output. DaemonHost calls nothing else, so a test gives it a function and
// needs no daemon.
type ExecFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// Defaults of a DaemonHost left zero.
const (
	DefaultDaemonBinary  = "mtmd"
	DefaultDaemonTimeout = 3 * time.Second
	// EnvDaemonSocket names the daemon's socket: when it is set and the
	// socket exists, a daemon is taken to be reachable.
	EnvDaemonSocket = "MTMD_SOCKET"
)

// DaemonBinaries are the daemons AutoHost looks for, in order.
var DaemonBinaries = []string{"mtmd", "tuios"}

// DaemonHost is a Host whose sessions live in a daemon (mtmd, tuios), so
// they outlive this process: quitting the app leaves the programs running
// and the next start finds them again.
//
// It talks to the daemon through its command line, one invocation per call:
//
//	BINARY [--server NAME] new-pane --id ID --cols N --rows N [--cwd DIR] [--env K=V]... [-- CMD]
//	BINARY [--server NAME] send-keys --id ID --hex HEX
//	BINARY [--server NAME] resize-pane --id ID --cols N --rows N
//	BINARY [--server NAME] capture-pane --id ID --json
//	BINARY [--server NAME] kill-pane --id ID
//	BINARY [--server NAME] list-panes --json
//
// capture-pane prints {"lines","plain","scrollback","cursor":{"x","y","hidden"},
// "cols","rows","pid","exited","exitCode","title","altScreen","appCursorKeys"};
// output that is not JSON is read as the screen's plain rows. list-panes
// prints a list of {"id","title","pid","cwd","command","cols","rows","exited"}.
type DaemonHost struct {
	// Binary is the daemon's command: "mtmd", "tuios", or several words such
	// as "mtm __mtmd". Empty is DefaultDaemonBinary.
	Binary string
	// Server names the daemon instance, so several can run side by side.
	// Empty is the daemon's default.
	Server string
	// Exec runs the commands; nil runs the binary.
	Exec ExecFunc
	// Timeout bounds each command; 0 is DefaultDaemonTimeout.
	Timeout time.Duration
	// MinInterval is how long a Snapshot is reused before the daemon is
	// asked again; 0 asks every time.
	MinInterval time.Duration

	mu     sync.Mutex
	frames map[string]*daemonFrame
}

type daemonFrame struct {
	frame Frame
	sum   uint64
	at    time.Time
	known bool // a capture has been read
}

// RemotePane is one pane the daemon holds.
type RemotePane struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Command string `json:"command,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Exited  bool   `json:"exited,omitempty"`
}

// NewDaemonHost returns a host over the daemon binary and server name.
func NewDaemonHost(binary, server string) *DaemonHost {
	return &DaemonHost{Binary: binary, Server: server}
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// argv is the command line of one daemon command: the binary's own words,
// the server name, then the command.
func (h *DaemonHost) argv(args ...string) []string {
	words := strings.Fields(h.Binary)
	if len(words) == 0 {
		words = []string{DefaultDaemonBinary}
	}
	if h.Server != "" {
		words = append(words, "--server", h.Server)
	}
	return append(words, args...)
}

func (h *DaemonHost) run(args ...string) ([]byte, error) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultDaemonTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	run := h.Exec
	if run == nil {
		run = runCommand
	}
	argv := h.argv(args...)
	out, err := run(ctx, argv[0], argv[1:]...)
	if err != nil {
		return out, fmt.Errorf("ptyhost: %s %s: %w", argv[0], args[0], err)
	}
	return out, nil
}

func (h *DaemonHost) entry(id string) *daemonFrame {
	if h.frames == nil {
		h.frames = map[string]*daemonFrame{}
	}
	if h.frames[id] == nil {
		h.frames[id] = &daemonFrame{}
	}
	return h.frames[id]
}

// Open starts cmd in a new pane of the daemon.
func (h *DaemonHost) Open(id, cmd, cwd string, cols, rows int) error {
	return h.OpenWithEnv(id, cmd, cwd, cols, rows, nil)
}

// OpenWithEnv is Open with extra environment variables for the program.
func (h *DaemonHost) OpenWithEnv(id, cmd, cwd string, cols, rows int, env map[string]string) error {
	if id == "" {
		return errors.New("ptyhost: a daemon pane needs an id")
	}
	args := []string{"new-pane", "--id", id, "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows)}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+env[k])
	}
	if cmd != "" {
		args = append(args, "--", cmd)
	}
	if _, err := h.run(args...); err != nil {
		return err
	}
	h.mu.Lock()
	e := h.entry(id)
	e.frame = Frame{ID: id, Cols: cols, Rows: rows, Cursor: buffer.Cursor{Shape: buffer.CursorHidden}, Revision: e.frame.Revision + 1}
	e.known, e.at = false, time.Time{}
	h.mu.Unlock()
	return nil
}

// Write sends input bytes to the pane's program. They travel as hex, so
// control bytes and escape sequences arrive as they were typed.
func (h *DaemonHost) Write(id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	_, err := h.run("send-keys", "--id", id, "--hex", hex.EncodeToString(data))
	h.expire(id)
	return err
}

// Resize changes the pane's window size.
func (h *DaemonHost) Resize(id string, cols, rows int) error {
	if _, err := h.run("resize-pane", "--id", id, "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows)); err != nil {
		return err
	}
	h.mu.Lock()
	e := h.entry(id)
	e.frame.Cols, e.frame.Rows = cols, rows
	e.frame.Revision++
	e.at = time.Time{}
	h.mu.Unlock()
	return nil
}

// expire makes the next Snapshot ask the daemon again.
func (h *DaemonHost) expire(id string) {
	h.mu.Lock()
	if e := h.frames[id]; e != nil {
		e.at = time.Time{}
	}
	h.mu.Unlock()
}

type capture struct {
	Lines      []string `json:"lines"`
	Plain      []string `json:"plain"`
	Scrollback []string `json:"scrollback"`
	Cursor     *struct {
		X      int  `json:"x"`
		Y      int  `json:"y"`
		Hidden bool `json:"hidden"`
	} `json:"cursor"`
	Cols           int    `json:"cols"`
	Rows           int    `json:"rows"`
	PID            int    `json:"pid"`
	Exited         bool   `json:"exited"`
	ExitCode       int    `json:"exitCode"`
	Title          string `json:"title"`
	AltScreen      bool   `json:"altScreen"`
	AppCursorKeys  bool   `json:"appCursorKeys"`
	BracketedPaste bool   `json:"bracketedPaste"`
}

func trimRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(r, " \t\r")
	}
	return out
}

// decodeCapture reads what capture-pane printed: the JSON document, or
// plain text rows.
func decodeCapture(id string, out []byte) Frame {
	f := Frame{ID: id, Cursor: buffer.Cursor{Shape: buffer.CursorHidden}}
	var c capture
	if trimmed := bytes.TrimSpace(out); len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(trimmed, &c) == nil {
		f.Lines, f.Plain, f.Scrollback = c.Lines, c.Plain, c.Scrollback
		if f.Plain == nil {
			f.Plain = trimRows(c.Lines)
		}
		if f.Lines == nil {
			f.Lines = slices.Clone(c.Plain)
		}
		if c.Cursor != nil {
			f.Cursor.X, f.Cursor.Y = c.Cursor.X, c.Cursor.Y
			if !c.Cursor.Hidden {
				f.Cursor.Shape = buffer.CursorBlock
			}
		}
		f.Cols, f.Rows, f.PID, f.Exited, f.ExitCode = c.Cols, c.Rows, c.PID, c.Exited, c.ExitCode
		f.Title, f.AltScreen, f.AppCursorKeys, f.BracketedPaste = c.Title, c.AltScreen, c.AppCursorKeys, c.BracketedPaste
		return f
	}
	text := strings.TrimRight(string(out), "\n")
	if text != "" {
		f.Lines = strings.Split(text, "\n")
	}
	f.Plain = trimRows(f.Lines)
	return f
}

// Snapshot reads the pane's screen from the daemon. Its Revision grows only
// when what the daemon shows changed. It reports false for a pane the
// daemon does not hold.
func (h *DaemonHost) Snapshot(id string) (Frame, bool) {
	h.mu.Lock()
	if e := h.frames[id]; e != nil && e.known && h.MinInterval > 0 && time.Since(e.at) < h.MinInterval {
		f := e.frame
		h.mu.Unlock()
		return f, true
	}
	h.mu.Unlock()

	out, err := h.run("capture-pane", "--id", id, "--json")
	if err != nil {
		h.mu.Lock()
		delete(h.frames, id)
		h.mu.Unlock()
		return Frame{}, false
	}
	f := decodeCapture(id, out)
	sum := fnv.New64a()
	sum.Write(out)

	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entry(id)
	if f.Cols == 0 {
		f.Cols = e.frame.Cols
	}
	if f.Rows == 0 {
		f.Rows = max(e.frame.Rows, len(f.Lines))
	}
	f.Revision = e.frame.Revision
	if !e.known || e.sum != sum.Sum64() {
		f.Revision++
	}
	e.frame, e.sum, e.at, e.known = f, sum.Sum64(), time.Now(), true
	return f, true
}

// Close ends the pane in the daemon.
func (h *DaemonHost) Close(id string) error {
	_, err := h.run("kill-pane", "--id", id)
	h.mu.Lock()
	delete(h.frames, id)
	h.mu.Unlock()
	return err
}

// CloseAll forgets what this process knew of the daemon's panes. The panes
// themselves stay: outliving the app is what a daemon is for. Close ends one.
func (h *DaemonHost) CloseAll() {
	h.mu.Lock()
	h.frames = nil
	h.mu.Unlock()
}

// ListRemotePanes lists every pane the daemon holds, whoever opened it.
func (h *DaemonHost) ListRemotePanes() ([]RemotePane, error) {
	out, err := h.run("list-panes", "--json")
	if err != nil {
		return nil, err
	}
	var panes []RemotePane
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(trimmed, &panes); err != nil {
		var wrapped struct {
			Panes []RemotePane `json:"panes"`
		}
		if err2 := json.Unmarshal(trimmed, &wrapped); err2 != nil {
			return nil, fmt.Errorf("ptyhost: list-panes did not print a list of panes: %w", err)
		}
		panes = wrapped.Panes
	}
	return panes, nil
}

// Reachable reports whether the daemon answers.
func (h *DaemonHost) Reachable() bool {
	_, err := h.ListRemotePanes()
	return err == nil
}

// autoHost is AutoHost with its probes given: look finds a binary on PATH,
// stat reports whether a socket exists, and exec runs the daemon's commands.
func autoHost(preferDaemon bool, look func(string) (string, error), socket string, stat func(string) bool, run ExecFunc) Host {
	if !preferDaemon {
		return NewLocalHost()
	}
	for _, name := range DaemonBinaries {
		if _, err := look(name); err != nil {
			continue
		}
		if h := (&DaemonHost{Binary: name, Exec: run, MinInterval: 30 * time.Millisecond}); (socket != "" && stat(socket)) || h.Reachable() {
			return h
		}
	}
	return NewLocalHost()
}

// AutoHost picks the host panes run on. With preferDaemon it is a
// DaemonHost when one of DaemonBinaries is on PATH and its daemon answers
// (or the socket named by EnvDaemonSocket exists); otherwise, and whenever
// no daemon is reachable, it is a LocalHost, whose sessions end with the app.
func AutoHost(preferDaemon bool) Host {
	return autoHost(preferDaemon, exec.LookPath, os.Getenv(EnvDaemonSocket), func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}, nil)
}
