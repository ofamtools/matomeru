package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHarnessLaunchWithPromptAllHarnesses(t *testing.T) {
	emptyDir := t.TempDir()

	cases := []struct {
		harness       string
		customCmd     string
		prompt        string
		wantCmd       string
		wantDelivered bool
	}{
		{harness: "claude", prompt: "test", wantCmd: "claude test", wantDelivered: true},
		{harness: "codex", prompt: "test", wantCmd: "codex test", wantDelivered: true},
		{harness: "agy", prompt: "test", wantCmd: "agy -i test", wantDelivered: true},
		{harness: "antigravity", prompt: "test", wantCmd: "agy -i test", wantDelivered: true},
		{harness: "omp", prompt: "test", wantCmd: "omp test", wantDelivered: true},
		{harness: "gemini", prompt: "test", wantCmd: "gemini -i test", wantDelivered: true},
		{harness: "opencode", prompt: "test", wantCmd: "opencode --prompt test", wantDelivered: true},
		{harness: "pi", prompt: "test", wantCmd: "pi test", wantDelivered: true},
		{harness: "zsh", prompt: "test", wantCmd: "zsh", wantDelivered: false},
		{harness: "claude", prompt: "", wantCmd: "claude", wantDelivered: false},
		{harness: "claude", customCmd: "claude --resume", prompt: "test", wantCmd: "claude --resume", wantDelivered: false},
		{harness: "agy", prompt: "hello 'world'\n$USER", wantCmd: "agy -i 'hello '\\''world'\\''\n$USER'", wantDelivered: true},
	}

	for _, tc := range cases {
		cmd, _, delivered, err := ResolveHarnessLaunchWithPrompt(emptyDir, tc.harness, "", "p-1", tc.customCmd, tc.prompt)
		if err != nil {
			t.Fatalf("ResolveHarnessLaunchWithPrompt(%q, %q): %v", tc.harness, tc.prompt, err)
		}
		if cmd != tc.wantCmd || delivered != tc.wantDelivered {
			t.Errorf("harness=%q prompt=%q: got (%q, delivered=%v), want (%q, delivered=%v)",
				tc.harness, tc.prompt, cmd, delivered, tc.wantCmd, tc.wantDelivered)
		}
	}

	// Verify user harnesses.json overrides are honored alongside built-in prompt delivery.
	cfgDir := t.TempDir()
	raw := `{"harnesses":{"claude":{"command":["claude","--model","opus"]},"agy":{"command":["agy","--mode","auto"]}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "harnesses.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, _, delivered, err := ResolveHarnessLaunchWithPrompt(cfgDir, "claude", "", "p-1", "", "test")
	if err != nil || !delivered || cmd != "claude --model opus test" {
		t.Fatalf("claude config override = (%q, delivered=%v, err=%v)", cmd, delivered, err)
	}
	cmd, _, delivered, err = ResolveHarnessLaunchWithPrompt(cfgDir, "agy", "", "p-1", "", "test")
	if err != nil || !delivered || cmd != "agy --mode auto -i test" {
		t.Fatalf("agy config override = (%q, delivered=%v, err=%v)", cmd, delivered, err)
	}
}
