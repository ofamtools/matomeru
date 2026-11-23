// Package harness loads and saves Matomeru's data-driven harness configuration
// (~/.config/matomeru/harnesses.json), parses live machine inventory from o-ai,
// and merges them into a finite picker list without running subprocesses.
//
// harness imports only the standard library.
package harness

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Entry sources produced by MergeInstalled.
const (
	SourceInstalled = "installed"
	SourceCustom    = "custom"
	SourceShell     = "shell"
)

// HarnessConfig is a data-driven harness entry stored in harnesses.json.
type HarnessConfig struct {
	Name       string            `json:"name"`
	Label      string            `json:"label,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	HookConfig map[string]any    `json:"hookConfig,omitempty"`
	HookArgs   []string          `json:"hookArgs,omitempty"`
	MCPFlag    []string          `json:"mcpFlag,omitempty"`
	Shell      bool              `json:"shell,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
}

// FileConfig represents ~/.config/matomeru/harnesses.json.
type FileConfig struct {
	Default   string                   `json:"default,omitempty"`
	Harnesses map[string]HarnessConfig `json:"harnesses,omitempty"`
}

// InstalledHarness is one ai.harness inventory row emitted by o-ai.
type InstalledHarness struct {
	ID            string `json:"id"`
	Installed     bool   `json:"installed"`
	Configured    bool   `json:"configured"`
	Executable    string `json:"executable,omitempty"`
	ProcessStatus string `json:"processStatus,omitempty"`
	CatalogStatus string `json:"catalogStatus,omitempty"`
}

// Entry is one resolved picker item produced by MergeInstalled.
type Entry struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Label      string            `json:"label,omitempty"`
	Command    []string          `json:"command"`
	Env        map[string]string `json:"env,omitempty"`
	HookConfig map[string]any    `json:"hookConfig,omitempty"`
	HookArgs   []string          `json:"hookArgs,omitempty"`
	MCPFlag    []string          `json:"mcpFlag,omitempty"`
	Executable string            `json:"executable,omitempty"`
	Installed  bool              `json:"installed"`
	Configured bool              `json:"configured"`
	Custom     bool              `json:"custom,omitempty"`
	Shell      bool              `json:"shell,omitempty"`
	Source     string            `json:"source"`
}

// DefaultConfigPath returns ~/.config/matomeru/harnesses.json (honoring
// $XDG_CONFIG_HOME when set).
func DefaultConfigPath() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "matomeru", "harnesses.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "matomeru", "harnesses.json")
	}
	return filepath.Join(home, ".config", "matomeru", "harnesses.json")
}

// DefaultConfigs returns the minimal built-in harness defaults for omp, claude,
// codex, gemini, antigravity, and zsh. Users can override any field in
// harnesses.json.
func DefaultConfigs() map[string]HarnessConfig {
	return map[string]HarnessConfig{
		"omp": {
			Name:    "omp",
			Label:   "OMP",
			Command: []string{"omp"},
			HookConfig: map[string]any{
				"command": "mtm-hook omp",
				"events": []string{
					"session_start",
					"agent_start",
					"agent_end",
					"tool_start",
					"tool_end",
					"tool_error",
					"tool_approval_requested",
					"tool_approval_resolved",
					"subagent_start",
					"subagent_stop",
				},
			},
		},
		"claude": {
			Name:    "claude",
			Label:   "Claude Code",
			Command: []string{"claude"},
			HookConfig: map[string]any{
				"hooks": map[string]any{
					"UserPromptSubmit":   []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"PreToolUse":         []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"PostToolUse":        []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"PostToolUseFailure": []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"PermissionRequest":  []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude", "timeout": 310}}}},
					"Notification":       []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"Stop":               []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"StopFailure":        []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"SessionEnd":         []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"SubagentStart":      []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
					"SubagentStop":       []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": "mtm-hook claude"}}}},
				},
			},
		},
		"codex": {
			Name:    "codex",
			Label:   "Codex",
			Command: []string{"codex"},
			HookConfig: map[string]any{
				"command": "mtm-hook codex",
				"events": []string{
					"SessionStart",
					"UserPromptSubmit",
					"PreToolUse",
					"PostToolUse",
					"PostToolUseFailure",
					"PermissionRequest",
					"Stop",
					"StopFailure",
					"Interrupt",
					"SessionEnd",
				},
			},
		},
		"gemini": {
			Name:    "gemini",
			Label:   "Gemini CLI",
			Command: []string{"gemini"},
			HookConfig: map[string]any{
				"command": "mtm-hook gemini",
				"events": []string{
					"SessionStart",
					"BeforeAgent",
					"BeforeTool",
					"AfterTool",
					"Notification",
					"AfterAgent",
					"SessionEnd",
				},
			},
		},
		"agy": {
			Name:    "agy",
			Label:   "Antigravity",
			Command: []string{"agy"},
			HookConfig: map[string]any{
				"command": "mtm-hook antigravity",
				"events": []string{
					"PreInvocation",
					"PostInvocation",
					"BeforeTool",
					"AfterTool",
					"PermissionRequest",
					"SessionEnd",
				},
			},
		},
		"antigravity": {
			Name:    "antigravity",
			Label:   "Antigravity",
			Command: []string{"agy"},
			HookConfig: map[string]any{
				"command": "mtm-hook antigravity",
				"events": []string{
					"PreInvocation",
					"PostInvocation",
					"BeforeTool",
					"AfterTool",
					"PermissionRequest",
					"SessionEnd",
				},
			},
		},
		"zsh": {
			Name:    "zsh",
			Label:   "Shell (zsh)",
			Command: []string{"zsh"},
			Shell:   true,
		},
	}
}

// ParseConfig parses a harnesses.json (or JSONC with comments/trailing commas).
// Supports:
//   - {"default": "claude", "harnesses": {"claude": {...}}}
//   - {"default": "claude", "harnesses": [{"name": "claude", ...}]}
//   - [{"name": "claude", ...}]
func ParseConfig(data []byte) (FileConfig, error) {
	clean := stripJSONC(data)
	trimmed := strings.TrimSpace(string(clean))
	if trimmed == "" {
		return FileConfig{}, nil
	}

	// Array shorthand: [{"name": "...", ...}]
	if strings.HasPrefix(trimmed, "[") {
		var list []HarnessConfig
		if err := json.Unmarshal(clean, &list); err != nil {
			return FileConfig{}, fmt.Errorf("harness: parse config array: %w", err)
		}
		cfg := FileConfig{Harnesses: make(map[string]HarnessConfig, len(list))}
		for i, h := range list {
			id := normalizeID(h.Name)
			if id == "" {
				return FileConfig{}, fmt.Errorf("harness: entry %d missing name", i+1)
			}
			h.Name = id
			cfg.Harnesses[id] = h
		}
		return cfg, nil
	}

	// Object form: {"default": "...", "harnesses": {...} or [...]}
	var raw struct {
		Default   string          `json:"default"`
		Harnesses json.RawMessage `json:"harnesses"`
	}
	if err := json.Unmarshal(clean, &raw); err != nil {
		return FileConfig{}, fmt.Errorf("harness: parse config: %w", err)
	}
	cfg := FileConfig{
		Default:   normalizeID(raw.Default),
		Harnesses: map[string]HarnessConfig{},
	}

	hTrim := strings.TrimSpace(string(raw.Harnesses))
	if hTrim == "" || hTrim == "null" {
		return cfg, nil
	}
	if strings.HasPrefix(hTrim, "[") {
		var list []HarnessConfig
		if err := json.Unmarshal(raw.Harnesses, &list); err != nil {
			return FileConfig{}, fmt.Errorf("harness: parse harnesses array: %w", err)
		}
		for i, h := range list {
			id := normalizeID(h.Name)
			if id == "" {
				return FileConfig{}, fmt.Errorf("harness: entry %d missing name", i+1)
			}
			h.Name = id
			cfg.Harnesses[id] = h
		}
		return cfg, nil
	}

	var byName map[string]HarnessConfig
	if err := json.Unmarshal(raw.Harnesses, &byName); err != nil {
		return FileConfig{}, fmt.Errorf("harness: parse harnesses map: %w", err)
	}
	for k, h := range byName {
		id := normalizeID(h.Name)
		if id == "" {
			id = normalizeID(k)
		}
		if id == "" {
			return FileConfig{}, errors.New("harness: empty harness name in map")
		}
		h.Name = id
		cfg.Harnesses[id] = h
	}
	return cfg, nil
}

// LoadFile reads and parses a harnesses.json file. If the file does not exist,
// LoadFile returns an empty FileConfig and nil error.
func LoadFile(path string) (FileConfig, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileConfig{}, nil
		}
		return FileConfig{}, err
	}
	return ParseConfig(data)
}

// SaveFile atomically writes cfg as indented JSON to path, creating parent
// directories as needed.
func SaveFile(path string, cfg FileConfig) error {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".harnesses-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// ParseOAIJSON parses JSON output from `o-ai query --kind ai.harness`,
// `o-ai harness list --json`, `o-ai inventory show --json`, or a direct JSON
// array of InstalledHarness rows.
func ParseOAIJSON(data []byte) ([]InstalledHarness, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}

	// Object shapes:
	// 1. o-ai query --kind ai.harness: {"items":[{"kind":"ai.harness","id":"...","fields":{...}}],"total":N}
	// 2. o-ai inventory show --json:   {"harnesses":[{"id":"...","installed":true,...}]}
	if strings.HasPrefix(trimmed, "{") {
		var envelope struct {
			Items []struct {
				Kind   string          `json:"kind"`
				ID     string          `json:"id"`
				Fields json.RawMessage `json:"fields"`
			} `json:"items"`
			Harnesses []InstalledHarness `json:"harnesses"`
		}
		if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
			return nil, fmt.Errorf("harness: parse o-ai json: %w", err)
		}
		if len(envelope.Harnesses) > 0 {
			return normalizeInstalled(envelope.Harnesses), nil
		}
		out := make([]InstalledHarness, 0, len(envelope.Items))
		for _, item := range envelope.Items {
			if item.Kind != "" && item.Kind != "ai.harness" && item.Kind != "harness" {
				continue
			}
			var h InstalledHarness
			if len(item.Fields) > 0 {
				if err := json.Unmarshal(item.Fields, &h); err != nil {
					return nil, fmt.Errorf("harness: parse o-ai item %q: %w", item.ID, err)
				}
			}
			if h.ID == "" {
				h.ID = item.ID
			}
			if id := normalizeID(h.ID); id != "" {
				h.ID = id
				out = append(out, h)
			}
		}
		return normalizeInstalled(out), nil
	}

	// Array shapes:
	// 1. o-ai harness list --json: [{"id":"harness/claude","label":"claude","data":{...}}]
	// 2. Direct []InstalledHarness
	if strings.HasPrefix(trimmed, "[") {
		var rawList []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &rawList); err != nil {
			return nil, fmt.Errorf("harness: parse o-ai json array: %w", err)
		}
		out := make([]InstalledHarness, 0, len(rawList))
		for _, elem := range rawList {
			var wrapped struct {
				ID    string          `json:"id"`
				Label string          `json:"label"`
				Data  json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(elem, &wrapped); err != nil {
				return nil, fmt.Errorf("harness: parse o-ai array element: %w", err)
			}
			var h InstalledHarness
			if len(wrapped.Data) > 0 && string(wrapped.Data) != "null" {
				if err := json.Unmarshal(wrapped.Data, &h); err != nil {
					return nil, fmt.Errorf("harness: parse o-ai data field: %w", err)
				}
			} else {
				if err := json.Unmarshal(elem, &h); err != nil {
					return nil, fmt.Errorf("harness: parse o-ai harness row: %w", err)
				}
			}
			if h.ID == "" {
				h.ID = wrapped.Label
			}
			if h.ID == "" {
				h.ID = wrapped.ID
			}
			if id := normalizeID(h.ID); id != "" {
				h.ID = id
				out = append(out, h)
			}
		}
		return normalizeInstalled(out), nil
	}

	return nil, errors.New("harness: o-ai json must be an object or array")
}

// MergeInstalled merges live machine inventory rows from o-ai with built-in
// defaults and user overrides from harnesses.json into the finite picker list:
//   - installed harnesses actually available on the machine
//   - custom user-defined harnesses in harnesses.json
//   - plain shell (zsh, or a user-configured shell entry)
//
// MergeInstalled is completely pure and never executes subprocesses.
func MergeInstalled(installed []InstalledHarness, cfg FileConfig) []Entry {
	defaults := DefaultConfigs()
	userCfg := make(map[string]HarnessConfig, len(cfg.Harnesses))
	for k, h := range cfg.Harnesses {
		id := normalizeID(h.Name)
		if id == "" {
			id = normalizeID(k)
		}
		if id == "" {
			continue
		}
		h.Name = id
		userCfg[id] = h
	}

	seen := map[string]bool{}
	var installedEntries []Entry
	var customEntries []Entry
	var shellEntries []Entry

	// 1. Installed harnesses reported by o-ai.
	for _, inv := range normalizeInstalled(installed) {
		if !inv.Installed {
			continue
		}
		id := inv.ID
		if seen[id] {
			continue
		}
		base := defaults[id]
		merged := base
		if alias := aliasID(id); alias != "" {
			if merged.Name == "" {
				merged = defaults[alias]
			}
			if au, hasAlias := userCfg[alias]; hasAlias {
				merged = overlayConfig(merged, au, id)
				if len(au.Command) == 0 {
					seen[alias] = true
				}
			}
		}
		merged = overlayConfig(merged, userCfg[id], id)
		if merged.Disabled {
			seen[id] = true
			continue
		}
		if len(merged.Command) == 0 {
			if inv.Executable != "" {
				merged.Command = []string{inv.Executable}
			} else {
				merged.Command = []string{id}
			}
		}
		entry := entryFromConfig(merged, inv, false, SourceInstalled)
		if entry.Shell {
			shellEntries = append(shellEntries, entry)
		} else {
			installedEntries = append(installedEntries, entry)
		}
		seen[id] = true
	}

	// 2. Custom user-defined entries (and user-customized shell entries) from harnesses.json.
	for id, u := range userCfg {
		if seen[id] || u.Disabled {
			continue
		}
		def, hasDef := defaults[id]
		merged := overlayConfig(def, u, id)
		// If the user explicitly configured a shell entry, keep it in the shell group.
		if merged.Shell {
			if len(merged.Command) == 0 {
				merged.Command = []string{id}
			}
			shellEntries = append(shellEntries, entryFromConfig(merged, InstalledHarness{ID: id}, false, SourceShell))
			seen[id] = true
			continue
		}
		// Only include non-installed entries when the user explicitly provided a
		// command (or it is a custom harness not in built-in defaults).
		if len(u.Command) == 0 && hasDef {
			continue
		}
		if len(merged.Command) == 0 {
			merged.Command = []string{id}
		}
		customEntries = append(customEntries, entryFromConfig(merged, InstalledHarness{ID: id}, true, SourceCustom))
		seen[id] = true
	}

	// 3. Always include plain shell (zsh) unless explicitly disabled or already present.
	if len(shellEntries) == 0 {
		if u, ok := userCfg["zsh"]; !ok || !u.Disabled {
			merged := overlayConfig(defaults["zsh"], u, "zsh")
			merged.Shell = true
			if len(merged.Command) == 0 {
				merged.Command = []string{"zsh"}
			}
			shellEntries = append(shellEntries, entryFromConfig(merged, InstalledHarness{ID: "zsh"}, false, SourceShell))
		}
	}

	sortByID := func(a, b Entry) int { return cmp.Compare(a.ID, b.ID) }
	slices.SortFunc(installedEntries, sortByID)
	slices.SortFunc(customEntries, sortByID)
	slices.SortFunc(shellEntries, sortByID)

	out := make([]Entry, 0, len(installedEntries)+len(customEntries)+len(shellEntries))
	out = append(out, installedEntries...)
	out = append(out, customEntries...)
	out = append(out, shellEntries...)

	// Promote configured default harness to the top when present.
	if defID := normalizeID(cfg.Default); defID != "" {
		if idx := slices.IndexFunc(out, func(e Entry) bool { return e.ID == defID }); idx > 0 {
			def := out[idx]
			copy(out[1:idx+1], out[0:idx])
			out[0] = def
		}
	}

	return out
}

// ExpandMCPFlag replaces {{mcp_url}} and {{pane_id}} placeholders in a
// harness's MCPFlag template without mutating the input slice.
func ExpandMCPFlag(tpl []string, mcpURL, paneID string) []string {
	if len(tpl) == 0 {
		return nil
	}
	out := make([]string, len(tpl))
	for i, part := range tpl {
		s := strings.ReplaceAll(part, "{{mcp_url}}", mcpURL)
		s = strings.ReplaceAll(s, "{{pane_id}}", paneID)
		out[i] = s
	}
	return out
}

// BuildLaunchCommand constructs the effective argv and env map for launching
// entry from pure configuration data (Command + HookArgs + expanded MCPFlag).
func BuildLaunchCommand(entry Entry, mcpURL, paneID string) ([]string, map[string]string) {
	argv := make([]string, 0, len(entry.Command)+len(entry.HookArgs)+len(entry.MCPFlag))
	argv = append(argv, entry.Command...)
	argv = append(argv, entry.HookArgs...)
	if mcpURL != "" && len(entry.MCPFlag) > 0 {
		argv = append(argv, ExpandMCPFlag(entry.MCPFlag, mcpURL, paneID)...)
	}
	var env map[string]string
	if len(entry.Env) > 0 {
		env = make(map[string]string, len(entry.Env))
		maps.Copy(env, entry.Env)
	}
	return argv, env
}

func overlayConfig(base, override HarnessConfig, id string) HarnessConfig {
	out := HarnessConfig{
		Name:       id,
		Label:      base.Label,
		Command:    slices.Clone(base.Command),
		HookArgs:   slices.Clone(base.HookArgs),
		MCPFlag:    slices.Clone(base.MCPFlag),
		Shell:      base.Shell,
		Disabled:   base.Disabled,
		HookConfig: cloneMap(base.HookConfig),
	}
	if len(base.Env) > 0 {
		out.Env = make(map[string]string, len(base.Env))
		maps.Copy(out.Env, base.Env)
	}

	if override.Label != "" {
		out.Label = override.Label
	}
	if len(override.Command) > 0 {
		out.Command = slices.Clone(override.Command)
	}
	if len(override.Env) > 0 {
		if out.Env == nil {
			out.Env = make(map[string]string, len(override.Env))
		}
		maps.Copy(out.Env, override.Env)
	}
	if len(override.HookConfig) > 0 {
		out.HookConfig = cloneMap(override.HookConfig)
	}
	if len(override.HookArgs) > 0 {
		out.HookArgs = slices.Clone(override.HookArgs)
	}
	if len(override.MCPFlag) > 0 {
		out.MCPFlag = slices.Clone(override.MCPFlag)
	}
	if override.Shell {
		out.Shell = true
	}
	if override.Disabled {
		out.Disabled = true
	}
	if out.Label == "" {
		out.Label = id
	}
	return out
}

func entryFromConfig(c HarnessConfig, inv InstalledHarness, custom bool, source string) Entry {
	return Entry{
		ID:         c.Name,
		Name:       c.Name,
		Label:      c.Label,
		Command:    slices.Clone(c.Command),
		Env:        maps.Clone(c.Env),
		HookConfig: cloneMap(c.HookConfig),
		HookArgs:   slices.Clone(c.HookArgs),
		MCPFlag:    slices.Clone(c.MCPFlag),
		Executable: inv.Executable,
		Installed:  inv.Installed,
		Configured: inv.Configured,
		Custom:     custom,
		Shell:      c.Shell,
		Source:     source,
	}
}

func normalizeInstalled(in []InstalledHarness) []InstalledHarness {
	out := make([]InstalledHarness, 0, len(in))
	seen := map[string]bool{}
	for _, h := range in {
		id := normalizeID(h.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		h.ID = id
		out = append(out, h)
	}
	return out
}

func normalizeID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "harness/")
	s = strings.TrimPrefix(s, "ai.harness/")
	return strings.TrimSpace(s)
}

// AliasID returns the known alternate ID for a built-in harness (e.g.
// "agy" <-> "antigravity"), or "" if none exists.
func AliasID(id string) string {
	return aliasID(normalizeID(id))
}

func aliasID(id string) string {
	switch id {
	case "agy":
		return "antigravity"
	case "antigravity":
		return "agy"
	default:
		return ""
	}
}

func cloneMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return maps.Clone(m)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return maps.Clone(m)
	}
	return out
}

// stripJSONC removes // and /* */ comments and trailing commas, preserving
// string literals intact.
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
			if i < len(src) {
				out = append(out, '\n')
			}
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
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
