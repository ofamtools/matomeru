package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
)

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := run(args, bytes.NewReader(nil), &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func TestHookCLIAddListTestRemove(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("MTM_CONFIG_DIR", cfg)
	outFile := filepath.Join(t.TempDir(), "event.json")
	t.Setenv("HOOK_OUT", outFile)
	sock := filepath.Join(t.TempDir(), "absent.sock") // no daemon: file rules only

	if _, stderr, code := runCLI(t, "hook", "add", "--id", "notify", "--harness", "codex", "--once", "--run", `cat > "$HOOK_OUT"`, "--socket", sock); code != 0 {
		t.Fatalf("hook add: %s", stderr)
	}
	if _, _, code := runCLI(t, "hook", "add", "--id", "empty", "--socket", sock); code == 0 {
		t.Fatal("hook add without --run/--argv/--webhook succeeded")
	}
	rules, err := daemon.LoadHookRules(daemon.HooksPath(cfg))
	if err != nil || len(rules) != 1 || !rules[0].Once || rules[0].Match.Harness != "codex" || rules[0].Run.Command[0] != "/bin/sh" {
		t.Fatalf("hooks.json = %+v (%v)", rules, err)
	}

	stdout, stderr, code := runCLI(t, "hook", "list", "--json", "--socket", sock)
	var listed []daemon.HookRule
	if code != 0 || json.Unmarshal([]byte(stdout), &listed) != nil || len(listed) != 1 || listed[0].Source != daemon.HookSourceFile {
		t.Fatalf("hook list = %d %q %q", code, stdout, stderr)
	}

	if _, stderr, code := runCLI(t, "hook", "test", "notify", "--pane", "p9", "--socket", sock); code != 0 {
		t.Fatalf("hook test: %s", stderr)
	}
	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	var ev daemon.AgentDoneEvent
	if err := json.Unmarshal(raw, &ev); err != nil || ev.PaneID != "p9" || ev.Event != daemon.EventAgentDone {
		t.Fatalf("delivered %s (%v)", raw, err)
	}

	if _, stderr, code := runCLI(t, "hook", "remove", "notify", "--socket", sock); code != 0 {
		t.Fatalf("hook remove: %s", stderr)
	}
	if rules, _ := daemon.LoadHookRules(daemon.HooksPath(cfg)); len(rules) != 0 {
		t.Fatalf("rule not removed: %+v", rules)
	}
}

func TestAgentWaitCLIUnblocksOnAgentDone(t *testing.T) {
	t.Setenv("MTM_CONFIG_DIR", t.TempDir())
	_, client := startInProcessDaemon(t, false)
	sock := client.SocketPath()
	ctx := context.Background()

	if _, stderr, code := runCLI(t, "hook", "add", "--memory", "--id", "mem", "--argv", "true", "--socket", sock); code != 0 {
		t.Fatalf("hook add --memory: %s", stderr)
	}
	if _, err := client.HookEvent(ctx, daemon.HookEvent{PaneID: "pw", Kind: daemon.HookKindState, State: daemon.StateRunning}); err != nil {
		t.Fatal(err)
	}
	type res struct {
		out, err string
		code     int
	}
	done := make(chan res, 1)
	go func() {
		o, e, c := runCLI(t, "agent", "wait", "--pane", "pw", "--timeout", "5s", "--json", "--socket", sock)
		done <- res{o, e, c}
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := client.HookEvent(ctx, daemon.HookEvent{PaneID: "pw", Kind: daemon.HookKindState, State: daemon.StateDone, Summary: "shipped"}); err != nil {
		t.Fatal(err)
	}
	r := <-done
	var ev daemon.AgentDoneEvent
	if r.code != 0 || json.Unmarshal([]byte(r.out), &ev) != nil || ev.PaneID != "pw" || ev.Summary != "shipped" {
		t.Fatalf("agent wait = %d %q %q", r.code, r.out, r.err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		stdout, _, _ := runCLI(t, "hook", "log", "--json", "--socket", sock)
		if strings.Contains(stdout, `"ruleId": "mem"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hook log never showed the memory rule: %s", stdout)
		}
		time.Sleep(20 * time.Millisecond)
	}

	_, stderr, code := runCLI(t, "agent", "wait", "pw", "--next", "--timeout", "100ms", "--socket", sock)
	if code == 0 || !strings.Contains(stderr, "did not finish within 100ms") {
		t.Fatalf("--next timeout = %d %q", code, stderr)
	}
}
