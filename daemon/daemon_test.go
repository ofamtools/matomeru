package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/ochazuke/core"
)

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtmd-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "mtm.sock")
}

func startTestServer(t *testing.T, cfg Config) (*Server, *Client) {
	t.Helper()
	if cfg.SocketPath == "" {
		cfg.SocketPath = shortSocketPath(t)
	}
	if cfg.SchedulerInterval == 0 {
		cfg.SchedulerInterval = -1 // disable background ticker unless tested explicitly
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background())
	})
	return srv, NewClient(cfg.SocketPath)
}

func waitSnapshotContains(t *testing.T, h *HostAdapter, id, substr string, timeout time.Duration) ptyhost.Frame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last ptyhost.Frame
	for time.Now().Before(deadline) {
		f, ok := h.Snapshot(id)
		if ok {
			last = f
			if strings.Contains(strings.Join(f.Plain, "\n"), substr) {
				return f
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pane %q did not contain %q within %v; last screen:\n%s", id, substr, timeout, strings.Join(last.Plain, "\n"))
	return last
}

func TestDaemonStatusEnvInjectionAndHostAdapter(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv, client := startTestServer(t, Config{Store: store})

	// Status reports pid, socket, mcpURL, paneCount=0, uptime.
	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.PID != os.Getpid() || st.Socket != srv.SocketPath() || st.MCPURL == "" || st.PaneCount != 0 || st.Uptime == "" {
		t.Fatalf("unexpected status = %+v", st)
	}

	// Duplicate start on live socket returns ErrAlreadyRunning.
	dup, err := New(Config{SocketPath: srv.SocketPath()})
	if err != nil {
		t.Fatal(err)
	}
	if err := dup.Start(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate Start error = %v, want ErrAlreadyRunning", err)
	}

	// Shared HTTP MCP server health + pane-scoped call works end-to-end.
	resp, err := http.Get(st.MCPURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health status = %d", resp.StatusCode)
	}

	// Open a pane through HostAdapter and verify MTM_PANE_ID, MTM_SOCKET, MTM_MCP_URL are injected!
	host := NewHostAdapter(client)
	var _ ptyhost.Host = host
	var _ ptyhost.EnvOpener = host

	cmd := `printf "ID=%s\nSOCK=%s\nMCP=%s\nEXTRA=%s\nREADY\n" "$MTM_PANE_ID" "$MTM_SOCKET" "$MTM_MCP_URL" "$CUSTOM_VAR"; cat`
	if err := host.OpenWithEnv("p1", cmd, "", 120, 24, map[string]string{"CUSTOM_VAR": "ok"}); err != nil {
		t.Fatalf("OpenWithEnv: %v", err)
	}
	frame := waitSnapshotContains(t, host, "p1", "READY", 3*time.Second)
	screen := strings.Join(frame.Plain, "\n")
	wantMCP := st.MCPURL + "/mcp/p1"
	for _, want := range []string{"ID=p1", "SOCK=" + srv.SocketPath(), "MCP=" + wantMCP, "EXTRA=ok"} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen missing %q:\n%s", want, screen)
		}
	}

	// Verify the injected MTM_MCP_URL actually auto-binds pane p1 over HTTP!
	mcpBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": mcpui.ToolCreatePage,
			"arguments": map[string]any{
				"title": "From Daemon Pane",
				"root":  map[string]any{"kind": "text", "props": map[string]any{"text": "hello"}},
			},
		},
	})
	mcpResp, err := http.Post(wantMCP, "application/json", bytes.NewReader(mcpBody))
	if err != nil {
		t.Fatalf("POST %s: %v", wantMCP, err)
	}
	defer mcpResp.Body.Close()
	var rpc map[string]any
	if err := json.NewDecoder(mcpResp.Body).Decode(&rpc); err != nil {
		t.Fatal(err)
	}
	pageID := rpc["result"].(map[string]any)["structuredContent"].(map[string]any)["id"].(string)
	pageRec, err := store.Get(core.Ref{Kind: data.KindPage, ID: pageID})
	if err != nil {
		t.Fatal(err)
	}
	if paneRef, _ := pageRec.RefField("pane"); paneRef.ID != "p1" {
		t.Fatalf("HTTP MCP page pane = %v, want p1", paneRef)
	}

	// Write + Resize work through HostAdapter.
	if err := host.Write("p1", []byte("ECHO_LINE\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitSnapshotContains(t, host, "p1", "ECHO_LINE", 3*time.Second)

	if err := host.Resize("p1", 100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if f, ok := host.Snapshot("p1"); !ok || f.Cols != 100 || f.Rows != 30 {
		t.Fatalf("after resize snapshot = %dx%d (ok=%v)", f.Cols, f.Rows, ok)
	}

	// CloseAll on HostAdapter must NOT kill daemon panes (panes survive closing mtm).
	host.CloseAll()
	panes, err := host.List()
	if err != nil || len(panes) != 1 || panes[0].ID != "p1" {
		t.Fatalf("after CloseAll panes = %+v, err=%v", panes, err)
	}

	// Explicit Close kills the pane.
	if err := host.Close("p1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := host.Snapshot("p1"); ok {
		t.Fatal("closed pane still returned a snapshot")
	}
	panes, err = client.List(context.Background())
	if err != nil || len(panes) != 0 {
		t.Fatalf("after Close list = %+v, err=%v", panes, err)
	}
}

func TestDaemonHookTelemetryBlockingApprovalAndReset(t *testing.T) {
	_, client := startTestServer(t, Config{ApprovalTimeout: 5 * time.Second})
	ctx := context.Background()

	// Send state, activity, and subagent events.
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID: "p1", Harness: "claude", Kind: HookKindState, State: StateRunning, Summary: "Working on task",
	}); err != nil {
		t.Fatalf("state hook_event: %v", err)
	}
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID: "p1", Harness: "claude", Kind: HookKindActivity, Tool: "Bash", Summary: "Running tests",
	}); err != nil {
		t.Fatalf("activity hook_event: %v", err)
	}
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID: "p1", Harness: "claude", Kind: HookKindSubagent, SubagentID: "sub-1", SubagentAction: SubagentStart, Summary: "researcher",
	}); err != nil {
		t.Fatalf("subagent start: %v", err)
	}

	tel, err := client.Telemetry(ctx, "p1")
	if err != nil {
		t.Fatalf("Telemetry: %v", err)
	}
	pt := tel.Telemetry["p1"]
	if pt == nil || pt.State != StateRunning || pt.Tool != "Bash" || pt.SubagentCount != 1 {
		t.Fatalf("telemetry p1 = %+v", pt)
	}

	// Stop subagent -> count returns to 0.
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID: "p1", Harness: "claude", Kind: HookKindSubagent, SubagentID: "sub-1", SubagentAction: SubagentStop,
	}); err != nil {
		t.Fatalf("subagent stop: %v", err)
	}
	tel, _ = client.Telemetry(ctx, "p1")
	if tel.Telemetry["p1"].SubagentCount != 0 {
		t.Fatalf("subagent count after stop = %d", tel.Telemetry["p1"].SubagentCount)
	}

	// Blocking PermissionRequest approval: blocks until resolve_approval unblocks it.
	resCh := make(chan *HookEventResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := client.HookEvent(ctx, HookEvent{
			PaneID:    "p1",
			Harness:   "claude",
			Kind:      HookKindApproval,
			Tool:      "Bash",
			Summary:   "rm -rf build",
			RequestID: "req-1",
			Payload:   map[string]any{"command": "rm -rf build"},
		})
		if err != nil {
			errCh <- err
			return
		}
		resCh <- r
	}()

	// Wait until pending approval appears and pane state becomes needs_you.
	deadline := time.Now().Add(2 * time.Second)
	for {
		tel, err = client.Telemetry(ctx, "")
		if err == nil && len(tel.PendingApprovals) == 1 && tel.Telemetry["p1"].State == StateNeedsYou {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("approval did not register; tel=%+v err=%v", tel, err)
		}
		time.Sleep(15 * time.Millisecond)
	}

	if err := client.ResolveApproval(ctx, "req-1", DecisionAllow, "approved by owner"); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("approval HookEvent error: %v", err)
	case res := <-resCh:
		if !res.OK || res.Decision != DecisionAllow || res.Reason != "approved by owner" {
			t.Fatalf("approval result = %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval HookEvent did not unblock")
	}

	// Pane state moved back to running after allow.
	tel, _ = client.Telemetry(ctx, "p1")
	if tel.Telemetry["p1"].State != StateRunning || len(tel.PendingApprovals) != 0 {
		t.Fatalf("after resolve telemetry = %+v, pending=%d", tel.Telemetry["p1"], len(tel.PendingApprovals))
	}

	// Approval timeout returns deny.
	timeoutRes, err := client.HookEvent(ctx, HookEvent{
		PaneID:    "p1",
		Kind:      HookKindApproval,
		RequestID: "req-timeout",
		TimeoutMs: 60,
	})
	if err != nil {
		t.Fatalf("timeout HookEvent error: %v", err)
	}
	if timeoutRes.Decision != DecisionDeny || !strings.Contains(timeoutRes.Reason, "timed out") {
		t.Fatalf("timeout result = %+v", timeoutRes)
	}

	// Open two panes + enqueue a pending approval, then run "reset" and verify EVERYTHING is cleared.
	if err := client.Open(ctx, OpenRequest{ID: "r1", Cmd: "sleep 30"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Open(ctx, OpenRequest{ID: "r2", Cmd: "sleep 30"}); err != nil {
		t.Fatal(err)
	}
	resetAprCh := make(chan *HookEventResult, 1)
	go func() {
		r, _ := client.HookEvent(ctx, HookEvent{PaneID: "r1", Kind: HookKindApproval, RequestID: "req-reset"})
		resetAprCh <- r
	}()
	deadline = time.Now().Add(2 * time.Second)
	for {
		tel, _ = client.Telemetry(ctx, "")
		if len(tel.PendingApprovals) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("req-reset never appeared")
		}
		time.Sleep(15 * time.Millisecond)
	}

	if err := client.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	select {
	case r := <-resetAprCh:
		if r == nil || r.Decision != DecisionDeny {
			t.Fatalf("reset approval result = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reset did not unblock waiting approval")
	}

	panes, err := client.List(ctx)
	if err != nil || len(panes) != 0 {
		t.Fatalf("after reset panes = %+v, err=%v", panes, err)
	}
	tel, err = client.Telemetry(ctx, "")
	if err != nil || len(tel.Telemetry) != 0 || len(tel.PendingApprovals) != 0 {
		t.Fatalf("after reset telemetry = %+v, err=%v", tel, err)
	}
	st, err := client.Status(ctx)
	if err != nil || st.PaneCount != 0 {
		t.Fatalf("after reset status = %+v, err=%v", st, err)
	}
}

func TestEnsureRunningAndShutdown(t *testing.T) {
	sock := shortSocketPath(t)
	var srv *Server

	client := NewClient(sock)
	launched := 0
	client.Launcher = func(path string) error {
		launched++
		var err error
		srv, err = New(Config{SocketPath: path, SchedulerInterval: -1})
		if err != nil {
			return err
		}
		return srv.Start(context.Background())
	}

	st, err := client.EnsureRunning(context.Background())
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if launched != 1 || st.Socket != sock {
		t.Fatalf("launched=%d status=%+v", launched, st)
	}

	// Second call reuses running daemon without launching again.
	if _, err := client.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("second EnsureRunning: %v", err)
	}
	if launched != 1 {
		t.Fatalf("launched = %d, want 1", launched)
	}

	// Shutdown stops daemon cleanly and removes socket.
	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket still exists after shutdown")
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func TestDaemonHookAutoIndexing(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	owner := core.Actor{Kind: "user", ID: "owner"}
	task, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Daily Workspace"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, ID: "p-idx", Fields: map[string]any{"title": "claude", "task": task.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	_, client := startTestServer(t, Config{Store: store, DisableMCP: true})
	ctx := context.Background()

	// 1. PostToolUse Write of a Markdown file creates a KindDocument linked to the pane's task + RelCreated from pane.
	mdPath := filepath.Join(t.TempDir(), "architecture.md")
	if err := os.WriteFile(mdPath, []byte("# Daily Architecture\n\nDetails here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID:  pane.ID,
		Harness: "claude",
		Kind:    HookKindActivity,
		State:   StateRunning,
		Tool:    "Write",
		Files:   []string{mdPath, "/proj/matomeru/daemon/index.go"},
	}); err != nil {
		t.Fatalf("PostToolUse HookEvent: %v", err)
	}

	// Re-writing the same markdown file deduplicates instead of creating a duplicate KindDocument.
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID:  pane.ID,
		Harness: "claude",
		Kind:    HookKindActivity,
		State:   StateRunning,
		Tool:    "Edit",
		Files:   []string{mdPath},
	}); err != nil {
		t.Fatalf("second PostToolUse HookEvent: %v", err)
	}

	docs, err := store.Query(data.Query{Kind: data.KindDocument})
	if err != nil || len(docs.Items) != 1 {
		t.Fatalf("documents = %+v, err=%v; want 1", docs.Items, err)
	}
	doc := docs.Items[0]
	if doc.Text("title") != "Daily Architecture" || doc.Text("filePath") != mdPath || doc.Text("source") != data.DocumentSourceTool {
		t.Fatalf("document fields = %+v", doc.Fields)
	}
	if tr, ok := doc.RefField("task"); !ok || tr != task.Ref() {
		t.Fatalf("document task = %v (%v), want %v", tr, ok, task.Ref())
	}
	if rels, err := store.Related(pane.Ref(), data.RelCreated, data.Outgoing); err != nil || len(rels) != 1 || rels[0].To != doc.Ref() {
		t.Fatalf("pane RelCreated = %+v, err=%v", rels, err)
	}

	// 2. Neither prompt nor progress text creates a Context record.
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID:  pane.ID,
		Harness: "claude",
		Kind:    HookKindState,
		State:   StateRunning,
		Prompt:  "Please review https://prompt.example.com and app/prompt.go.",
	}); err != nil {
		t.Fatalf("UserPromptSubmit HookEvent: %v", err)
	}
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID:        pane.ID,
		Harness:       "claude",
		Kind:          HookKindActivity,
		State:         StateRunning,
		Tool:          "Write",
		Response:      "Progress: https://progress.example.com and app/progress.go.",
		FinalResponse: "Bogus: https://tool.example.com and app/tool.go.",
	}); err != nil {
		t.Fatalf("activity HookEvent: %v", err)
	}
	if ctxs, err := store.Query(data.Query{Kind: data.KindContext}); err != nil || len(ctxs.Items) != 0 {
		t.Fatalf("intermediate hook context = %+v, err=%v; want none", ctxs.Items, err)
	}

	// 3. A final answer arrives on the real daemon socket.
	if _, err := client.HookEvent(ctx, HookEvent{
		PaneID:        pane.ID,
		Harness:       "claude",
		Kind:          HookKindState,
		State:         StateDone,
		Response:      "Previous app/telemetry.go",
		FinalResponse: "See https://github.com/ofam/matomeru/pull/42, ofam/matomeru#42, app/final.go:17 and https://docs.example.com/guide. Updated README.md, main.go:4:2 and Makefile:12, e.g. for `v1.2.3`.",
	}); err != nil {
		t.Fatalf("completed HookEvent: %v", err)
	}

	ctxs, err := store.Query(data.Query{Kind: data.KindContext})
	if err != nil {
		t.Fatalf("query context: %v", err)
	}
	const wantContextCount = 7
	if len(ctxs.Items) != wantContextCount {
		t.Fatalf("got %d context items, want %d: %+v", len(ctxs.Items), wantContextCount, ctxs.Items)
	}

	byTitle := map[string]data.Record{}
	for _, rec := range ctxs.Items {
		byTitle[rec.Text("title")] = rec
		if rec.Text("source") != "response" {
			t.Errorf("context %q source = %q, want response", rec.Text("title"), rec.Text("source"))
		}
		if pr, ok := rec.RefField("pane"); !ok || pr != pane.Ref() {
			t.Errorf("context %q pane = %v (%v), want %v", rec.Text("title"), pr, ok, pane.Ref())
		}
		if tr, ok := rec.RefField("task"); !ok || tr != task.Ref() {
			t.Errorf("context %q task = %v (%v), want %v", rec.Text("title"), tr, ok, task.Ref())
		}
	}
	prRec, ok := byTitle["ofam/matomeru#42"]
	if !ok || prRec.Text("contextKind") != "pr" || prRec.Text("value") != "https://github.com/ofam/matomeru/pull/42" {
		t.Fatalf("PR context record = %+v (ok=%v)", prRec.Fields, ok)
	}
	if rec, ok := byTitle["final.go"]; !ok || rec.Text("value") != "app/final.go:17" || rec.Text("line") != "17" {
		t.Fatalf("final path context record = %+v", rec.Fields)
	}
	for _, want := range []struct{ title, value, line, col string }{
		{"README.md", "README.md", "", ""},
		{"main.go", "main.go:4:2", "4", "2"},
		{"Makefile", "Makefile:12", "12", ""},
	} {
		rec := byTitle[want.title]
		if rec.Text("value") != want.value || rec.Text("line") != want.line || rec.Text("col") != want.col {
			t.Errorf("%s context record = %+v, want %+v", want.title, rec.Fields, want)
		}
	}
	if _, ok := byTitle["https://docs.example.com/guide"]; !ok {
		t.Fatalf("missing URL context record: %+v", byTitle)
	}
	if rec, ok := byTitle["v1.2.3"]; !ok || rec.Text("contextKind") != "code" || rec.Text("value") != "v1.2.3" {
		t.Fatalf("inline code context record = %+v (ok=%v), want contextKind=code", rec.Fields, ok)
	}

	// Both pane and task relate to each final context record.
	if rels, err := store.Related(task.Ref(), data.RelContext, data.Outgoing); err != nil || len(rels) != wantContextCount {
		t.Fatalf("task RelContext count = %d, err=%v; want %d", len(rels), err, wantContextCount)
	}
	if rels, err := store.Related(pane.Ref(), data.RelContext, data.Outgoing); err != nil || len(rels) != wantContextCount {
		t.Fatalf("pane RelContext count = %d, err=%v; want %d", len(rels), err, wantContextCount)
	}
}

func TestDaemonHookCodeBlockExtractionAndDedupe(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	owner := core.Actor{Kind: "user", ID: "owner"}
	task, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Code Extract"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{"title": "claude", "task": task.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}

	_, client := startTestServer(t, Config{Store: store, DisableMCP: true})
	ctx := context.Background()

	final := strings.Join([]string{
		"Edited `app/model.go` and ran `go test ./...`.",
		"```go",
		"package main",
		"const token = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890\"",
		"```",
	}, "\n")

	for range 2 {
		if _, err := client.HookEvent(ctx, HookEvent{
			PaneID:        pane.ID,
			Harness:       "claude",
			Kind:          HookKindState,
			State:         StateDone,
			FinalResponse: final,
		}); err != nil {
			t.Fatalf("HookEvent: %v", err)
		}
	}

	ctxs, err := store.Query(data.Query{Kind: data.KindContext})
	if err != nil {
		t.Fatal(err)
	}
	// Expect 3 deduplicated items: file app/model.go, fenced go block, inline `go test ./...`.
	if len(ctxs.Items) != 3 {
		t.Fatalf("got %d context items, want 3: %+v", len(ctxs.Items), ctxs.Items)
	}

	var goRec, inlineRec, fileRec *data.Record
	for i := range ctxs.Items {
		rec := ctxs.Items[i]
		switch {
		case rec.Text("contextKind") == "file":
			fileRec = &rec
		case rec.Text("contextKind") == "code" && rec.Text("language") == "go":
			goRec = &rec
		case rec.Text("contextKind") == "code" && rec.Text("value") == "go test ./...":
			inlineRec = &rec
		}
	}
	if fileRec == nil || fileRec.Text("value") != "app/model.go" {
		t.Fatalf("missing file context for app/model.go: %+v", ctxs.Items)
	}
	if goRec == nil {
		t.Fatalf("missing fenced go code block: %+v", ctxs.Items)
	}
	if goRec.Text("title") != "go: package main" {
		t.Errorf("goRec title = %q, want %q", goRec.Text("title"), "go: package main")
	}
	if !strings.HasPrefix(goRec.Text("canonKey"), "code:") || len(goRec.Text("canonKey")) != len("code:")+16 {
		t.Errorf("goRec canonKey = %q, want hash key", goRec.Text("canonKey"))
	}
	if strings.Contains(goRec.Text("value"), "ghp_abcdefghijklmnopqrstuvwxyz1234567890") {
		t.Errorf("secret was not masked in extracted code block: %q", goRec.Text("value"))
	}
	if inlineRec == nil || inlineRec.Text("title") != "go test ./..." {
		t.Fatalf("missing inline code record: %+v", ctxs.Items)
	}
}

func TestDaemonSubscribePushStreamingAndScrollbackPreservation(t *testing.T) {
	_, client := startTestServer(t, Config{DisableMCP: true})
	host := NewHostAdapter(client)
	defer host.CloseAll()

	changes := host.Changes()

	// Wait briefly for the subscription stream to connect.
	deadline := time.Now().Add(2 * time.Second)
	for !host.subConnected.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !host.subConnected.Load() {
		t.Fatal("HostAdapter subscribe stream did not connect")
	}

	// Open a pane that creates scrollback lines, then echoes stdin via cat.
	cmd := `i=1; while [ $i -le 30 ]; do printf "HIST_%02d\n" $i; i=$((i+1)); done; printf "PROMPT_READY\n"; cat`
	if err := host.Open("sub1", cmd, "", 80, 10); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Wait for push-delivered frames containing PROMPT_READY (without polling RPC).
	waitPushed := func(substr string) ptyhost.Frame {
		t.Helper()
		dl := time.After(3 * time.Second)
		for {
			if f, ok := host.Snapshot("sub1"); ok && strings.Contains(strings.Join(f.Plain, "\n"), substr) {
				return f
			}
			select {
			case <-changes:
			case <-dl:
				f, _ := host.Snapshot("sub1")
				t.Fatalf("timed out waiting for pushed %q; screen:\n%s", substr, strings.Join(f.Plain, "\n"))
			}
		}
	}

	f1 := waitPushed("PROMPT_READY")
	if len(f1.Scrollback) == 0 {
		t.Fatalf("expected non-empty scrollback after 30 lines in 10-row pane")
	}

	// Send keystrokes over persistent Write connection and verify immediate push (<500ms)
	// AND that scrollback is preserved across ScrollbackUnchanged frames.
	start := time.Now()
	if err := host.Write("sub1", []byte("FAST_ECHO_TEST\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f2 := waitPushed("FAST_ECHO_TEST")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("push latency too slow: %v", elapsed)
	}
	if len(f2.Scrollback) < len(f1.Scrollback) {
		t.Fatalf("scrollback lost across push update: got %d, had %d", len(f2.Scrollback), len(f1.Scrollback))
	}

	// Second Write on reused persistent connection works smoothly.
	if err := host.Write("sub1", []byte("SECOND_KEY\n")); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	waitPushed("SECOND_KEY")
}

// TestHelperDaemonUpgradeServer is invoked as a subprocess by
// TestDaemonZeroDowntimeExecUpgrade to prove real syscall.Exec handoff.
func TestHelperDaemonUpgradeServer(t *testing.T) {
	sock := os.Getenv("MTM_TEST_UPGRADE_SOCK")
	if sock == "" {
		return
	}
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatalf("data.Open: %v", err)
	}
	defer store.Close()

	srv, err := New(Config{
		SocketPath:        sock,
		Store:             store,
		SchedulerInterval: -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestDaemonZeroDowntimeExecUpgrade(t *testing.T) {
	sock := shortSocketPath(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	// Launch a real daemon subprocess running this test binary.
	cmd := exec.Command(exe, "-test.run=^TestHelperDaemonUpgradeServer$")
	cmd.Env = append(os.Environ(), "MTM_TEST_UPGRADE_SOCK="+sock)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper daemon: %v", err)
	}
	client := NewClient(sock)
	t.Cleanup(func() {
		_ = client.Shutdown(context.Background())
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Wait until the helper daemon is ready.
	var stBefore *StatusResult
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, serr := client.Status(context.Background())
		if serr == nil {
			stBefore = st
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if stBefore == nil {
		t.Fatalf("helper daemon never started; stderr:\n%s", stderr.String())
	}

	// Open a pane that spawns a background child "subagent" (sleep 60 &) and echoes stdin via cat.
	host := NewHostAdapter(client)
	defer host.CloseAll()
	_ = host.Changes()

	paneCmd := `sleep 60 & sub=$!; printf "SUBPID=%d\nBEFORE_UPGRADE\n" "$sub"; cat`
	if err := host.Open("live1", paneCmd, "", 80, 20); err != nil {
		t.Fatalf("Open: %v", err)
	}
	frameBefore := waitSnapshotContains(t, host, "live1", "BEFORE_UPGRADE", 4*time.Second)

	// Extract the child subagent PID from the terminal output.
	subPID := 0
	for _, line := range frameBefore.Plain {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "SUBPID="); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && n > 0 {
				subPID = n
			}
		}
	}
	if subPID <= 0 {
		t.Fatalf("could not parse SUBPID from screen:\n%s", strings.Join(frameBefore.Plain, "\n"))
	}
	t.Cleanup(func() {
		_ = syscall.Kill(subPID, syscall.SIGKILL)
	})

	// Record telemetry before upgrade to verify it also survives exec.
	if _, err := client.HookEvent(context.Background(), HookEvent{
		PaneID:  "live1",
		Harness: "claude",
		Kind:    HookKindState,
		State:   StateRunning,
		Summary: "Running subagent",
	}); err != nil {
		t.Fatalf("HookEvent: %v", err)
	}

	// Write over the adapter's persistent write connection BEFORE the upgrade
	// so the post-upgrade Write must recover from a connection the exec closed.
	if err := host.Write("live1", []byte("PRE_UPGRADE_WRITE\n")); err != nil {
		t.Fatalf("Write before upgrade: %v", err)
	}
	waitSnapshotContains(t, host, "live1", "PRE_UPGRADE_WRITE", 4*time.Second)
	if stBefore.Generation != 1 {
		t.Fatalf("fresh daemon generation = %d, want 1", stBefore.Generation)
	}

	// Trigger a REAL syscall.Exec zero-downtime upgrade of the daemon process!
	upRes, err := client.Upgrade(context.Background(), exe)
	if err != nil {
		t.Fatalf("Upgrade: %v (stderr:\n%s)", err, stderr.String())
	}
	if upRes.PID != stBefore.PID {
		t.Fatalf("upgraded PID = %d, want same PID %d", upRes.PID, stBefore.PID)
	}
	// Upgrade must have waited for the NEW exec'd process, not the old one.
	if upRes.Generation != stBefore.Generation+1 {
		t.Fatalf("upgraded Generation = %d, want %d", upRes.Generation, stBefore.Generation+1)
	}
	if upRes.MCPURL != stBefore.MCPURL {
		t.Fatalf("upgraded MCPURL = %q, want unchanged %q", upRes.MCPURL, stBefore.MCPURL)
	}
	if upRes.PaneCount != 1 {
		t.Fatalf("upgraded PaneCount = %d, want 1", upRes.PaneCount)
	}

	// 1. Both the pane process AND its child subagent MUST still be alive!
	if err := syscall.Kill(subPID, 0); err != nil {
		t.Fatalf("subagent pid %d died during daemon upgrade: %v", subPID, err)
	}
	if frameBefore.PID > 0 {
		if err := syscall.Kill(frameBefore.PID, 0); err != nil {
			t.Fatalf("pane process pid %d died during daemon upgrade: %v", frameBefore.PID, err)
		}
	}

	// 2. Shared HTTP MCP server must still answer on the EXACT SAME port!
	resp, err := http.Get(stBefore.MCPURL + "/health")
	if err != nil {
		t.Fatalf("GET %s/health after upgrade: %v", stBefore.MCPURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP /health status after upgrade = %d", resp.StatusCode)
	}

	// 3. Telemetry must survive upgrade.
	tel, err := client.Telemetry(context.Background(), "live1")
	if err != nil || tel.Telemetry["live1"] == nil || tel.Telemetry["live1"].Summary != "Running subagent" {
		t.Fatalf("telemetry after upgrade = %+v, err=%v", tel, err)
	}

	// 4. Writing to the live pane after upgrade still flows to cat and echoes back!
	if err := host.Write("live1", []byte("AFTER_UPGRADE_ALIVE\n")); err != nil {
		t.Fatalf("Write after upgrade: %v", err)
	}
	waitSnapshotContains(t, host, "live1", "AFTER_UPGRADE_ALIVE", 4*time.Second)

	// Cleanly close pane (kills pane process) and verify subagent cleanup.
	if err := host.Close("live1"); err != nil {
		t.Fatalf("Close after upgrade: %v", err)
	}
}
