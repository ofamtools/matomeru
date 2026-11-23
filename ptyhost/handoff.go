package ptyhost

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"tuios.dev/vt"
)

// HandoffSession describes one live PTY session passed across a zero-downtime
// daemon exec upgrade. The PTY master FD has FD_CLOEXEC cleared so the child
// process and all of its subagents never receive SIGHUP.
type HandoffSession struct {
	ID         string   `json:"id"`
	FD         int      `json:"fd"`
	PID        int      `json:"pid"`
	Cols       int      `json:"cols"`
	Rows       int      `json:"rows"`
	Exited     bool     `json:"exited,omitempty"`
	ExitCode   int      `json:"exitCode,omitempty"`
	Lines      []string `json:"lines,omitempty"`
	Scrollback []string `json:"scrollback,omitempty"`
	CursorX    int      `json:"cursorX,omitempty"`
	CursorY    int      `json:"cursorY,omitempty"`
	Title      string   `json:"title,omitempty"`
	AltScreen  bool     `json:"altScreen,omitempty"`
}

// ClearCloseOnExec clears FD_CLOEXEC on fd so it stays open across syscall.Exec.
func ClearCloseOnExec(fd uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// ExportForUpgrade snapshots all open sessions and clears FD_CLOEXEC on every
// PTY master FD so a replacement daemon binary launched via syscall.Exec can
// adopt them without closing the PTYs or sending SIGHUP to running processes
// and subagents.
func (h *LocalHost) ExportForUpgrade() ([]HandoffSession, error) {
	h.mu.Lock()
	ids := slices.Sorted(maps.Keys(h.sessions))
	sessions := make([]*session, 0, len(ids))
	for _, id := range ids {
		sessions = append(sessions, h.sessions[id])
	}
	h.mu.Unlock()

	out := make([]HandoffSession, 0, len(sessions))
	for _, s := range sessions {
		f, ok := h.Snapshot(s.id)
		if !ok {
			continue
		}
		fd := -1
		if s.ptmx != nil {
			rawFD := s.ptmx.Fd()
			if err := ClearCloseOnExec(rawFD); err != nil {
				return nil, fmt.Errorf("ptyhost: export %q clear cloexec: %w", s.id, err)
			}
			fd = int(rawFD)
		}
		out = append(out, HandoffSession{
			ID:         s.id,
			FD:         fd,
			PID:        f.PID,
			Cols:       f.Cols,
			Rows:       f.Rows,
			Exited:     f.Exited,
			ExitCode:   f.ExitCode,
			Lines:      f.Lines,
			Scrollback: f.Scrollback,
			CursorX:    f.Cursor.X,
			CursorY:    f.Cursor.Y,
			Title:      f.Title,
			AltScreen:  f.AltScreen,
		})
	}
	return out, nil
}

// AdoptFromUpgrade reattaches inherited PTY master FDs exported by
// ExportForUpgrade before syscall.Exec. It immediately re-enables FD_CLOEXEC
// on each adopted FD so future child processes do not inherit sibling PTYs.
func (h *LocalHost) AdoptFromUpgrade(items []HandoffSession) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = map[string]*session{}
	}
	h.changesLocked()

	for _, hs := range items {
		if hs.ID == "" || hs.FD < 0 {
			continue
		}
		if _, exists := h.sessions[hs.ID]; exists {
			continue
		}
		syscall.CloseOnExec(hs.FD)
		ptmx := os.NewFile(uintptr(hs.FD), "pty-"+hs.ID)
		cols, rows := max(hs.Cols, 1), max(hs.Rows, 1)
		emu := vt.NewEmulator(cols, rows)
		hist := h.History
		if hist <= 0 {
			hist = DefaultHistory
		}
		emu.SetScrollbackMaxLines(hist)

		restoreEmulator(emu, hs)

		s := &session{
			id:       hs.ID,
			pid:      hs.PID,
			ptmx:     ptmx,
			emu:      emu,
			rev:      1,
			exited:   hs.Exited,
			code:     hs.ExitCode,
			histMax:  hist,
			readDone: make(chan struct{}),
			waitDone: make(chan struct{}),
		}
		if len(hs.Scrollback) > 0 {
			s.hist = append([]string(nil), hs.Scrollback...)
			s.histLen = len(s.hist)
			s.histGen = emu.ScrollbackGeneration()
			s.histOK = true
		}
		h.sessions[hs.ID] = s
		go h.read(s)
		go h.reapInherited(s)
		if !hs.Exited && hs.PID > 0 {
			// Ask full-screen TUIs (Claude Code, Codex, vim) to repaint cleanly.
			_ = syscall.Kill(-hs.PID, syscall.SIGWINCH)
		}
	}
	return nil
}

func restoreEmulator(emu *vt.Emulator, hs HandoffSession) {
	if hs.AltScreen {
		_, _ = emu.Write([]byte("\x1b[?1049h"))
	}
	if len(hs.Lines) > 0 {
		var b strings.Builder
		b.WriteString("\x1b[H")
		for i, line := range hs.Lines {
			if i > 0 {
				b.WriteString("\r\n")
			}
			b.WriteString(line)
			b.WriteString("\x1b[0m")
		}
		fmt.Fprintf(&b, "\x1b[%d;%dH", hs.CursorY+1, hs.CursorX+1)
		_, _ = emu.Write([]byte(b.String()))
	}
	if hs.Title != "" {
		_, _ = emu.Write([]byte("\x1b]2;" + hs.Title + "\x07"))
	}
	_ = emu.TakeResponses()
}

func (h *LocalHost) reapInherited(s *session) {
	defer close(s.waitDone)
	if s.exited || s.pid <= 0 {
		return
	}
	var ws syscall.WaitStatus
	for {
		_, err := syscall.Wait4(s.pid, &ws, 0, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.ECHILD) {
			// Process is not a direct child (for example in an in-process test):
			// wait until the PTY closes or the PID exits.
			t := time.NewTicker(50 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-s.readDone:
					s.mu.Lock()
					s.exited = true
					s.rev++
					s.mu.Unlock()
					h.changed(s)
					return
				case <-t.C:
					if err := syscall.Kill(s.pid, 0); errors.Is(err, syscall.ESRCH) {
						s.mu.Lock()
						s.exited = true
						s.rev++
						s.mu.Unlock()
						h.changed(s)
						return
					}
				}
			}
		}
		code := 0
		if err != nil {
			code = -1
		} else if ws.Exited() {
			code = ws.ExitStatus()
		} else if ws.Signaled() {
			code = 128 + int(ws.Signal())
		}
		s.mu.Lock()
		s.exited, s.code = true, code
		s.rev++
		s.mu.Unlock()
		h.changed(s)
		return
	}
}
