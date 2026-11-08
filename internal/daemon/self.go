package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// SelfStart starts the daemon that is linked into the mtm binary itself
// (the hidden route Word, cmd/mtm/daemon_shell.go): mtm Word --server NAME
// start-server. It needs no tuios on PATH.
type SelfStart struct {
	Word    string // the hidden host word the route answers to
	Exe     string // the binary to run; "" is os.Executable
	PinDir  string // where pinned copies live; "" runs Exe directly
	Label   string // the build label in a pinned copy's name
	InUse   func(path string) bool
	Environ func() []string // nil is os.Environ
}

// Start is a StartFunc: it runs "<binary> Word --server NAME start-server"
// detached (its own session, no stdio). The binary is a pinned copy when
// PinDir is set, so overwriting the installed mtm cannot kill the daemon
// that start-server leaves running; when the copy cannot be made the
// installed binary runs instead (a start is never refused for it).
func (s SelfStart) Start(_ context.Context, name string) (<-chan error, error) {
	exe := s.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("mtm executable: %w", err)
		}
	}
	if s.PinDir != "" {
		if pinned, err := Pin(exe, s.PinDir, s.Label, s.InUse); err == nil {
			exe = pinned
		}
	}
	env := os.Environ
	if s.Environ != nil {
		env = s.Environ
	}
	what := fmt.Sprintf("%s %s --server %s start-server", filepath.Base(exe), s.Word, name)
	cmd := exec.Command(exe, s.Word, "--server", name, "start-server")
	cmd.Env = ScrubEnv(env())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil {
			err = fmt.Errorf("%s: %w", what, err)
		}
		exited <- err
	}()
	return exited, nil
}

// The variables of an enclosing multiplexer or agent session; a daemon
// started from inside one would hand them to every pane it runs.
var (
	scrubPrefixes = []string{"TUIOS_", "TMUX", "ZELLIJ"}
	scrubKeys     = []string{"STY", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "CODEX_SESSION_ID"}
)

// ScrubEnv is env without the enclosing multiplexer's and agent's variables,
// including TUIOS_SOCKET and TUIOS_SERVER (which would aim the daemon at the
// server mtm itself runs in).
func ScrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		for _, p := range scrubPrefixes {
			if strings.HasPrefix(key, p) {
				continue next
			}
		}
		for _, k := range scrubKeys {
			if key == k {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

// pinPrefix names the pinned copies: mtm-<label>-<key> under the pin dir.
const pinPrefix = "mtm-"

// Pin copies the binary exe into dir, named by label and by the binary's path,
// size and modification time, and returns the copy's path; an existing copy
// of the same build is reused. The copy is atomic (a temp file renamed into
// place), so two starters racing leave one whole file. A copy, not a hard
// link: an installer that overwrites the file in place would change a link's
// contents under the running daemon. Copies of other builds that no process
// runs (inUse; nil means ProcessUses) are removed afterwards.
func Pin(exe, dir, label string, inUse func(string) bool) (string, error) {
	info, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", exe, info.Size(), info.ModTime().UnixNano())))
	dest := filepath.Join(dir, pinPrefix+pinLabel(label)+"-"+hex.EncodeToString(sum[:5]))
	if st, err := os.Stat(dest); err == nil && st.Size() == info.Size() {
		return dest, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := copyExecutable(exe, dest); err != nil {
		return "", err
	}
	if inUse == nil {
		inUse = ProcessUses
	}
	prunePins(dir, dest, inUse)
	return dest, nil
}

func pinLabel(label string) string {
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "devel"
	}
	return b.String()
}

func copyExecutable(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".pin-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// prunePins removes the pinned copies in dir other than keep that no process
// runs.
func prunePins(dir, keep string, inUse func(string) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if path == keep || !strings.HasPrefix(e.Name(), pinPrefix) {
			continue
		}
		if !inUse(path) {
			_ = os.Remove(path)
		}
	}
}

// ProcessUses reports whether a running process was started from path. It
// answers true when it cannot tell, so nothing in use is ever removed.
func ProcessUses(path string) bool {
	if runtime.GOOS == "linux" {
		matches, _ := filepath.Glob("/proc/[0-9]*/exe")
		for _, m := range matches {
			if target, err := os.Readlink(m); err == nil && strings.TrimSuffix(target, " (deleted)") == path {
				return true
			}
		}
		return false
	}
	out, err := exec.Command("ps", "-axo", "comm=").Output()
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == path {
			return true
		}
	}
	return false
}
