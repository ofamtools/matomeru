package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/harness"
)

// A claude launch carries the hook settings on its command line, so the agent
// reports through mtm agent hook with no global settings file; agent.hooks
// off launches it unchanged, and a harness with no hookArgs never gets any.
func TestLaunchCarriesHookSettingsUntilTheSettingIsOff(t *testing.T) {
	t.Parallel()
	app := open(t)
	home := t.TempDir()
	app.home = func() (string, error) { return home, nil }
	app.executable = func() (string, error) { return "/opt/it's mtm/mtm", nil }
	h, ok := app.harnesses.Get("claude")
	if !ok {
		t.Fatal("no claude harness")
	}
	flags := app.hookFlags(h)
	if len(flags) != 2 || flags[0] != "--settings" || !strings.Contains(flags[1], `'/opt/it'\\''s mtm/mtm' agent hook claude-code --integration 3`) {
		t.Fatalf("flags = %q", flags)
	}
	if got := app.hookFlags(harness.Harness{Name: "x", Command: []string{"x"}}); got != nil {
		t.Fatalf("a harness with no hookArgs got %q", got)
	}
	if err := app.r.Settings().Set(settingAgentHooks, false); err != nil {
		t.Fatal(err)
	}
	if got := app.hookFlags(h); got != nil {
		t.Fatalf("agent.hooks off still gave %q", got)
	}
}

func TestAgentShellTakesOnlyItsOwnWords(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	for _, words := range [][]string{{"agent", "launch"}, {"agent"}, {"project", "hook"}, {}} {
		if _, handled := agentShell(words, Options{}, strings.NewReader(""), &out, &errOut); handled {
			t.Fatalf("%q was taken by the agent shell", words)
		}
	}
	for _, word := range []string{"hook", "statusline", "state"} {
		out.Reset()
		code, handled := agentShell([]string{"agent", word, "-h"}, Options{}, strings.NewReader(""), &out, &errOut)
		if !handled || code != 0 || !strings.Contains(out.String(), "usage: mtm agent "+word) {
			t.Fatalf("agent %s -h: handled %v code %d out %q", word, handled, code, out.String())
		}
	}
}

// codexHooks asks `codex app-server` (hooks/list, no model session) in a temp
// CODEX_HOME which hooks flags declare, returning event -> trustStatus.
func codexHooks(t *testing.T, flags []string) map[string]string {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	cmd := exec.Command(codexBin, append(slices.Clone(flags), "app-server")...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Skip("no codex: ", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for _, m := range []string{
		`{"id":1,"method":"initialize","params":{"clientInfo":{"name":"mtm-test","version":"0"}}}`,
		`{"method":"initialized"}`,
		`{"id":2,"method":"hooks/list","params":{"cwds":[` + strconv.Quote(cwd) + `]}}`,
	} {
		fmt.Fprintln(in, m)
	}
	got := map[string]string{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(out)
		sc.Buffer(nil, 1<<22)
		for sc.Scan() {
			var m struct {
				ID     int `json:"id"`
				Result struct {
					Data []struct {
						Hooks []struct{ EventName, TrustStatus string }
					}
				}
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID != 2 {
				continue
			}
			for _, d := range m.Result.Data {
				for _, h := range d.Hooks {
					got[h.EventName] = h.TrustStatus
				}
			}
			return
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("codex app-server hooks/list timed out")
	}
	return got
}

// A codex launch carries one command-line hook per event plus the trust
// hashes, so Codex lists every one trusted (not untrusted) with no global
// hooks file. Checked against the installed codex's own hooks/list.
func TestCodexLaunchHooksAreTrusted(t *testing.T) {
	t.Parallel()
	if codexBin == "" {
		t.Skip("codex is not installed")
	}
	app := open(t)
	home := t.TempDir()
	app.home = func() (string, error) { return home, nil }
	app.executable = func() (string, error) { return "/opt/it's mtm/mtm", nil }
	h, _ := app.harnesses.Get("codex")
	flags := app.hookFlags(h)
	if len(flags) != 2*len(h.Hooks.Events)+2 {
		t.Fatalf("flags = %q", flags)
	}
	got := codexHooks(t, flags)
	for _, ev := range h.Hooks.Events {
		name := strings.ToLower(ev.Name[:1]) + ev.Name[1:]
		if got[name] != "trusted" {
			t.Errorf("%s: trustStatus %q, want trusted (all: %v)", name, got[name], got)
		}
	}
	// Without the trust words, the same hooks are untrusted: the test would
	// not see the difference if the hash were ignored.
	if bare := codexHooks(t, flags[:len(flags)-2]); bare["sessionStart"] != "untrusted" {
		t.Fatalf("hooks without a trust hash are %q", bare["sessionStart"])
	}
}

// An omp launch carries the extension mtm writes (-e FILE, with mtm's path
// in it) unless the global extension is installed; agent.hooks off adds none.
func TestOmpLaunchCarriesTheExtensionUnlessInstalled(t *testing.T) {
	t.Parallel()
	app := open(t)
	home := t.TempDir()
	app.home = func() (string, error) { return home, nil }
	app.executable = func() (string, error) { return `/opt/mtm "x"/mtm`, nil }
	h, _ := app.harnesses.Get("omp")
	flags := app.hookFlags(h)
	if len(flags) != 2 || flags[0] != "-e" {
		t.Fatalf("flags = %q", flags)
	}
	body, err := os.ReadFile(flags[1])
	if err != nil || !strings.Contains(string(body), `const HOOK = ["/opt/mtm \"x\"/mtm","agent","hook"];`) {
		t.Fatalf("extension %s: %v\n%s", flags[1], err, body)
	}
	ext := filepath.Join(home, ".omp", "agent", "extensions")
	if err := os.MkdirAll(ext, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "mtm-omp-agent-state.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := app.hookFlags(h); got != nil {
		t.Fatalf("the global extension is installed, got %q", got)
	}
	if err := os.Remove(filepath.Join(ext, "mtm-omp-agent-state.ts")); err != nil {
		t.Fatal(err)
	}
	if err := app.r.Settings().Set(settingAgentHooks, false); err != nil {
		t.Fatal(err)
	}
	if got := app.hookFlags(h); got != nil {
		t.Fatalf("agent.hooks off still gave %q", got)
	}
}

// A harness whose global config already runs mtm's hook gets no per-launch
// hooks (nothing reports twice); a config without it, or none, gets them.
func TestLaunchSkipsHooksTheGlobalConfigAlreadyRuns(t *testing.T) {
	t.Parallel()
	cases := []struct{ harness, file, with, without string }{
		{"claude", ".claude/settings.json", `{"hooks":{"Stop":[{"hooks":[{"command":"mtm agent hook claude-code --integration 3"}]}]}}`, `{"hooks":{"Stop":[{"hooks":[{"command":"other-tool"}]}]}}`},
		{"codex", ".codex/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"command":"/x/mtm-hook codex --integration 1"}]}]}}`, `{"hooks":{}}`},
		{"omp", ".omp/agent/extensions/mtm-omp-agent-state.ts", "x", ""},
	}
	for _, c := range cases {
		app := open(t)
		home := t.TempDir()
		app.home = func() (string, error) { return home, nil }
		app.executable = func() (string, error) { return "/opt/mtm", nil }
		h, ok := app.harnesses.Get(c.harness)
		if !ok {
			t.Fatal(c.harness)
		}
		if len(app.hookFlags(h)) == 0 {
			t.Fatalf("%s: no global config, no per-launch hooks", c.harness)
		}
		path := filepath.Join(home, c.file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if c.without != "" {
			os.WriteFile(path, []byte(c.without), 0o644)
			if len(app.hookFlags(h)) == 0 {
				t.Fatalf("%s: a config without the hook still skipped them", c.harness)
			}
		}
		os.WriteFile(path, []byte(c.with), 0o644)
		if got := app.hookFlags(h); got != nil {
			t.Fatalf("%s: the global config runs the hook, got %q", c.harness, got)
		}
	}
}
