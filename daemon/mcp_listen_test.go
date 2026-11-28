package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestListenMCPDefaultAndBusyFallback(t *testing.T) {
	// Hold DefaultMCPAddr (or find it already held by a live daemon): either way it is busy.
	if hold, err := net.Listen("tcp", DefaultMCPAddr); err == nil {
		defer hold.Close()
	}
	ln, err := listenMCP("")
	if err != nil {
		t.Fatalf("implicit listen with busy default: %v", err)
	}
	defer ln.Close()
	if got := ln.Addr().String(); got == DefaultMCPAddr {
		t.Fatalf("implicit listen bound busy default %s", got)
	}

	if strict, err := listenMCP(DefaultMCPAddr); err == nil {
		strict.Close()
		t.Fatalf("explicit busy %s: want error", DefaultMCPAddr)
	}
}

func TestListenMCPPrefersDefaultWhenFree(t *testing.T) {
	probe, err := net.Listen("tcp", DefaultMCPAddr)
	if err != nil {
		t.Skipf("%s in use on this host: %v", DefaultMCPAddr, err)
	}
	_ = probe.Close()
	ln, err := listenMCP("")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if got := ln.Addr().String(); got != DefaultMCPAddr {
		t.Fatalf("implicit listen addr = %s, want %s", got, DefaultMCPAddr)
	}
}

func TestStartFailsOnBusyExplicitMCPAddr(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	dir, err := os.MkdirTemp("/tmp", "mtm-mcpaddr-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store, err := data.Open(filepath.Join(dir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	srv, err := New(Config{
		SocketPath:        filepath.Join(dir, "mtm.sock"),
		MCPAddr:           busy.Addr().String(),
		Store:             store,
		SchedulerInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err == nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("Start on busy explicit %s: want error", busy.Addr())
	}
}
