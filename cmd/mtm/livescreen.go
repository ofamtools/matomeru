package main

import (
	"fmt"
	"io"
	"sync"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// liveScreen replays a pane's stream-pane frames into a terminal emulator,
// as gmtm's live session does: the snapshot paints a fresh screen, output
// frames continue it, resize frames resize it. The emulator knows what a
// capture cannot: the cursor's cell, shape and visibility. engine is the
// terminal.engine the stream opened with ("" is ochazuke's default); each
// snapshot makes a new emulator of it.
type liveScreen struct {
	mu     sync.Mutex
	engine string
	emu    ocli.Emulator
	err    error // the emulator failed on the stream; frames come from captures
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
		emu, err := newEmulator(l.engine, int(f.Cols), int(f.Rows))
		if err != nil {
			l.err = err
			return
		}
		l.emu = emu
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
	return ocli.EmulatorFrame(l.emu), true
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
	// answers the program. Closing the emulator ends the drain.
	_ = l.emu.Close()
	l.emu = nil
}

// newEmulator makes an emulator of the engine whose replies are drained
// and dropped.
func newEmulator(engine string, cols, rows int) (ocli.Emulator, error) {
	e, err := ocli.NewEmulator(engine, max(1, cols), max(1, rows))
	if err != nil {
		return nil, err
	}
	go func() { _, _ = io.Copy(io.Discard, e) }()
	return e, nil
}

// engineUser is a stream that replays into an emulator (liveStream);
// App.openStream gives it the terminal.engine setting, so a change applies
// to streams opened after it.
type engineUser interface {
	useEngine(name string)
}

// useEngine sets the engine the next snapshot's emulator is made with.
func (s *liveStream) useEngine(name string) {
	s.screen.mu.Lock()
	s.screen.engine = name
	s.screen.mu.Unlock()
}
