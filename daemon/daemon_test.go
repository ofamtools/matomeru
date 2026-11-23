package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
