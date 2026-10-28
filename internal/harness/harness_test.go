package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
