// Package handlers decides what opens a file, a directory or a URL. The
// decision is data: a list of rules, each matching targets by a glob on the
// file name, a URL scheme or the word "dir", and naming the handler that
// opens them. The app carries the handlers out; this package only matches.
//
// handlers imports only the standard library.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Rule kinds: who carries the rule out.
const (
	// KindBuiltin is a handler the app has itself, named by Rule.Name.
	KindBuiltin = "builtin"
	// KindExec runs Rule.Command with the target, in a pane of its own.
	KindExec = "exec"
	// KindShell runs Rule.Command with the target as a background shell command without opening a pane.
	KindShell = "shell"
)

// Names of the built-in handlers.
const (
	MarkdownReader = "markdown-reader"
	SideVim        = "side-vim"
	PathPicker     = "pathpicker"
	NewPane        = "new-pane"
	SystemOpen     = "system-open"
)

// MatchDir is the Match of a rule for directories.
const MatchDir = "dir"

// Rule says that targets matching Match are opened by the handler Name.
//
// Match is a glob on the target's file name ("*.csv", case-insensitive), a
// URL pattern ("https://*") or MatchDir. Command is the command line of a
// KindExec rule: "{target}" stands for the target, which is appended when
// the command does not name it. The matching rule with the highest Priority
// is the one "open" uses; the others are offered by "open with".
type Rule struct {
	Name     string `json:"name"`
	Match    string `json:"match"`
	Kind     string `json:"kind"`
	Command  string `json:"command,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

// Registry is an ordered list of rules.
type Registry struct {
	rules []Rule
	// IsDir reports whether a path is a directory; nil asks the file system.
	IsDir func(path string) bool
}

// New returns a registry holding rules.
func New(rules ...Rule) *Registry { return &Registry{rules: slices.Clone(rules)} }

// Default returns a registry holding DefaultRules.
func Default() *Registry { return New(DefaultRules()...) }

// DefaultRules returns the rules Matomeru starts with.
func DefaultRules() []Rule {
	return []Rule{
		{Name: "o-csv2", Match: "*.csv", Kind: KindExec, Command: "o-csv2 {target}", Priority: 100},
		{Name: "o-csv2", Match: "*.tsv", Kind: KindExec, Command: "o-csv2 {target}", Priority: 100},
		{Name: "o-json2", Match: "*.json", Kind: KindExec, Command: "o-json2 {target}", Priority: 100},
		{Name: "o-json2", Match: "*.jsonl", Kind: KindExec, Command: "o-json2 {target}", Priority: 100},
		{Name: MarkdownReader, Match: "*.md", Kind: KindBuiltin, Priority: 100},
		{Name: SideVim, Match: "*.md", Kind: KindBuiltin, Priority: 50},
		{Name: PathPicker, Match: MatchDir, Kind: KindBuiltin, Priority: 100},
		{Name: NewPane, Match: MatchDir, Kind: KindBuiltin, Priority: 50},
		{Name: SystemOpen, Match: "http://*", Kind: KindBuiltin, Priority: 100},
		{Name: SystemOpen, Match: "https://*", Kind: KindBuiltin, Priority: 100},
		{Name: SystemOpen, Match: "*.png", Kind: KindBuiltin, Priority: 100},
		{Name: SystemOpen, Match: "*.jpg", Kind: KindBuiltin, Priority: 100},
		{Name: SystemOpen, Match: "*.jpeg", Kind: KindBuiltin, Priority: 100},
		{Name: SystemOpen, Match: "*.gif", Kind: KindBuiltin, Priority: 100},
		{Name: SystemOpen, Match: "*.webp", Kind: KindBuiltin, Priority: 100},
		{Name: SystemOpen, Match: "*.svg", Kind: KindBuiltin, Priority: 100},
	}
}

// Rules returns a copy of the rules, in declaration order.
func (r *Registry) Rules() []Rule { return slices.Clone(r.rules) }

// Add lays rules over the registry: a rule with the Name and Match of an
// existing one replaces it in place, any other is added at the end.
func (r *Registry) Add(rules ...Rule) {
	for _, rule := range rules {
		i := slices.IndexFunc(r.rules, func(old Rule) bool { return old.Name == rule.Name && old.Match == rule.Match })
		if i >= 0 {
			r.rules[i] = rule
		} else {
			r.rules = append(r.rules, rule)
		}
	}
}

// LoadJSONC lays the rules of a handlers.json over the registry. The file is
// JSON with comments and trailing commas: a list of rules, or an object with
// a "rules" list. A rule needs a name and a match; its kind defaults to
// "exec" when it has a command and to "builtin" otherwise. Nothing is
// changed when the file cannot be read.
func (r *Registry) LoadJSONC(data []byte) error {
	clean := stripJSONC(data)
	var rules []Rule
	if err := json.Unmarshal(clean, &rules); err != nil {
		var wrapped struct {
			Rules []Rule `json:"rules"`
		}
		if err2 := json.Unmarshal(clean, &wrapped); err2 != nil {
			return fmt.Errorf("handlers: %w", err)
		}
		rules = wrapped.Rules
	}
	for i := range rules {
		rule := &rules[i]
		switch {
		case rule.Name == "" || rule.Match == "":
			return fmt.Errorf("handlers: rule %d needs a name and a match", i+1)
		case rule.Kind == "" && rule.Command != "":
			rule.Kind = KindExec
		case rule.Kind == "":
			rule.Kind = KindBuiltin
		case rule.Kind != KindExec && rule.Kind != KindBuiltin && rule.Kind != KindShell:
			return fmt.Errorf("handlers: rule %d (%s): kind %q is not %q, %q, or %q", i+1, rule.Name, rule.Kind, KindBuiltin, KindExec, KindShell)
		}
		if (rule.Kind == KindExec || rule.Kind == KindShell) && rule.Command == "" {
			return errors.New("handlers: rule " + rule.Name + " is of kind " + rule.Kind + " and has no command")
		}
	}
	r.Add(rules...)
	return nil
}

// IsURL reports whether target is a URL: it has a scheme followed by "://".
func IsURL(target string) bool {
	scheme, rest, ok := strings.Cut(target, "://")
	if !ok || scheme == "" || rest == "" {
		return false
	}
	for _, c := range scheme {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func (r *Registry) isDir(target string) bool {
	if strings.HasSuffix(target, "/") {
		return true
	}
	if r.IsDir != nil {
		return r.IsDir(target)
	}
	info, err := os.Stat(target)
	return err == nil && info.IsDir()
}

// matches reports whether one rule's Match covers target.
func (r *Registry) matches(match, target string) bool {
	switch {
	case match == MatchDir:
		return !IsURL(target) && r.isDir(target)
	case IsURL(match) || strings.HasSuffix(match, "://"):
		if !IsURL(target) {
			return false
		}
		ok, err := path.Match(strings.ToLower(match), strings.ToLower(target))
		if err == nil && ok {
			return true
		}
		// "https://*" covers every https URL, slashes and all.
		prefix, wild := strings.CutSuffix(match, "*")
		return wild && strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix))
	case IsURL(target):
		return false
	}
	name := strings.ToLower(filepath.Base(strings.TrimRight(target, "/")))
	ok, err := path.Match(strings.ToLower(match), name)
	return err == nil && ok
}

// Match returns the rules that open target, the highest priority first;
// rules of one priority keep their declaration order. The first is what
// "open" uses.
func (r *Registry) Match(target string) []Rule {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	var out []Rule
	for _, rule := range r.rules {
		if r.matches(rule.Match, target) {
			out = append(out, rule)
		}
	}
	slices.SortStableFunc(out, func(a, b Rule) int { return b.Priority - a.Priority })
	return out
}

// Quote writes s as one word of a POSIX shell command line.
func Quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;~#") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CommandLine is the shell command line of a KindExec rule for target.
func (rule Rule) CommandLine(target string) string {
	if strings.Contains(rule.Command, "{target}") {
		return strings.ReplaceAll(rule.Command, "{target}", Quote(target))
	}
	return strings.TrimSpace(rule.Command + " " + Quote(target))
}

// stripJSONC removes // and /* */ comments and trailing commas, leaving
// strings as they are.
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
			out = append(out, src[i:min(j+1, len(src))]...)
			i = j
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i++
		case c == ',':
			j := i + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r') {
				j++
			}
			if j < len(src) && (src[j] == ']' || src[j] == '}') {
				continue // a trailing comma
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
