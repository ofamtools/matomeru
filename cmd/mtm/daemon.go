package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"ofam.dev/matomeru/agenthook"
	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/ochazuke/config"
	"ofam.dev/oden"
)

type daemonStatusOutput struct {
	Running   bool    `json:"running"`
	PID       int     `json:"pid,omitempty"`
	Socket    string  `json:"socket"`
	MCPURL    string  `json:"mcpURL,omitempty"`
	PaneCount int     `json:"paneCount"`
	Uptime    string  `json:"uptime,omitempty"`
	UptimeSec float64 `json:"uptimeSec,omitempty"`
}

func mtmAgentCommand() oden.Command {
	return oden.Command{
		Name:     "agent",
		Summary:  "agent harness hook forwarder and turn state updates",
		Category: "Daemon",
		Children: []oden.Command{
			{
				Name:    "hook",
				Summary: "read hook JSON from stdin and forward to the local daemon",
				Usage:   "[harness] [event] [--socket PATH] [--pane ID] [--timeout DUR] [--explain]",
				Handle: func(c *oden.Ctx) error {
					if code := agenthook.Run(c.Args, c.Stdin, c.Stdout, c.Stderr); code != 0 {
						return fmt.Errorf("agent hook exited with status %d", code)
					}
					return nil
				},
			},
			{
				Name:    "state",
				Summary: "send a manual turn state update (running|needs_you|idle|done)",
				Usage:   "<running|needs_you|idle|done> [summary] [--socket PATH] [--pane ID] [--explain]",
				Handle: func(c *oden.Ctx) error {
					if code := agenthook.Run(append([]string{"state"}, c.Args...), c.Stdin, c.Stdout, c.Stderr); code != 0 {
						return fmt.Errorf("agent state exited with status %d", code)
					}
					return nil
				},
			},
		},
	}
}

func mtmDaemonCommand(g globalOpts) oden.Command {
	socketParam := oden.Parameter{
		Name:        "socket",
		Type:        "string",
		Description: "Unix domain socket path (default: $MTM_SOCKET or ~/.local/share/matomeru/mtm.sock)",
	}
	serveParams := []oden.Parameter{
		socketParam,
		{Name: "mcp-addr", Type: "string", Default: "127.0.0.1:0", Description: "TCP listen address for the shared HTTP MCP server"},
		{Name: "db", Type: "string", Description: "Path to the SQLite database"},
	}
	socketOnly := []oden.Parameter{socketParam}

	return oden.Command{
		Name:     "daemon",
		Summary:  "manage the local self-hosted Matomeru daemon",
		Category: "Daemon",
		Children: []oden.Command{
			{
				Name:       "serve",
				Summary:    "run the local daemon (PTY host, HTTP MCP server, and scheduler)",
				Usage:      "[--socket PATH] [--mcp-addr ADDR] [--db PATH]",
				Parameters: serveParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(serveParams)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm daemon serve [--socket PATH] [--mcp-addr ADDR] [--db PATH]")
					}
					dbOpts := g
					if db := p.String("db"); db != "" {
						dbOpts.db = db
					}
					return runDaemonServe(c.Context, dbOpts, p.String("socket"), p.String("mcp-addr"))
				},
			},
			{
				Name:       "status",
				Summary:    "show local daemon status",
				Usage:      "[--socket PATH] [--json]",
				Parameters: socketOnly,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(socketOnly)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm daemon status [--socket PATH] [--json]")
					}
					sock := daemon.ResolveSocketPath(p.String("socket"))
					st, err := daemon.NewClient(sock).Status(c.Context)
					if err != nil {
						if p.Format() == "json" {
							return printJSON(c.Stdout, daemonStatusOutput{
								Running: false,
								Socket:  sock,
							})
						}
						_, werr := fmt.Fprintf(c.Stdout, "running:   false\nsocket:    %s\n", sock)
						return werr
					}
					out := daemonStatusOutput{
						Running:   true,
						PID:       st.PID,
						Socket:    st.Socket,
						MCPURL:    st.MCPURL,
						PaneCount: st.PaneCount,
						Uptime:    st.Uptime,
						UptimeSec: st.UptimeSec,
					}
					if p.Format() == "json" {
						return printJSON(c.Stdout, out)
					}
					mcpURL := out.MCPURL
					if mcpURL == "" {
						mcpURL = "(disabled)"
					}
					_, werr := fmt.Fprintf(c.Stdout, "running:   true\npid:       %d\nsocket:    %s\nmcpURL:    %s\npaneCount: %d\nuptime:    %s\n",
						out.PID, out.Socket, mcpURL, out.PaneCount, out.Uptime)
					return werr
				},
			},
			{
				Name:       "reset",
				Summary:    "drop all live panes and clear telemetry on the local daemon",
				Usage:      "[--socket PATH] [--json]",
				Parameters: socketOnly,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(socketOnly)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm daemon reset [--socket PATH] [--json]")
					}
					sock := daemon.ResolveSocketPath(p.String("socket"))
					if err := daemon.NewClient(sock).Reset(c.Context); err != nil {
						return err
					}
					if p.Format() == "json" {
						return printJSON(c.Stdout, map[string]any{
							"ok":     true,
							"reset":  true,
							"socket": sock,
						})
					}
					_, werr := fmt.Fprintf(c.Stdout, "daemon reset: dropped all live panes (%s)\n", sock)
					return werr
				},
			},
			{
				Name:       "stop",
				Summary:    "stop the local daemon cleanly",
				Usage:      "[--socket PATH] [--json]",
				Parameters: socketOnly,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(socketOnly)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm daemon stop [--socket PATH] [--json]")
					}
					sock := daemon.ResolveSocketPath(p.String("socket"))
					if err := daemon.NewClient(sock).Shutdown(c.Context); err != nil {
						return err
					}
					if p.Format() == "json" {
						return printJSON(c.Stdout, map[string]any{
							"ok":      true,
							"stopped": true,
							"socket":  sock,
						})
					}
					_, werr := fmt.Fprintf(c.Stdout, "daemon stopped (%s)\n", sock)
					return werr
				},
			},
			{
				Name:    "upgrade",
				Summary: "re-exec the daemon in place without killing live panes or subagents",
				Usage:   "[--binary PATH] [--socket PATH] [--json]",
				Parameters: []oden.Parameter{
					socketParam,
					{Name: "binary", Type: "string", Description: "Replacement mtm binary path (default: current mtm executable)"},
				},
				Handle: func(c *oden.Ctx) error {
					upgradeParams := []oden.Parameter{
						socketParam,
						{Name: "binary", Type: "string", Description: "Replacement mtm binary path (default: current mtm executable)"},
					}
					p, err := c.Parse(upgradeParams)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm daemon upgrade [--binary PATH] [--socket PATH] [--json]")
					}
					bin := strings.TrimSpace(p.String("binary"))
					if bin == "" {
						if exe, err := os.Executable(); err == nil {
							bin = exe
						}
					}
					sock := daemon.ResolveSocketPath(p.String("socket"))
					res, err := daemon.NewClient(sock).Upgrade(c.Context, bin)
					if err != nil {
						return err
					}
					if p.Format() == "json" {
						return printJSON(c.Stdout, map[string]any{
							"ok":        true,
							"upgraded":  true,
							"pid":       res.PID,
							"binary":    bin,
							"socket":    res.Socket,
							"mcpURL":    res.MCPURL,
							"paneCount": res.PaneCount,
						})
					}
					_, werr := fmt.Fprintf(c.Stdout, "daemon upgraded in place: pid=%d panes=%d mcpURL=%s (%s)\n",
						res.PID, res.PaneCount, res.MCPURL, res.Socket)
					return werr
				},
			},
		},
	}
}

func runDaemonServe(ctx context.Context, g globalOpts, socketFlag, mcpAddr string) error {
	store, err := openStoreWithSeed(g)
	if err != nil {
		return err
	}
	defer store.Close()

	sock := daemon.ResolveSocketPath(socketFlag)
	srv, err := daemon.New(daemon.Config{
		SocketPath: sock,
		MCPAddr:    mcpAddr,
		Store:      store,
	})
	if err != nil {
		return err
	}
	return srv.Serve(ctx)
}

// daemonEnabled reports whether live TUI launches should auto-connect to the
// local self-hosted daemon. It defaults to true unless MTM_NO_DAEMON=1 (or
// true) or settings.json explicitly sets "terminal.daemon": false.
func daemonEnabled(cfgDir string, getenv func(string) string) bool {
	if getenv != nil {
		v := strings.TrimSpace(getenv("MTM_NO_DAEMON"))
		if v == "1" || strings.EqualFold(v, "true") {
			return false
		}
	}
	if cfgDir == "" {
		return true
	}
	raw, err := os.ReadFile(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		return true
	}
	clean := config.StripJSONC(raw)
	if len(strings.TrimSpace(string(clean))) == 0 {
		return true
	}
	var m map[string]any
	if err := json.Unmarshal(clean, &m); err != nil {
		return true
	}
	if val, ok := m["terminal.daemon"]; ok {
		if b, isBool := val.(bool); isBool && !b {
			return false
		}
	}
	return true
}

func launchDaemonServe(socketPath, dbPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"daemon", "serve"}
	if dbPath != "" {
		args = append(args, "--db", dbPath)
	}
	args = append(args, "--socket", socketPath)
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func buildTUIOptions(ctx context.Context, dbPath, cfgDir string, stderr io.Writer, launcher func(socketPath string) error) (app.Options, []string) {
	opts := app.Options{ConfigDir: cfgDir, Live: true}
	if !daemonEnabled(cfgDir, os.Getenv) {
		opts.Host = ptyhost.NewLocalHost()
		return opts, nil
	}
	sock := daemon.ResolveSocketPath("")
	client := daemon.NewClient(sock)
	if launcher != nil {
		client.Launcher = launcher
	} else {
		client.Launcher = func(socketPath string) error {
			return launchDaemonServe(socketPath, dbPath)
		}
	}
	ensureCtx, cancel := context.WithTimeout(ctx, daemon.DefaultEnsureTimeout)
	defer cancel()
	if _, err := client.EnsureRunning(ensureCtx); err != nil {
		msg := fmt.Sprintf("daemon unavailable (%v); falling back to local PTY host", err)
		if stderr != nil {
			fmt.Fprintf(stderr, "mtm: warning: %s\n", msg)
		}
		opts.Host = ptyhost.NewLocalHost()
		return opts, []string{msg}
	}
	opts.Daemon = client
	opts.Host = daemon.NewHostAdapter(client)
	return opts, nil
}
