package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/oden"
)

const defaultAgentWaitTimeout = 30 * time.Minute

var agentWaitParams = []oden.Parameter{
	{Name: "pane", Type: "string", Description: "Pane ID to wait on (or the first positional argument)"},
	{Name: "timeout", Type: "string", Default: "30m", Description: "Give up after this duration (e.g. 90s, 10m; 0 waits forever)"},
	{Name: "next", Type: "bool", Description: "Wait for the next agent.done even if the pane is already done or idle"},
	{Name: "socket", Type: "string", Description: "Unix domain socket path (default: $MTM_SOCKET or ~/.local/share/matomeru/mtm.sock)"},
}

// mtmAgentWaitCommand is `mtm agent wait`: block until a pane's agent finishes.
func mtmAgentWaitCommand() oden.Command {
	return oden.Command{
		Name:       "wait",
		Summary:    "block until a pane emits agent.done (running/needs_you -> done/idle)",
		Usage:      "--pane <ID> [--timeout 30m] [--next] [--json]",
		Parameters: agentWaitParams,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(agentWaitParams)
			if err != nil {
				return err
			}
			pane := strings.TrimSpace(p.String("pane"))
			if pos := p.Positional(); pane == "" && len(pos) == 1 {
				pane = pos[0]
			}
			if pane == "" {
				return errors.New("usage: mtm agent wait --pane <ID> [--timeout 30m] [--next] [--json]")
			}
			timeout, err := parseWaitTimeout(p.String("timeout"))
			if err != nil {
				return err
			}
			ev, err := waitAgentDone(c.Context, daemon.ResolveSocketPath(p.String("socket")), pane, timeout, p.Bool("next"))
			if err != nil {
				return err
			}
			return printAgentDone(c.Stdout, ev, p.Format() == "json")
		},
	}
}

func parseWaitTimeout(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultAgentWaitTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("--timeout %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("--timeout %q must not be negative", raw)
	}
	return d, nil
}

func waitAgentDone(ctx context.Context, sock, pane string, timeout time.Duration, next bool) (*daemon.AgentDoneEvent, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ev, err := daemon.NewClient(sock).WaitAgentDone(ctx, pane, next)
	if errors.Is(err, context.DeadlineExceeded) || (err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return nil, fmt.Errorf("agent wait: pane %s did not finish within %s", pane, timeout)
	}
	return ev, err
}

func printAgentDone(w io.Writer, ev *daemon.AgentDoneEvent, asJSON bool) error {
	if asJSON {
		return printJSON(w, ev)
	}
	from := ev.PrevState
	if from == "" {
		from = "already"
	}
	line := fmt.Sprintf("%s %s: %s -> %s", ev.Event, ev.PaneID, from, ev.State)
	if ev.Summary != "" {
		line += "  " + ev.Summary
	}
	_, err := fmt.Fprintln(w, line)
	return err
}
