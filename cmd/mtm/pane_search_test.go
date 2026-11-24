package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPaneSearchCLI(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "mtm.db")
	c := cli{t: t, db: db}

	transcriptPath := filepath.Join(dir, "pane.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(
		`{"type":"assistant","message":{"role":"assistant","content":"cli transcript token VOYAGER_42"}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	p1 := decode[record](t, c.ok("pane", "create", "--title", "worker-alpha", "--field", "harness=claude", "--field", "transcriptPath="+transcriptPath))
	p2 := decode[record](t, c.ok("pane", "create", "--title", "worker-archived", "--field", "harness=zsh"))
	c.ok("pane", "archive", p2.ID)
	c.ok("question", "create", "--field", "prompt=Verify VOYAGER_QA", "--field", "pane=pane:"+p1.ID)

	// Point --socket at a nonexistent path so CLI does not pick up any host daemon.
	fakeSock := filepath.Join(dir, "no.sock")

	// JSON output for chat transcript search.
	res := decode[struct {
		Total   int `json:"total"`
		Results []struct {
			PaneID string `json:"paneId"`
			Title  string `json:"title"`
		} `json:"results"`
	}](t, c.ok("pane", "search", "VOYAGER_42", "--source", "chat", "--socket", fakeSock, "--json"))
	if res.Total != 1 || res.Results[0].PaneID != p1.ID {
		t.Fatalf("pane search chat json = %+v", res)
	}

	// Human-readable output for records search.
	out := c.ok("pane", "search", "VOYAGER_QA", "--source", "records", "--socket", fakeSock)
	if !strings.Contains(out, "worker-alpha") || !strings.Contains(out, "[qa]") {
		t.Fatalf("pane search human output:\n%s", out)
	}

	// Archived pane excluded by default, included with --archived.
	noArch := decode[struct{ Total int }](t, c.ok("pane", "search", "worker-archived", "--socket", fakeSock, "--json"))
	if noArch.Total != 0 {
		t.Fatalf("archived pane returned without --archived: %+v", noArch)
	}
	withArch := decode[struct{ Total int }](t, c.ok("pane", "search", "worker-archived", "--archived", "--socket", fakeSock, "--json"))
	if withArch.Total != 1 {
		t.Fatalf("archived pane missing with --archived: %+v", withArch)
	}
}
