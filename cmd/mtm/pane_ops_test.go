package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

func runMTMCLI(t *testing.T, dbPath string, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	full := append([]string{"--db", dbPath}, args...)
	if code := run(full, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("mtm %v exited %d\nstdout: %s\nstderr: %s", args, code, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestCLINounSetRelatedPageAndPaneSpawnNoLaunch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mtm.db")

	// Create project task + lane via CLI.
	taskOut := runMTMCLI(t, dbPath, "task", "create", "--title", "Portal Project", "--field", "defaultDir=/tmp/portal")
	var taskRec data.Record
	if err := json.Unmarshal([]byte(taskOut), &taskRec); err != nil {
		t.Fatalf("unmarshal task: %v\n%s", err, taskOut)
	}

	laneOut := runMTMCLI(t, dbPath, "lane", "create", "--title", "Agent Lane")
	var laneRec data.Record
	if err := json.Unmarshal([]byte(laneOut), &laneRec); err != nil {
		t.Fatalf("unmarshal lane: %v\n%s", err, laneOut)
	}

	// `mtm lane set <id> task=task:<id> laneStatus=running` sets refs and fields ergonomically.
	setOut := runMTMCLI(t, dbPath, "lane", "set", laneRec.ID, "task=task:"+taskRec.ID, "laneStatus=running")
	if !strings.Contains(setOut, taskRec.ID) || !strings.Contains(setOut, `"running"`) {
		t.Fatalf("lane set output = %s", setOut)
	}

	// `mtm pane spawn --no-launch --harness claude --project <id> --lane <id>` creates pane, links project & lane claim.
	spawnOut := runMTMCLI(t, dbPath, "pane", "spawn", "--no-launch", "--harness", "claude", "--project", taskRec.ID, "--lane", laneRec.ID)
	if !strings.Contains(spawnOut, `"claude"`) || !strings.Contains(spawnOut, "/tmp/portal") || !strings.Contains(spawnOut, `"claim"`) {
		t.Fatalf("pane spawn output = %s", spawnOut)
	}

	// `mtm lane related <id>` lists the claim relation!
	relOut := runMTMCLI(t, dbPath, "lane", "related", laneRec.ID, "--rel", "claim")
	if !strings.Contains(relOut, `"claim"`) || !strings.Contains(relOut, `"pane"`) {
		t.Fatalf("lane related output = %s", relOut)
	}

	// Built-in `page` (`ui.page`) noun is now exposed directly as a CLI subcommand.
	pagesOut := runMTMCLI(t, dbPath, "page", "list")
	if !strings.Contains(pagesOut, "[") {
		t.Fatalf("page list output = %s", pagesOut)
	}
}
