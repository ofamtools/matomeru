package main

import (
	"bytes"
	"strings"
	"testing"

	"ofam.dev/matomeru/agenthook"
)

func TestHookEntrypointSmoke(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := agenthook.RunWithEnv([]string{"--help"}, func(string) string { return "" }, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("help exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "mtm-hook") {
		t.Fatalf("expected usage text, got %q", stdout.String())
	}
}
