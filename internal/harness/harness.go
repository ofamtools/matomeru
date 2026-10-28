// Package harness starts agent CLIs (claude, omp, codex, or anything a user
// defines) in terminal panes. A harness is data, not code: its argv, the
// flag that picks a model, the models it offers, how it takes its first
// prompt and how to tell it is ready for one. Built-ins cover claude, omp
// and codex; a JSON file adds harnesses or changes them by name, so a new
// harness needs no Go change. Launch is the one flow every harness and a
// plain shell share: open a pane running the argv, wait until it is ready,
// type the prompt into it. The package knows nothing about the app or the
// daemon's wire format; a Driver supplies the three pane operations.
package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// How a harness takes its first prompt.
const (
	// PromptSendText types the prompt into the pane once it is ready and
	// submits it (the default): it works for any program, a shell included.
	PromptSendText = "send-text"
	// PromptArg appends the prompt to the argv as the last argument.
	PromptArg = "arg"
)

// How a harness takes a file attached to a prompt (an image pasted into
// mtm's prompt box), when the prompt arrives through its terminal.
const (
	// AttachMention writes the file's absolute path into the prompt text
	// as an @ mention (@/abs/path, @"/abs/a b" with a space), which the
	// harness reads when the prompt is submitted. The default: it needs
	// no clipboard and no timing, so a queued prompt typed later by tuios
	// attaches the same way.
	//   - Claude Code (2.1.312): @-mentions become at_mentioned_files
	//     attachments read through its Read pipeline, which turns an
	//     image into an image block; @"..." quotes a path with spaces.
	//   - omp (18.13.0): extractFileMentions and
	//     generateFileMentionMessages (src/utils/file-mentions.ts) run in
	//     AgentSession.prompt and attach an image file as an image.
	AttachMention = "mention"
	// AttachPaste types each file's absolute path as a bracketed paste of
	// its own (no submit) before the text: the harness's paste handler
	// attaches a pasted image path, while an @ mention in submitted text
	// is only text. Codex (codex-cli 0.162.1, ChatComposer::handle_paste
	// → handle_paste_image_path in codex-rs/tui) is one. A queued prompt
	// still carries @ mentions: tuios types it as one paste.
	AttachPaste = "paste"
)

// Harness is one agent CLI as data.
//
//	{name: "omp", command: ["omp"], modelFlag: "--model", models: [],
//	 prompt: "send-text", ready: {state: "idle,done", timeoutMs: 15000}, env: {},
//	 attach: "mention"}
type Harness struct {
	Name      string            `json:"name"`
	Command   []string          `json:"command"`             // argv; command[0] is looked up on PATH
	ModelFlag string            `json:"modelFlag,omitempty"` // "--model"; empty: the harness takes no model
	Models    []string          `json:"models,omitempty"`    // the accepted models, the first the default; empty: any model, no default
	Prompt    string            `json:"prompt,omitempty"`    // PromptSendText (default) or PromptArg
	Ready     Ready             `json:"ready,omitzero"`      // when the pane takes a prompt; zero: a fixed delay
	Env       map[string]string `json:"env,omitempty"`       // set for the harness's process (through env(1))
	Attach    string            `json:"attach,omitempty"`    // AttachMention (default) or AttachPaste: how a prompt's files reach it
}

// Ready says when a new pane takes its first prompt: the agent state the
// pane reports (the daemon reads it from hooks, the screen or the title),
// or a regular expression its output matches. Neither set means a fixed
// delay. TimeoutMS bounds the wait.
type Ready struct {
	State     string `json:"state,omitempty"`   // agent states, comma-separated: "idle,done"
	Pattern   string `json:"pattern,omitempty"` // a regular expression on the pane's output: "> $"
	TimeoutMS int    `json:"timeoutMs,omitempty"`
}

// Zero reports whether no readiness probe is set.
func (r Ready) Zero() bool { return r.State == "" && r.Pattern == "" }

// DefaultReadyTimeoutMS bounds a readiness wait whose harness sets none.
const DefaultReadyTimeoutMS = 15000

// Builtins are the harnesses matomeru knows without a file, lifted from
// how matomeru-old launched them (internal/prompts/models.go: every one
// takes --model; tuios reports claude's state from its hooks and screen,
// omp's from the extension in internal/setup/omp.ts, codex's from its
// screen, so each is ready at idle or done).
func Builtins() []Harness {
	ready := Ready{State: "idle,done", TimeoutMS: DefaultReadyTimeoutMS}
	return []Harness{
		{Name: "claude", Command: []string{"claude"}, ModelFlag: "--model", Models: []string{"opus", "sonnet", "haiku"}, Prompt: PromptSendText, Ready: ready, Attach: AttachMention},
		{Name: "omp", Command: []string{"omp"}, ModelFlag: "--model", Prompt: PromptSendText, Ready: ready, Attach: AttachMention},
		{Name: "codex", Command: []string{"codex"}, ModelFlag: "--model", Prompt: PromptSendText, Ready: ready, Attach: AttachPaste},
	}
}

// Validate reports what makes a definition unusable.
func (h Harness) Validate() error {
	if strings.TrimSpace(h.Name) == "" {
		return errors.New("harness has no name")
	}
	if len(h.Command) == 0 || strings.TrimSpace(h.Command[0]) == "" {
		return fmt.Errorf("harness %s: command is empty; give the argv, e.g. [%q]", h.Name, h.Name)
	}
	switch h.Prompt {
	case "", PromptSendText, PromptArg:
	default:
		return fmt.Errorf("harness %s: prompt %q is not a prompt mode; use %q or %q", h.Name, h.Prompt, PromptSendText, PromptArg)
	}
	switch h.Attach {
	case "", AttachMention, AttachPaste:
	default:
		return fmt.Errorf("harness %s: attach %q is not an attach mode; use %q or %q", h.Name, h.Attach, AttachMention, AttachPaste)
	}
	if len(h.Models) > 0 && h.ModelFlag == "" {
		return fmt.Errorf("harness %s: models are listed but modelFlag is empty", h.Name)
	}
	if h.Ready.TimeoutMS < 0 {
		return fmt.Errorf("harness %s: ready.timeoutMs is negative", h.Name)
	}
	return nil
}

// AttachMode is how the harness takes a prompt's files: Attach, else
// AttachMention.
func (h Harness) AttachMode() string {
	if h.Attach == "" {
		return AttachMention
	}
	return h.Attach
}

// Model checks model against the harness and returns the one to pass: the
// harness's default (its first model) when model is empty, model itself
// when it is listed or the harness lists none.
func (h Harness) Model(model string) (string, error) {
	if model == "" {
		if len(h.Models) > 0 {
			return h.Models[0], nil
		}
		return "", nil
	}
	if h.ModelFlag == "" {
		return "", fmt.Errorf("harness %s takes no model (no modelFlag); leave model empty", h.Name)
	}
	if len(h.Models) > 0 && !slices.Contains(h.Models, model) {
		return "", fmt.Errorf("model %q is not one of %s's models: %s", model, h.Name, strings.Join(h.Models, ", "))
	}
	return model, nil
}

// Argv is the command a pane runs for model (already checked by Model) and
// prompt: the harness's command, its env through env(1), the model flag
// when a model is given, and the prompt last when the harness takes it as
// an argument. The zero Harness (a plain shell) has no argv.
func (h Harness) Argv(model, prompt string) []string {
	if len(h.Command) == 0 {
		return nil
	}
	var argv []string
	if len(h.Env) > 0 {
		argv = append(argv, "env")
		keys := make([]string, 0, len(h.Env))
		for k := range h.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			argv = append(argv, k+"="+h.Env[k])
		}
	}
	argv = append(argv, h.Command...)
	if model != "" && h.ModelFlag != "" {
		argv = append(argv, h.ModelFlag, model)
	}
	if prompt != "" && h.Prompt == PromptArg {
		argv = append(argv, prompt)
	}
	return argv
}

// Registry is the loaded harnesses in order: the built-ins, then the
// ones a file added.
type Registry struct{ list []Harness }

// NewRegistry validates hs and keeps them in order; a later one with a
// name already seen is an error.
func NewRegistry(hs []Harness) (*Registry, error) {
	r := &Registry{}
	for _, h := range hs {
		if err := h.Validate(); err != nil {
			return nil, err
		}
		if _, ok := r.Get(h.Name); ok {
			return nil, fmt.Errorf("harness %s is defined twice", h.Name)
		}
		r.list = append(r.list, h)
	}
	return r, nil
}

// List returns the harnesses in order.
func (r *Registry) List() []Harness { return slices.Clone(r.list) }

// Names returns the harness names in order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.list))
	for i, h := range r.list {
		out[i] = h.Name
	}
	return out
}

// Get finds a harness by name.
func (r *Registry) Get(name string) (Harness, bool) {
	for _, h := range r.list {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

// Lookup is Get with an error naming the known harnesses.
func (r *Registry) Lookup(name string) (Harness, error) {
	if h, ok := r.Get(name); ok {
		return h, nil
	}
	return Harness{}, fmt.Errorf("unknown harness %q; known: %s", name, strings.Join(r.Names(), ", "))
}

// Load is the built-ins merged with the file at path: a JSON array of
// harness objects. An entry whose name is a built-in's changes only the
// fields it gives (an entry {"name": "omp", "command": ["omp-dev"]} keeps
// omp's model flag and readiness); any other entry adds a harness. A
// missing file (or an empty path) is the built-ins alone.
func Load(path string) (*Registry, error) {
	list := Builtins()
	if path == "" {
		return NewRegistry(list)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewRegistry(list)
	}
	if err != nil {
		return nil, err
	}
	merged, err := Merge(list, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r, err := NewRegistry(merged)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// Merge applies a harness file's JSON array to base by name (see Load).
func Merge(base []Harness, data []byte) ([]Harness, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("harness file must be a JSON array of harness objects: %w", err)
	}
	out := slices.Clone(base)
	for i, raw := range entries {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &named); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if named.Name == "" {
			return nil, fmt.Errorf("entry %d has no name", i)
		}
		at := slices.IndexFunc(out, func(h Harness) bool { return h.Name == named.Name })
		var h Harness
		if at >= 0 {
			h = out[at]
			// A field the entry gives replaces the built-in's whole value.
			h.Command, h.Models, h.Env = slices.Clone(h.Command), slices.Clone(h.Models), nil
			if base := out[at].Env; base != nil {
				h.Env = make(map[string]string, len(base))
				for k, v := range base {
					h.Env[k] = v
				}
			}
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&h); err != nil {
			return nil, fmt.Errorf("harness %s: %w", named.Name, err)
		}
		if at >= 0 {
			out[at] = h
		} else {
			out = append(out, h)
		}
	}
	return out, nil
}
