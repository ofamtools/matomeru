package daemon

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/harness"
	"ofam.dev/matomeru/mcpui"
)

// ResolveHarnessLaunch resolves a harness ID (e.g. claude, codex, agy, omp,
// gemini, opencode, pi, zsh) against harnesses.json and built-in defaults into
// a shell command string and extra environment variables.
func ResolveHarnessLaunch(configDir, harnessID, mcpURL, paneID, customCmd string) (string, map[string]string, error) {
	cmd, env, _, err := ResolveHarnessLaunchWithPrompt(configDir, harnessID, mcpURL, paneID, customCmd, "")
	return cmd, env, err
}

// ResolveHarnessLaunchWithPrompt resolves harnessID with an optional initial
// prompt. When the harness declares a CLI prompt mode ("positional" or "flag")
// and customCmd is empty, the shell-escaped prompt is embedded directly in the
// returned launch command and deliveredInCmd is true.
func ResolveHarnessLaunchWithPrompt(configDir, harnessID, mcpURL, paneID, customCmd, prompt string) (string, map[string]string, bool, error) {
	if strings.TrimSpace(customCmd) != "" {
		return strings.TrimSpace(customCmd), nil, false, nil
	}
	id := strings.ToLower(strings.TrimSpace(harnessID))
	if id == "" || id == "zsh" || id == "bash" || id == "sh" || id == "shell" {
		if id == "bash" || id == "sh" || id == "zsh" {
			return id, nil, false, nil
		}
		if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" {
			return sh, nil, false, nil
		}
		return "zsh", nil, false, nil
	}
	var cfg harness.FileConfig
	if configDir != "" {
		if loaded, err := harness.LoadFile(filepath.Join(configDir, "harnesses.json")); err == nil {
			cfg = loaded
		}
	}
	if entry, ok := harness.ResolveEntry(id, cfg); ok {
		argv, env, delivered := harness.BuildLaunchCommandWithPrompt(entry, mcpURL, paneID, prompt)
		if len(argv) > 0 {
			return harness.ShellJoin(argv), env, delivered, nil
		}
	}
	return id, nil, false, nil
}

// DefaultConfigDir returns the Matomeru config directory (~/.config/matomeru or $MTM_CONFIG_DIR).
func DefaultConfigDir() string {
	if d := strings.TrimSpace(os.Getenv("MTM_CONFIG_DIR")); d != "" {
		return d
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "matomeru")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "matomeru")
}

// serverPaneRuntime implements mcpui.PaneRuntime in-process against a running
// daemon Server (zero socket overhead for HTTP MCP callers).
type serverPaneRuntime struct {
	s *Server
}

// NewServerPaneRuntime returns an in-process mcpui.PaneRuntime backed by s.
func NewServerPaneRuntime(s *Server) mcpui.PaneRuntime {
	return &serverPaneRuntime{s: s}
}

func (r *serverPaneRuntime) Open(_ context.Context, req mcpui.PaneOpenRequest) error {
	resp, _ := r.s.handleRequest(Request{
		Op:   OpOpen,
		ID:   req.ID,
		Cmd:  req.Cmd,
		Cwd:  req.Cwd,
		Cols: req.Cols,
		Rows: req.Rows,
		Env:  req.Env,
	})
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func (r *serverPaneRuntime) Write(_ context.Context, paneID string, data []byte) error {
	resp, _ := r.s.handleRequest(Request{Op: OpWrite, ID: paneID, Data: data})
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func (r *serverPaneRuntime) Snapshot(_ context.Context, paneID string) (mcpui.PaneSnapshot, bool, error) {
	resp, _ := r.s.handleRequest(Request{Op: OpSnapshot, ID: paneID})
	if !resp.OK {
		return mcpui.PaneSnapshot{}, false, errors.New(resp.Error)
	}
	if !resp.Found || resp.Frame == nil {
		return mcpui.PaneSnapshot{}, false, nil
	}
	return mcpui.PaneSnapshot{
		Plain:    resp.Frame.Plain,
		Cols:     resp.Frame.Cols,
		Rows:     resp.Frame.Rows,
		Exited:   resp.Frame.Exited,
		ExitCode: resp.Frame.ExitCode,
	}, true, nil
}

func (r *serverPaneRuntime) Status(_ context.Context, paneID string) (mcpui.PaneLiveStatus, error) {
	st := mcpui.PaneLiveStatus{}
	if base := r.s.MCPURL(); base != "" {
		st.MCPURL = base + "/mcp/" + url.PathEscape(paneID)
	}
	for _, info := range r.s.listPanes() {
		if info.ID == paneID {
			st.Live = true
			st.Exited = info.Exited
			st.ExitCode = info.ExitCode
			st.Cols = info.Cols
			st.Rows = info.Rows
			st.Cmd = info.Cmd
			st.Cwd = info.Cwd
			break
		}
	}
	tels, approvals := r.s.telemetry.snapshot(paneID)
	if pt := tels[paneID]; pt != nil {
		st.HookState = pt.State
	}
	for _, ap := range approvals {
		if ap.PaneID == paneID {
			st.PendingCount++
		}
	}
	return st, nil
}

func (r *serverPaneRuntime) Close(_ context.Context, paneID string) error {
	resp, _ := r.s.handleRequest(Request{Op: OpClose, ID: paneID})
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

// clientPaneRuntime implements mcpui.PaneRuntime over the local daemon Unix
// socket, auto-starting the daemon if needed (used by CLI & stdio MCP).
type clientPaneRuntime struct {
	socketPath string
	dbPath     string
}

// NewClientPaneRuntime returns a daemon-client backed mcpui.PaneRuntime.
func NewClientPaneRuntime(socketPath, dbPath string) mcpui.PaneRuntime {
	return &clientPaneRuntime{socketPath: socketPath, dbPath: dbPath}
}

func (r *clientPaneRuntime) client(ctx context.Context) (*Client, error) {
	c := NewClient(r.socketPath)
	if _, err := c.EnsureRunning(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (r *clientPaneRuntime) Open(ctx context.Context, req mcpui.PaneOpenRequest) error {
	c, err := r.client(ctx)
	if err != nil {
		return err
	}
	return c.Open(ctx, OpenRequest{
		ID:   req.ID,
		Cmd:  req.Cmd,
		Cwd:  req.Cwd,
		Cols: req.Cols,
		Rows: req.Rows,
		Env:  req.Env,
	})
}

func (r *clientPaneRuntime) Write(ctx context.Context, paneID string, data []byte) error {
	c, err := r.client(ctx)
	if err != nil {
		return err
	}
	return c.Write(ctx, paneID, data)
}

func (r *clientPaneRuntime) Snapshot(ctx context.Context, paneID string) (mcpui.PaneSnapshot, bool, error) {
	c, err := r.client(ctx)
	if err != nil {
		return mcpui.PaneSnapshot{}, false, err
	}
	frame, found, err := c.Snapshot(ctx, paneID)
	if err != nil || !found {
		return mcpui.PaneSnapshot{}, found, err
	}
	return mcpui.PaneSnapshot{
		Plain:    frame.Plain,
		Cols:     frame.Cols,
		Rows:     frame.Rows,
		Exited:   frame.Exited,
		ExitCode: frame.ExitCode,
	}, true, nil
}

func (r *clientPaneRuntime) Status(ctx context.Context, paneID string) (mcpui.PaneLiveStatus, error) {
	c, err := r.client(ctx)
	if err != nil {
		return mcpui.PaneLiveStatus{}, err
	}
	st := mcpui.PaneLiveStatus{}
	if ds, err := c.Status(ctx); err == nil && ds.MCPURL != "" {
		st.MCPURL = ds.MCPURL + "/mcp/" + url.PathEscape(paneID)
	}
	if panes, err := c.List(ctx); err == nil {
		for _, info := range panes {
			if info.ID == paneID {
				st.Live = true
				st.Exited = info.Exited
				st.ExitCode = info.ExitCode
				st.Cols = info.Cols
				st.Rows = info.Rows
				st.Cmd = info.Cmd
				st.Cwd = info.Cwd
				break
			}
		}
	}
	if snap, err := c.Telemetry(ctx, paneID); err == nil && snap != nil {
		if pt := snap.Telemetry[paneID]; pt != nil {
			st.HookState = pt.State
		}
		for _, ap := range snap.PendingApprovals {
			if ap.PaneID == paneID {
				st.PendingCount++
			}
		}
	}
	return st, nil
}

func (r *clientPaneRuntime) Close(ctx context.Context, paneID string) error {
	c, err := r.client(ctx)
	if err != nil {
		return err
	}
	return c.Close(ctx, paneID)
}
