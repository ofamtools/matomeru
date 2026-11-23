package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Default hook commands for non-Claude harnesses.
const (
	DefaultCodexHookCmd       = "mtm-hook codex"
	DefaultAntigravityHookCmd = "mtm-hook antigravity"
	DefaultGeminiHookCmd      = "mtm-hook gemini"
	AntigravityHookKey        = "mtm-agent-state"
	OMPExtensionFilename      = "mtm-omp-agent-state.ts"
)

// CodexHookEvents lists all 10 Codex lifecycle events wired to mtm-hook.
var CodexHookEvents = []ClaudeHookEventSpec{
	{Event: "SessionStart", Timeout: 5},
	{Event: "UserPromptSubmit", Timeout: 5},
	{Event: "PreToolUse", Timeout: 5},
	{Event: "PostToolUse", Timeout: 5},
	{Event: "PostToolUseFailure", Timeout: 5},
	{Event: "PermissionRequest", Timeout: 310},
	{Event: "Stop", Timeout: 5},
	{Event: "StopFailure", Timeout: 5},
	{Event: "Interrupt", Timeout: 2},
	{Event: "SessionEnd", Timeout: 2},
}

// AntigravityHookEventSpec defines one Antigravity lifecycle hook event.
type AntigravityHookEventSpec struct {
	Event   string
	Timeout int
	Grouped bool // true for PostToolUse (matcher + nested hooks array)
}

// AntigravityHookEvents lists the 5 non-blocking Antigravity telemetry events.
// PreToolUse is intentionally omitted because Antigravity requires PreToolUse
// hooks to return a permission decision (allow/deny/ask).
var AntigravityHookEvents = []AntigravityHookEventSpec{
	{Event: "SessionStart", Timeout: 5},
	{Event: "PreInvocation", Timeout: 5},
	{Event: "PostToolUse", Timeout: 5, Grouped: true},
	{Event: "PostInvocation", Timeout: 5},
	{Event: "Stop", Timeout: 5},
}

// GeminiHookEvents lists all 7 Gemini CLI lifecycle events (timeouts in ms).
var GeminiHookEvents = []ClaudeHookEventSpec{
	{Event: "SessionStart", Timeout: 5000},
	{Event: "BeforeAgent", Timeout: 5000},
	{Event: "BeforeTool", Timeout: 5000},
	{Event: "AfterTool", Timeout: 5000},
	{Event: "Notification", Timeout: 5000},
	{Event: "AfterAgent", Timeout: 5000},
	{Event: "SessionEnd", Timeout: 5000},
}

// ConfigureHooks configures lifecycle hooks idempotently across all 5 supported
// harnesses: Claude Code, Codex, Antigravity, Gemini CLI, and OMP (Oh My Pi).
func ConfigureHooks(opts Options) ([]Action, error) {
	var all []Action

	claudeActs, err := ConfigureClaudeHooks(opts)
	all = append(all, claudeActs...)
	if err != nil {
		return all, err
	}

	codexActs, err := ConfigureCodexHooks(opts)
	all = append(all, codexActs...)
	if err != nil {
		return all, err
	}

	agyActs, err := ConfigureAntigravityHooks(opts)
	all = append(all, agyActs...)
	if err != nil {
		return all, err
	}

	geminiActs, err := ConfigureGeminiHooks(opts)
	all = append(all, geminiActs...)
	if err != nil {
		return all, err
	}

	ompAct, err := ConfigureOMPHook(opts)
	all = append(all, ompAct)
	if err != nil {
		return all, err
	}

	return all, nil
}

// ConfigureCodexHooks merges all 10 mtm-hook events into ~/.codex/hooks.json
// and ensures `[features] hooks = true` is enabled in ~/.codex/config.toml.
func ConfigureCodexHooks(opts Options) ([]Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}

	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	hooksActs, err := configureNestedJSONHooks(
		hooksPath,
		"codex",
		DefaultCodexHookCmd,
		CodexHookEvents,
		"s",
		isMTMHarnessHookCommand("codex"),
		opts,
	)
	if err != nil {
		return hooksActs, err
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	featAct, err := configureCodexFeaturesHooks(configPath, opts)
	hooksActs = append(hooksActs, featAct)
	return hooksActs, err
}

// ConfigureAntigravityHooks merges the named "mtm-agent-state" hook entry into
// ~/.gemini/config/hooks.json while preserving all sibling named hook entries.
func ConfigureAntigravityHooks(opts Options) ([]Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}

	hooksPath := filepath.Join(home, ".gemini", "config", "hooks.json")
	raw, exists, err := readOptionalFile(hooksPath)
	if err != nil {
		return nil, err
	}

	out, changed, eventActions, err := mergeAntigravityHooksJSON(raw, DefaultAntigravityHookCmd, opts.Force)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", hooksPath, err)
	}

	var backup string
	if changed && !opts.DryRun && !opts.Check {
		backup, err = atomicWriteWithBackup(hooksPath, out, 0o600, opts.now())
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", hooksPath, err)
		}
	}

	finalizeHookEventActions(eventActions, hooksPath, exists, backup, opts)
	return eventActions, nil
}

// ConfigureGeminiHooks merges all 7 Gemini CLI lifecycle events (5000ms timeout)
// into ~/.gemini/settings.json while preserving mcpServers, security, and sibling hooks.
func ConfigureGeminiHooks(opts Options) ([]Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}

	settingsPath := filepath.Join(home, ".gemini", "settings.json")
	return configureNestedJSONHooks(
		settingsPath,
		"gemini",
		DefaultGeminiHookCmd,
		GeminiHookEvents,
		"ms",
		isMTMHarnessHookCommand("gemini"),
		opts,
	)
}

// ConfigureOMPHook installs or updates the embedded OMP agent-state extension at
// ~/.omp/agent/extensions/mtm-omp-agent-state.ts idempotently.
func ConfigureOMPHook(opts Options) (Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return Action{}, err
	}

	extPath := filepath.Join(home, ".omp", "agent", "extensions", OMPExtensionFilename)
	act := Action{
		Component: "hooks",
		Target:    "omp/" + OMPExtensionFilename,
		Path:      extPath,
	}

	raw, exists, err := readOptionalFile(extPath)
	if err != nil {
		return act, err
	}

	want := []byte(embeddedOMPExtension)
	if exists && bytes.Equal(raw, want) {
		act.Status = StatusUnchanged
		return act, nil
	}

	act.Detail = "mtm-hook omp extension"
	switch {
	case opts.Check:
		if !exists {
			act.Status = StatusMissing
		} else {
			act.Status = StatusOutdated
		}
	case opts.DryRun:
		if !exists {
			act.Status = StatusWouldCreate
		} else {
			act.Status = StatusWouldUpdate
		}
	default:
		backup, werr := atomicWriteWithBackup(extPath, want, 0o644, opts.now())
		if werr != nil {
			return act, fmt.Errorf("write %s: %w", extPath, werr)
		}
		if !exists {
			act.Status = StatusCreated
		} else {
			act.Status = StatusUpdated
			act.Backup = backup
		}
	}
	return act, nil
}

func readOptionalFile(path string) (raw []byte, exists bool, err error) {
	data, rerr := os.ReadFile(path)
	if rerr == nil {
		return data, true, nil
	}
	if os.IsNotExist(rerr) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("read %s: %w", path, rerr)
}

func configureNestedJSONHooks(
	path, harnessPrefix, cmd string,
	specs []ClaudeHookEventSpec,
	timeoutUnit string,
	matchFn func(existingCmd, desiredCmd string) bool,
	opts Options,
) ([]Action, error) {
	raw, exists, err := readOptionalFile(path)
	if err != nil {
		return nil, err
	}

	out, changed, eventActions, err := mergeNestedHooksJSON(raw, harnessPrefix, cmd, specs, timeoutUnit, matchFn, opts.Force)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var backup string
	if changed && !opts.DryRun && !opts.Check {
		backup, err = atomicWriteWithBackup(path, out, 0o600, opts.now())
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
	}

	finalizeHookEventActions(eventActions, path, exists, backup, opts)
	return eventActions, nil
}

func mergeNestedHooksJSON(
	raw []byte,
	harnessPrefix, cmd string,
	specs []ClaudeHookEventSpec,
	timeoutUnit string,
	matchFn func(existingCmd, desiredCmd string) bool,
	force bool,
) (out []byte, changed bool, eventActions []Action, err error) {
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

	eventActions = make([]Action, 0, len(specs))
	for _, spec := range specs {
		act, didChange, err := upsertHookEvent(hooksMap, spec, harnessPrefix+"/"+spec.Event, cmd, timeoutUnit, matchFn, force)
		if err != nil {
			return nil, false, nil, err
		}
		if didChange {
			changed = true
		}
		eventActions = append(eventActions, act)
	}

	if !changed {
		return raw, false, eventActions, nil
	}

	root["hooks"] = hooksMap
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, nil, fmt.Errorf("marshal hooks JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	return encoded, true, eventActions, nil
}

func mergeAntigravityHooksJSON(raw []byte, cmd string, force bool) (out []byte, changed bool, eventActions []Action, err error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, false, nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if root == nil {
			return nil, false, nil, fmt.Errorf("invalid JSON: top-level value must be an object")
		}
	}

	entryMap := map[string]any{}
	if existing, ok := root[AntigravityHookKey]; ok && existing != nil {
		m, isMap := existing.(map[string]any)
		if !isMap {
			return nil, false, nil, fmt.Errorf("invalid %s field: expected JSON object", AntigravityHookKey)
		}
		entryMap = m
	}

	matchFn := isMTMHarnessHookCommand("antigravity")
	eventActions = make([]Action, 0, len(AntigravityHookEvents))

	for _, spec := range AntigravityHookEvents {
		var (
			act       Action
			didChange bool
		)
		if spec.Grouped {
			act, didChange, err = upsertAntigravityGroupedEvent(entryMap, spec, cmd, matchFn, force)
		} else {
			act, didChange, err = upsertAntigravityFlatEvent(entryMap, spec, cmd, matchFn, force)
		}
		if err != nil {
			return nil, false, nil, err
		}
		if didChange {
			changed = true
		}
		eventActions = append(eventActions, act)
	}

	if !changed {
		return raw, false, eventActions, nil
	}

	root[AntigravityHookKey] = entryMap
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, nil, fmt.Errorf("marshal antigravity hooks JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	return encoded, true, eventActions, nil
}

func upsertAntigravityFlatEvent(
	entryMap map[string]any,
	spec AntigravityHookEventSpec,
	cmd string,
	matchFn func(existingCmd, desiredCmd string) bool,
	force bool,
) (Action, bool, error) {
	var list []any
	if rawList, ok := entryMap[spec.Event]; ok && rawList != nil {
		arr, isArr := rawList.([]any)
		if !isArr {
			return Action{}, false, fmt.Errorf("invalid %s.%s: expected JSON array", AntigravityHookKey, spec.Event)
		}
		list = arr
	}

	found := false
	updated := false
	for _, item := range list {
		hm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		curCmd, _ := hm["command"].(string)
		if !matchFn(curCmd, cmd) {
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
			}
		}
	}

	act := Action{
		Component: "hooks",
		Target:    "antigravity/" + spec.Event,
	}
	switch {
	case !found:
		list = append(list, map[string]any{
			"type":    "command",
			"command": cmd,
			"timeout": spec.Timeout,
		})
		entryMap[spec.Event] = list
		act.Status = StatusCreated
		act.Detail = fmt.Sprintf("%s (timeout %ds)", cmd, spec.Timeout)
		return act, true, nil
	case updated:
		act.Status = StatusUpdated
		act.Detail = fmt.Sprintf("%s (timeout %ds)", cmd, spec.Timeout)
		return act, true, nil
	default:
		act.Status = StatusUnchanged
		return act, false, nil
	}
}

func upsertAntigravityGroupedEvent(
	entryMap map[string]any,
	spec AntigravityHookEventSpec,
	cmd string,
	matchFn func(existingCmd, desiredCmd string) bool,
	force bool,
) (Action, bool, error) {
	var groups []any
	if rawGroups, ok := entryMap[spec.Event]; ok && rawGroups != nil {
		arr, isArr := rawGroups.([]any)
		if !isArr {
			return Action{}, false, fmt.Errorf("invalid %s.%s: expected JSON array", AntigravityHookKey, spec.Event)
		}
		groups = arr
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
			if !matchFn(curCmd, cmd) {
				continue
			}
			found = true
			if force {
				curMatcher, _ := gm["matcher"].(string)
				curType, _ := hm["type"].(string)
				curTimeout, hasTimeout := numberToInt(hm["timeout"])
				if curMatcher != "*" || curType != "command" || curCmd != cmd || !hasTimeout || curTimeout != spec.Timeout {
					gm["matcher"] = "*"
					hm["type"] = "command"
					hm["command"] = cmd
					hm["timeout"] = spec.Timeout
					updated = true
				}
			}
		}
	}

	act := Action{
		Component: "hooks",
		Target:    "antigravity/" + spec.Event,
	}
	switch {
	case !found:
		groups = append(groups, map[string]any{
			"matcher": "*",
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": cmd,
					"timeout": spec.Timeout,
				},
			},
		})
		entryMap[spec.Event] = groups
		act.Status = StatusCreated
		act.Detail = fmt.Sprintf("%s (timeout %ds)", cmd, spec.Timeout)
		return act, true, nil
	case updated:
		act.Status = StatusUpdated
		act.Detail = fmt.Sprintf("%s (timeout %ds)", cmd, spec.Timeout)
		return act, true, nil
	default:
		act.Status = StatusUnchanged
		return act, false, nil
	}
}

func configureCodexFeaturesHooks(path string, opts Options) (Action, error) {
	act := Action{
		Component: "hooks",
		Target:    "codex/features.hooks",
		Path:      path,
	}

	raw, exists, err := readOptionalFile(path)
	if err != nil {
		return act, err
	}

	out, changed, existedKey := mergeCodexFeaturesHooksTOML(raw)
	if !changed {
		act.Status = StatusUnchanged
		return act, nil
	}

	act.Detail = "[features] hooks = true"
	switch {
	case opts.Check:
		if !existedKey {
			act.Status = StatusMissing
		} else {
			act.Status = StatusOutdated
		}
	case opts.DryRun:
		if !exists {
			act.Status = StatusWouldCreate
		} else {
			act.Status = StatusWouldUpdate
		}
	default:
		backup, werr := atomicWriteWithBackup(path, out, 0o600, opts.now())
		if werr != nil {
			return act, fmt.Errorf("write %s: %w", path, werr)
		}
		if !exists {
			act.Status = StatusCreated
		} else {
			act.Status = StatusUpdated
			act.Backup = backup
		}
	}
	return act, nil
}

func parseTOMLKeyValue(line string) (key, val string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	if idx := strings.IndexByte(trimmed, '#'); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	k, v, found := strings.Cut(trimmed, "=")
	if !found {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}

func mergeCodexFeaturesHooksTOML(raw []byte) (out []byte, changed bool, existed bool) {
	spans := splitTOMLLines(raw)

	sectionIdx := -1
	for i, sp := range spans {
		if hdr, ok := parseTOMLTableHeader(sp.text); ok && hdr == "[features]" {
			sectionIdx = i
			break
		}
	}

	if sectionIdx >= 0 {
		nextSectionIdx := len(spans)
		for j := sectionIdx + 1; j < len(spans); j++ {
			if _, ok := parseTOMLTableHeader(spans[j].text); ok {
				nextSectionIdx = j
				break
			}
		}

		for j := sectionIdx + 1; j < nextSectionIdx; j++ {
			k, v, ok := parseTOMLKeyValue(spans[j].text)
			if !ok || k != "hooks" {
				continue
			}
			if v == "true" {
				return raw, false, true
			}
			var buf bytes.Buffer
			buf.Write(raw[:spans[j].start])
			buf.WriteString("hooks = true\n")
			buf.Write(raw[spans[j].end:])
			return buf.Bytes(), true, true
		}

		// Insert hooks = true inside [features] after its last non-blank line.
		insertAfter := sectionIdx
		for j := nextSectionIdx - 1; j > sectionIdx; j-- {
			if strings.TrimSpace(spans[j].text) != "" {
				insertAfter = j
				break
			}
		}
		insertOff := spans[insertAfter].end
		var buf bytes.Buffer
		buf.Write(raw[:insertOff])
		if !strings.HasSuffix(spans[insertAfter].text, "\n") {
			buf.WriteByte('\n')
		}
		buf.WriteString("hooks = true\n")
		buf.Write(raw[insertOff:])
		return buf.Bytes(), true, false
	}

	const block = "[features]\nhooks = true\n"
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte(block), true, false
	}

	var buf bytes.Buffer
	buf.Write(raw)
	switch {
	case bytes.HasSuffix(raw, []byte("\n\n")):
	case bytes.HasSuffix(raw, []byte("\n")):
		buf.WriteByte('\n')
	default:
		buf.WriteString("\n\n")
	}
	buf.WriteString(block)
	return buf.Bytes(), true, false
}

func isMTMHarnessHookCommand(harness string) func(existingCmd, desiredCmd string) bool {
	return func(existingCmd, desiredCmd string) bool {
		existingCmd = strings.TrimSpace(existingCmd)
		if existingCmd == "" {
			return false
		}
		if existingCmd == strings.TrimSpace(desiredCmd) {
			return true
		}
		tokens := commandTokensAfterWrappers(existingCmd)
		if len(tokens) == 0 {
			return false
		}
		bin := filepath.Base(strings.Trim(tokens[0], `"'`))
		if bin != "mtm-hook" {
			return false
		}
		for _, arg := range tokens[1:] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			low := strings.ToLower(strings.Trim(arg, `"'`))
			switch harness {
			case "claude":
				return strings.HasPrefix(low, "claude")
			case "codex":
				return strings.HasPrefix(low, "codex") || low == "openai-codex"
			case "antigravity":
				return low == "antigravity" || low == "agy"
			case "gemini":
				return strings.HasPrefix(low, "gemini")
			case "omp":
				return low == "omp" || low == "oh-my-pi"
			default:
				return low == harness
			}
		}
		return false
	}
}

func finalizeHookEventActions(eventActions []Action, path string, exists bool, backup string, opts Options) {
	for i := range eventActions {
		eventActions[i].Path = path
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
}
