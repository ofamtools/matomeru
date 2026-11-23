package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultClaudeHookCmd is the default hook command configured in Claude Code settings.json.
const DefaultClaudeHookCmd = "mtm-hook claude-code"

// ClaudeHookEventSpec defines one Claude Code lifecycle hook event and its timeout.
type ClaudeHookEventSpec struct {
	Event   string
	Timeout int
}

// ClaudeHookEvents lists all 14 Claude Code lifecycle events wired to mtm-hook.
var ClaudeHookEvents = []ClaudeHookEventSpec{
	{Event: "SessionStart", Timeout: 5},
	{Event: "UserPromptSubmit", Timeout: 5},
	{Event: "PreToolUse", Timeout: 5},
	{Event: "PostToolUse", Timeout: 5},
	{Event: "PostToolUseFailure", Timeout: 5},
	{Event: "PermissionRequest", Timeout: 310},
	{Event: "PermissionDenied", Timeout: 5},
	{Event: "Notification", Timeout: 5},
	{Event: "ElicitationResult", Timeout: 5},
	{Event: "Stop", Timeout: 5},
	{Event: "StopFailure", Timeout: 5},
	{Event: "SessionEnd", Timeout: 5},
	{Event: "SubagentStart", Timeout: 5},
	{Event: "SubagentStop", Timeout: 5},
}

func resolveClaudeSettingsPath(home string) (string, error) {
	if strings.TrimSpace(home) == "" {
		if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
			return filepath.Join(dir, "settings.json"), nil
		}
	}
	h, err := resolveHome(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude", "settings.json"), nil
}

// ConfigureClaudeHooks merges all 14 mtm-hook lifecycle hooks into Claude Code's
// settings.json without disturbing user settings, sibling matcher groups, or sibling hooks.
func ConfigureClaudeHooks(opts Options) ([]Action, error) {
	settingsPath, err := resolveClaudeSettingsPath(opts.Home)
	if err != nil {
		return nil, err
	}

	var raw []byte
	exists := false
	if data, rerr := os.ReadFile(settingsPath); rerr == nil {
		raw = data
		exists = true
	} else if !os.IsNotExist(rerr) {
		return nil, fmt.Errorf("read %s: %w", settingsPath, rerr)
	}

	out, changed, eventActions, err := MergeClaudeSettingsJSON(raw, opts.HookCmd, opts.Force)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", settingsPath, err)
	}

	var backup string
	if changed && !opts.DryRun && !opts.Check {
		backup, err = atomicWriteWithBackup(settingsPath, out, 0o600, opts.now())
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", settingsPath, err)
		}
	}

	for i := range eventActions {
		eventActions[i].Path = settingsPath
		switch eventActions[i].Status {
		case StatusCreated:
			switch {
			case opts.Check:
				eventActions[i].Status = StatusMissing
			case opts.DryRun:
				if !exists {
					eventActions[i].Status = StatusWouldCreate
				} else {
					eventActions[i].Status = StatusWouldUpdate
				}
			default:
				if exists {
					eventActions[i].Status = StatusUpdated
					eventActions[i].Backup = backup
				}
			}
		case StatusUpdated:
			switch {
			case opts.Check:
				eventActions[i].Status = StatusOutdated
			case opts.DryRun:
				eventActions[i].Status = StatusWouldUpdate
			default:
				eventActions[i].Backup = backup
			}
		}
	}
	return eventActions, nil
}

// MergeClaudeSettingsJSON merges the 14 mtm-hook lifecycle entries into raw
// Claude Code settings.json content. It preserves all unrelated keys, hook
// events, sibling matcher groups, and sibling hooks, and avoids duplicates by
// recognizing existing mtm-hook Claude invocations.
func MergeClaudeSettingsJSON(raw []byte, hookCmd string, force bool) (out []byte, changed bool, eventActions []Action, err error) {
	cmd := strings.TrimSpace(hookCmd)
	if cmd == "" {
		cmd = DefaultClaudeHookCmd
	}

	root := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, false, nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if root == nil {
			return nil, false, nil, fmt.Errorf("invalid JSON: top-level value must be an object")
		}
	}

	hooksMap := map[string]any{}
	if existingHooks, ok := root["hooks"]; ok && existingHooks != nil {
		m, isMap := existingHooks.(map[string]any)
		if !isMap {
			return nil, false, nil, fmt.Errorf("invalid hooks field: expected JSON object")
		}
		hooksMap = m
	}

	eventActions = make([]Action, 0, len(ClaudeHookEvents))
	for _, spec := range ClaudeHookEvents {
		var groups []any
		if rawGroups, ok := hooksMap[spec.Event]; ok && rawGroups != nil {
			list, isList := rawGroups.([]any)
			if !isList {
				return nil, false, nil, fmt.Errorf("invalid hooks.%s: expected JSON array", spec.Event)
			}
			groups = list
		}

		found := false
		updated := false
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				continue
			}
			hookList, ok := gm["hooks"].([]any)
			if !ok {
				continue
			}
			for _, h := range hookList {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				curCmd, _ := hm["command"].(string)
				if !isMTMClaudeHookCommand(curCmd, cmd) {
					continue
				}
				found = true
				if force {
					curType, _ := hm["type"].(string)
					curTimeout, hasTimeout := numberToInt(hm["timeout"])
					if curType != "command" || curCmd != cmd || !hasTimeout || curTimeout != spec.Timeout {
						hm["type"] = "command"
						hm["command"] = cmd
						hm["timeout"] = spec.Timeout
						updated = true
						changed = true
					}
				}
			}
		}

		act := Action{
			Component: "hooks",
			Target:    "claude/" + spec.Event,
		}
		switch {
		case !found:
			groups = append(groups, map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": cmd,
						"timeout": spec.Timeout,
					},
				},
			})
			hooksMap[spec.Event] = groups
			changed = true
			act.Status = StatusCreated
			act.Detail = fmt.Sprintf("%s (timeout %ds)", cmd, spec.Timeout)
		case updated:
			act.Status = StatusUpdated
			act.Detail = fmt.Sprintf("%s (timeout %ds)", cmd, spec.Timeout)
		default:
			act.Status = StatusUnchanged
		}
		eventActions = append(eventActions, act)
	}

	if !changed {
		return raw, false, eventActions, nil
	}

	root["hooks"] = hooksMap
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, nil, fmt.Errorf("marshal settings JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	return encoded, true, eventActions, nil
}

func isMTMClaudeHookCommand(existingCmd, desiredCmd string) bool {
	existingCmd = strings.TrimSpace(existingCmd)
	if existingCmd == "" {
		return false
	}
	if existingCmd == strings.TrimSpace(desiredCmd) {
		return true
	}
	fields := strings.Fields(existingCmd)
	if len(fields) == 0 {
		return false
	}
	// Skip leading env / KEY=VAL / exec wrappers.
	idx := 0
	for idx < len(fields) {
		tok := fields[idx]
		if tok == "env" || tok == "exec" || strings.Contains(tok, "=") {
			idx++
			continue
		}
		break
	}
	if idx >= len(fields) {
		return false
	}
	bin := filepath.Base(strings.Trim(fields[idx], `"'`))
	if bin != "mtm-hook" {
		return false
	}
	// Look for claude / claude-code / claudecode harness arg (ignoring flags).
	for _, arg := range fields[idx+1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		low := strings.ToLower(strings.Trim(arg, `"'`))
		if strings.HasPrefix(low, "claude") {
			return true
		}
		return false
	}
	return false
}

func numberToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), float64(int(n)) == n
	default:
		return 0, false
	}
}
