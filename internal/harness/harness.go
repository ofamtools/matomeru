// Package harness starts agent CLIs (claude, omp, codex, or anything a user
// defines) in terminal panes. A harness is data, not code: its argv, the
// flag that picks a model, the models it offers, how it takes its first
// prompt and how to tell it is ready for one. Built-ins cover claude, omp
// and codex; a JSON file adds harnesses or changes them by name, so a new
// harness needs no Go change: the built-ins are themselves such a file
// (builtins.json), and every difference between harnesses is a field
// mtm reads, never a test of a harness's name. Launch is the one flow every harness and a
// plain shell share: open a pane running the argv, wait until it is ready,
// type the prompt into it. The package knows nothing about the app or the
// daemon's wire format; a Driver supplies the three pane operations.
package harness

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/drive"
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
//	 attach: "mention", promptBox: {deliver: "queue", submit: "enter"},
//	 drive: "terminal", view: {open: "turns", split: 50}}
type Harness struct {
	Name    string   `json:"name"`
	Command []string `json:"command"` // argv; command[0] is looked up on PATH
	// Ids are the harness ids tuios reports for a pane running the
	// harness, as path.Match globs ("claude*" covers claude-code); empty:
	// the harness's name. A pane's capabilities (its log, settings,
	// prompt box, completions) come from the harness whose ids cover the
	// id tuios reports, the one mtm launched it with first.
	Ids       []string          `json:"ids,omitempty"`
	ModelFlag string            `json:"modelFlag,omitempty"` // "--model"; empty: the harness takes no model
	Models    []string          `json:"models,omitempty"`    // the accepted models, the first the default; empty: any model, no default
	Prompt    string            `json:"prompt,omitempty"`    // PromptSendText (default) or PromptArg
	Ready     Ready             `json:"ready,omitzero"`      // when the pane takes a prompt; zero: a fixed delay
	Env       map[string]string `json:"env,omitempty"`       // set for the harness's process (through env(1))
	Attach    string            `json:"attach,omitempty"`    // AttachMention (default) or AttachPaste: how a prompt's files reach it
	// Settings are how a running session's settings change (settings.go),
	// by name: model, effort, fast, permission-mode. A file's rows replace
	// the built-in's by name; null removes one.
	Settings map[string]Setting `json:"settings,omitempty"`
	// ContextWindows are context windows in tokens by model pattern (a
	// path.Match glob), for a harness whose log gives none.
	ContextWindows map[string]int64 `json:"contextWindows,omitempty"`
	// Session is where the harness records the session a running agent
	// writes now (session.go); nil: only the id tuios reports. A file's
	// session object merges by field into the built-in's; null removes it.
	Session *SessionSource `json:"session,omitempty"`
	// Log is the format of the harness's session log (its transcript),
	// which mtm reads for a pane's turns and its subagents (LogClaude,
	// LogOmp) or only for its settings line (LogCodex, Codex's rollout).
	// Empty, or a format mtm has no reader for: the pane's log is not
	// read (its turns are mtm's journal, it shows no subagents).
	Log string `json:"log,omitempty"`
	// Scroll is what the scroll keys (PageUp and the like) do in the
	// harness's live pane: ScrollCopy enters mtm's copy mode scrolled by a
	// page (for an agent that draws inline and has no scroll of its own),
	// ScrollForward or empty sends the key to the program.
	Scroll ScrollMode `json:"scroll,omitempty"`
	// Compose is what the prompt box completes from in the harness's
	// panes (compose.go); nil: files only. A file's compose object
	// merges by field into the built-in's; null removes it.
	Compose *Compose `json:"compose,omitempty"`
	// MCP names the recipe `mtm mcp config` prints for the harness
	// (internal/mcp: claude, codex, omp, opencode); empty: none.
	MCP string `json:"mcp,omitempty"`
	// Extends, on a file entry that adds a harness, names the harness
	// it starts as a copy of; its own fields then change that copy by
	// field, as an entry naming a built-in does ({"name": "claude-work",
	// "extends": "claude", "env": {"CLAUDE_CONFIG_DIR": "~/.claude-work"}}).
	Extends string `json:"extends,omitempty"`
	// PromptBox is how mtm's prompt box writes to the harness's panes
	// (promptbox.go); nil: AgentPromptBox. A file's promptBox object
	// merges by field into the built-in's; null removes it.
	PromptBox *PromptBox `json:"promptBox,omitempty"`
	// Drive is how a launch runs the harness (internal/drive): terminal
	// (a tuios pane, the default), exec (a plain process whose output
	// streams into a job; a send-text prompt goes to its stdin) or api
	// (reserved: refused as not available yet). A launch may override it.
	Drive string `json:"drive,omitempty"`
	// View is how mtm's pane detail opens the harness's panes (view.go);
	// nil: the agent.view settings. A file's view object merges by field
	// into the built-in's; null removes it.
	View *View `json:"view,omitempty"`
	// HookArgs are argv words a launch adds so the agent reports its own
	// state (turn and approval state, session id, transcript, prompt,
	// subagents) to the daemon through mtm agent hook, with the hook
	// settings carried per launch instead of in a global config file.
	// {mtm} is the shell-quoted path of the mtm binary; {mtm_json} is the
	// same escaped to sit inside a JSON string; {mtm_hook} and
	// {mtm_hook_json} are the hook command (mtm-hook beside mtm, else
	// mtm agent hook; hooks.go expand). Claude Code takes them as
	// --settings JSON. Empty: the harness takes none (see the agent.hooks
	// setting, which turns them off). Codex takes hooks per launch with -c and
	// holds them untrusted until each one's hash is stated: see Hooks.
	HookArgs []string `json:"hookArgs,omitempty"`
	// Hooks adds one argv group per event (Codex: -c hooks.Event=[...]) and,
	// when the harness holds command-line hooks untrusted, the trust words
	// carrying each hook's hash (hooks.go). Nil: none.
	Hooks *HookSpec `json:"hooks,omitempty"`
	// HookGlobal lists the global config files that may already run mtm's
	// hook; one that holds it turns every per-launch hook of this row off
	// (hooks.go HookGlobal). Empty: always add them.
	HookGlobal []HookGlobal `json:"hookGlobal,omitempty"`
	// HookFiles are files mtm writes for a launch and names on its command
	// line (OMP: -e extension.ts); each is skipped when a file it would
	// duplicate is installed already (hooks.go). Empty: none.
	HookFiles []HookFile `json:"hookFiles,omitempty"`
	// AgentEnv names environment variables the harness sets in the
	// processes its agent runs (Claude Code sets CLAUDECODE): a CLI call
	// that finds one of them set, inside a pane mtm did not launch, runs as
	// that harness's agent (cmd/mtm/actor.go). Empty: only a pane mtm
	// launched with the harness is known as its agent.
	AgentEnv []string `json:"agentEnv,omitempty"`
}

// DriveOf is the harness's drive, drive.Terminal when it gives none.
func (h Harness) DriveOf() string { return drive.Of(h.Drive, drive.Terminal) }

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

//go:embed builtins.json
var builtinsJSON []byte

// Builtins are the harnesses matomeru knows without a file:
// builtins.json, in the same format as a user's harnesses.json. The
// launch rows are lifted from how matomeru-old launched them
// (internal/prompts/models.go: every one takes --model; tuios reports
// claude's state from its hooks and screen, omp's from the extension in
// internal/setup/omp.ts, codex's from its screen, so each is ready at
// idle or done).
//
// The settings rows are the in-session commands of the harness versions
// on the owner's machine, read from each harness (2026-10-28):
//
//   - Claude Code 2.1.312: /model <model> (it also saves the model as the
//     default for new sessions: "Set model to `Opus 5.5` and saved as your
//     default for new sessions"), /effort <low|medium|high|xhigh|max|auto>,
//     /fast [on|off]. The permission mode has no command: shift+tab (BTab)
//     cycles default → acceptEdits → plan → bypassPermissions (when the
//     session may bypass) → auto (when the account has it) → default, and
//     the prompt's footer says which is on. Its context windows per model
//     are not in its log; they are what the owner's sessions show (an Opus
//     5.5 session read 436k tokens of context). A model none matches shows
//     tokens.
//   - omp 18.13.0 (src/slash-commands/builtin-modes.ts): /switch <model>
//     (session only; /model in the TUI opens its picker and drops the
//     argument), /effort <level>, /fast <on|off>. No permission modes.
//   - Codex 0.162.1: /model and /permissions open pickers and take no
//     argument, so no row: Codex's settings are shown, not changed.
//
// The session sources: Claude Code rewrites sessions/<pid>.json when the
// process moves to another session (2.1.312: {"pid", "sessionId", "cwd",
// "kind", "procStart", …}); omp (oh-my-pi 18) writes terminal-sessions/
// <tty>, the folder on line 1 and the session file on line 2, when it
// sets a session file.
//
// The compose rows are each harness's own search path (see
// internal/compose): Claude Code's skills and commands under its config
// directory, .claude/ up to the repository root and its enabled plugins;
// omp's from ~/.omp/agent/config.yml; Codex's under ~/.codex and
// ~/.agents. Claude Code's commands are its documented built-ins (2.1.x),
// omp's its slash-command registry (src/slash-commands/builtin-*.ts,
// 18.13.0), Codex's from its TUI. A skill is /name in Claude Code
// (/plugin:name for a plugin's), /skill:name in omp
// (src/extensibility/skills.ts) and $name in Codex (codex-cli 0.162.1).
func Builtins() []Harness {
	var list []Harness
	dec := json.NewDecoder(strings.NewReader(string(builtinsJSON)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil {
		panic("harness: builtins.json: " + err.Error())
	}
	return list
}

// Session log formats (Harness.Log).
const (
	LogClaude = "claude"
	LogOmp    = "omp"
	LogCodex  = "codex"
)

// ScrollMode is a value of Harness.Scroll: a scroll behaviour, a domain of
// its own (not a verb or mode name).
type ScrollMode string

// Scroll behaviours (Harness.Scroll).
const (
	ScrollCopy    ScrollMode = "copy"
	ScrollForward ScrollMode = "forward"
)

// ScrollBehaviour is what a ScrollMode means.
type ScrollBehaviour struct {
	// EntersCopy: a scroll key in the live pane enters mtm's copy mode,
	// scrolled; else the key goes to the program.
	EntersCopy bool
}

// ScrollModes is every scroll value a harness row may name and what it
// means; the empty value forwards.
var ScrollModes = map[ScrollMode]ScrollBehaviour{
	"":            {},
	ScrollForward: {},
	ScrollCopy:    {EntersCopy: true},
}

// ScrollEntersCopy reports whether the harness's scroll keys enter copy mode.
func (h Harness) ScrollEntersCopy() bool { return ScrollModes[h.Scroll].EntersCopy }

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
	if _, ok := ScrollModes[h.Scroll]; !ok {
		return fmt.Errorf("harness %s: scroll %q is not a scroll behaviour; use %q or %q", h.Name, h.Scroll, ScrollCopy, ScrollForward)
	}
	if len(h.Models) > 0 && h.ModelFlag == "" {
		return fmt.Errorf("harness %s: models are listed but modelFlag is empty", h.Name)
	}
	if h.Ready.TimeoutMS < 0 {
		return fmt.Errorf("harness %s: ready.timeoutMs is negative", h.Name)
	}
	for name, s := range h.Settings {
		if !slices.Contains(SettingNames, name) {
			return fmt.Errorf("harness %s: unknown setting %q; known: %s", h.Name, name, strings.Join(SettingNames, ", "))
		}
		if err := s.validate(h.Name, name); err != nil {
			return err
		}
	}
	return h.validateParts()
}

// validateParts checks the harness's objects: its session, prompt box,
// drive, view, compose row, ids and context windows.
func (h Harness) validateParts() error {
	if h.Session != nil {
		if err := h.Session.validate(h.Name); err != nil {
			return err
		}
	}
	if h.PromptBox != nil {
		if err := h.PromptBox.validate(h.Name); err != nil {
			return err
		}
	}
	if err := drive.Check(h.Drive); err != nil {
		return fmt.Errorf("harness %s: %w", h.Name, err)
	}
	if h.View != nil {
		if err := h.View.validate(h.Name); err != nil {
			return err
		}
	}
	if h.Compose != nil {
		if err := h.Compose.validate(h.Name); err != nil {
			return err
		}
	}
	for _, id := range h.Ids {
		if _, err := path.Match(id, ""); err != nil || strings.TrimSpace(id) == "" {
			return fmt.Errorf("harness %s: ids %q: a glob of the id tuios reports", h.Name, id)
		}
	}
	for p, n := range h.ContextWindows {
		if _, err := path.Match(p, ""); err != nil || n < 0 {
			return fmt.Errorf("harness %s: contextWindows %q: a glob and a token count ≥ 0", h.Name, p)
		}
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
func (h Harness) Argv(model, prompt string) []string { return h.ArgvWith(model, prompt, nil) }

// ArgvWith is Argv with flags (LaunchSettings' launch flags) after the
// model flag and before a prompt given as an argument.
func (h Harness) ArgvWith(model, prompt string, flags []string) []string {
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
	argv = append(argv, flags...)
	if prompt != "" && h.Prompt == PromptArg {
		argv = append(argv, prompt)
	}
	return argv
}

// HookFlags are HookArgs with the mtm binary's path filled in: the words a
// launch adds so the agent's hooks run mtm agent hook. mtm is the binary's
// path; with none, or a harness with no HookArgs, there are none.
func (h Harness) HookFlags(mtm string) []string {
	if mtm == "" || len(h.HookArgs) == 0 {
		return nil
	}
	out := make([]string, len(h.HookArgs))
	for i, a := range h.HookArgs {
		out[i] = expand(a, mtm)
	}
	return out
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

// AgentFromEnv is the first harness (in registry order) one of whose
// AgentEnv variables getenv finds set; "" when none is.
func (r *Registry) AgentFromEnv(getenv func(string) string) string {
	for _, h := range r.list {
		for _, name := range h.AgentEnv {
			if getenv(name) != "" {
				return h.Name
			}
		}
	}
	return ""
}

// Covers reports whether the harness's Ids (else its name) cover id, a
// harness id tuios reports.
func (h Harness) Covers(id string) bool {
	if id == "" {
		return false
	}
	if len(h.Ids) == 0 {
		return id == h.Name
	}
	for _, g := range h.Ids {
		if ok, _ := path.Match(g, id); ok {
			return true
		}
	}
	return false
}

// ForAgent is the harness of a pane tuios reports as running id: the
// harness named first (the one mtm launched the pane with) when it covers
// id, else the first in registry order that does.
func (r *Registry) ForAgent(id, first string) (Harness, bool) {
	if h, ok := r.Get(first); ok && first != "" && h.Covers(id) {
		return h, true
	}
	for _, h := range r.list {
		if h.Covers(id) {
			return h, true
		}
	}
	return Harness{}, false
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
// omp's model flag and readiness); any other entry adds a harness,
// starting empty or, with extends, as a copy of the harness it names. A
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

// clone is h with its slices, maps and objects copied, so an entry
// merged into it changes none of h's. A field the entry gives replaces
// the whole value; a settings or contextWindows object merges by key, and
// a session, promptBox, view or compose object by field.
func (h Harness) clone() Harness {
	h.Command, h.Models, h.AgentEnv, h.Ids = slices.Clone(h.Command), slices.Clone(h.Models), slices.Clone(h.AgentEnv), slices.Clone(h.Ids)
	h.HookArgs, h.HookFiles, h.HookGlobal = slices.Clone(h.HookArgs), slices.Clone(h.HookFiles), slices.Clone(h.HookGlobal)
	if h.Hooks != nil {
		spec := *h.Hooks
		spec.Events, spec.Args = slices.Clone(spec.Events), slices.Clone(spec.Args)
		if spec.Trust != nil {
			trust := *spec.Trust
			trust.Args = slices.Clone(trust.Args)
			spec.Trust = &trust
		}
		h.Hooks = &spec
	}
	h.Env, h.Settings, h.ContextWindows = maps.Clone(h.Env), maps.Clone(h.Settings), maps.Clone(h.ContextWindows)
	if h.Session != nil {
		src := *h.Session
		src.Agents, src.Env = slices.Clone(src.Agents), slices.Clone(src.Env)
		h.Session = &src
	}
	if h.PromptBox != nil {
		box := *h.PromptBox
		h.PromptBox = &box
	}
	if h.View != nil {
		view := *h.View
		h.View = &view
	}
	if h.Compose != nil {
		h.Compose = h.Compose.clone()
	}
	return h
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
			Name    string `json:"name"`
			Extends string `json:"extends"`
		}
		if err := json.Unmarshal(raw, &named); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if named.Name == "" {
			return nil, fmt.Errorf("entry %d has no name", i)
		}
		at := slices.IndexFunc(out, func(h Harness) bool { return h.Name == named.Name })
		var h Harness
		switch from := slices.IndexFunc(out, func(h Harness) bool { return h.Name == named.Extends }); {
		case named.Extends != "" && at >= 0:
			return nil, fmt.Errorf("harness %s: extends is for an entry that adds a harness; %s exists, so the entry changes it", named.Name, named.Name)
		case named.Extends != "" && from < 0:
			return nil, fmt.Errorf("harness %s: extends %q, which is not a harness defined before it", named.Name, named.Extends)
		case at >= 0:
			h = out[at].clone()
		case from >= 0:
			h = out[from].clone()
			h.Name = named.Name
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
