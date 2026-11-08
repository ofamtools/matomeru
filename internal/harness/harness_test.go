package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuiltinsAreValidAndBuildTheirArgv(t *testing.T) {
	r, err := NewRegistry(Builtins())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Names(), " "); got != "claude omp codex" {
		t.Fatalf("names = %s", got)
	}
	for _, c := range []struct {
		name, model, prompt, want string
	}{
		{"claude", "", "fix it", "claude --model opus"},
		{"claude", "sonnet", "", "claude --model sonnet"},
		{"omp", "", "fix it", "omp"},
		{"omp", "openai-codex/gpt-6.1-sol", "fix it", "omp --model openai-codex/gpt-6.1-sol"},
		{"codex", "o5", "", "codex --model o5"},
	} {
		h, _ := r.Get(c.name)
		model, err := h.Model(c.model)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(h.Argv(model, c.prompt), " "); got != c.want {
			t.Errorf("%s %q: argv %q, want %q", c.name, c.model, got, c.want)
		}
		if h.Ready.State != "idle,done" || h.Prompt != PromptSendText {
			t.Errorf("%s: ready %+v prompt %q", c.name, h.Ready, h.Prompt)
		}
	}
}

func TestModelIsCheckedAgainstTheHarness(t *testing.T) {
	r, _ := NewRegistry(Builtins())
	claude, _ := r.Get("claude")
	if _, err := claude.Model("gpt"); err == nil || err.Error() != `model "gpt" is not one of claude's models: opus, sonnet, haiku` {
		t.Fatalf("bad model: %v", err)
	}
	none := Harness{Name: "sh", Command: []string{"sh"}}
	if _, err := none.Model("x"); err == nil || !strings.Contains(err.Error(), "takes no model") {
		t.Fatalf("model on a harness with no flag: %v", err)
	}
	if _, err := r.Lookup("aider"); err == nil || err.Error() != `unknown harness "aider"; known: claude, omp, codex` {
		t.Fatalf("unknown harness: %v", err)
	}
}

func TestArgModeEnvAndValidation(t *testing.T) {
	h := Harness{Name: "aider", Command: []string{"aider", "--yes"}, ModelFlag: "--model", Prompt: PromptArg, Env: map[string]string{"B": "2", "A": "1"}}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := h.Argv("m", "do it"); !reflect.DeepEqual(got, []string{"env", "A=1", "B=2", "aider", "--yes", "--model", "m", "do it"}) {
		t.Fatalf("argv = %q", got)
	}
	if (Harness{}).Argv("m", "p") != nil {
		t.Fatal("the zero harness (a shell) has an argv")
	}
	for _, c := range []struct {
		h    Harness
		want string
	}{
		{Harness{Name: "x"}, "harness x: command is empty"},
		{Harness{Name: "x", Command: []string{" "}}, "harness x: command is empty"},
		{Harness{Name: "x", Command: []string{"x"}, Prompt: "stdin"}, `harness x: prompt "stdin" is not a prompt mode; use "send-text" or "arg"`},
		{Harness{Name: "x", Command: []string{"x"}, Models: []string{"a"}}, "models are listed but modelFlag is empty"},
		{Harness{Command: []string{"x"}}, "harness has no name"},
		{Harness{Name: "x", Command: []string{"x"}, Attach: "clipboard"}, `harness x: attach "clipboard" is not an attach mode; use "mention" or "paste"`},
	} {
		if err := c.h.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.h, err, c.want)
		}
	}
}

// How each built-in takes a prompt's files is data: Claude Code and omp
// read @ mentions of a path at submit, Codex attaches a pasted path. A
// harness that gives none takes mentions, and a file entry changes it.
func TestAttachModes(t *testing.T) {
	want := map[string]string{"claude": AttachMention, "omp": AttachMention, "codex": AttachPaste}
	for _, h := range Builtins() {
		if h.AttachMode() != want[h.Name] {
			t.Errorf("%s attaches by %q, want %q", h.Name, h.AttachMode(), want[h.Name])
		}
	}
	if (Harness{Name: "x"}).AttachMode() != AttachMention {
		t.Fatal("a harness with no attach mode does not take mentions")
	}
	merged, err := Merge(Builtins(), []byte(`[{"name": "codex", "attach": "mention"}]`))
	if err != nil || merged[2].AttachMode() != AttachMention || merged[2].ModelFlag != "--model" {
		t.Fatalf("merge: %+v %v", merged[2], err)
	}
}

// Each built-in agent's box queues; a file's promptBox changes only the
// fields it gives, a harness without one takes the agent default, and an
// unknown delivery or submit is refused.
func TestPromptBoxIsData(t *testing.T) {
	for _, h := range Builtins() {
		if h.Box() != AgentPromptBox {
			t.Errorf("%s's box = %+v", h.Name, h.PromptBox)
		}
	}
	if (PromptBox{}).Resolved() != AgentPromptBox || (PromptBox{Deliver: DeliverSend}).Resolved() != TerminalPromptBox {
		t.Fatal("left-out fields are not the defaults")
	}
	merged, err := Merge(Builtins(), []byte(`[{"name": "codex", "promptBox": {"submit": "none"}}, {"name": "repl", "command": ["python3"], "promptBox": {"deliver": "send"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := merged[2].Box(); got != (PromptBox{Deliver: DeliverQueue, Submit: SubmitNone}) {
		t.Fatalf("codex = %+v", got)
	}
	if got := merged[3].Box(); got != TerminalPromptBox {
		t.Fatalf("repl = %+v", got)
	}
	if (Harness{Name: "x"}).Box() != AgentPromptBox {
		t.Fatal("a harness with no promptBox is not an agent's")
	}
	// null removes a built-in's promptBox: the agent default.
	if merged, err := Merge(Builtins(), []byte(`[{"name": "omp", "promptBox": null}]`)); err != nil || merged[1].PromptBox != nil || merged[1].Box() != AgentPromptBox {
		t.Fatalf("null: %+v %v", merged[1].PromptBox, err)
	}
	// Merging never writes through to the built-ins' box.
	base := Builtins()
	if _, err := Merge(base, []byte(`[{"name": "claude", "promptBox": {"deliver": "send"}}]`)); err != nil || base[0].Box() != AgentPromptBox {
		t.Fatalf("merge wrote through: %+v %v", base[0].Box(), err)
	}
	for _, bad := range []PromptBox{{Deliver: "type"}, {Submit: "tab"}} {
		if err := (Harness{Name: "x", Command: []string{"x"}, PromptBox: &bad}).Validate(); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

func TestLoadMergesTheFileByName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harnesses.json")
	if r, err := Load(path); err != nil || len(r.List()) != 3 {
		t.Fatalf("missing file: %v, %v", r, err)
	}
	file := `[
	  {"name": "omp", "command": ["omp-dev", "--no-extensions"]},
	  {"name": "aider", "command": ["aider"], "modelFlag": "--model", "models": ["sonnet"], "prompt": "arg"}
	]`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Names(), " "); got != "claude omp codex aider" {
		t.Fatalf("names = %s", got)
	}
	omp, _ := r.Get("omp")
	if got := strings.Join(omp.Argv("x/y", ""), " "); got != "omp-dev --no-extensions --model x/y" || omp.Ready.State != "idle,done" {
		t.Fatalf("omp = %+v", omp)
	}
	if builtin := Builtins()[1]; strings.Join(builtin.Command, " ") != "omp" {
		t.Fatalf("the merge changed the built-in: %+v", builtin)
	}
	for _, c := range []struct{ file, want string }{
		{`{"name": "omp"}`, "must be a JSON array"},
		{`[{"command": ["x"]}]`, "entry 0 has no name"},
		{`[{"name": "x", "comand": ["x"]}]`, `unknown field "comand"`},
		{`[{"name": "x", "command": []}]`, "harness x: command is empty"},
		{`[{"name": "x", "command": ["x"], "prompt": "pipe"}]`, `prompt "pipe" is not a prompt mode`},
		{`[{"name": "x", "command": ["x"], "attach": "ctrl+v"}]`, `attach "ctrl+v" is not an attach mode`},
	} {
		if err := os.WriteFile(path, []byte(c.file), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: %v, want %q", c.file, err, c.want)
		}
	}
}

// driver records the launch flow's calls in order.
type driver struct {
	calls    []string
	notReady error
}

func (d *driver) OpenWindow(_ context.Context, w Window) (string, error) {
	d.calls = append(d.calls, "open "+w.Session+" "+w.Name+" "+w.Cwd+" ["+strings.Join(w.Command, " ")+"]")
	return "w7", nil
}

func (d *driver) WaitReady(_ context.Context, session, window string, r Ready) error {
	d.calls = append(d.calls, "wait "+session+"/"+window+" "+r.State+r.Pattern)
	return d.notReady
}

func (d *driver) SendText(_ context.Context, session, window, text string) error {
	d.calls = append(d.calls, "send "+session+"/"+window+" "+text)
	return nil
}

func TestLaunchOpensWaitsAndTypesThePrompt(t *testing.T) {
	r, _ := NewRegistry(Builtins())
	omp, _ := r.Get("omp")
	spec := Spec{Session: "s", Name: "fix", Cwd: "/src", Prompt: "fix the test"}
	var slept []time.Duration
	newLauncher := func(d Driver) *Launcher {
		return &Launcher{Driver: d, Delay: time.Second, Sleep: func(_ context.Context, t time.Duration) error { slept = append(slept, t); return nil }}
	}
	for _, c := range []struct {
		name   string
		h      Harness
		spec   Spec
		ready  error
		calls  []string
		prompt string
		slept  int
	}{
		{"omp: ready then send-text", omp, spec, nil, []string{"open s fix /src [omp]", "wait s/w7 idle,done", "send s/w7 fix the test"}, PromptSent, 0},
		{"not ready: the prompt is kept", omp, spec, errors.New("timeout"), []string{"open s fix /src [omp]", "wait s/w7 idle,done"}, PromptNotSent, 0},
		{"no probe: the fixed delay", Harness{Name: "sh", Command: []string{"bash"}}, spec, nil, []string{"open s fix /src [bash]", "send s/w7 fix the test"}, PromptSent, 1},
		{"arg mode: in the argv", Harness{Name: "a", Command: []string{"a"}, Prompt: PromptArg}, spec, nil, []string{"open s fix /src [a fix the test]"}, PromptInArgv, 0},
		{"a shell: no prompt", Harness{}, Spec{Session: "s", Name: "shell", Cwd: "/src"}, nil, []string{"open s shell /src []"}, PromptNone, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			slept = nil
			d := &driver{notReady: c.ready}
			out, err := newLauncher(d).Launch(context.Background(), c.h, c.spec)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d.calls, c.calls) || out.Prompt != c.prompt || out.Window != "w7" || len(slept) != c.slept {
				t.Fatalf("calls %q, result %+v, slept %v", d.calls, out, slept)
			}
			if c.prompt == PromptNotSent && !strings.Contains(out.Note, "omp was not ready: timeout") {
				t.Fatalf("note = %q", out.Note)
			}
		})
	}
}

func TestHookFlagsCarryTheHookSettingsPerLaunch(t *testing.T) {
	h := Builtins()[0]
	if h.Name != "claude" || len(h.HookArgs) != 2 || h.HookArgs[0] != "--settings" {
		t.Fatalf("claude's hookArgs = %q", h.HookArgs)
	}
	for _, exe := range []string{"/usr/local/bin/mtm", "/tmp/a b/it's/mtm", `/tmp/"q"/mtm`} {
		flags := h.HookFlags(exe)
		if len(flags) != 2 || flags[0] != "--settings" {
			t.Fatalf("flags for %q = %q", exe, flags)
		}
		var settings struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Type, Command string
					Timeout       int
				}
			}
		}
		if err := json.Unmarshal([]byte(flags[1]), &settings); err != nil {
			t.Fatalf("the --settings word is not JSON for %q: %v\n%s", exe, err, flags[1])
		}
		for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop", "SessionEnd", "SubagentStart", "SubagentStop", "Notification"} {
			g := settings.Hooks[ev]
			if len(g) != 1 || len(g[0].Hooks) != 1 {
				t.Fatalf("%s: %+v", ev, g)
			}
			c := g[0].Hooks[0]
			// Run through a shell, the command must name the binary as one word.
			q := "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
			if c.Type != "command" || c.Command != q+" agent hook claude-code --integration 3" {
				t.Fatalf("%s command = %q, want %q", ev, c.Command, q+" agent hook claude-code --integration 3")
			}
			if (ev == "PermissionRequest") != (c.Timeout == 310) {
				t.Fatalf("%s timeout = %d", ev, c.Timeout)
			}
		}
	}
	if got := h.HookFlags(""); got != nil {
		t.Fatalf("no binary: %q", got)
	}
	if got := Builtins()[1].HookFlags("/x/mtm"); got != nil {
		t.Fatalf("a harness without hookArgs got %q", got)
	}
}

func TestLaunchAddsSpecFlagsAfterSettings(t *testing.T) {
	h := Harness{Name: "x", Command: []string{"x"}, ModelFlag: "--model"}
	got := h.ArgvWith("m", "", append([]string{"--a"}, "--settings", "{}"))
	want := []string{"x", "--model", "m", "--a", "--settings", "{}"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// HookHash is Codex's own hash: this value is the currentHash `codex
// app-server` hooks/list reported (0.166.0) for the same hook.
func TestHookHashMatchesCodex(t *testing.T) {
	c := "mtm agent hook codex --integration 1"
	if got, want := HookHash("session_start", c, 5), "sha256:b7d454271e2c7a4b2c0ae058789079041370a5368a2119c23574d66abd112112"; got != want {
		t.Fatalf("session_start = %s, want %s", got, want)
	}
	if got, want := HookHash("interrupt", c, 2), "sha256:3dd15f6bbe94186fa72af18775de63fe119e2c97fdf89b2ff21a404c36c7f44b"; got != want {
		t.Fatalf("interrupt = %s, want %s", got, want)
	}
}

// The hook command is the small mtm-hook binary when it sits beside mtm,
// else mtm agent hook; settings, event words and files all follow it.
func TestHookCommandPrefersTheSiblingBinary(t *testing.T) {
	dir := t.TempDir()
	mtm := filepath.Join(dir, "mtm")
	if got := HookCommand(mtm); !slices.Equal(got, []string{mtm, "agent", "hook"}) {
		t.Fatalf("no sibling: %q", got)
	}
	sib := filepath.Join(dir, "mtm-hook")
	if err := os.WriteFile(sib, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := HookCommand(mtm); !slices.Equal(got, []string{sib}) {
		t.Fatalf("sibling: %q", got)
	}
	var codex, claude Harness
	for _, h := range Builtins() {
		switch h.Name {
		case "codex":
			codex = h
		case "claude":
			claude = h
		}
	}
	if f := strings.Join(codex.EventFlags(mtm), "\n"); !strings.Contains(f, `command="`+"'"+sib+"' codex --integration 1"+`"`) {
		t.Fatalf("codex flags:\n%s", f)
	}
	if f := claude.HookFlags(mtm)[1]; !strings.Contains(f, "'"+sib+"' claude-code --integration 3") || strings.Contains(f, "agent hook") {
		t.Fatalf("claude settings: %s", f)
	}
	body, _ := HookFileContent("omp-agent-state.ts", mtm)
	if !strings.Contains(body, `const HOOK = ["`+sib+`"];`) {
		t.Fatalf("omp extension: %s", body)
	}
}
