package agenthook

import (
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/internal/agenthook/atomicfile"
)

// The daemon joins a pane to the transcript a hook names but keeps the path
// in memory only and reports it nowhere. The hook, which sees the path in its
// payload, keeps it beside the daemon's socket (the daemon's own run
// directory, so it goes with the daemon), one small file per pane.
// mtm pane show and mtm agent status read it back with ReadTranscript.

// TranscriptStamp is the file that keeps window's transcript path in dir.
func TranscriptStamp(dir, window string) string {
	return filepath.Join(dir, statusLineStampName("transcript", window))
}

// keepTranscript records path as window's transcript. A write that fails
// changes nothing: the hook never fails for it.
func keepTranscript(dir, window, path string) {
	if dir == "" || window == "" || path == "" {
		return
	}
	_ = atomicfile.WritePrivate(TranscriptStamp(dir, window), []byte(path), false)
}

// ReadTranscript is the transcript path the last hook of window's pane named
// in dir (the directory of the daemon's socket), "" when none did.
func ReadTranscript(dir, window string) string {
	if dir == "" || window == "" {
		return ""
	}
	data, err := os.ReadFile(TranscriptStamp(dir, window))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
