package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Command is how a harness starts the server: this binary, the flags that
// name the same database and daemon, then mcp serve.
type Command struct {
	Executable string   `json:"command"`
	Args       []string `json:"args"`
}

// HarnessConfig is what one harness needs to see the mtm server: the entry
// to merge into its config file, or arguments that pass it on the agent's
// command line. mtm prints it and writes nothing; adding it to a harness's
// config is the owner's decision.
type HarnessConfig struct {
	Harness string   `json:"harness"`
	File    string   `json:"file,omitempty"` // the config file the entry belongs in
	Args    []string `json:"args,omitempty"` // agent arguments that pass the entry instead
	Entry   any      `json:"entry"`
}

// recipes are how each harness id gets the server, by recipe name (a
// harnesses.json row names its recipe in "mcp"); harnesses lists them in
// the order Harnesses prints.
var recipes = map[string]func(cmd Command, home string, getenv func(string) string) HarnessConfig{
	"claude": func(cmd Command, home string, _ func(string) string) HarnessConfig {
		entry := Stdio(cmd)
		inline, _ := json.Marshal(entry)
		// User scope: ~/.claude.json's mcpServers; --mcp-config takes the
		// same JSON for one run without touching any file.
		return HarnessConfig{File: filepath.Join(home, ".claude.json"), Args: []string{"--mcp-config", string(inline)}, Entry: entry}
	},
	"codex": func(cmd Command, home string, getenv func(string) string) HarnessConfig {
		argv, _ := json.Marshal(cmd.Args)
		dir := getenv("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		return HarnessConfig{File: filepath.Join(dir, "config.toml"), Args: []string{"-c", fmt.Sprintf("mcp_servers.mtm.command=%q", cmd.Executable), "-c", "mcp_servers.mtm.args=" + string(argv)}, Entry: map[string]any{"mcp_servers": map[string]any{"mtm": map[string]any{"command": cmd.Executable, "args": cmd.Args}}}}
	},
	"omp": func(cmd Command, home string, getenv func(string) string) HarnessConfig {
		return HarnessConfig{File: ompConfigPath(home, getenv), Entry: map[string]any{"mcpServers": map[string]any{"mtm": map[string]any{"command": cmd.Executable, "args": cmd.Args}}}}
	},
	"opencode": func(cmd Command, home string, getenv func(string) string) HarnessConfig {
		return HarnessConfig{File: opencodeConfigPath(home, getenv), Entry: map[string]any{"mcp": map[string]any{"mtm": map[string]any{"type": "local", "command": append([]string{cmd.Executable}, cmd.Args...), "enabled": true}}}}
	},
}

var harnesses = []string{"claude", "codex", "omp", "opencode"}

// ErrUnknownHarness marks a harness mtm has no configuration recipe for.
var ErrUnknownHarness = errors.New("mcp: no configuration known for harness")

// Harnesses lists the harness ids Config knows.
func Harnesses() []string { return append([]string{}, harnesses...) }

// Stdio is the generic mcpServers entry: {"mcpServers": {"mtm": {...}}}.
func Stdio(cmd Command) map[string]any {
	return map[string]any{"mcpServers": map[string]any{"mtm": map[string]any{"type": "stdio", "command": cmd.Executable, "args": cmd.Args}}}
}

// Config describes, without writing anything, how a harness gets the
// server. home is the user's home directory; getenv reads the variables
// that move a harness's config (os.Getenv when nil).
func Config(cmd Command, harness, home string, getenv func(string) string) (HarnessConfig, error) {
	return ConfigAs(cmd, harness, harness, home, getenv)
}

// ConfigAs is Config for a harness that takes another's recipe (a
// harnesses.json row's "mcp"): the entry recipe makes, under harness's
// name.
func ConfigAs(cmd Command, harness, recipe, home string, getenv func(string) string) (HarnessConfig, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	build, ok := recipes[recipe]
	if !ok {
		return HarnessConfig{}, fmt.Errorf("%w %q (known: %s)", ErrUnknownHarness, harness, strings.Join(harnesses, ", "))
	}
	out := build(cmd, home, getenv)
	out.Harness = harness
	return out, nil
}

func ompConfigPath(home string, getenv func(string) string) string {
	agent := getenv("PI_CODING_AGENT_DIR")
	if agent == "" {
		config := getenv("PI_CONFIG_DIR")
		if config == "" {
			config = ".omp"
		}
		agent = filepath.Join(home, config, "agent")
	}
	return filepath.Join(agent, "mcp.json")
}

func opencodeConfigPath(home string, getenv func(string) string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "opencode", "opencode.json")
}
