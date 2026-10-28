package harness

import (
	"slices"
	"strings"
	"testing"
)

func TestBuiltinSettingsRows(t *testing.T) {
	r, err := NewRegistry(Builtins())
	if err != nil {
		t.Fatal(err)
	}
	claude, _ := r.Get("claude")
	for _, c := range []struct{ name, value, text string }{
		{SettingModel, "fable", "/model fable"},
		{SettingEffort, "high", "/effort high"},
		{SettingFast, "on", "/fast on"},
	} {
		s, ok := claude.Setting(c.name)
		if !ok || s.Text(c.value) != c.text {
			t.Errorf("claude %s: %+v %q, want %q", c.name, s, s.Text(c.value), c.text)
		}
	}
	if got := claude.Choices(SettingEffort); !slices.Equal(got, []string{"low", "medium", "high", "xhigh", "max", "auto"}) {
		t.Fatalf("claude effort choices %v", got)
	}
	perm, ok := claude.Setting(SettingPermission)
	if !ok || perm.Key != "BTab" || perm.Command != "" {
		t.Fatalf("claude permission-mode %+v", perm)
	}
	if got := claude.Choices(SettingPermission); got[0] != "default" || !slices.Contains(got, "bypassPermissions") {
		t.Fatalf("permission choices %v", got)
	}
	omp, _ := r.Get("omp")
	if s, _ := omp.Setting(SettingModel); s.Text("openai/gpt-6") != "/switch openai/gpt-6" {
		t.Fatalf("omp model %+v", s)
	}
	if _, ok := omp.Setting(SettingPermission); ok {
		t.Fatal("omp has no permission modes")
	}
	codex, _ := r.Get("codex")
	for _, name := range SettingNames {
		if _, ok := codex.Setting(name); ok {
			t.Fatalf("codex %s: its pickers take no argument", name)
		}
	}
	// The model's choices fall back to the models the harness launches with.
	h := Harness{Name: "x", Command: []string{"x"}, ModelFlag: "-m", Models: []string{"a", "b"}, Settings: map[string]Setting{SettingModel: {Command: "/m {value}"}}}
	if got := h.Choices(SettingModel); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("model choices %v", got)
	}
}

func TestSettingCycleSteps(t *testing.T) {
	s := Setting{Key: "BTab", Cycle: []string{"default", "acceptEdits", "plan", "auto"}, Screen: map[string]string{"acceptEdits": "accept edits on", "plan": "plan mode on", "auto": "auto mode on"}}
	for _, c := range []struct {
		from, to string
		n        int
	}{{"default", "plan", 2}, {"plan", "default", 2}, {"auto", "acceptEdits", 2}, {"plan", "plan", 0}} {
		if n, err := s.Steps(c.from, c.to); err != nil || n != c.n {
			t.Errorf("%s → %s: %d %v, want %d", c.from, c.to, n, err, c.n)
		}
	}
	if _, err := s.Steps("", "plan"); err == nil || !strings.Contains(err.Error(), "current value") {
		t.Fatalf("unknown current: %v", err)
	}
	if _, err := s.Steps("plan", "yolo"); err == nil {
		t.Fatal("a value off the cycle steps")
	}
	for screen, want := range map[string]string{
		"> \n  ⏸ plan mode on (shift+tab to cycle)": "plan",
		"> \n  ? for shortcuts":                     "default",
		"  ⏵⏵ accept edits on":                      "acceptEdits",
	} {
		if got := s.OnScreen(screen); got != want {
			t.Errorf("screen %q: %q, want %q", screen, got, want)
		}
	}
}

func TestSettingsValidateAndMerge(t *testing.T) {
	for _, c := range []struct {
		s    map[string]Setting
		want string
	}{
		{map[string]Setting{"speed": {Command: "/s {value}"}}, "unknown setting"},
		{map[string]Setting{SettingModel: {Command: "/model"}}, "has no {value}"},
		{map[string]Setting{SettingModel: {Command: "/m {value}", Key: "BTab"}}, "both command and key"},
		{map[string]Setting{SettingPermission: {Key: "BTab", Cycle: []string{"a"}}}, "fewer than two"},
		{map[string]Setting{SettingPermission: {Key: "BTab", Cycle: []string{"a", "b"}, Screen: map[string]string{"c": "x"}}}, "not in its cycle"},
	} {
		h := Harness{Name: "x", Command: []string{"x"}, Settings: c.s}
		if err := h.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want %q", c.s, err, c.want)
		}
	}
	// A file row replaces the built-in's row by name, null removes one,
	// and the rest stay; the built-ins are not changed.
	merged, err := Merge(Builtins(), []byte(`[{"name": "claude", "settings": {"effort": {"values": ["low", "max"], "command": "/think {value}"}, "fast": null}, "contextWindows": {"claude-sonnet-5*": 1000000}}]`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(merged)
	if err != nil {
		t.Fatal(err)
	}
	claude, _ := r.Get("claude")
	if s, _ := claude.Setting(SettingEffort); s.Text("max") != "/think max" || !slices.Equal(claude.Choices(SettingEffort), []string{"low", "max"}) {
		t.Fatalf("effort row %+v", s)
	}
	if _, ok := claude.Setting(SettingFast); ok {
		t.Fatal("null kept the fast row")
	}
	if s, ok := claude.Setting(SettingModel); !ok || s.Text("opus") != "/model opus" {
		t.Fatalf("model row lost: %+v", s)
	}
	if n, _ := claude.ContextWindow("claude-sonnet-5-5"); n != 1_000_000 {
		t.Fatalf("merged window %d", n)
	}
	if n, _ := claude.ContextWindow("claude-haiku-5-5"); n != 200_000 {
		t.Fatalf("built-in window kept: %d", n)
	}
	base := Builtins()[0]
	if s, _ := base.Setting(SettingFast); s.Command != "/fast {value}" {
		t.Fatal("merge changed the built-ins")
	}
}

func TestContextWindowMostSpecificWins(t *testing.T) {
	h := Builtins()[0]
	for model, want := range map[string]int64{
		"claude-opus-5-5":       1_000_000,
		"claude-sonnet-5-5[1m]": 1_000_000,
		"claude-sonnet-5-5":     200_000,
	} {
		if n, err := h.ContextWindow(model); err != nil || n != want {
			t.Errorf("%s: %d %v, want %d", model, n, err, want)
		}
	}
	if _, err := h.ContextWindow("gpt-6"); err != ErrNoWindow {
		t.Fatalf("no match: %v", err)
	}
}

// A launch sets each setting the way its row says: flags in the argv, a
// command typed before the prompt, a note for a setting with neither; a
// value outside the choices is refused.
func TestLaunchSettings(t *testing.T) {
	var claude, codex Harness
	for _, h := range Builtins() {
		switch h.Name {
		case "claude":
			claude = h
		case "codex":
			codex = h
		}
	}
	flags, typed, notes, err := claude.LaunchSettings(map[string]string{SettingEffort: "high", SettingFast: "on", SettingPermission: "plan"})
	if err != nil || strings.Join(flags, " ") != "--permission-mode plan" || strings.Join(typed, "|") != "/effort high|/fast on" || len(notes) != 0 {
		t.Fatalf("claude: %q %q %q %v", flags, typed, notes, err)
	}
	if _, _, _, err := claude.LaunchSettings(map[string]string{SettingEffort: "huge"}); err == nil {
		t.Fatal("an effort claude does not take was taken")
	}
	if _, _, notes, err := codex.LaunchSettings(map[string]string{SettingEffort: "high"}); err != nil || len(notes) != 1 {
		t.Fatalf("codex: %q %v", notes, err)
	}
	if got := strings.Join(claude.ArgvWith("sonnet", "", []string{"--permission-mode", "plan"}), " "); got != "claude --model sonnet --permission-mode plan" {
		t.Fatalf("argv %q", got)
	}
}
