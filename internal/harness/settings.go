package harness

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// The per-session settings a harness profile can change in a running
// session, by name. A harness's "settings" rows say how: the command its
// own prompt takes ("/model {value}"), or the key that steps the setting
// through a cycle (Claude Code's shift+tab through its permission modes).
// The rows are data, so a new harness version is a harnesses.json change:
// a file row replaces the built-in row of the same name, and null removes
// it (the setting is then read-only in mtm).
const (
	SettingModel      = "model"
	SettingEffort     = "effort"
	SettingFast       = "fast"
	SettingPermission = "permission-mode"
)

// SettingNames are the settings mtm knows, in the order it draws them.
var SettingNames = []string{SettingModel, SettingEffort, SettingFast, SettingPermission}

// ValuePlaceholder is where a Setting's Command takes the chosen value.
const ValuePlaceholder = "{value}"

// Setting is how one per-session setting is changed inside a running
// harness: typed (Command, submitted like a prompt) or stepped (Key, sent
// once per step through Cycle, the pane's Screen telling which value is
// on). The zero Setting is none.
//
//	{"values": ["low", "high"], "command": "/effort {value}"}
//	{"key": "BTab", "cycle": ["default", "plan"], "screen": {"plan": "plan mode on"}}
type Setting struct {
	Values  []string          `json:"values,omitempty"`  // the valid values in chooser order; empty: Cycle, the harness's models (model), else any value
	Command string            `json:"command,omitempty"` // typed into the pane and submitted, {value} the chosen value
	Key     string            `json:"key,omitempty"`     // a tuios key name that steps the setting to the next value of Cycle
	Cycle   []string          `json:"cycle,omitempty"`   // the order Key steps through, wrapping
	Screen  map[string]string `json:"screen,omitempty"`  // text the pane's screen shows while a cycled value is on; a value without one shows none of the others
	Note    string            `json:"note,omitempty"`    // a side effect worth saying (a command that also saves a default)
	// Flags are the argv words that set the setting when the harness
	// starts, {value} the chosen value ("--permission-mode", "{value}").
	// A launch that sets the setting uses them; without them it types the
	// Command once the pane is ready (Launch). They are not an in-session
	// change, so a row with only Flags is read-only in a running session.
	Flags []string `json:"flags,omitempty"`
}

// Zero reports whether the row sets nothing.
func (s Setting) Zero() bool { return s.Command == "" && s.Key == "" }

// validate reports what makes the row unusable.
func (s Setting) validate(harness, name string) error {
	switch {
	case s.Zero() && len(s.Flags) == 0:
		return nil
	case len(s.Flags) > 0 && !slices.ContainsFunc(s.Flags, func(f string) bool { return strings.Contains(f, ValuePlaceholder) }) && name != SettingFast:
		return fmt.Errorf("harness %s: setting %s flags %q have no %s", harness, name, s.Flags, ValuePlaceholder)
	case s.Command != "" && s.Key != "":
		return fmt.Errorf("harness %s: setting %s gives both command and key; use one", harness, name)
	case s.Key != "" && len(s.Cycle) < 2:
		return fmt.Errorf("harness %s: setting %s steps with key %q but its cycle has fewer than two values", harness, name, s.Key)
	case s.Command != "" && !strings.Contains(s.Command, ValuePlaceholder) && name != SettingFast:
		return fmt.Errorf("harness %s: setting %s command %q has no %s", harness, name, s.Command, ValuePlaceholder)
	}
	for v := range s.Screen {
		if !slices.Contains(s.Cycle, v) {
			return fmt.Errorf("harness %s: setting %s screen names %q, which is not in its cycle", harness, name, v)
		}
	}
	return nil
}

// Setting is the harness's row for a setting; false when it has none.
func (h Harness) Setting(name string) (Setting, bool) {
	s, ok := h.Settings[name]
	if !ok || s.Zero() {
		return Setting{}, false
	}
	return s, true
}

// Choices are the values a chooser offers for the setting: its Values,
// else its Cycle, else (the model) the models the harness launches with;
// empty is any value.
func (h Harness) Choices(name string) []string {
	s := h.Settings[name]
	switch {
	case len(s.Values) > 0:
		return slices.Clone(s.Values)
	case len(s.Cycle) > 0:
		return slices.Clone(s.Cycle)
	case name == SettingModel:
		return slices.Clone(h.Models)
	}
	return nil
}

// Text is what the pane is typed to set value: the command with value in
// place of {value}.
func (s Setting) Text(value string) string {
	return strings.ReplaceAll(s.Command, ValuePlaceholder, value)
}

// Steps is how many presses of Key move the setting from from to to
// along Cycle.
func (s Setting) Steps(from, to string) (int, error) {
	i, j := slices.Index(s.Cycle, from), slices.Index(s.Cycle, to)
	switch {
	case j < 0:
		return 0, fmt.Errorf("%q is not in the cycle %s", to, strings.Join(s.Cycle, " → "))
	case i < 0:
		return 0, fmt.Errorf("the current value %q is not in the cycle %s", from, strings.Join(s.Cycle, " → "))
	}
	return (j - i + len(s.Cycle)) % len(s.Cycle), nil
}

// OnScreen is the cycled value the pane's screen shows: the value whose
// Screen text the screen holds, else the one value of Cycle without a
// Screen text (the cycle's plain state), else "".
func (s Setting) OnScreen(screen string) string {
	for _, v := range s.Cycle {
		if text := s.Screen[v]; text != "" && strings.Contains(screen, text) {
			return v
		}
	}
	var plain []string
	for _, v := range s.Cycle {
		if s.Screen[v] == "" {
			plain = append(plain, v)
		}
	}
	if len(plain) == 1 {
		return plain[0]
	}
	return ""
}

// ErrNoWindow is ContextWindow's answer when no pattern matches.
var ErrNoWindow = errors.New("no context window known")

// ContextWindow is the context window, in tokens, of model: the most
// specific of the harness's contextWindows patterns (path.Match globs,
// the longest first) that matches it.
func (h Harness) ContextWindow(model string) (int64, error) {
	if model == "" {
		return 0, ErrNoWindow
	}
	pats := make([]string, 0, len(h.ContextWindows))
	for p := range h.ContextWindows {
		pats = append(pats, p)
	}
	slices.SortFunc(pats, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	for _, p := range pats {
		if ok, _ := path.Match(p, model); ok && h.ContextWindows[p] > 0 {
			return h.ContextWindows[p], nil
		}
	}
	return 0, ErrNoWindow
}

// builtinSettings are the in-session commands of the harness versions on
// the owner's machine, read from each harness (2026-10-28):
//
//   - Claude Code 2.1.312: /model <model> (it also saves the model as the
//     default for new sessions: "Set model to `Opus 5.5` and saved as your
//     default for new sessions"), /effort <low|medium|high|xhigh|max|auto>,
//     /fast [on|off]. The permission mode has no command: shift+tab (BTab)
//     cycles default → acceptEdits → plan → bypassPermissions (when the
//     session may bypass) → auto (when the account has it) → default, and
//     the prompt's footer says which is on.
//   - omp 18.13.0 (src/slash-commands/builtin-modes.ts): /switch <model>
//     (session only; /model in the TUI opens its picker and drops the
//     argument), /effort <level>, /fast <on|off>. No permission modes.
//   - Codex 0.162.1: /model and /permissions open pickers and take no
//     argument, so no row: Codex's settings are shown, not changed.
func builtinSettings(name string) (map[string]Setting, map[string]int64) {
	switch name {
	case "claude":
		return map[string]Setting{
			SettingModel:  {Values: []string{"opus", "fable", "sonnet", "haiku", "default"}, Command: "/model {value}", Note: "Claude Code also saves it as the default for new sessions"},
			SettingEffort: {Values: []string{"low", "medium", "high", "xhigh", "max", "auto"}, Command: "/effort {value}"},
			SettingFast:   {Values: []string{"on", "off"}, Command: "/fast {value}"},
			SettingPermission: {Key: "BTab", Cycle: []string{"default", "acceptEdits", "plan", "bypassPermissions", "auto"}, Screen: map[string]string{
				"acceptEdits": "accept edits on", "plan": "plan mode on", "bypassPermissions": "bypass permissions on", "auto": "auto mode on",
			}, Flags: []string{"--permission-mode", "{value}"}},
		}, map[string]int64{
			// Claude Code's own window per model is not in its log; these
			// are what the owner's sessions show (an Opus 5.5 session read
			// 436k tokens of context). A model none matches shows tokens.
			"claude-opus-5*":   1_000_000,
			"claude-fable-5*":  1_000_000,
			"claude-*\\[1m\\]": 1_000_000,
			"claude-*":         200_000,
		}
	case "omp":
		return map[string]Setting{
			SettingModel:  {Command: "/switch {value}"},
			SettingEffort: {Values: []string{"off", "minimal", "low", "medium", "high", "xhigh", "max", "auto"}, Command: "/effort {value}"},
			SettingFast:   {Values: []string{"on", "off"}, Command: "/fast {value}"},
		}, nil
	}
	return nil, nil
}

// LaunchSettings is how a launch applies settings (by name: effort, fast,
// permission-mode; the model has its own flag): the argv words of the
// rows with Flags, the commands to type once the pane is ready for the
// rows with only a Command, and a note for each setting the harness has
// no way to set at launch. A value outside the setting's choices is an
// error. Settings are taken in SettingNames order.
func (h Harness) LaunchSettings(values map[string]string) (flags, typed, notes []string, err error) {
	for _, name := range SettingNames {
		v := strings.TrimSpace(values[name])
		if v == "" || name == SettingModel {
			continue
		}
		if choices := h.Choices(name); len(choices) > 0 && !slices.Contains(choices, v) {
			return nil, nil, nil, fmt.Errorf("%s takes %s: %s", h.Name, name, strings.Join(choices, ", "))
		}
		s := h.Settings[name]
		switch {
		case len(s.Flags) > 0:
			for _, f := range s.Flags {
				flags = append(flags, strings.ReplaceAll(f, ValuePlaceholder, v))
			}
		case s.Command != "":
			typed = append(typed, s.Text(v))
		default:
			notes = append(notes, fmt.Sprintf("%s has no launch flag or command for %s; %s=%s not applied (add a settings row in harnesses.json)", h.Name, name, name, v))
		}
	}
	return flags, typed, notes, nil
}
