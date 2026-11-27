package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"ofam.dev/matomeru/ptyhost"
)

const (
	// DefaultClientTimeout bounds normal non-blocking RPC calls.
	DefaultClientTimeout = 5 * time.Second
	// DefaultEnsureTimeout bounds waiting for a newly launched daemon to answer.
	DefaultEnsureTimeout = 5 * time.Second
)

// Client speaks the newline-delimited JSON RPC protocol to a local daemon over
// its Unix domain socket.
type Client struct {
	socketPath string
	// Timeout bounds normal RPC calls (default DefaultClientTimeout).
	Timeout time.Duration
	// Launcher spawns the daemon when EnsureRunning finds no running daemon.
	// When nil, EnsureRunning spawns os.Executable() with "daemon serve --socket <path>".
	Launcher func(socketPath string) error
}

// NewClient constructs a Client targeting socketPath (or ResolveSocketPath("") when empty).
func NewClient(socketPath string) *Client {
	return &Client{socketPath: ResolveSocketPath(socketPath)}
}

// SocketPath returns the Unix socket path the client connects to.
func (c *Client) SocketPath() string { return c.socketPath }

func (c *Client) call(ctx context.Context, req Request, timeout time.Duration) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = c.Timeout
	}
	if timeout <= 0 {
		timeout = DefaultClientTimeout
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("daemon dial %s: %w", c.socketPath, err)
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stopWatch := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopWatch()

	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("daemon encode %s: %w", req.Op, err)
	}
	b = append(b, '\n')
	if _, err := conn.Write(b); err != nil {
		return Response{}, fmt.Errorf("daemon write %s: %w", req.Op, err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLineBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Response{}, fmt.Errorf("daemon read %s: %w", req.Op, err)
		}
		return Response{}, fmt.Errorf("daemon %s: connection closed without response", req.Op)
	}
	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("daemon decode %s: %w", req.Op, err)
	}
	if !resp.OK {
		msg := resp.Error
		if msg == "" {
			msg = "request failed"
		}
		return resp, errors.New(msg)
	}
	return resp, nil
}

// EnsureRunning dials the daemon; if unreachable, invokes Launcher (or spawns
// the current binary in detached "daemon serve" mode) and waits until Status succeeds.
func (c *Client) EnsureRunning(ctx context.Context) (*StatusResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if st, err := c.Status(ctx); err == nil {
		return st, nil
	}

	launcher := c.Launcher
	if launcher == nil {
		launcher = defaultSelfLauncher
	}
	if err := launcher(c.socketPath); err != nil {
		return nil, fmt.Errorf("daemon launch: %w", err)
	}

	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, DefaultEnsureTimeout)
		defer cancel()
	}

	backoff := 20 * time.Millisecond
	var lastErr error
	for {
		st, err := c.Status(waitCtx)
		if err == nil {
			return st, nil
		}
		lastErr = err
		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("daemon did not become ready at %s: %w (last error: %v)", c.socketPath, waitCtx.Err(), lastErr)
		case <-time.After(backoff):
			if backoff < 100*time.Millisecond {
				backoff += 20 * time.Millisecond
			}
		}
	}
}

func defaultSelfLauncher(socketPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon", "serve", "--socket", socketPath)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Status queries daemon PID, socket, MCP URL, pane count, and uptime.
func (c *Client) Status(ctx context.Context) (*StatusResult, error) {
	resp, err := c.call(ctx, Request{Op: OpStatus}, 0)
	if err != nil {
		return nil, err
	}
	return &StatusResult{
		PID:       resp.PID,
		Socket:    resp.Socket,
		MCPURL:    resp.MCPURL,
		PaneCount: resp.PaneCount,
		Uptime:    resp.Uptime,
		UptimeSec: resp.UptimeSec,
		Version:   resp.Version,
		Commit:    resp.Commit,
		Dirty:     resp.Dirty,
		BuiltAt:   resp.BuiltAt,
		Binary:    resp.Binary,
	}, nil
}

// Open starts a new PTY pane in the daemon.
func (c *Client) Open(ctx context.Context, req OpenRequest) error {
	_, err := c.call(ctx, Request{
		Op:   OpOpen,
		ID:   req.ID,
		Cmd:  req.Cmd,
		Cwd:  req.Cwd,
		Cols: req.Cols,
		Rows: req.Rows,
		Env:  req.Env,
	}, 0)
	return err
}

// Write sends input bytes to a pane.
func (c *Client) Write(ctx context.Context, id string, data []byte) error {
	_, err := c.call(ctx, Request{Op: OpWrite, ID: id, Data: data}, 0)
	return err
}

// Resize resizes a pane's terminal.
func (c *Client) Resize(ctx context.Context, id string, cols, rows int) error {
	_, err := c.call(ctx, Request{Op: OpResize, ID: id, Cols: cols, Rows: rows}, 0)
	return err
}

// Snapshot reads a pane's current Frame. Reports (zero, false, nil) if the pane does not exist.
func (c *Client) Snapshot(ctx context.Context, id string) (ptyhost.Frame, bool, error) {
	resp, err := c.call(ctx, Request{Op: OpSnapshot, ID: id}, 0)
	if err != nil {
		return ptyhost.Frame{}, false, err
	}
	if !resp.Found || resp.Frame == nil {
		return ptyhost.Frame{}, false, nil
	}
	return *resp.Frame, true, nil
}

// List returns metadata for all panes currently held by the daemon.
func (c *Client) List(ctx context.Context) ([]PaneInfo, error) {
	resp, err := c.call(ctx, Request{Op: OpList}, 0)
	if err != nil {
		return nil, err
	}
	return resp.Panes, nil
}

// Close kills and removes one pane from the daemon.
func (c *Client) Close(ctx context.Context, id string) error {
	_, err := c.call(ctx, Request{Op: OpClose, ID: id}, 0)
	return err
}

// Reset kills ALL panes and clears all pending approvals and hook telemetry.
func (c *Client) Reset(ctx context.Context) error {
	_, err := c.call(ctx, Request{Op: OpReset}, 0)
	return err
}

// Shutdown cleanly stops the daemon.
func (c *Client) Shutdown(ctx context.Context) error {
	_, err := c.call(ctx, Request{Op: OpShutdown}, 0)
	return err
}

// Upgrade re-execs the running daemon binary in place without killing any live
// PTY panes, child processes, or subagents, and waits until the upgraded daemon
// responds to Status again.
func (c *Client) Upgrade(ctx context.Context, binary string) (*UpgradeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := c.call(ctx, Request{Op: OpUpgrade, Binary: binary}, 0)
	if err != nil {
		return nil, err
	}
	res := &UpgradeResult{
		PID:       resp.PID,
		Binary:    binary,
		Socket:    resp.Socket,
		MCPURL:    resp.MCPURL,
		PaneCount: resp.PaneCount,
	}
	// Wait briefly for the newly exec'd daemon to start accepting connections.
	waitCtx, cancel := context.WithTimeout(ctx, DefaultEnsureTimeout)
	defer cancel()
	backoff := 20 * time.Millisecond
	for {
		if st, serr := c.Status(waitCtx); serr == nil {
			res.PID = st.PID
			res.Socket = st.Socket
			res.MCPURL = st.MCPURL
			res.PaneCount = st.PaneCount
			return res, nil
		}
		select {
		case <-waitCtx.Done():
			return res, nil
		case <-time.After(backoff):
			if backoff < 100*time.Millisecond {
				backoff += 20 * time.Millisecond
			}
		}
	}
}

// HookEvent sends a hook telemetry or blocking approval event to the daemon.
func (c *Client) HookEvent(ctx context.Context, ev HookEvent) (*HookEventResult, error) {
	timeout := c.Timeout
	if ev.Kind == HookKindApproval {
		if ev.TimeoutMs > 0 {
			timeout = time.Duration(ev.TimeoutMs)*time.Millisecond + 10*time.Second
		} else {
			timeout = DefaultApprovalTimeout + 10*time.Second
		}
	}
	resp, err := c.call(ctx, Request{
		Op:             OpHookEvent,
		PaneID:         ev.PaneID,
		Harness:        ev.Harness,
		Kind:           ev.Kind,
		State:          ev.State,
		Tool:           ev.Tool,
		Summary:        ev.Summary,
		Files:          ev.Files,
		Prompt:         ev.Prompt,
		Response:       ev.Response,
		FinalResponse:  ev.FinalResponse,
		SubagentID:     ev.SubagentID,
		SubagentAction: ev.SubagentAction,
		RequestID:      ev.RequestID,
		Payload:        ev.Payload,
		TimeoutMs:      ev.TimeoutMs,
	}, timeout)
	if err != nil {
		return nil, err
	}
	return &HookEventResult{
		OK:       resp.OK,
		Decision: resp.Decision,
		Reason:   resp.Reason,
	}, nil
}

// ResolveApproval unblocks a waiting PermissionRequest approval on the daemon.
func (c *Client) ResolveApproval(ctx context.Context, requestID, decision, reason string) error {
	_, err := c.call(ctx, Request{
		Op:        OpResolveApproval,
		RequestID: requestID,
		Decision:  decision,
		Reason:    reason,
	}, 0)
	return err
}

// Telemetry fetches current PaneTelemetry and PendingApprovals for all panes (or one paneID).
func (c *Client) Telemetry(ctx context.Context, paneID string) (*TelemetryResult, error) {
	resp, err := c.call(ctx, Request{Op: OpTelemetry, PaneID: paneID}, 0)
	if err != nil {
		return nil, err
	}
	tel := resp.Telemetry
	if tel == nil {
		tel = map[string]*PaneTelemetry{}
	}
	pending := resp.PendingApprovals
	if pending == nil {
		pending = []*PendingApproval{}
	}
	return &TelemetryResult{
		Telemetry:        tel,
		PendingApprovals: pending,
	}, nil
}

// RegisterHook adds or replaces an in-memory callback rule on the daemon.
func (c *Client) RegisterHook(ctx context.Context, rule HookRule) error {
	_, err := c.call(ctx, Request{Op: OpHookRegister, Rule: &rule}, 0)
	return err
}

// UnregisterHook removes in-memory callback rule id, reporting whether it existed.
func (c *Client) UnregisterHook(ctx context.Context, id string) (bool, error) {
	resp, err := c.call(ctx, Request{Op: OpHookUnregister, ID: id}, 0)
	return resp.Found, err
}

// HookRules lists the daemon's hooks.json and in-memory callback rules.
func (c *Client) HookRules(ctx context.Context) ([]HookRule, error) {
	resp, err := c.call(ctx, Request{Op: OpHookList}, 0)
	return resp.Rules, err
}

// HookLog returns recent callback deliveries, oldest first (limit <= 0 = all).
func (c *Client) HookLog(ctx context.Context, limit int) ([]HookDelivery, error) {
	resp, err := c.call(ctx, Request{Op: OpHookLog, Limit: limit}, 0)
	return resp.Deliveries, err
}

// WaitAgentDone blocks until paneID emits agent.done, or ctx ends. Unless
// next is set, a pane whose telemetry already reads done or idle returns at
// once with an event built from that telemetry (PrevState empty).
func (c *Client) WaitAgentDone(ctx context.Context, paneID string, next bool) (*AgentDoneEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("daemon dial %s: %w", c.socketPath, err)
	}
	defer conn.Close()
	stopWatch := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stopWatch()

	b, err := json.Marshal(Request{Op: OpWatchEvents, PaneID: paneID})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("daemon write %s: %w", OpWatchEvents, err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLineBytes)
	read := func() (Response, error) {
		if !scanner.Scan() {
			if ctx.Err() != nil {
				return Response{}, ctx.Err()
			}
			if err := scanner.Err(); err != nil {
				return Response{}, fmt.Errorf("daemon read %s: %w", OpWatchEvents, err)
			}
			return Response{}, fmt.Errorf("daemon %s: connection closed", OpWatchEvents)
		}
		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			return Response{}, fmt.Errorf("daemon decode %s: %w", OpWatchEvents, err)
		}
		if !resp.OK {
			return resp, errors.New(resp.Error)
		}
		return resp, nil
	}
	if _, err := read(); err != nil {
		return nil, err
	}
	if !next {
		if ev := c.currentDone(ctx, paneID); ev != nil {
			return ev, nil
		}
	}
	for {
		resp, err := read()
		if err != nil {
			return nil, err
		}
		if isDoneFor(resp.Event, paneID) {
			return resp.Event, nil
		}
	}
}

// isDoneFor reports whether ev is an agent.done event of paneID (any pane when empty).
func isDoneFor(ev *AgentDoneEvent, paneID string) bool {
	return ev != nil && ev.Event == EventAgentDone && (paneID == "" || ev.PaneID == paneID)
}

func (c *Client) currentDone(ctx context.Context, paneID string) *AgentDoneEvent {
	if paneID == "" {
		return nil
	}
	res, err := c.Telemetry(ctx, paneID)
	if err != nil {
		return nil
	}
	pt := res.Telemetry[paneID]
	if pt == nil || !isRestState(pt.State) {
		return nil
	}
	ev := &AgentDoneEvent{
		Event:          EventAgentDone,
		PaneID:         paneID,
		Harness:        pt.Harness,
		State:          pt.State,
		Summary:        pt.Summary,
		SessionID:      pt.SessionID,
		TranscriptPath: pt.TranscriptPath,
		At:             pt.LastEventAt,
	}
	if n := len(pt.RecentTurns); n > 0 {
		ev.Response = pt.RecentTurns[n-1].Response
	}
	return ev
}
