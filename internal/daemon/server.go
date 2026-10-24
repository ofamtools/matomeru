package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ensureWait is how long Ensure waits for a server it started to answer.
const ensureWait = 5 * time.Second

// StartFunc starts the TUIOS server named name and returns at once; the
// channel receives the start command's exit (nil when it detached cleanly).
type StartFunc func(ctx context.Context, name string) (<-chan error, error)

// StartServer runs tuios --server NAME start-server detached from this
// process (its own session, no stdio), with TUIOS_SOCKET and TUIOS_SERVER
// removed from its environment so it never reaches the daemon mtm runs in.
func StartServer(_ context.Context, name string) (<-chan error, error) {
	path, err := exec.LookPath("tuios")
	if err != nil {
		return nil, fmt.Errorf("tuios is not on PATH: %w", err)
	}
	cmd := exec.Command(path, "--server", name, "start-server")
	cmd.Env = withoutDaemonEnv(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("tuios --server %s start-server: %w", name, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

func withoutDaemonEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "TUIOS_SOCKET=") || strings.HasPrefix(kv, "TUIOS_SERVER=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Label names the daemon in messages: the server name, else the socket.
func (c *Client) Label() string {
	if c.Server != "" {
		return c.Server
	}
	return c.Socket
}

// Ensure makes the daemon answer. It probes the socket (a connect); when
// nothing listens (no socket file, or connection refused) and the client
// names a Server, it runs Start once and polls the socket every 50ms until
// it answers, the start command fails, or Wait passes. A client with no
// Server only probes. Calls are serialized, so a second Ensure finds the
// server the first started. Every failure reads "daemon NAME not running: ...".
func (c *Client) Ensure(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.probe(ctx)
	if err == nil {
		return nil
	}
	if c.Server == "" || !absent(err) {
		return c.notRunning(err)
	}
	start := c.Start
	if start == nil {
		start = StartServer
	}
	exited, err := start(ctx, c.Server)
	if err != nil {
		return c.notRunning(err)
	}
	wait := c.Wait
	if wait == 0 {
		wait = ensureWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if c.probe(ctx) == nil {
			return nil
		}
		select {
		case err := <-exited:
			if err != nil {
				return c.notRunning(fmt.Errorf("tuios --server %s start-server: %w", c.Server, err))
			}
			exited = nil // it detached; keep polling
		case <-tick.C:
		case <-deadline.C:
			return c.notRunning(fmt.Errorf("started, but %s did not answer within %s", c.Socket, wait))
		case <-ctx.Done():
			return c.notRunning(ctx.Err())
		}
	}
}

// probe connects to the socket and hangs up.
func (c *Client) probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	return conn.Close()
}

// absent is a dial error that means no daemon listens: the socket file is
// missing or stale.
func absent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

func (c *Client) notRunning(err error) error {
	return fmt.Errorf("daemon %s not running: %w", c.Label(), err)
}
