package daemon

import (
	"context"
	"sync"
	"time"

	"ofam.dev/matomeru/ptyhost"
)

// HostAdapter implements ptyhost.Host and ptyhost.EnvOpener over a local
// daemon *Client so PTY panes survive closing Matomeru.
//
// CloseAll only drops local snapshot caches; panes remain alive in the daemon
// until explicitly closed via Close or Reset.
type HostAdapter struct {
	client *Client
	// MinInterval optionally caches Snapshot responses for this duration.
	MinInterval time.Duration

	mu    sync.Mutex
	cache map[string]cachedFrame
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

// Write implements ptyhost.Host.
func (h *HostAdapter) Write(id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	err := h.client.Write(context.Background(), id, data)
	h.expire(id)
	return err
}

// Resize implements ptyhost.Host.
func (h *HostAdapter) Resize(id string, cols, rows int) error {
	err := h.client.Resize(context.Background(), id, cols, rows)
	if err == nil {
		h.expire(id)
	}
	return err
}

// Snapshot implements ptyhost.Host.
func (h *HostAdapter) Snapshot(id string) (ptyhost.Frame, bool) {
	if h.MinInterval > 0 {
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
	if h.MinInterval > 0 {
		h.mu.Lock()
		h.cache[id] = cachedFrame{frame: f, at: time.Now()}
		h.mu.Unlock()
	}
	return f, true
}

// Close implements ptyhost.Host by closing one pane in the daemon.
func (h *HostAdapter) Close(id string) error {
	h.expire(id)
	return h.client.Close(context.Background(), id)
}

// CloseAll implements ptyhost.Host. Because panes belong to the daemon and are
// meant to outlive Matomeru, CloseAll only clears local cached snapshots.
// Call Reset to kill every pane on the daemon.
func (h *HostAdapter) CloseAll() {
	h.mu.Lock()
	h.cache = map[string]cachedFrame{}
	h.mu.Unlock()
}

// List returns every pane currently held by the daemon.
func (h *HostAdapter) List() ([]PaneInfo, error) {
	return h.client.List(context.Background())
}

// Reset kills all panes and clears hook telemetry and pending approvals on the daemon.
func (h *HostAdapter) Reset() error {
	h.CloseAll()
	return h.client.Reset(context.Background())
}

func (h *HostAdapter) expire(id string) {
	h.mu.Lock()
	delete(h.cache, id)
	h.mu.Unlock()
}
