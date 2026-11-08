package main

import (
	"bytes"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/harness"
)

// A claude launch carries the hook settings on its command line, so the agent
// reports through mtm agent hook with no global settings file; agent.hooks
// off launches it unchanged, and a harness with no hookArgs never gets any.
func TestLaunchCarriesHookSettingsUntilTheSettingIsOff(t *testing.T) {
	t.Parallel()
	app := open(t)
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
