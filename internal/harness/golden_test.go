package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The goldens pin what the built-ins produce: every launch's argv, typed
// setting commands and pane calls (testdata/launch.golden.json), and the
// built-in rows themselves (testdata/builtins.golden.json). A change to
// the built-in data shows as a diff of these files; GOLDEN_UPDATE=1
// rewrites them.

func checkGolden(t *testing.T, name string, got any) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path := filepath.Join("testdata", name)
	if os.Getenv("GOLDEN_UPDATE") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (GOLDEN_UPDATE=1 writes it)", err)
	}
	if !bytes.Equal(b, want) {
		t.Fatalf("%s differs from what the built-ins produce now:\n%s", path, b)
	}
}

// TestBuiltinLaunchesAreGolden launches every built-in with each model,
// prompt and setting combination and compares the flow with the golden.
func TestBuiltinLaunchesAreGolden(t *testing.T) {
	type launch struct {
		Harness  string            `json:"harness"`
		Model    string            `json:"model,omitempty"`
		Prompt   string            `json:"prompt,omitempty"`
		Settings map[string]string `json:"settings,omitempty"`
		Error    string            `json:"error,omitempty"`
		Result   *Result           `json:"result,omitempty"`
		Calls    []string          `json:"calls,omitempty"`
	}
	specs := []struct {
		model, prompt string
		settings      map[string]string
	}{
		{"", "", nil},
		{"", "fix the test", nil},
		{"sonnet", "fix it", nil},
		{"openai/sol", "", nil},
		{"", "go", map[string]string{SettingEffort: "high"}},
		{"", "go", map[string]string{SettingFast: "on", SettingPermission: "plan"}},
		{"", "", map[string]string{SettingPermission: "acceptEdits"}},
		{"", "go", map[string]string{SettingEffort: "minimal"}},
	}
	var out []launch
	for _, h := range Builtins() {
		for _, s := range specs {
			row := launch{Harness: h.Name, Model: s.model, Prompt: s.prompt, Settings: s.settings}
			model, err := h.Model(s.model)
			if err != nil {
				row.Error = err.Error()
				out = append(out, row)
				continue
			}
			d := &driver{}
			l := &Launcher{Driver: d, Delay: time.Second, Settle: time.Millisecond, Sleep: func(context.Context, time.Duration) error { return nil }}
			res, err := l.Launch(context.Background(), h, Spec{Session: "s", Name: "n", Cwd: "/src", Model: model, Prompt: s.prompt, Settings: s.settings})
			if err != nil {
				row.Error = err.Error()
			} else {
				row.Result = &res
			}
			row.Calls = d.calls
			out = append(out, row)
		}
	}
	checkGolden(t, "launch.golden.json", out)
}

// TestBuiltinRowsAreGolden pins the built-in rows field by field.
func TestBuiltinRowsAreGolden(t *testing.T) {
	list := Builtins()
	for _, h := range list {
		if err := h.Validate(); err != nil {
			t.Fatal(fmt.Errorf("built-in %s: %w", h.Name, err))
		}
	}
	checkGolden(t, "builtins.golden.json", list)
}
