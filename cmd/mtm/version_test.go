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
)

func TestVersionCommandCompactAndVerbose(t *testing.T) {
	// 1. --version stays compact 1-line for scripts and agents.
	var out, errBuf bytes.Buffer
	if code := run([]string{"--version"}, bytes.NewReader(nil), &out, &errBuf); code != 0 {
		t.Fatalf("mtm --version failed (%d): %s", code, errBuf.String())
	}
	wantCompact := "mtm " + daemon.Version + "\n"
	if out.String() != wantCompact {
		t.Fatalf("mtm --version = %q, want %q", out.String(), wantCompact)
	}

	// 2. Start a real daemon and verify `mtm version` prints build details + MATCH status.
	dir, err := os.MkdirTemp("/tmp", "mtmver-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "mtm.sock")
	srv, err := daemon.New(daemon.Config{SocketPath: sock, DisableMCP: true, SchedulerInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	out.Reset()
	errBuf.Reset()
	if code := run([]string{"version", "--socket", sock}, bytes.NewReader(nil), &out, &errBuf); code != 0 {
		t.Fatalf("mtm version failed (%d): %s", code, errBuf.String())
	}
	text := out.String()
	for _, want := range []string{"mtm " + daemon.Version, "commit:", "go:", "daemon:  running", "status:  MATCH"} {
		if !strings.Contains(text, want) {
			t.Errorf("mtm version missing %q:\n%s", want, text)
		}
	}

	// 3. `mtm version --json` returns structured build + daemon comparison.
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"version", "--socket", sock, "--json"}, bytes.NewReader(nil), &out, &errBuf); code != 0 {
		t.Fatalf("mtm version --json failed (%d): %s", code, errBuf.String())
	}
	var got versionOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode version json: %v\n%s", err, out.String())
	}
	if got.Version != daemon.Version || !got.Daemon.Running || !got.Daemon.Match || got.Daemon.Version != daemon.Version {
		t.Fatalf("unexpected version json: %+v", got)
	}
}
