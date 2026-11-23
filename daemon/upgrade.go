package daemon

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"ofam.dev/matomeru/ptyhost"
)

// EnvDaemonHandoff points a freshly exec'd daemon binary at its JSON state
// handoff file so it inherits live PTY master FDs, listener FDs, and telemetry
// without killing any running panes or subagents.
const EnvDaemonHandoff = "MTM_DAEMON_HANDOFF"

type daemonHandoff struct {
	UnixFD    int                       `json:"unixFD"`
	MCPFD     int                       `json:"mcpFD"`
	MCPURL    string                    `json:"mcpURL,omitempty"`
	StartedAt time.Time                 `json:"startedAt,omitempty"`
	Panes     []paneMeta                `json:"panes,omitempty"`
	Sessions  []ptyhost.HandoffSession  `json:"sessions,omitempty"`
	Telemetry map[string]*PaneTelemetry `json:"telemetry,omitempty"`
}

// UpgradeResult describes a successful zero-downtime daemon exec upgrade.
type UpgradeResult struct {
	PID       int    `json:"pid"`
	Binary    string `json:"binary"`
	Socket    string `json:"socket"`
	MCPURL    string `json:"mcpURL,omitempty"`
	PaneCount int    `json:"paneCount"`
}

func resolveUpgradeBinary(requested string) (string, error) {
	bin := strings.TrimSpace(requested)
	if bin == "" {
		if len(os.Args) > 0 && os.Args[0] != "" {
			if p, err := exec.LookPath(os.Args[0]); err == nil {
				bin = p
			}
		}
		if bin == "" {
			exe, err := os.Executable()
			if err != nil {
				return "", fmt.Errorf("daemon upgrade: resolve binary: %w", err)
			}
			bin = exe
		}
	}
	abs, err := filepath.Abs(bin)
	if err == nil {
		bin = abs
	}
	info, err := os.Stat(bin)
	if err != nil {
		return "", fmt.Errorf("daemon upgrade: stat %s: %w", bin, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("daemon upgrade: %s is not executable", bin)
	}
	return bin, nil
}

// ExportHandoff snapshots live panes, telemetry, and dup'd listener FDs into a
// mode-0600 handoff file and clears FD_CLOEXEC on every inherited FD. The
// caller must keep returned files referenced until syscall.Exec completes (or
// close them if handing off in-process).
func (s *Server) ExportHandoff() (handoffPath string, keepFiles []*os.File, paneCount int, err error) {
	s.mu.Lock()
	unixLn := s.unixLn
	httpLn := s.httpLn
	mcpURL := s.mcpURL
	startedAt := s.startedAt
	metas := make([]paneMeta, 0, len(s.panes))
	for _, m := range s.panes {
		metas = append(metas, *m)
	}
	s.mu.Unlock()

	ul, ok := unixLn.(*net.UnixListener)
	if !ok || ul == nil {
		return "", nil, 0, fmt.Errorf("daemon upgrade: unix listener unavailable")
	}
	ul.SetUnlinkOnClose(false)
	unixFile, err := ul.File()
	if err != nil {
		return "", nil, 0, fmt.Errorf("daemon upgrade: dup unix listener: %w", err)
	}
	if err := ptyhost.ClearCloseOnExec(unixFile.Fd()); err != nil {
		_ = unixFile.Close()
		return "", nil, 0, fmt.Errorf("daemon upgrade: clear cloexec unix listener: %w", err)
	}
	keepFiles = append(keepFiles, unixFile)

	mcpFD := -1
	if tl, ok := httpLn.(*net.TCPListener); ok && tl != nil {
		httpFile, herr := tl.File()
		if herr != nil {
			_ = unixFile.Close()
			return "", nil, 0, fmt.Errorf("daemon upgrade: dup mcp listener: %w", herr)
		}
		if err := ptyhost.ClearCloseOnExec(httpFile.Fd()); err != nil {
			_ = httpFile.Close()
			_ = unixFile.Close()
			return "", nil, 0, fmt.Errorf("daemon upgrade: clear cloexec mcp listener: %w", err)
		}
		mcpFD = int(httpFile.Fd())
		keepFiles = append(keepFiles, httpFile)
	}

	sessions, err := s.host.ExportForUpgrade()
	if err != nil {
		for _, f := range keepFiles {
			_ = f.Close()
		}
		return "", nil, 0, err
	}

	tel, _ := s.telemetry.snapshot("")
	payload := daemonHandoff{
		UnixFD:    int(unixFile.Fd()),
		MCPFD:     mcpFD,
		MCPURL:    mcpURL,
		StartedAt: startedAt,
		Panes:     metas,
		Sessions:  sessions,
		Telemetry: tel,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		for _, f := range keepFiles {
			_ = f.Close()
		}
		return "", nil, 0, fmt.Errorf("daemon upgrade: marshal handoff: %w", err)
	}

	handoffPath = filepath.Join(filepath.Dir(s.socketPath), fmt.Sprintf("mtm-handoff-%d-%d.json", os.Getpid(), time.Now().UnixNano()))
	if err := os.WriteFile(handoffPath, raw, 0o600); err != nil {
		for _, f := range keepFiles {
			_ = f.Close()
		}
		return "", nil, 0, fmt.Errorf("daemon upgrade: write handoff: %w", err)
	}
	return handoffPath, keepFiles, len(sessions), nil
}

func (s *Server) loadHandoff(path string) (*daemonHandoff, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("daemon: read handoff %s: %w", path, err)
	}
	_ = os.Remove(path)
	var h daemonHandoff
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("daemon: parse handoff %s: %w", path, err)
	}
	return &h, nil
}

func adoptFileListener(fd int, name string) (net.Listener, error) {
	f := os.NewFile(uintptr(fd), name)
	if f == nil {
		return nil, fmt.Errorf("daemon: invalid inherited fd %d (%s)", fd, name)
	}
	ln, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("daemon: adopt %s fd %d: %w", name, fd, err)
	}
	return ln, nil
}

func (s *Server) execUpgrade(binary, handoffPath string, keepFiles []*os.File) {
	// Mark upgrading BEFORE cancelling s.ctx so Serve() never runs Shutdown()
	// or s.host.CloseAll() while we prepare syscall.Exec.
	s.upgrading.Store(true)
	if s.stopSched != nil {
		s.stopSched()
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	unixLn := s.unixLn
	httpSrv := s.httpSrv
	s.mu.Unlock()
	if unixLn != nil {
		if ul, ok := unixLn.(*net.UnixListener); ok {
			ul.SetUnlinkOnClose(false)
		}
		_ = unixLn.Close()
	}
	if httpSrv != nil {
		_ = httpSrv.Close()
	}

	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, EnvDaemonHandoff+"=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, EnvDaemonHandoff+"="+handoffPath)

	argv := append([]string{binary}, os.Args[1:]...)
	execFn := s.cfg.ExecFunc
	if execFn == nil {
		execFn = syscall.Exec
	}
	_ = execFn(binary, argv, env)
	runtime.KeepAlive(keepFiles)
}

func (ts *telemetryStore) restore(panes map[string]*PaneTelemetry) {
	if len(panes) == 0 {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for id, pt := range panes {
		if pt == nil {
			continue
		}
		cp := *pt
		cp.Payload = cloneMap(pt.Payload)
		if len(pt.ActiveSubagents) > 0 {
			cp.ActiveSubagents = make(map[string]SubagentInfo, len(pt.ActiveSubagents))
			maps.Copy(cp.ActiveSubagents, pt.ActiveSubagents)
		}
		ts.panes[id] = &cp
	}
}
