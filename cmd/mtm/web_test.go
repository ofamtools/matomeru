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

func TestWebURLCommand(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mtm-web-*")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "mtm.sock")
	dbPath := filepath.Join(dir, "mtm.db")
	store, err := data.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	srv, err := daemon.New(daemon.Config{
		SocketPath:        sock,
		MCPAddr:           "127.0.0.1:0",
		Store:             store,
		SchedulerInterval: -1,
	})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	mdPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(mdPath, []byte("# Plan\n\n- [ ] Ready\n"), 0o644); err != nil {
		t.Fatalf("write md: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"web", "url", mdPath, "--socket", sock, "--json"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("mtm web url exited %d: stderr=%s", code, stderr.String())
	}
	var out struct {
		URL  string `json:"url"`
		File string `json:"file"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal output %q: %v", stdout.String(), err)
	}
	if !strings.HasPrefix(out.URL, srv.MCPURL()+"/md/view?file=") {
		t.Fatalf("unexpected url %q (daemon base %q)", out.URL, srv.MCPURL())
	}
	if out.File != mdPath {
		t.Fatalf("file = %q, want %q", out.File, mdPath)
	}
}
