package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
)

func TestDaemonURLCommand(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mtm-durl-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "mtm.sock")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"daemon", "url", "--socket", sock}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatalf("mtm daemon url without daemon exited 0: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not running") {
		t.Fatalf("stderr = %q, want not running", stderr.String())
	}

	store, err := data.Open(filepath.Join(dir, "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv, err := daemon.New(daemon.Config{SocketPath: sock, MCPAddr: "127.0.0.1:0", Store: store, SchedulerInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	base := srv.MCPURL()

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"daemon", "url", "--socket", sock}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("mtm daemon url exited %d: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != base {
		t.Fatalf("plain url = %q, want %q", got, base)
	}

	stdout.Reset()
	if code := run([]string{"daemon", "url", "--socket", sock, "--json"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("mtm daemon url --json exited %d: %s", code, stderr.String())
	}
	var out daemonURLOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout.String(), err)
	}
	want := daemonURLOutput{URL: base, MCPURL: base + "/mcp", WebURL: base + "/md/", PID: os.Getpid(), Socket: sock}
	if out != want {
		t.Fatalf("json = %+v, want %+v", out, want)
	}
}
