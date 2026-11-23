package actions

import (
	"maps"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// ExecSpec declares how a record action executes an external command.
type ExecSpec struct {
	Argv         []string          `json:"argv"`
	Dir          string            `json:"dir,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Foreground   bool              `json:"foreground,omitempty"`
	Title        string            `json:"title,omitempty"`
	RefreshKinds []string          `json:"refreshKinds,omitempty"`
}

// GuardSpec declares a precondition evaluated against the focused record before
// running the action. When HideWhenFalse is false (the default), the action
// stays visible in `;` Actions and emits RefuseMessage when invoked on a
// non-matching record.
type GuardSpec struct {
	When          string `json:"when"`
	RefuseMessage string `json:"refuseMessage,omitempty"`
	HideWhenFalse bool   `json:"hideWhenFalse,omitempty"`
}

// ConfirmSpec declares a confirmation modal shown before execution.
type ConfirmSpec struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Label   string `json:"label,omitempty"`
	Tone    string `json:"tone,omitempty"`
}

// FeedbackSpec declares optional toast/status templates for each lifecycle phase.
type FeedbackSpec struct {
	Running string `json:"running,omitempty"`
	Success string `json:"success,omitempty"`
	Failure string `json:"failure,omitempty"`
	Queued  string `json:"queued,omitempty"`
}

// RecordActionSpec is the unified declarative specification for a record-scoped
// command action, shared by user actions.json entries and mounted extension
// catalog commands.
type RecordActionSpec struct {
	ID          string           `json:"id"`
	Description string           `json:"description,omitempty"`
	Noun        string           `json:"noun,omitempty"`
	Verb        string           `json:"verb,omitempty"`
	Kinds       []string         `json:"kinds,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	Params      []core.ParamSpec `json:"params,omitempty"`
	Source      string           `json:"source,omitempty"`
	Binary      string           `json:"binary,omitempty"`
	CLI         string           `json:"cli,omitempty"`
	Key         string           `json:"key,omitempty"`
	KeyHint     string           `json:"keyHint,omitempty"`
	History     string           `json:"history,omitempty"`
	UserAction  bool             `json:"userAction,omitempty"`
	LegacyDef   *ActionDef       `json:"-"`
	ExtraMeta   map[string]any   `json:"meta,omitempty"`
	Exec        ExecSpec         `json:"exec"`
	Guard       *GuardSpec       `json:"guard,omitempty"`
	Confirm     *ConfirmSpec     `json:"confirm,omitempty"`
	Feedback    *FeedbackSpec    `json:"feedback,omitempty"`
}

// ToCommandSpec converts s into a core.CommandSpec suitable for registration in
// core.Registry, `;` Actions, `:` Palette, inspector, and bindings.json.
func (s RecordActionSpec) ToCommandSpec() core.CommandSpec {
	id := strings.TrimSpace(s.ID)
	noun := strings.TrimSpace(s.Noun)
	verb := strings.TrimSpace(s.Verb)
	if noun == "" && len(s.Kinds) == 1 {
		noun = strings.TrimSpace(s.Kinds[0])
	}

	desc := strings.TrimSpace(s.Description)
	if desc == "" && len(s.Exec.Argv) > 0 {
		desc = strings.Join(s.Exec.Argv, " ")
	}
	title := desc
	if title == "" {
		title = id
	}

	tags := slices.Clone(s.Tags)
	if len(tags) == 0 {
		tags = []string{"action"}
	}
	if noun != "" && !slices.Contains(tags, noun) {
		tags = append(tags, noun)
	}

	meta := map[string]any{}
	if len(s.ExtraMeta) > 0 {
		maps.Copy(meta, s.ExtraMeta)
	}
	meta[MetaRecord] = true
	meta[MetaTitle] = title
	meta[MetaForeground] = s.Exec.Foreground
	if len(s.Exec.Argv) > 0 {
		meta[MetaCommand] = slices.Clone(s.Exec.Argv)
	}
	if noun != "" {
		meta[MetaNoun] = noun
		meta[MetaScope] = noun
	}
	if verb != "" {
		meta[MetaVerb] = verb
	}
	if s.Source != "" {
		meta[MetaSource] = s.Source
	}
	if s.UserAction {
		meta[MetaUserAction] = true
	}
	if s.LegacyDef != nil {
		meta[MetaAction] = *s.LegacyDef
	}
	if len(s.Kinds) > 0 {
		filtered := make([]string, 0, len(s.Kinds))
		for _, k := range s.Kinds {
			k = strings.TrimSpace(k)
			if k != "" && k != "record" && k != "*" {
				filtered = append(filtered, k)
			}
		}
		if len(filtered) > 0 {
			meta[MetaKinds] = filtered
		}
	}
	if s.Guard != nil && s.Guard.HideWhenFalse && strings.TrimSpace(s.Guard.When) != "" {
		meta["when"] = strings.TrimSpace(s.Guard.When)
	}
	cli := strings.TrimSpace(s.CLI)
	if cli == "" {
		if existing, ok := meta["cli"].(string); ok {
			cli = strings.TrimSpace(existing)
		}
	}
	if cli == "" && len(s.Exec.Argv) > 0 {
		display := slices.Clone(s.Exec.Argv)
		if display[0] == "{{binary}}" {
			switch {
			case s.Source != "" && s.Source != ActionsFile:
				display[0] = s.Source
			case s.Binary != "":
				display[0] = s.Binary
			}
		}
		cli = strings.Join(display, " ")
	}
	if cli != "" {
		meta["cli"] = cli
	}
	if s.History != "" {
		meta[MetaHistory] = s.History
	}
	if s.Exec.Dir != "" {
		meta[MetaDir] = s.Exec.Dir
	}
	if len(s.Exec.Env) > 0 {
		meta[MetaEnv] = maps.Clone(s.Exec.Env)
	}

	return core.CommandSpec{
		ID:          id,
		Description: desc,
		Params:      slices.Clone(s.Params),
		Tags:        tags,
		Meta:        meta,
	}
}

// ExpandTemplate substitutes {{key}} and fallback chains {{a|b|'fallback'}} /
// {{a|b|"fallback"}} in a single pass so substituted values containing {{...}}
// are never re-expanded.
func ExpandTemplate(tpl string, vars map[string]string) string {
	if !strings.Contains(tpl, "{{") {
		return tpl
	}
	var out strings.Builder
	out.Grow(len(tpl))
	i := 0
	for i < len(tpl) {
		openIdx := strings.Index(tpl[i:], "{{")
		if openIdx < 0 {
			out.WriteString(tpl[i:])
			break
		}
		out.WriteString(tpl[i : i+openIdx])
		start := i + openIdx + 2
		closeIdx := findPlaceholderEnd(tpl, start)
		if closeIdx < 0 {
			// Unterminated {{: keep literal remainder.
			out.WriteString(tpl[i+openIdx:])
			break
		}
		expr := tpl[start:closeIdx]
		out.WriteString(evalTemplateExpr(expr, vars))
		i = closeIdx + 2
	}
	return out.String()
}

// ExpandArgv expands every element of argv independently using ExpandTemplate
// without mutating argv.
func ExpandArgv(argv []string, vars map[string]string) []string {
	if len(argv) == 0 {
		return nil
	}
	out := make([]string, len(argv))
	for i, arg := range argv {
		out[i] = ExpandTemplate(arg, vars)
	}
	return out
}

// ExpandEnvMap expands every value in env using ExpandTemplate without mutating env.
func ExpandEnvMap(env map[string]string, vars map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = ExpandTemplate(v, vars)
	}
	return out
}

func findPlaceholderEnd(s string, start int) int {
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '}' && i+1 < len(s) && s[i+1] == '}' {
			return i
		}
	}
	return -1
}

func evalTemplateExpr(expr string, vars map[string]string) string {
	parts := splitFallbackChain(expr)
	for _, raw := range parts {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if lit, ok := unquoteLiteral(part); ok {
			return lit
		}
		if val := lookupTemplateVar(part, vars); val != "" {
			return val
		}
	}
	return ""
}

func splitFallbackChain(expr string) []string {
	var parts []string
	var quote byte
	start := 0
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(expr) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '|' {
			parts = append(parts, expr[start:i])
			start = i + 1
		}
	}
	parts = append(parts, expr[start:])
	return parts
}

func unquoteLiteral(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	q := s[0]
	if (q != '\'' && q != '"') || s[len(s)-1] != q {
		return "", false
	}
	inner := s[1 : len(s)-1]
	if !strings.ContainsRune(inner, '\\') {
		return inner, true
	}
	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
		}
		b.WriteByte(inner[i])
	}
	return b.String(), true
}

func lookupTemplateVar(key string, vars map[string]string) string {
	if vars == nil {
		return ""
	}
	if val, ok := vars[key]; ok && val != "" {
		return val
	}
	switch key {
	case "label":
		if val := vars["title"]; val != "" {
			return val
		}
		if val := vars["id"]; val != "" {
			return val
		}
	case "target":
		if val := vars["id"]; val != "" {
			return val
		}
	case "id":
		if val := vars["target"]; val != "" {
			return val
		}
	}
	return ""
}
