package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultMCPCmd is the default binary invoked by harness MCP server configurations.
const DefaultMCPCmd = "mtm"

var mtmMCPArgs = []string{"mcp", "serve"}

// ConfigureMCP configures the mtm MCP server ("mtm mcp serve") across Claude Code,
// Codex, Antigravity, and Gemini CLI idempotently, preserving all unrelated settings
// and sibling MCP servers.
func ConfigureMCP(opts Options) ([]Action, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}

	cmd := strings.TrimSpace(opts.MCPCmd)
	if cmd == "" {
		cmd = DefaultMCPCmd
	}

	actions := make([]Action, 0, 4)

	claudeAct, err := configureJSONMCP(
		"claude",
		filepath.Join(home, ".claude.json"),
		"stdio",
		cmd,
		opts,
	)
	if err != nil {
		return actions, err
	}
	actions = append(actions, claudeAct)

	codexAct, err := configureCodexTOMLMCP(
		filepath.Join(home, ".codex", "config.toml"),
		cmd,
		opts,
	)
	if err != nil {
		return actions, err
	}
	actions = append(actions, codexAct)

	antigravityAct, err := configureJSONMCP(
		"antigravity",
		filepath.Join(home, ".gemini", "config", "mcp_config.json"),
		"",
		cmd,
		opts,
	)
	if err != nil {
		return actions, err
	}
	actions = append(actions, antigravityAct)

	geminiAct, err := configureJSONMCP(
		"gemini",
		filepath.Join(home, ".gemini", "settings.json"),
		"",
		cmd,
		opts,
	)
	if err != nil {
		return actions, err
	}
	actions = append(actions, geminiAct)

	return actions, nil
}

func configureJSONMCP(target, path, serverType, mcpCmd string, opts Options) (Action, error) {
	var raw []byte
	exists := false
	if data, err := os.ReadFile(path); err == nil {
		raw = data
		exists = true
	} else if !os.IsNotExist(err) {
		return Action{}, fmt.Errorf("read %s: %w", path, err)
	}

	out, changed, existedEntry, err := mergeMCPSettingsJSON(raw, serverType, mcpCmd, opts.Force)
	if err != nil {
		return Action{}, fmt.Errorf("%s: %w", path, err)
	}

	return applyMCPAction(target, path, exists, existedEntry, changed, mcpCmd, out, opts)
}

func configureCodexTOMLMCP(path, mcpCmd string, opts Options) (Action, error) {
	var raw []byte
	exists := false
	if data, err := os.ReadFile(path); err == nil {
		raw = data
		exists = true
	} else if !os.IsNotExist(err) {
		return Action{}, fmt.Errorf("read %s: %w", path, err)
	}

	out, changed, existedEntry := mergeCodexMCPTOML(raw, mcpCmd, opts.Force)
	return applyMCPAction("codex", path, exists, existedEntry, changed, mcpCmd, out, opts)
}

func applyMCPAction(target, path string, fileExists, entryExisted, changed bool, mcpCmd string, out []byte, opts Options) (Action, error) {
	act := Action{
		Component: "mcp",
		Target:    target,
		Path:      path,
	}
	if !changed {
		act.Status = StatusUnchanged
		return act, nil
	}

	detail := fmt.Sprintf("%s mcp serve", mcpCmd)
	switch {
	case opts.Check:
		if !entryExisted {
			act.Status = StatusMissing
			act.Detail = "mtm MCP server not configured"
		} else {
			act.Status = StatusOutdated
			act.Detail = fmt.Sprintf("mtm MCP server differs (want %s)", detail)
		}
	case opts.DryRun:
		if !fileExists {
			act.Status = StatusWouldCreate
		} else {
			act.Status = StatusWouldUpdate
		}
		act.Detail = detail
	default:
		backup, err := atomicWriteWithBackup(path, out, 0o600, opts.now())
		if err != nil {
			return act, fmt.Errorf("write %s: %w", path, err)
		}
		if !fileExists {
			act.Status = StatusCreated
		} else {
			act.Status = StatusUpdated
			act.Backup = backup
		}
		act.Detail = detail
	}
	return act, nil
}

func mergeMCPSettingsJSON(raw []byte, serverType, mcpCmd string, force bool) (out []byte, changed bool, existed bool, err error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, false, false, fmt.Errorf("invalid JSON: %w", err)
		}
		if root == nil {
			return nil, false, false, fmt.Errorf("invalid JSON: top-level value must be an object")
		}
	}

	servers := map[string]any{}
	if existingServers, ok := root["mcpServers"]; ok && existingServers != nil {
		m, isMap := existingServers.(map[string]any)
		if !isMap {
			return nil, false, false, fmt.Errorf("invalid mcpServers field: expected JSON object")
		}
		servers = m
	}

	if existingEntry, ok := servers["mtm"]; ok {
		if !force {
			return raw, false, true, nil
		}
		if equalMCPServerEntry(existingEntry, serverType, mcpCmd, mtmMCPArgs) {
			return raw, false, true, nil
		}
		existed = true
	}

	argsAny := make([]any, len(mtmMCPArgs))
	for i, a := range mtmMCPArgs {
		argsAny[i] = a
	}
	entry := map[string]any{
		"command": mcpCmd,
		"args":    argsAny,
	}
	if serverType != "" {
		entry["type"] = serverType
	}
	servers["mtm"] = entry
	root["mcpServers"] = servers

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, existed, fmt.Errorf("marshal MCP settings JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	return encoded, true, existed, nil
}

func equalMCPServerEntry(v any, wantType, wantCmd string, wantArgs []string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	wantLen := 2
	if wantType != "" {
		wantLen = 3
		if curType, _ := m["type"].(string); curType != wantType {
			return false
		}
	}
	if len(m) != wantLen {
		return false
	}
	if curCmd, _ := m["command"].(string); curCmd != wantCmd {
		return false
	}
	rawArgs, ok := m["args"].([]any)
	if !ok || len(rawArgs) != len(wantArgs) {
		return false
	}
	for i, a := range rawArgs {
		s, ok := a.(string)
		if !ok || s != wantArgs[i] {
			return false
		}
	}
	return true
}

type tomlLineSpan struct {
	start int
	end   int
	text  string
}

func splitTOMLLines(raw []byte) []tomlLineSpan {
	if len(raw) == 0 {
		return nil
	}
	var spans []tomlLineSpan
	start := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			spans = append(spans, tomlLineSpan{
				start: start,
				end:   i + 1,
				text:  string(raw[start : i+1]),
			})
			start = i + 1
		}
	}
	if start < len(raw) {
		spans = append(spans, tomlLineSpan{
			start: start,
			end:   len(raw),
			text:  string(raw[start:]),
		})
	}
	return spans
}

func parseTOMLTableHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	if idx := strings.IndexByte(trimmed, '#'); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	if !strings.HasSuffix(trimmed, "]") {
		return "", false
	}
	if strings.ContainsAny(trimmed, "=,") {
		return "", false
	}
	return trimmed, true
}

func formatCodexMTMMCPBlock(mcpCmd string) string {
	return fmt.Sprintf("[mcp_servers.mtm]\ncommand = %s\nargs = [\"mcp\", \"serve\"]\n", strconv.Quote(mcpCmd))
}

func mergeCodexMCPTOML(raw []byte, mcpCmd string, force bool) (out []byte, changed bool, existed bool) {
	block := formatCodexMTMMCPBlock(mcpCmd)
	spans := splitTOMLLines(raw)

	sectionIdx := -1
	for i, sp := range spans {
		if hdr, ok := parseTOMLTableHeader(sp.text); ok && hdr == "[mcp_servers.mtm]" {
			sectionIdx = i
			break
		}
	}

	if sectionIdx >= 0 {
		if !force {
			return raw, false, true
		}
		nextSectionIdx := len(spans)
		for j := sectionIdx + 1; j < len(spans); j++ {
			if _, ok := parseTOMLTableHeader(spans[j].text); ok {
				nextSectionIdx = j
				break
			}
		}
		replaceEndIdx := nextSectionIdx
		for replaceEndIdx > sectionIdx+1 && strings.TrimSpace(spans[replaceEndIdx-1].text) == "" {
			replaceEndIdx--
		}
		startOff := spans[sectionIdx].start
		endOff := spans[replaceEndIdx-1].end

		var buf bytes.Buffer
		buf.Write(raw[:startOff])
		buf.WriteString(block)
		buf.Write(raw[endOff:])
		merged := buf.Bytes()
		if bytes.Equal(merged, raw) {
			return raw, false, true
		}
		return merged, true, true
	}

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
