package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

func startTestDaemon(t *testing.T, store *data.Store) (*daemon.Server, *daemon.Client) {
	t.Helper()
	// Use a short directory under /tmp so Unix socket paths stay well below macOS's 104-byte limit.
	sockDir, err := os.MkdirTemp("/tmp", "mtm-w2-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "d.sock")

	srv, err := daemon.New(daemon.Config{
		SocketPath:        sockPath,
		Store:             store,
		SchedulerInterval: -1, // tests drive Scheduler.Tick directly
		ApprovalTimeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background())
	})
	return srv, daemon.NewClient(sockPath)
}

type recordingEnvHost struct {
	lastID   string
	lastCmd  string
	lastCwd  string
	lastEnv  map[string]string
	sessions map[string]ptyhost.Frame
}

func newRecordingEnvHost() *recordingEnvHost {
	return &recordingEnvHost{sessions: map[string]ptyhost.Frame{}}
}

func (h *recordingEnvHost) Open(id, cmd, cwd string, cols, rows int) error {
	return h.OpenWithEnv(id, cmd, cwd, cols, rows, nil)
}

func (h *recordingEnvHost) OpenWithEnv(id, cmd, cwd string, cols, rows int, env map[string]string) error {
	h.lastID = id
	h.lastCmd = cmd
	h.lastCwd = cwd
	h.lastEnv = map[string]string{}
	for k, v := range env {
		h.lastEnv[k] = v
	}
	h.sessions[id] = ptyhost.Frame{Cols: cols, Rows: rows, Revision: 1, Lines: []string{"ok"}, Plain: []string{"ok"}}
	return nil
}

func (h *recordingEnvHost) Write(string, []byte) error    { return nil }
func (h *recordingEnvHost) Resize(string, int, int) error { return nil }
func (h *recordingEnvHost) Snapshot(id string) (ptyhost.Frame, bool) {
	f, ok := h.sessions[id]
	return f, ok
}
func (h *recordingEnvHost) Close(id string) error {
	delete(h.sessions, id)
	return nil
}
func (h *recordingEnvHost) CloseAll() { h.sessions = map[string]ptyhost.Frame{} }

func TestWave2DaemonResetAndStartupReconciliation(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")

	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	_, client := startTestDaemon(t, store)

	a, err := New(store, Options{Live: true, Daemon: client, Watch: time.Hour, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	if a.rt.Daemon != client {
		t.Fatal("setupLive did not store opts.Daemon on App")
	}
	if _, ok := a.panes.Host.(*daemon.HostAdapter); !ok {
		t.Fatalf("setupLive did not wire daemon.HostAdapter: got %T", a.panes.Host)
	}
	d := NewDriver(a, 120, 36)

	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":   "survivor",
		"harness": "sh",
		"command": "echo daemon-alive; exec cat",
		"cwd":     t.TempDir(),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	wake(d)
	d.Keys("1", "enter", "enter")
	if a.Mode() != ModeTerminal || !a.hasSession(pane.ID) {
		t.Fatalf("failed to attach daemon pane: mode=%s status=%q", a.Mode(), a.Status())
	}
	eventually(t, "daemon pane output", func() bool {
		f, ok := a.panes.Host.Snapshot(pane.ID)
		return ok && slices.ContainsFunc(f.Plain, func(l string) bool { return strings.TrimSpace(l) == "daemon-alive" })
	})

	// Closing the app leaves the daemon pane alive so a fresh App reconciles and stays attached.
	a.Close()
	b, err := New(store, Options{Live: true, Daemon: client, Watch: time.Hour, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	bd := NewDriver(b, 120, 36)

	if len(b.rail().Rows) != 1 || b.rail().Rows[0].Ref.ID != pane.ID {
		t.Fatalf("surviving daemon pane missing after restart: rows=%d", len(b.rail().Rows))
	}
	if !b.hasSession(pane.ID) {
		t.Fatal("surviving daemon pane did not remain attached on restart")
	}

	// Run daemon.reset via default binding (g R) and confirm via y.
	bd.Keys("1", "g", "R")
	if b.Modal() != "confirm" || b.Mode() != ModeConfirm {
		t.Fatalf("g R did not open confirmation modal: modal=%q mode=%q status=%q", b.Modal(), b.Mode(), b.Status())
	}
	bd.Keys("y")
	if !strings.Contains(b.Status(), "daemon reset") {
		t.Fatalf("daemon.reset status = %q", b.Status())
	}
	panesAfter, err := client.List(context.Background())
	if err != nil || len(panesAfter) != 0 {
		t.Fatalf("daemon still has panes after reset: panes=%v err=%v", panesAfter, err)
	}
	if len(b.rt.live.frames) != 0 {
		t.Fatalf("local frame cache not cleared: %v", b.rt.live.frames)
	}
	rec, err := store.Get(pane.Ref())
	if err != nil || !rec.IsArchived() || rec.Text("runStatus") != "exited" {
		t.Fatalf("dropped pane not archived/exited: rec=%+v err=%v", rec, err)
	}
	if len(b.rail().Rows) != 0 {
		t.Fatalf("rail still shows panes after reset: %d", len(b.rail().Rows))
	}

	// Simulate stale active pane in store while daemon has 0 live panes (e.g. after external reset).
	stale, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":     "stale-pane",
		"runStatus": "running",
		"cwd":       t.TempDir(),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(store, Options{Live: true, Daemon: client, Watch: time.Hour, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if len(c.rail().Rows) != 0 {
		t.Fatalf("startup with 0 live daemon panes should open with 0 pre-listed panes, got %d", len(c.rail().Rows))
	}
	staleAfter, err := store.Get(stale.Ref())
	if err != nil || !staleAfter.IsArchived() {
		t.Fatalf("stale pane was not archived on startup reconciliation: rec=%+v err=%v", staleAfter, err)
	}
}

func TestWave2HarnessDiscoveryFormChoicesAndLaunch(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")

	cfgDir := t.TempDir()
	harnessesJSON := `{
		"default": "myagent",
		"harnesses": {
			"myagent": {
				"name": "myagent",
				"label": "My Custom Agent",
				"command": ["my-agent-bin", "--interactive"],
				"hookArgs": ["--hooks", "mtm-hook"],
				"mcpFlag": ["--mcp-url", "{{mcp_url}}", "--pane", "{{pane_id}}"],
				"env": {
					"MY_AGENT_PROFILE": "wave2",
					"MY_AGENT_HOOKS": "enabled"
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(cfgDir, harnessesFileName), []byte(harnessesJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	oaiPayload := map[string]any{
		"items": []map[string]any{
			{"kind": "ai.harness", "id": "claude", "fields": map[string]any{"id": "claude", "installed": true, "executable": "/usr/local/bin/claude"}},
			{"kind": "ai.harness", "id": "codex", "fields": map[string]any{"id": "codex", "installed": true, "executable": "/usr/local/bin/codex"}},
			{"kind": "ai.harness", "id": "gemini", "fields": map[string]any{"id": "gemini", "installed": false}},
		},
		"total": 3,
	}
	rawPayload, _ := json.Marshal(oaiPayload)
	script(t, binDir, "o-ai", fmt.Sprintf("cat <<'EOF'\n%s\nEOF\n", string(rawPayload)))

	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	srv, client := startTestDaemon(t, store)
	recHost := newRecordingEnvHost()

	a, err := New(store, Options{
		ConfigDir:     cfgDir,
		Live:          true,
		ExtensionDirs: []string{binDir},
		Daemon:        client,
		Host:          recHost,
		Watch:         time.Hour,
		FS:            smokeFS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// Wait for the async o-ai background discovery goroutine and deliver its wakeup.
	a.WaitBackground()
	wake(d)

	gotIDs := make([]string, 0, len(a.Harnesses()))
	for _, e := range a.Harnesses() {
		gotIDs = append(gotIDs, e.ID)
	}
	wantIDs := []string{"myagent", "zsh", "claude", "codex"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("merged harnesses = %v, want %v", gotIDs, wantIDs)
	}

	// Open New Agent (n) on 1 Panes and verify dynamic agent harness choices (zsh excluded).
	d.Keys("1", "n")
	rf := a.activeRecordForm()
	if a.Modal() != "form" || rf == nil || rf.kind != data.KindPane || rf.Model().Title != "New Agent" {
		t.Fatalf("n on 1 Panes did not open New Agent form: modal=%q form=%+v", a.Modal(), rf)
	}
	var harnessSpec core.ParamSpec
	found := false
	for _, sp := range rf.Model().Specs {
		if sp.Name == "harness" {
			harnessSpec = sp
			found = true
			break
		}
	}
	wantAgentIDs := []string{"myagent", "claude", "codex"}
	if !found || !slices.Equal(harnessSpec.Enum, wantAgentIDs) {
		t.Fatalf("New Agent harness enum = %v (found=%v), want %v", harnessSpec.Enum, found, wantAgentIDs)
	}
	if gotDefault := data.Text(rf.Model().Values["harness"]); gotDefault != "myagent" {
		t.Fatalf("New Agent default harness = %q, want %q", gotDefault, "myagent")
	}
	d.Keys("esc")

	// Unfiltered record.new {kind: "pane"} keeps the full merged harness list including zsh.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:     views.CmdNew,
		Args:   map[string]any{"kind": data.KindPane},
		Source: "test",
		Actor:  User,
	}})
	rf = a.activeRecordForm()
	if a.Modal() != "form" || rf == nil || rf.kind != data.KindPane {
		t.Fatalf("record.new pane did not open form: modal=%q form=%+v", a.Modal(), rf)
	}
	found = false
	for _, sp := range rf.Model().Specs {
		if sp.Name == "harness" {
			harnessSpec = sp
			found = true
			break
		}
	}
	if !found || !slices.Equal(harnessSpec.Enum, wantIDs) {
		t.Fatalf("New pane harness enum = %v (found=%v), want %v", harnessSpec.Enum, found, wantIDs)
	}
	d.Keys("esc")

	// Create a pane with harness="myagent" and empty command, then attach it.
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":   "custom agent pane",
		"harness": "myagent",
		"cwd":     t.TempDir(),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.attach(*pane); err != nil {
		t.Fatal(err)
	}
	wantMCP := srv.MCPURL() + "/mcp/" + pane.ID
	wantCmd := fmt.Sprintf("my-agent-bin --interactive --hooks mtm-hook --mcp-url %s --pane %s", wantMCP, pane.ID)
	if recHost.lastCmd != wantCmd {
		t.Fatalf("BuildLaunchCommand cmd = %q, want %q", recHost.lastCmd, wantCmd)
	}
	if recHost.lastEnv["MY_AGENT_PROFILE"] != "wave2" || recHost.lastEnv["MY_AGENT_HOOKS"] != "enabled" {
		t.Fatalf("harness env not merged into OpenWithEnv: %v", recHost.lastEnv)
	}
	if recHost.lastEnv["MTM_PANE_ID"] != pane.ID {
		t.Fatalf("MTM_PANE_ID = %q, want %q", recHost.lastEnv["MTM_PANE_ID"], pane.ID)
	}
}

func TestWave2LiveTelemetryAndBlockingApprovals(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	_, client := startTestDaemon(t, store)

	// Create two panes with fixed promptedAt timestamps so p1 is first and p2 is second.
	now := time.Now().UTC()
	p1, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":      "alpha-pane",
		"harness":    "claude",
		"promptedAt": now.Format(data.TimeFormat),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":      "beta-pane",
		"harness":    "codex",
		"promptedAt": now.Add(-time.Minute).Format(data.TimeFormat),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Daemon: client, Watch: time.Hour, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	railOrder := func() []string {
		rows := a.rail().Rows
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = r.Ref.ID
		}
		return ids
	}
	if got := railOrder(); !slices.Equal(got, []string{p1.ID, p2.ID}) {
		t.Fatalf("initial rail order = %v, want [%s %s]", got, p1.ID, p2.ID)
	}

	// Send non-blocking hook telemetry for p2 (state, active tool, summary, 2 active subagents).
	ctx := context.Background()
	for _, ev := range []daemon.HookEvent{
		{PaneID: p2.ID, Harness: "codex", Kind: daemon.HookKindActivity, State: daemon.StateRunning, Tool: "Bash", Summary: "running unit tests"},
		{PaneID: p2.ID, Harness: "codex", Kind: daemon.HookKindSubagent, SubagentID: "sub-1", SubagentAction: daemon.SubagentStart, Summary: "worker 1"},
		{PaneID: p2.ID, Harness: "codex", Kind: daemon.HookKindSubagent, SubagentID: "sub-2", SubagentAction: daemon.SubagentStart, Summary: "worker 2"},
	} {
		if _, err := client.HookEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	// WatchCheck wakes once on telemetry change, and stays silent when unchanged.
	if !a.WatchCheck() {
		t.Fatal("WatchCheck did not detect telemetry change")
	}
	if a.WatchCheck() {
		t.Fatal("WatchCheck reported a second change when telemetry was unchanged")
	}
	<-a.rt.live.wake
	wake(d)

	// Hook telemetry must NEVER reorder the pane list.
	if got := railOrder(); !slices.Equal(got, []string{p1.ID, p2.ID}) {
		t.Fatalf("hook telemetry reordered pane list: got %v, want [%s %s]", got, p1.ID, p2.ID)
	}

	// Select p2 and verify minimal hook indicator (hook fired + which hook, NO verbose tool/summary dump)
	// plus rail metadata renders cleanly.
	d.Keys("1", "j")
	frame := d.Frame()
	if !strings.Contains(frame, "Hook: ") || !strings.Contains(frame, "[Bash +2]") {
		t.Fatalf("1 Panes missing minimal hook indicator or rail metadata:\n%s", frame)
	}
	if strings.Contains(frame, "tool: Bash") || strings.Contains(frame, "2 subagents") {
		t.Fatalf("minimal hook indicator should strip verbose tool/subagent dumps by default:\n%s", frame)
	}

	// Select p1 and trigger a real blocking PermissionRequest approval call on the test daemon.
	d.Keys("k")
	type hookRes struct {
		res *daemon.HookEventResult
		err error
	}
	allowCh := make(chan hookRes, 1)
	go func() {
		r, err := client.HookEvent(context.Background(), daemon.HookEvent{
			PaneID:    p1.ID,
			Harness:   "claude",
			Kind:      daemon.HookKindApproval,
			Tool:      "Bash",
			Summary:   "rm -rf /tmp/demo",
			RequestID: "req-allow-1",
			TimeoutMs: 5000,
		})
		allowCh <- hookRes{res: r, err: err}
	}()

	eventually(t, "pending approval req-allow-1", func() bool {
		a.WatchCheck()
		return len(a.pendingApprovalsFor(p1.ID)) == 1
	})
	wake(d)

	approvalFrame := d.Frame()
	for _, want := range []string{"Permission Request", "Tool: Bash", "rm -rf /tmp/demo", "needs_you", "Y approve"} {
		if !strings.Contains(approvalFrame, want) {
			t.Fatalf("approval banner missing %q:\n%s", want, approvalFrame)
		}
	}

	// Press Y (pane.approve) and verify the blocking HookEvent unblocks with "allow".
	d.Keys("Y")
	select {
	case got := <-allowCh:
		if got.err != nil || got.res == nil || got.res.Decision != daemon.DecisionAllow {
			t.Fatalf("pane.approve result = %+v, err = %v", got.res, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocking approval to unblock on pane.approve")
	}
	if len(a.pendingApprovalsFor(p1.ID)) != 0 {
		t.Fatal("pending approval still cached after pane.approve")
	}
	if pt := a.paneTelemetry(p1.ID); pt == nil || pt.State != daemon.StateRunning {
		t.Fatalf("telemetry after approve = %+v, want state=running", pt)
	}

	// Now test pane.deny (D) on a second real blocking PermissionRequest.
	denyCh := make(chan hookRes, 1)
	go func() {
		r, err := client.HookEvent(context.Background(), daemon.HookEvent{
			PaneID:    p1.ID,
			Harness:   "claude",
			Kind:      daemon.HookKindApproval,
			Tool:      "Write",
			Summary:   "overwrite /etc/hosts",
			RequestID: "req-deny-2",
			TimeoutMs: 5000,
		})
		denyCh <- hookRes{res: r, err: err}
	}()

	eventually(t, "pending approval req-deny-2", func() bool {
		a.WatchCheck()
		return len(a.pendingApprovalsFor(p1.ID)) == 1
	})
	wake(d)
	if !strings.Contains(d.Frame(), "overwrite /etc/hosts") {
		t.Fatalf("second approval banner not shown:\n%s", d.Frame())
	}

	d.Keys("D")
	select {
	case got := <-denyCh:
		if got.err != nil || got.res == nil || got.res.Decision != daemon.DecisionDeny {
			t.Fatalf("pane.deny result = %+v, err = %v", got.res, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocking approval to unblock on pane.deny")
	}
	if len(a.pendingApprovalsFor(p1.ID)) != 0 {
		t.Fatal("pending approval still cached after pane.deny")
	}
	if pt := a.paneTelemetry(p1.ID); pt == nil || pt.State != daemon.StateIdle {
		t.Fatalf("telemetry after deny = %+v, want state=idle", pt)
	}
}

func TestWave2SchedulePauseResumeMatchesDaemonScheduler(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	fixedNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	schedRec, _, err := store.Create(data.CreateInput{Kind: data.KindSchedule, Fields: map[string]any{
		"title":        "Hourly health check",
		"scheduleKind": "run",
		"cron":         "0 * * * *",
		"command":      "echo ok",
		"nextDueAt":    fixedNow.Add(-time.Minute).Format(data.TimeFormat),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	var ran atomic.Int32
	sched := daemon.NewScheduler(daemon.SchedulerConfig{
		Store: store,
		Clock: func() time.Time { return fixedNow },
		Runner: daemon.RunnerFunc(func(context.Context, string) (string, error) {
			ran.Add(1)
			return "ok", nil
		}),
	})

	a, err := New(store, Options{FS: smokeFS(), Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// Browse schedules in Work (g n -> schedules).
	d.Keys("g", "n", "/")
	d.Type("schedules")
	d.Keys("enter")
	if a.workKind() != data.KindSchedule {
		t.Fatalf("workKind = %q, want schedule", a.workKind())
	}

	// Press p -> schedule.pause.
	d.Keys("p")
	rec, err := store.Get(schedRec.Ref())
	if err != nil || !rec.Bool("paused") {
		t.Fatalf("schedule.pause did not set paused=true: fields=%v err=%v status=%q", rec.Fields, err, a.Status())
	}
	if !strings.Contains(d.Frame(), "paused") {
		t.Fatalf("paused badge not shown on schedule:\n%s", d.Frame())
	}
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 0 {
		t.Fatalf("daemon scheduler fired a paused schedule (%d runs)", ran.Load())
	}

	// Press p again -> schedule.resume.
	d.Keys("p")
	rec, err = store.Get(schedRec.Ref())
	if err != nil || rec.Bool("paused") {
		t.Fatalf("schedule.resume did not clear paused: fields=%v err=%v status=%q", rec.Fields, err, a.Status())
	}
	if err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 1 {
		t.Fatalf("daemon scheduler did not fire resumed schedule: ran=%d", ran.Load())
	}
}

func TestWave2BindingsCheckZeroErrors(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
	specs := CommandSpecs()
	for _, want := range []string{CmdDaemonReset, CmdPaneApprove, CmdPaneDeny, CmdSchedulePause, CmdScheduleResume} {
		if !slices.ContainsFunc(specs, func(s core.CommandSpec) bool { return s.ID == want }) {
			t.Errorf("CommandSpecs missing %q", want)
		}
	}
}
