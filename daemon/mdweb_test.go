package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestDaemonServesInteractiveMarkdown(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mtm-mdweb-*")
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

	srv, err := New(Config{
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

	baseURL := srv.MCPURL()
	if baseURL == "" {
		t.Fatal("expected daemon HTTP URL")
	}

	mdPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(mdPath, []byte("# Notes\n\n- [ ] Check me\n\nOwner: {{input:owner=\"\" placeholder=\"Name\"}}\n"), 0o644); err != nil {
		t.Fatalf("write md: %v", err)
	}

	// Render view under /md/view?file=<abs>
	resp, err := http.Get(baseURL + "/md/view?file=" + url.QueryEscape(mdPath))
	if err != nil {
		t.Fatalf("GET /md/view: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /md/view status = %d, want 200", resp.StatusCode)
	}

	// Fetch JSON IR
	docResp, err := http.Get(baseURL + "/md/api/doc?file=" + url.QueryEscape(mdPath))
	if err != nil {
		t.Fatalf("GET /md/api/doc: %v", err)
	}
	defer docResp.Body.Close()
	var doc struct {
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(docResp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode doc: %v", err)
	}
	if doc.Revision == "" {
		t.Fatal("empty revision from /md/api/doc")
	}

	// Mutate checkbox via /md/api/mutate and verify write-back to disk
	payload, _ := json.Marshal(map[string]any{
		"file":     mdPath,
		"revision": doc.Revision,
		"mutation": map[string]any{
			"kind":    "checkbox",
			"id":      "chk-0",
			"checked": true,
		},
	})
	mutResp, err := http.Post(baseURL+"/md/api/mutate", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /md/api/mutate: %v", err)
	}
	defer mutResp.Body.Close()
	if mutResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /md/api/mutate status = %d, want 200", mutResp.StatusCode)
	}

	updated, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read updated md: %v", err)
	}
	if !strings.Contains(string(updated), "- [x] Check me") {
		t.Fatalf("expected disk write-back '- [x] Check me', got:\n%s", string(updated))
	}

	// Existing /health endpoint still works alongside /md/
	healthResp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200", healthResp.StatusCode)
	}
}
