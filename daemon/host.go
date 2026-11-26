package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ofam.dev/matomeru/ptyhost"
)

// HostAdapter implements ptyhost.Host, ptyhost.EnvOpener, and the live
// Changes() <-chan string feed over a local daemon *Client so PTY panes
// survive closing Matomeru while still rendering keystrokes with sub-millisecond
// push latency.
//
// CloseAll stops local background streams, closes persistent connections, and
// drops local snapshot caches; panes remain alive in the daemon until
// explicitly closed via Close or Reset.
type HostAdapter struct {
	client *Client
	// MinInterval optionally caches Snapshot responses when not subscribed.
	MinInterval time.Duration

	mu    sync.Mutex
	cache map[string]cachedFrame

	writeMu   sync.Mutex
	writeConn net.Conn
	writeScan *bufio.Scanner

	subMu        sync.Mutex
	changes      chan string
	subCancel    context.CancelFunc
	subWg        sync.WaitGroup
	subConnected atomic.Bool
}

type cachedFrame struct {
	frame ptyhost.Frame
	at    time.Time
}

var (
	_ ptyhost.Host      = (*HostAdapter)(nil)
	_ ptyhost.EnvOpener = (*HostAdapter)(nil)
)

// NewHostAdapter returns a ptyhost.Host + ptyhost.EnvOpener adapter backed by client.
func NewHostAdapter(client *Client) *HostAdapter {
	return &HostAdapter{
		client: client,
		cache:  map[string]cachedFrame{},
	}
}

// Client returns the underlying daemon *Client.
func (h *HostAdapter) Client() *Client { return h.client }

// Changes returns a channel that receives a pane ID whenever the daemon pushes
// updated terminal output, size, or exit state for that pane. Starting Changes
// opens a persistent OpSubscribe stream that also keeps the local frame cache
// warm so Snapshot reads directly from memory without blocking the UI thread.
func (h *HostAdapter) Changes() <-chan string {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	if h.changes == nil {
		h.changes = make(chan string, 64)
	}
	if h.subCancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.subCancel = cancel
		h.subWg.Add(1)
		go func() {
			defer h.subWg.Done()
			h.subscribeLoop(ctx)
		}()
	}
	return h.changes
}

func (h *HostAdapter) subscribeLoop(ctx context.Context) {
	backoff := 50 * time.Millisecond
	for {
		unsupported, err := h.runSubscribeOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			backoff = 50 * time.Millisecond
			continue
		}
		wait := backoff
		if unsupported {
			// Older daemon without OpSubscribe: retry slowly instead of spinning.
			wait = 2 * time.Second
		} else if backoff < 500*time.Millisecond {
			backoff *= 2
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (h *HostAdapter) runSubscribeOnce(ctx context.Context) (unsupported bool, err error) {
	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, DefaultClientTimeout)
	conn, err := d.DialContext(dialCtx, "unix", h.client.socketPath)
	cancel()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	stopWatch := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopWatch()

	b, err := json.Marshal(Request{Op: OpSubscribe})
	if err != nil {
		return false, err
	}
	b = append(b, '\n')
	if _, err := conn.Write(b); err != nil {
		return false, err
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLineBytes)

	// Read initial handshake ack.
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, errors.New("daemon subscribe closed before ack")
	}
	var ack Response
	if err := json.Unmarshal(scanner.Bytes(), &ack); err != nil {
		return false, err
	}
	if !ack.OK {
		return strings.Contains(ack.Error, "unknown op"), errors.New(ack.Error)
	}

	h.subConnected.Store(true)
	defer h.subConnected.Store(false)

	for scanner.Scan() {
		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			return false, err
		}
		if !resp.OK {
			continue
		}
		if !resp.Found {
			if resp.PaneID != "" {
				h.expire(resp.PaneID)
				h.emitChange(resp.PaneID)
			}
			continue
		}
		if resp.Frame == nil {
			continue
		}
		f := *resp.Frame
		id := f.ID
		if id == "" {
			id = resp.PaneID
			f.ID = id
		}
		h.mu.Lock()
		if resp.ScrollbackUnchanged {
			if prev, ok := h.cache[id]; ok {
				f.Scrollback = prev.frame.Scrollback
			}
		}
		h.cache[id] = cachedFrame{frame: f, at: time.Now()}
		h.mu.Unlock()
		h.emitChange(id)
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, errors.New("daemon subscribe stream ended")
}

func (h *HostAdapter) emitChange(id string) {
	if id == "" {
		return
	}
	h.subMu.Lock()
	ch := h.changes
	h.subMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- id:
	default:
	}
}

// Open implements ptyhost.Host.
func (h *HostAdapter) Open(id, cmd, cwd string, cols, rows int) error {
	return h.OpenWithEnv(id, cmd, cwd, cols, rows, nil)
}

// OpenWithEnv implements ptyhost.EnvOpener.
func (h *HostAdapter) OpenWithEnv(id, cmd, cwd string, cols, rows int, env map[string]string) error {
	err := h.client.Open(context.Background(), OpenRequest{
		ID:   id,
		Cmd:  cmd,
		Cwd:  cwd,
		Cols: cols,
		Rows: rows,
		Env:  env,
	})
	if err == nil {
		h.expire(id)
	}
	return err
}

// Write implements ptyhost.Host over a persistent Unix socket connection so
// interactive typing does not incur a socket dial per keystroke.
func (h *HostAdapter) Write(id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	err := h.writePersistent(id, data)
	if !h.subConnected.Load() {
		h.expire(id)
	}
	return err
}

func (h *HostAdapter) writePersistent(id string, data []byte) error {
	reqBytes, err := json.Marshal(Request{Op: OpWrite, ID: id, Data: data})
	if err != nil {
		return fmt.Errorf("daemon encode write: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	timeout := h.client.Timeout
	if timeout <= 0 {
		timeout = DefaultClientTimeout
	}

	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	for attempt := range 2 {
		reused := h.writeConn != nil
		if h.writeConn == nil {
			conn, err := net.DialTimeout("unix", h.client.socketPath, timeout)
			if err != nil {
				return fmt.Errorf("daemon dial %s: %w", h.client.socketPath, err)
			}
			sc := bufio.NewScanner(conn)
			sc.Buffer(make([]byte, 4096), maxRPCLineBytes)
			h.writeConn = conn
			h.writeScan = sc
		}
		_ = h.writeConn.SetDeadline(time.Now().Add(timeout))
		if _, err := h.writeConn.Write(reqBytes); err != nil {
			h.closeWriteConnLocked()
			if reused && attempt == 0 {
				continue
			}
			return fmt.Errorf("daemon write: %w", err)
		}
		if !h.writeScan.Scan() {
			serr := h.writeScan.Err()
			h.closeWriteConnLocked()
			if reused && attempt == 0 {
				continue
			}
			if serr != nil {
				return fmt.Errorf("daemon read write: %w", serr)
			}
			return errors.New("daemon write: connection closed without response")
		}
		_ = h.writeConn.SetDeadline(time.Time{})
		var resp Response
		if err := json.Unmarshal(h.writeScan.Bytes(), &resp); err != nil {
			h.closeWriteConnLocked()
			return fmt.Errorf("daemon decode write: %w", err)
		}
		if !resp.OK {
			msg := resp.Error
			if msg == "" {
				msg = "write failed"
			}
			return errors.New(msg)
		}
		return nil
	}
	return errors.New("daemon write: retry exhausted")
}

func (h *HostAdapter) closeWriteConnLocked() {
	if h.writeConn != nil {
		_ = h.writeConn.Close()
		h.writeConn = nil
		h.writeScan = nil
	}
}

// Resize implements ptyhost.Host.
func (h *HostAdapter) Resize(id string, cols, rows int) error {
	err := h.client.Resize(context.Background(), id, cols, rows)
	if err == nil {
		h.expire(id)
	}
	return err
}

// Snapshot implements ptyhost.Host. When subscribed via Changes(), Snapshot
// serves the latest daemon-pushed Frame directly from memory without blocking
// on socket I/O.
func (h *HostAdapter) Snapshot(id string) (ptyhost.Frame, bool) {
	subscribed := h.subConnected.Load()
	if subscribed {
		h.mu.Lock()
		if c, ok := h.cache[id]; ok {
			h.mu.Unlock()
			return c.frame, true
		}
		h.mu.Unlock()
	} else if h.MinInterval > 0 {
		h.mu.Lock()
		if c, ok := h.cache[id]; ok && time.Since(c.at) < h.MinInterval {
			h.mu.Unlock()
			return c.frame, true
		}
		h.mu.Unlock()
	}

	f, ok, err := h.client.Snapshot(context.Background(), id)
	if err != nil || !ok {
		h.expire(id)
		return ptyhost.Frame{}, false
	}
	if subscribed || h.MinInterval > 0 {
		h.mu.Lock()
		h.cache[id] = cachedFrame{frame: f, at: time.Now()}
		h.mu.Unlock()
	}
	return f, true
}

// ExitStatus reports whether the pane's process has exited, and its exit code,
// from the frame the daemon last pushed, without copying the screen. Only a
// pane with no cached frame costs a Snapshot. It reports ok false for a pane
// the daemon does not hold.
func (h *HostAdapter) ExitStatus(id string) (exited bool, code int, ok bool) {
	h.mu.Lock()
	c, cached := h.cache[id]
	h.mu.Unlock()
	if cached && (h.subConnected.Load() || (h.MinInterval > 0 && time.Since(c.at) < h.MinInterval)) {
		return c.frame.Exited, c.frame.ExitCode, true
	}
	f, ok := h.Snapshot(id)
	return f.Exited, f.ExitCode, ok
}

// Close implements ptyhost.Host by closing one pane in the daemon.
func (h *HostAdapter) Close(id string) error {
	h.expire(id)
	return h.client.Close(context.Background(), id)
}

// CloseAll implements ptyhost.Host. Because panes belong to the daemon and are
// meant to outlive Matomeru, CloseAll only stops local streams/connections and
// clears local cached snapshots. Call Reset to kill every pane on the daemon.
func (h *HostAdapter) CloseAll() {
	h.subMu.Lock()
	cancel := h.subCancel
	h.subCancel = nil
	h.subMu.Unlock()
	if cancel != nil {
		cancel()
		h.subWg.Wait()
	}

	h.writeMu.Lock()
	h.closeWriteConnLocked()
	h.writeMu.Unlock()

	h.mu.Lock()
	h.cache = map[string]cachedFrame{}
	h.mu.Unlock()
}

// List returns every pane currently held by the daemon.
func (h *HostAdapter) List() ([]PaneInfo, error) {
	return h.client.List(context.Background())
}

// ClearCache drops local cached snapshots without stopping active streams.
func (h *HostAdapter) ClearCache() {
	h.mu.Lock()
	h.cache = map[string]cachedFrame{}
	h.mu.Unlock()
}

// Reset kills all panes and clears hook telemetry and pending approvals on the daemon.
func (h *HostAdapter) Reset() error {
	h.ClearCache()
	return h.client.Reset(context.Background())
}

func (h *HostAdapter) expire(id string) {
	h.mu.Lock()
	delete(h.cache, id)
	h.mu.Unlock()
}
