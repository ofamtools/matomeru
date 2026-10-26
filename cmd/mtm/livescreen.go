package main

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/x/vt"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
)

// liveScreen replays a pane's stream-pane frames into a terminal emulator,
// as gmtm's live session does: the snapshot paints a fresh screen, output
// frames continue it, resize frames resize it. The emulator knows what a
// capture cannot: the cursor's cell, shape and visibility.
type liveScreen struct {
	mu  sync.Mutex
	emu *vt.Emulator
	err error // the emulator failed on the stream; frames come from captures
}

// apply feeds one stream frame to the emulator. A panic inside the
// emulator on bytes it cannot parse stops the replay rather than mtm.
func (l *liveScreen) apply(f daemon.Frame) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			l.err = fmt.Errorf("terminal emulator: %v", r)
			l.closeLocked()
		}
	}()
	switch f.Type {
	case 'S':
		l.closeLocked()
		l.emu = newEmulator(int(f.Cols), int(f.Rows))
		_, _ = l.emu.Write(f.Data)
	case 'O':
		if l.emu != nil {
			_, _ = l.emu.Write(f.Data)
		}
	case 'R':
		if l.emu != nil && f.Cols > 0 && f.Rows > 0 {
			l.emu.Resize(int(f.Cols), int(f.Rows))
		}
	}
}

// frame is the emulator's screen with its cursor, ok false before the first
// snapshot or after the emulator failed.
func (l *liveScreen) frame() (ochazuke.TerminalFrame, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.emu == nil || l.err != nil {
		return ochazuke.TerminalFrame{}, false
	}
	lines := strings.Split(l.emu.Render(), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	pos := l.emu.CursorPosition()
	style, _ := l.emu.CursorStyle()
	shape := map[vt.CursorStyle]string{vt.CursorBar: "bar", vt.CursorUnderline: "underline"}[style]
	return ochazuke.TerminalFrame{Lines: lines, Cursor: ochazuke.Pos{X: pos.X, Y: pos.Y}, Shape: shape, Hidden: l.emu.CursorHidden()}, true
}

func (l *liveScreen) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeLocked()
}

func (l *liveScreen) closeLocked() {
	if l.emu == nil {
		return
	}
	// The emulator's replies (DA, DSR) are drained and dropped: the daemon
	// answers the program. Closing the reply pipe ends the drain;
	// vt.Emulator.Close writes a flag the draining Read checks unguarded.
	if pw, ok := l.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
	}
	l.emu = nil
}

func newEmulator(cols, rows int) *vt.Emulator {
	e := vt.NewEmulator(max(1, cols), max(1, rows))
	go func() { _, _ = io.Copy(io.Discard, e) }()
	return e
}
