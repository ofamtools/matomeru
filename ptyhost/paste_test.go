package ptyhost

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

type pasteFakeHost struct {
	frame   Frame
	written [][]byte
}

func (h *pasteFakeHost) Open(string, string, string, int, int) error { return nil }
func (h *pasteFakeHost) Resize(string, int, int) error               { return nil }
func (h *pasteFakeHost) Close(string) error                          { return nil }
func (h *pasteFakeHost) CloseAll()                                   {}
func (h *pasteFakeHost) Snapshot(id string) (Frame, bool) {
	f := h.frame
	f.ID = id
	return f, true
}
func (h *pasteFakeHost) Write(_ string, data []byte) error {
	h.written = append(h.written, append([]byte(nil), data...))
	return nil
}

func TestControllerBracketedPasteMode2004(t *testing.T) {
	fh := &pasteFakeHost{}
	ctrl := NewController(fh)

	multi := "line 1\nline 2\nline 3"

	// DEC mode 2004 enabled -> wrapped in \x1b[200~...\x1b[201~
	fh.frame.BracketedPaste = true
	act := ctrl.Paste("p1", multi)
	sent, ok := act.(ActionSent)
	if !ok {
		t.Fatalf("expected ActionSent, got %T", act)
	}
	wantWrapped := []byte("\x1b[200~" + multi + "\x1b[201~")
	if !bytes.Equal(sent.Data, wantWrapped) || len(fh.written) != 1 || !bytes.Equal(fh.written[0], wantWrapped) {
		t.Fatalf("wrapped paste mismatch:\n got: %q\nwant: %q", sent.Data, wantWrapped)
	}

	// Embedded \x1b[201~ inside pasted text must be stripped to prevent breakout.
	fh.written = nil
	ctrl.Paste("p1", "a\x1b[201~b")
	if len(fh.written) != 1 || string(fh.written[0]) != "\x1b[200~ab\x1b[201~" {
		t.Fatalf("embedded end-marker not sanitized: %q", fh.written)
	}

	// DEC mode 2004 disabled -> sent raw without markers.
	fh.frame.BracketedPaste = false
	fh.written = nil
	ctrl.Paste("p1", multi)
	if len(fh.written) != 1 || string(fh.written[0]) != multi {
		t.Fatalf("raw paste mismatch: got %q, want %q", fh.written, multi)
	}

	// Pending prefix is bypassed by paste (matches V1 live/input.go).
	fh.frame.BracketedPaste = true
	fh.written = nil
	_ = ctrl.Key("p1", "ctrl+a")
	if ctrl.Mode("p1") != ModePrefix {
		t.Fatalf("expected ModePrefix, got %v", ctrl.Mode("p1"))
	}
	ctrl.Paste("p1", "hello")
	if ctrl.Mode("p1") != ModePassthrough {
		t.Fatalf("paste should clear ModePrefix, got %v", ctrl.Mode("p1"))
	}
	if len(fh.written) != 1 || string(fh.written[0]) != "\x1b[200~hello\x1b[201~" {
		t.Fatalf("paste after prefix mismatch: %q", fh.written)
	}
}

func TestLocalHostSnapshotDetectsBracketedPasteMode2004(t *testing.T) {
	host := NewLocalHost()
	t.Cleanup(host.CloseAll)

	// Enable DEC 2004 (\x1b[?2004h) and wait briefly so Snapshot observes it.
	if err := host.Open("bp", `printf '\033[?2004hready\n'; sleep 2`, "", 40, 10); err != nil {
		t.Fatalf("Open: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f, ok := host.Snapshot("bp"); ok && strings.Contains(strings.Join(f.Plain, "\n"), "ready") {
			if !f.BracketedPaste {
				t.Fatalf("expected BracketedPaste=true after DECSET 2004, got false")
			}
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timed out waiting for DECSET 2004 snapshot")
}
