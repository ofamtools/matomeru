// Package actions loads user-defined actions (~/.config/matomeru/actions.json)
// into core.CommandSpec entries and groups focused-record actions by namespace
// for the `;` Actions picker.
//
// actions imports only the Go standard library and ofam.dev/ochazuke/core.
package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// ActionsFile is the filename of the user-defined actions config file inside
// Matomeru's config directory.
const ActionsFile = "actions.json"

// Metadata keys attached to core.CommandSpec.Meta by ActionDef.ToCommandSpec.
const (
	MetaRecord     = "record"
	MetaKinds      = "kinds"
	MetaScope      = "scope"
	MetaNoun       = "noun"
	MetaVerb       = "verb"
	MetaTitle      = "title"
	MetaSource     = "source"
	MetaUserAction = "userAction"
	MetaAction     = "action"
	MetaForeground = "foreground"
	MetaHistory    = "history"
	MetaDir        = "dir"
	MetaCommand    = "command"
	MetaEnv        = "env"
)

// ActionDef is one user-defined verb loaded from actions.json.
type ActionDef struct {
	Noun        string            `json:"noun"`
	Verb        string            `json:"verb"`
	Description string            `json:"description,omitempty"`
	Command     []string          `json:"command"`
	Foreground  bool              `json:"foreground,omitempty"`
	History     string            `json:"history,omitempty"`
	Dir         string            `json:"dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// DefaultConfigPath returns ~/.config/matomeru/actions.json (honoring
// $XDG_CONFIG_HOME when set).
func DefaultConfigPath() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "matomeru", ActionsFile)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "matomeru", ActionsFile)
	}
	return filepath.Join(home, ".config", "matomeru", ActionsFile)
}

// LoadFile reads and parses a JSON or JSONC actions file. If path is empty,
// DefaultConfigPath is used. A missing file returns (nil, nil).
func LoadFile(path string) ([]ActionDef, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return Parse(raw)
}

// Parse parses a JSON or JSONC actions.json payload (supporting // and /* */
// comments and trailing commas). Accepts either a top-level JSON array [...]
// (the standard v1 format) or an object {"actions": [...]}.
func Parse(raw []byte) ([]ActionDef, error) {
	clean := stripJSONC(raw)
	trimmed := strings.TrimSpace(string(clean))
	if trimmed == "" {
		return nil, nil
	}

	var list []ActionDef
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(clean, &list); err != nil {
			return nil, fmt.Errorf("actions: parse array: %w", err)
		}
	case '{':
		var wrap struct {
			Actions []ActionDef `json:"actions"`
		}
		if err := json.Unmarshal(clean, &wrap); err != nil {
			return nil, fmt.Errorf("actions: parse object: %w", err)
		}
		if wrap.Actions == nil {
			return nil, errors.New("actions: object form requires an \"actions\" array")
		}
		list = wrap.Actions
	default:
		return nil, errors.New("actions: config must be a JSON array or {\"actions\": [...]}")
	}

	if len(list) == 0 {
		return nil, nil
	}

	out := make([]ActionDef, 0, len(list))
	seen := make(map[string]int, len(list))
	for i, def := range list {
		norm := def.normalized()
		if err := norm.Validate(); err != nil {
			return nil, fmt.Errorf("actions: entry %d: %w", i+1, err)
		}
		name := norm.CommandName()
		if prev, dup := seen[name]; dup {
			return nil, fmt.Errorf("actions: entry %d duplicates command %q (first defined at entry %d)", i+1, name, prev)
		}
		seen[name] = i + 1
		out = append(out, norm)
	}
	return out, nil
}

// Validate checks that the ActionDef has a valid noun, verb, and non-empty command.
func (a ActionDef) Validate() error {
	noun := strings.TrimSpace(a.Noun)
	if noun == "" {
		return errors.New("missing noun")
	}
	if !isValidNoun(noun) {
		return fmt.Errorf("invalid noun %q", noun)
	}
	verb := strings.TrimSpace(a.Verb)
	if verb == "" {
		return errors.New("missing verb")
	}
	if !isValidVerb(verb) {
		return fmt.Errorf("invalid verb %q", verb)
	}
	if len(a.Command) == 0 || strings.TrimSpace(a.Command[0]) == "" {
		return fmt.Errorf("%s.%s: missing command", noun, verb)
	}
	return nil
}

// CommandName returns the canonical "<noun>.<verb>" command ID, or "" if
// either noun or verb is missing or invalid.
func (a ActionDef) CommandName() string {
	noun := strings.TrimSpace(a.Noun)
	verb := strings.TrimSpace(a.Verb)
	if !isValidNoun(noun) || !isValidVerb(verb) {
		return ""
	}
	return noun + "." + verb
}

// ToCommandSpec converts a into a core.CommandSpec that registers directly
// into core.Registry and integrates with `;` Actions, `:` Palette, and
// bindings.json.
func (a ActionDef) ToCommandSpec() core.CommandSpec {
	norm := a.normalized()
	id := norm.CommandName()
	noun := norm.Noun
	verb := norm.Verb

	desc := norm.Description
	if desc == "" && len(norm.Command) > 0 {
		desc = strings.Join(norm.Command, " ")
	}
	title := desc
	if title == "" {
		title = id
	}

	tags := []string{"user", "action"}
	if noun != "" && !slices.Contains(tags, noun) {
		tags = append(tags, noun)
	}

	meta := map[string]any{
		MetaRecord:     true,
		MetaScope:      noun,
		MetaNoun:       noun,
		MetaVerb:       verb,
		MetaTitle:      title,
		MetaSource:     ActionsFile,
		MetaUserAction: true,
		MetaForeground: norm.Foreground,
		MetaCommand:    slices.Clone(norm.Command),
		MetaAction:     norm,
	}
	if noun != "" && noun != "record" && noun != "*" {
		meta[MetaKinds] = []string{noun}
	}
	if norm.History != "" {
		meta[MetaHistory] = norm.History
	}
	if norm.Dir != "" {
		meta[MetaDir] = norm.Dir
	}
	if len(norm.Env) > 0 {
		meta[MetaEnv] = maps.Clone(norm.Env)
	}

	return core.CommandSpec{
		ID:          id,
		Description: desc,
		Tags:        tags,
		Meta:        meta,
	}
}

// ToCommandSpecs converts a slice of ActionDefs to core.CommandSpecs.
func ToCommandSpecs(defs []ActionDef) []core.CommandSpec {
	if len(defs) == 0 {
		return nil
	}
	out := make([]core.CommandSpec, len(defs))
	for i, d := range defs {
		out[i] = d.ToCommandSpec()
	}
	return out
}

// ExpandCommand substitutes {{label}}, {{data}}, {{id}}, {{title}}, and any
// {{field}} placeholder in each argv element independently without shell
// parsing or escaping surprises. Replacement is single-pass so substituted
// values containing {{...}} are never re-expanded, and a.Command is never
// mutated.
func (a ActionDef) ExpandCommand(vars map[string]string) []string {
	if len(a.Command) == 0 {
		return nil
	}
	out := make([]string, len(a.Command))
	for i, arg := range a.Command {
		out[i] = a.expandString(arg, vars)
	}
	return out
}

// ExpandDir substitutes placeholders in a.Dir using the same rules as
// ExpandCommand.
func (a ActionDef) ExpandDir(vars map[string]string) string {
	if a.Dir == "" {
		return ""
	}
	return a.expandString(a.Dir, vars)
}

// ExpandEnv substitutes placeholders in a.Env values without mutating a.Env.
func (a ActionDef) ExpandEnv(vars map[string]string) map[string]string {
	if len(a.Env) == 0 {
		return nil
	}
	out := make(map[string]string, len(a.Env))
	for k, v := range a.Env {
		out[k] = a.expandString(v, vars)
	}
	return out
}

func (a ActionDef) expandString(tpl string, vars map[string]string) string {
	if !strings.Contains(tpl, "{{") {
		return tpl
	}
	return placeholderRe.ReplaceAllStringFunc(tpl, func(match string) string {
		sub := placeholderRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return ""
		}
		return a.lookupVar(sub[1], vars)
	})
}

func (a ActionDef) lookupVar(key string, vars map[string]string) string {
	if vars != nil {
		if val, ok := vars[key]; ok {
			return val
		}
	}
	switch key {
	case "label":
		if vars != nil {
			if val := vars["title"]; val != "" {
				return val
			}
			if val := vars["id"]; val != "" {
				return val
			}
		}
	case "target":
		if vars != nil {
			if val := vars["id"]; val != "" {
				return val
			}
		}
	case "id":
		if vars != nil {
			if val := vars["target"]; val != "" {
				return val
			}
		}
	case "noun":
		return strings.TrimSpace(a.Noun)
	case "verb":
		return strings.TrimSpace(a.Verb)
	}
	return ""
}

func (a ActionDef) normalized() ActionDef {
	return ActionDef{
		Noun:        strings.TrimSpace(a.Noun),
		Verb:        strings.TrimSpace(a.Verb),
		Description: strings.TrimSpace(a.Description),
		Command:     slices.Clone(a.Command),
		Foreground:  a.Foreground,
		History:     strings.TrimSpace(a.History),
		Dir:         strings.TrimSpace(a.Dir),
		Env:         maps.Clone(a.Env),
	}
}

func isValidNoun(noun string) bool {
	if noun == "" || strings.ContainsAny(noun, " \t\n\r") {
		return false
	}
	if strings.HasPrefix(noun, ".") || strings.HasSuffix(noun, ".") || strings.Contains(noun, "..") {
		return false
	}
	return true
}

func isValidVerb(verb string) bool {
	if verb == "" || strings.ContainsAny(verb, ". \t\n\r") {
		return false
	}
	return true
}

// stripJSONC removes // and /* */ comments and trailing commas outside of
// string literals. Newlines inside comments are kept so error offsets line up.
func stripJSONC(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			end := min(j+1, len(src))
			out = append(out, src[i:end]...)
			i = end - 1
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i < len(src) && !(src[i] == '*' && i+1 < len(src) && src[i+1] == '/') {
				if src[i] == '\n' {
					out = append(out, '\n')
				}
				i++
			}
			i++
		case c == ',':
			j := i + 1
			for j < len(src) {
				if src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r' {
					j++
				} else if src[j] == '/' && j+1 < len(src) && src[j+1] == '/' {
					for j < len(src) && src[j] != '\n' {
						j++
					}
				} else if src[j] == '/' && j+1 < len(src) && src[j+1] == '*' {
					j += 2
					for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
						j++
					}
					j += 2
				} else {
					break
				}
			}
			if j < len(src) && (src[j] == '}' || src[j] == ']') {
				continue // trailing comma: drop it; whitespace/comments follow normally
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
