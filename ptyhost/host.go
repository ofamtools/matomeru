// Package ptyhost runs programs in pseudo-terminals and keeps what they draw.
//
// A Host owns sessions by id. Each session is one process on a PTY whose
// output feeds a VT emulator; Snapshot reads the emulator as a Frame the app
// can put straight into a ui.KindTerminal node (Lines as props.lines, Cursor
// as props.cursor). The Controller turns named keys into PTY input, a ctrl+a
// prefix and a Copy Mode over the captured history, without Bubble Tea.
package ptyhost

import "ofam.dev/ochazuke/buffer"

// Host is a set of PTY sessions addressed by id.
type Host interface {
	// Open starts cmd in a new session. An empty cmd runs $SHELL (falling
	// back to /bin/sh); any other cmd is a shell command line, run with
	// /bin/sh -c. Opening an id that is already open is an error.
	Open(id, cmd, cwd string, cols, rows int) error
	// Write sends input bytes to the session's program.
	Write(id string, data []byte) error
	// Resize changes the session's window size.
	Resize(id string, cols, rows int) error
	// Snapshot reads the session's current screen. It reports false for an
	// unknown id.
	Snapshot(id string) (Frame, bool)
	// Close kills the session's process group, closes its PTY and waits for
	// its goroutines.
	Close(id string) error
	// CloseAll closes every session.
	CloseAll()
}

// Frame is one session's screen at one revision.
type Frame struct {
	ID string
	// Lines are the visible rows rendered with ANSI styles, Rows of them.
	Lines []string
	// Plain are the visible rows as plain text, trailing blanks trimmed.
	Plain []string
	// Scrollback is the history that scrolled off the top, oldest first, as
	// plain text. It is bounded by the host's history limit.
	Scrollback []string
	// Cursor is the hardware cursor in screen coordinates. Its Shape is
	// buffer.CursorHidden while the program hides it.
	Cursor     buffer.Cursor
	Cols, Rows int
	PID        int
	Exited     bool
	ExitCode   int
	Title      string
	// AltScreen is set while a full-screen program uses the alternate
	// screen; Scrollback is then the main screen's history.
	AltScreen bool
	// AppCursorKeys is set while the program asks for application cursor
	// keys (arrows encode as ESC O x).
	AppCursorKeys bool
	// BracketedPaste is set while the program enables DEC mode 2004
	// bracketed paste (\x1b[?2004h).
	BracketedPaste bool `json:"bracketedPaste,omitempty"`
	// Revision grows each time the session's output, size or state changes,
	// so the app can redraw only when it differs from the last one drawn.
	Revision uint64
}

// History is Scrollback followed by the visible plain lines: everything the
// session has shown, oldest first.
func (f Frame) History() []string {
	out := make([]string, 0, len(f.Scrollback)+len(f.Plain))
	out = append(out, f.Scrollback...)
	return append(out, f.Plain...)
}

// EnvOpener is a Host that can start a session with extra environment
// variables. LocalHost is one.
type EnvOpener interface {
	OpenWithEnv(id, cmd, cwd string, cols, rows int, env map[string]string) error
}
