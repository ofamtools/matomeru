package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
)

func TestTaskQueueCLIWorkflow(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "mtm.db")
	c := cli{t: t, db: db}

	// Create a project and two child tasks.
	proj := decode[record](t, c.ok("task", "create", "--title", "Matomeru Queue"))
	t1 := decode[record](t, c.ok("task", "create", "--title", "Wire MCP task tools", "--field", "priority=P1", "--field", "parent="+proj.ID))
	t2 := decode[record](t, c.ok("task", "create", "--title", "Low priority cleanup", "--field", "priority=P3", "--field", "parent="+proj.ID))

	// 1. mtm task flag
	flagged1 := decode[record](t, c.ok("task", "flag", t1.ID, "--assignee", "codex", "--json"))
	if flagged1.Fields["forAgent"] != true || flagged1.Fields["agentStatus"] != data.AgentStatusQueued || flagged1.Fields["agentAssignee"] != "codex" {
		t.Fatalf("flag t1 = %+v", flagged1.Fields)
	}
	flagged2 := decode[record](t, c.ok("task", "flag", t2.ID, "--status", "working", "--json"))
	if flagged2.Fields["forAgent"] != true || flagged2.Fields["agentStatus"] != data.AgentStatusWorking {
		t.Fatalf("flag t2 = %+v", flagged2.Fields)
	}

	// Unflag and re-flag t2 to verify --off.
	unflagged := decode[record](t, c.ok("task", "flag", t2.ID, "--off", "--json"))
	if unflagged.Fields["forAgent"] != false || unflagged.Fields["agentStatus"] != data.AgentStatusNone {
		t.Fatalf("flag --off t2 = %+v", unflagged.Fields)
	}
	c.ok("task", "flag", t2.ID, "--status", "working")

	// 2. mtm task note (with --attach and positional body, plus --body flag)
	imgPath := filepath.Join(dir, "screenshot.png")
	if err := os.WriteFile(imgPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	n1 := decode[record](t, c.ok("task", "note", t1.ID, "Please check edge cases", "--attach", imgPath, "--attach", "https://example.com/pr/1", "--json"))
	if n1.Fields["authorKind"] != "owner" || n1.Fields["body"] != "Please check edge cases" {
		t.Fatalf("note1 = %+v", n1.Fields)
	}
	n2 := decode[record](t, c.ok("task", "note", t1.ID, "--title", "Agent update", "--body", "Tests added", "--author-kind", "agent", "--author-label", "worker-a2", "--json"))
	if n2.Fields["authorKind"] != "agent" || n2.Fields["authorLabel"] != "worker-a2" {
		t.Fatalf("note2 = %+v", n2.Fields)
	}

	// 3. mtm task queue (JSON + human table)
	qQueued := decode[taskQueueOutput](t, c.ok("task", "queue", "--project", proj.ID, "--json"))
	if qQueued.Count != 1 || len(qQueued.Items) != 1 || qQueued.Items[0].Task.ID != t1.ID {
		t.Fatalf("task queue queued = %+v", qQueued)
	}
	if len(qQueued.Items[0].Notes) != 2 || len(qQueued.Items[0].Attachments) != 2 {
		t.Fatalf("task queue hydration: notes=%d attachments=%d", len(qQueued.Items[0].Notes), len(qQueued.Items[0].Attachments))
	}
	qOpen := decode[taskQueueOutput](t, c.ok("task", "queue", "--status", "open", "--json"))
	if qOpen.Count != 2 {
		t.Fatalf("task queue open count = %d, want 2", qOpen.Count)
	}
	humanQueue := c.ok("task", "queue", "--project", proj.ID)
	if !strings.Contains(humanQueue, "Wire MCP task tools") || !strings.Contains(humanQueue, "queued") {
		t.Fatalf("human queue output:\n%s", humanQueue)
	}

	// 4. mtm task thread (JSON + human)
	th := decode[taskThreadOutput](t, c.ok("task", "thread", t1.ID, "--json"))
	if th.Task.ID != t1.ID || len(th.Notes) != 2 || len(th.Attachments) != 2 {
		t.Fatalf("task thread json = %+v", th)
	}
	humanThread := c.ok("task", "thread", t1.ID)
	if !strings.Contains(humanThread, "Please check edge cases") || !strings.Contains(humanThread, "screenshot.png") {
		t.Fatalf("human thread output:\n%s", humanThread)
	}

	// 5. mtm task dispatch --dry-run (JSON + human)
	disp := decode[taskDispatchOutput](t, c.ok("task", "dispatch", "--project", proj.ID, "--prompt", "Run fast.", "--dry-run", "--json"))
	if !disp.DryRun || disp.Delivered || disp.Count != 1 {
		t.Fatalf("dispatch dry-run = %+v", disp)
	}
	for _, want := range []string{"# Agent Task Queue", "Run fast.", "Wire MCP task tools", "Please check edge cases"} {
		if !strings.Contains(disp.Prompt, want) {
			t.Errorf("dispatch prompt missing %q:\n%s", want, disp.Prompt)
		}
	}
	dryText := c.ok("task", "dispatch", "--project", proj.ID, "--dry-run")
	if !strings.Contains(dryText, "# Agent Task Queue") {
		t.Fatalf("dispatch dry-run text:\n%s", dryText)
	}
	afterDry := decode[record](t, c.ok("task", "get", t1.ID, "--json"))
	if afterDry.Fields["agentDispatchedAt"] != nil && afterDry.Fields["agentDispatchedAt"] != "" {
		t.Fatalf("dry-run dispatch must NOT stamp agentDispatchedAt: %+v", afterDry.Fields)
	}

	// 6. mtm task dispatch live delivery via fake daemon Unix socket (verifying bracketed paste + sole pane discovery)
	sock := shortTestSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	writtenCh := make(chan daemon.Request, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(cn net.Conn) {
				defer cn.Close()
				sc := bufio.NewScanner(cn)
				if !sc.Scan() {
					return
				}
				var req daemon.Request
				if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
					return
				}
				var resp daemon.Response
				switch req.Op {
				case daemon.OpList:
					resp = daemon.Response{OK: true, Panes: []daemon.PaneInfo{{ID: "pane-solo", Exited: false}}}
				case daemon.OpWrite:
					writtenCh <- req
					resp = daemon.Response{OK: true}
				default:
					resp = daemon.Response{OK: false, Error: "unexpected op " + req.Op}
				}
				b, _ := json.Marshal(resp)
				_, _ = cn.Write(append(b, '\n'))
			}(conn)
		}
	}()

	live := decode[taskDispatchOutput](t, c.ok("task", "dispatch", "--project", proj.ID, "--socket", sock, "--json"))
	if live.DryRun || !live.Delivered || live.PaneID != "pane-solo" || live.Count != 1 {
		t.Fatalf("live dispatch = %+v", live)
	}
	writeReq := <-writtenCh
	if writeReq.ID != "pane-solo" {
		t.Fatalf("write pane = %q, want pane-solo", writeReq.ID)
	}
	gotPayload := string(writeReq.Data)
	if !strings.HasPrefix(gotPayload, "\x1b[200~") || !strings.HasSuffix(gotPayload, "\x1b[201~\r") || !strings.Contains(gotPayload, "Wire MCP task tools") {
		t.Fatalf("unexpected bracketed paste payload: %q", gotPayload)
	}
	afterLive := decode[record](t, c.ok("task", "get", t1.ID, "--json"))
	if afterLive.Fields["agentDispatchedAt"] == nil || afterLive.Fields["agentDispatchedAt"] == "" {
		t.Fatalf("live dispatch did not stamp agentDispatchedAt: %+v", afterLive.Fields)
	}
	if paneRef, _ := afterLive.Fields["agentDispatchedPane"].(map[string]any); paneRef["id"] != "pane-solo" {
		t.Fatalf("live dispatch agentDispatchedPane = %v, want pane-solo", afterLive.Fields["agentDispatchedPane"])
	}

	// 7. mtm schedule preset agent-queue
	pane := decode[record](t, c.ok("pane", "create", "--title", "agent-worker"))
	sched := decode[record](t, c.ok("schedule", "preset", "agent-queue",
		"--title", "Drain queue",
		"--project", proj.ID,
		"--cron", "*/10 * * * *",
		"--pane", pane.ID,
		"--prompt", "Keep commits small",
		"--json",
	))
	if sched.Kind != data.KindSchedule {
		t.Fatalf("schedule kind = %q", sched.Kind)
	}
	if sched.Fields["scheduleKind"] != "agent_queue" || sched.Fields["cron"] != "*/10 * * * *" || sched.Fields["prompt"] != "Keep commits small" {
		t.Fatalf("schedule fields = %+v", sched.Fields)
	}
	if sched.Fields["nextDueAt"] == "" || sched.Fields["nextDueAt"] == nil {
		t.Fatalf("expected computed nextDueAt, got %+v", sched.Fields)
	}
	if taskRef, _ := sched.Fields["task"].(map[string]any); taskRef["id"] != proj.ID {
		t.Fatalf("schedule task ref = %v, want %s", sched.Fields["task"], proj.ID)
	}
	if paneRef, _ := sched.Fields["pane"].(map[string]any); paneRef["id"] != pane.ID {
		t.Fatalf("schedule pane ref = %v, want %s", sched.Fields["pane"], pane.ID)
	}

	// Invalid cron fails cleanly.
	c.fails("schedule", "preset", "agent-queue", "--cron", "not-a-cron")
	// Conflicting --off and --status fails cleanly.
	c.fails("task", "flag", t1.ID, "--off", "--status", "working")
}
