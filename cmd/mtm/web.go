package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/mdweb"
	"ofam.dev/oden"
)

func mtmWebCommand() oden.Command {
	socketParam := oden.Parameter{
		Name:        "socket",
		Type:        "string",
		Description: "Unix domain socket path (default: $MTM_SOCKET or ~/.local/share/matomeru/mtm.sock)",
	}
	serveParams := []oden.Parameter{
		{Name: "host", Type: "string", Default: "127.0.0.1", Description: "Bind host address"},
		{Name: "port", Type: "int", Default: "4173", Description: "TCP port (0 picks an available port)"},
		{Name: "open", Type: "bool", Description: "Open the rendered page in the default browser"},
	}
	openParams := []oden.Parameter{
		socketParam,
		{Name: "no-open", Type: "bool", Description: "Print the URL without launching the browser"},
		{Name: "json", Type: "bool", Description: "Output JSON"},
	}
	urlParams := []oden.Parameter{
		socketParam,
		{Name: "json", Type: "bool", Description: "Output JSON"},
	}
	actionsParams := []oden.Parameter{
		socketParam,
		{Name: "file", Type: "string", Description: "Filter action events to a specific file path"},
	}

	return oden.Command{
		Name:       "web",
		Summary:    "serve or open interactive Markdown and Mermaid files in the browser",
		Usage:      "[PATH] [--port PORT] [--host ADDR] [--open] | open <FILE> | url <FILE> | actions [--file PATH]",
		Category:   "Daemon",
		Parameters: serveParams,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(serveParams)
			if err != nil {
				return err
			}
			pos := p.Positional()
			if len(pos) > 1 {
				return errors.New("usage: mtm web [PATH] [--port PORT] [--host ADDR] [--open]")
			}
			target := "."
			if len(pos) == 1 {
				target = pos[0]
			}
			return runWebServe(c.Context, target, p.String("host"), p.Int("port"), p.Bool("open"), c.Stdout)
		},
		Children: []oden.Command{
			{
				Name:       "serve",
				Summary:    "run a standalone interactive Markdown HTML server for a file or directory",
				Usage:      "[PATH] [--port PORT] [--host ADDR] [--open]",
				Parameters: serveParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(serveParams)
					if err != nil {
						return err
					}
					pos := p.Positional()
					if len(pos) > 1 {
						return errors.New("usage: mtm web serve [PATH] [--port PORT] [--host ADDR] [--open]")
					}
					target := "."
					if len(pos) == 1 {
						target = pos[0]
					}
					return runWebServe(c.Context, target, p.String("host"), p.Int("port"), p.Bool("open"), c.Stdout)
				},
			},
			{
				Name:       "open",
				Summary:    "open a local Markdown or Mermaid file via the running mtm daemon",
				Usage:      "<FILE> [--socket PATH] [--no-open] [--json]",
				Parameters: openParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(openParams)
					if err != nil {
						return err
					}
					pos := p.Positional()
					if len(pos) != 1 {
						return errors.New("usage: mtm web open <FILE> [--socket PATH] [--no-open] [--json]")
					}
					viewURL, absPath, err := resolveDaemonMarkdownURL(c.Context, p.String("socket"), pos[0])
					if err != nil {
						return err
					}
					opened := false
					if !p.Bool("no-open") {
						if err := openBrowserURL(viewURL); err != nil {
							return fmt.Errorf("open browser for %s: %w", viewURL, err)
						}
						opened = true
					}
					if p.Bool("json") || p.Format() == "json" {
						return printJSON(c.Stdout, map[string]any{
							"url":    viewURL,
							"file":   absPath,
							"opened": opened,
						})
					}
					_, err = fmt.Fprintln(c.Stdout, viewURL)
					return err
				},
			},
			{
				Name:       "url",
				Summary:    "print the running mtm daemon URL for a local Markdown or Mermaid file",
				Usage:      "<FILE> [--socket PATH] [--json]",
				Parameters: urlParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(urlParams)
					if err != nil {
						return err
					}
					pos := p.Positional()
					if len(pos) != 1 {
						return errors.New("usage: mtm web url <FILE> [--socket PATH] [--json]")
					}
					viewURL, absPath, err := resolveDaemonMarkdownURL(c.Context, p.String("socket"), pos[0])
					if err != nil {
						return err
					}
					if p.Bool("json") || p.Format() == "json" {
						return printJSON(c.Stdout, map[string]any{
							"url":  viewURL,
							"file": absPath,
						})
					}
					_, err = fmt.Fprintln(c.Stdout, viewURL)
					return err
				},
			},
			{
				Name:       "actions",
				Summary:    "stream live user action events from the running mtm daemon as JSON lines",
				Usage:      "[--file PATH] [--socket PATH]",
				Parameters: actionsParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(actionsParams)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm web actions [--file PATH] [--socket PATH]")
					}
					return streamDaemonWebActions(c.Context, p.String("socket"), p.String("file"), c.Stdout)
				},
			},
		},
	}
}

func runWebServe(ctx context.Context, target, host string, port int, openBrowser bool, stdout interface{ Write([]byte) (int, error) }) error {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", target, err)
	}
	st, err := os.Stat(absTarget)
	if err != nil {
		return fmt.Errorf("stat %q: %w", absTarget, err)
	}
	cfg := mdweb.Config{RootDir: absTarget}
	if !st.IsDir() {
		cfg.RootDir = filepath.Dir(absTarget)
		cfg.DefaultFile = filepath.Base(absTarget)
	}
	h := mdweb.NewHandler(cfg)
	defer h.Close()

	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	defer ln.Close()

	viewURL := "http://" + ln.Addr().String() + "/"
	_, _ = fmt.Fprintf(stdout, "mtm web listening on %s\nserving: %s\n", viewURL, absTarget)
	if openBrowser {
		_ = openBrowserURL(viewURL)
	}

	srv := &http.Server{Handler: h}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func resolveDaemonMarkdownURL(ctx context.Context, socketFlag, fileArg string) (string, string, error) {
	absPath, err := filepath.Abs(fileArg)
	if err != nil {
		return "", "", fmt.Errorf("resolve file %q: %w", fileArg, err)
	}
	if st, err := os.Stat(absPath); err != nil {
		return "", "", fmt.Errorf("stat %q: %w", absPath, err)
	} else if st.IsDir() {
		return "", "", fmt.Errorf("%q is a directory; pass a .md or .mmd file or run `mtm web %s`", absPath, fileArg)
	}
	sock := daemon.ResolveSocketPath(socketFlag)
	st, err := daemon.NewClient(sock).Status(ctx)
	if err != nil {
		return "", "", fmt.Errorf("mtm daemon not running on %s (start `mtm` or run `mtm web %s`): %w", sock, fileArg, err)
	}
	if st.MCPURL == "" {
		return "", "", errors.New("mtm daemon HTTP server is disabled")
	}
	base := strings.TrimRight(st.MCPURL, "/")
	return base + "/md/view?file=" + url.QueryEscape(absPath), absPath, nil
}

func streamDaemonWebActions(ctx context.Context, socketFlag, fileFlag string, stdout interface{ Write([]byte) (int, error) }) error {
	sock := daemon.ResolveSocketPath(socketFlag)
	st, err := daemon.NewClient(sock).Status(ctx)
	if err != nil {
		return fmt.Errorf("mtm daemon not running on %s: %w", sock, err)
	}
	if st.MCPURL == "" {
		return errors.New("mtm daemon HTTP server is disabled")
	}
	streamURL := strings.TrimRight(st.MCPURL, "/") + "/md/api/actions/stream"
	if fileFlag != "" {
		abs, err := filepath.Abs(fileFlag)
		if err != nil {
			return err
		}
		streamURL += "?file=" + url.QueryEscape(abs)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("action stream returned HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	eventType := ""
	for sc.Scan() {
		line := sc.Text()
		if ev, ok := strings.CutPrefix(line, "event:"); ok {
			eventType = strings.TrimSpace(ev)
			continue
		}
		if dataLine, ok := strings.CutPrefix(line, "data:"); ok {
			if eventType == "action" {
				_, _ = fmt.Fprintln(stdout, strings.TrimSpace(dataLine))
			}
			continue
		}
		if line == "" {
			eventType = ""
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func openBrowserURL(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "linux":
		cmd = exec.Command("xdg-open", targetURL)
	default:
		return fmt.Errorf("unsupported OS %q for browser open", runtime.GOOS)
	}
	return cmd.Start()
}
