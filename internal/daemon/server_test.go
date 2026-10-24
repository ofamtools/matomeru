package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runtimeDir points the runtime dir at a short temp dir (unix socket paths
// are capped near 104 bytes) and clears the daemon env.
func runtimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtmd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("TUIOS_SOCKET", "")
	t.Setenv("TUIOS_SERVER", "")
	return dir
}

func listen(t *testing.T, socket string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
}

// fakeStart opens the socket the way start-server would, and counts calls.
func fakeStart(t *testing.T, calls *atomic.Int32) StartFunc {
	return func(_ context.Context, name string) (<-chan error, error) {
		calls.Add(1)
		listen(t, SocketFor(name))
		return nil, nil
	}
}

func TestForServerDerivesTheNamedSocket(t *testing.T) {
	dir := runtimeDir(t)
	c := ForServer("mtm-daemon")
	if want := filepath.Join(dir, "tuios", "servers", "mtm-daemon", "tuios.sock"); c.Socket != want || c.Server != "mtm-daemon" {
		t.Fatalf("ForServer = %+v, want socket %s", c, want)
	}
	t.Setenv("TUIOS_SERVER", "mtm-daemon")
	if got := New("").Socket; got != c.Socket {
		t.Fatalf("New with TUIOS_SERVER = %s, want %s", got, c.Socket)
	}
	if ForServer("../x").Socket != "" {
		t.Fatal("a name with a slash resolved a socket")
	}
}

func TestEnsureStartsAMissingServerOnce(t *testing.T) {
	runtimeDir(t)
	var calls atomic.Int32
	c := ForServer("ens")
	c.Start = fakeStart(t, &calls)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = c.Ensure(context.Background()) }()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil || calls.Load() != 1 {
		t.Fatalf("ensure: %v, starts %d", err, calls.Load())
	}
	if err := c.Ensure(context.Background()); err != nil || calls.Load() != 1 {
		t.Fatalf("again: %v, starts %d", err, calls.Load())
	}
}

func TestEnsureLeavesAListeningServerAlone(t *testing.T) {
	runtimeDir(t)
	var calls atomic.Int32
	c := ForServer("up")
	listen(t, c.Socket)
	c.Start = func(context.Context, string) (<-chan error, error) { calls.Add(1); return nil, nil }
	if err := c.Ensure(context.Background()); err != nil || calls.Load() != 0 {
		t.Fatalf("ensure: %v, starts %d", err, calls.Load())
	}
}

func TestEnsureReportsAFailedStart(t *testing.T) {
	runtimeDir(t)
	c := ForServer("bad")
	c.Start = func(context.Context, string) (<-chan error, error) { return nil, errors.New("tuios is not on PATH") }
	if err := c.Ensure(context.Background()); err == nil || err.Error() != "daemon bad not running: tuios is not on PATH" {
		t.Fatalf("start error: %v", err)
	}
	c.Start = func(context.Context, string) (<-chan error, error) {
		exited := make(chan error, 1)
		exited <- errors.New("exit status 1")
		return exited, nil
	}
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "daemon bad not running: tuios --server bad start-server: exit status 1") {
		t.Fatalf("exit error: %v", err)
	}
	c.Start = func(context.Context, string) (<-chan error, error) { return nil, nil }
	c.Wait = 150 * time.Millisecond
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("timeout: %v", err)
	}
}

// A client without a server name (an explicit socket, or the user's own
// daemon) only probes.
func TestEnsureNeverStartsWithoutAServer(t *testing.T) {
	dir := runtimeDir(t)
	var calls atomic.Int32
	for _, c := range []*Client{New(filepath.Join(dir, "none.sock")), New("")} {
		c.Start = func(context.Context, string) (<-chan error, error) { calls.Add(1); return nil, nil }
		if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "not running") {
			t.Fatalf("%s: %v", c.Socket, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("starts %d", calls.Load())
	}
}
