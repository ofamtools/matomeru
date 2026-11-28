package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/setup"
	"ofam.dev/oden"
)

// daemonURLOutput is the --json shape of `mtm daemon url`.
type daemonURLOutput struct {
	URL    string `json:"url"`
	MCPURL string `json:"mcpURL"`
	WebURL string `json:"webURL"`
	PID    int    `json:"pid"`
	Socket string `json:"socket"`
}

func mtmDaemonURLCommand(params []oden.Parameter) oden.Command {
	return oden.Command{
		Name:       "url",
		Summary:    "print the live daemon's HTTP base URL (MCP at /mcp, web at /md/)",
		Usage:      "[--socket PATH] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New("usage: mtm daemon url [--socket PATH] [--json]")
			}
			return runDaemonURL(c.Context, c.Stdout, p.String("socket"), p.Format() == "json")
		},
	}
}

// liveDaemonURL returns the running daemon's HTTP base URL via sock.
func liveDaemonURL(ctx context.Context, sock string) (*daemonURLOutput, error) {
	st, err := daemon.NewClient(sock).Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("daemon not running on %s: %w", sock, err)
	}
	if st.MCPURL == "" {
		return nil, fmt.Errorf("daemon on %s has its HTTP server disabled", sock)
	}
	return &daemonURLOutput{
		URL:    st.MCPURL,
		MCPURL: st.MCPURL + "/mcp",
		WebURL: st.MCPURL + "/md/",
		PID:    st.PID,
		Socket: st.Socket,
	}, nil
}

func runDaemonURL(ctx context.Context, w io.Writer, socketFlag string, asJSON bool) error {
	out, err := liveDaemonURL(ctx, daemon.ResolveSocketPath(socketFlag))
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(w, out)
	}
	_, err = fmt.Fprintln(w, out.URL)
	return err
}

// resolveSetupMCPURL fills the HTTP MCP endpoint for `mtm setup --transport
// http` when --mcp-url is absent: the live daemon's <url>/mcp, else
// setup.DefaultMCPURL. Other transports keep opts.MCPURL as given.
func resolveSetupMCPURL(ctx context.Context, opts setup.Options) string {
	if opts.MCPURL != "" || !strings.EqualFold(strings.TrimSpace(opts.MCPTransport), setup.MCPTransportHTTP) {
		return opts.MCPURL
	}
	if out, err := liveDaemonURL(ctx, daemon.ResolveSocketPath("")); err == nil {
		return out.MCPURL
	}
	return setup.DefaultMCPURL
}
