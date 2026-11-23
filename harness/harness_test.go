package harness_test

import (
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/harness"
)

func TestDefaultsAndConfigJSONCRoundTrip(t *testing.T) {
	defs := harness.DefaultConfigs()
	for _, want := range []string{"omp", "claude", "codex", "gemini", "antigravity", "zsh"} {
		cfg, ok := defs[want]
		if !ok {
			t.Fatalf("missing default harness %q", want)
		}
		if len(cfg.Command) == 0 {
			t.Errorf("default harness %q has empty command", want)
		}
	}
	if !defs["zsh"].Shell {
		t.Error("expected zsh default to have Shell=true")
	}

	jsonc := []byte(`{
		// User default harness
		"default": "claude",
		"harnesses": {
			/* Custom launch command and MCP template */
			"claude": {
				"command": ["claude", "--dangerously-skip-permissions"],
				"env": {"FOO": "bar"},
				"mcpFlag": ["--mcp-config", "{\"url\":\"{{mcp_url}}\",\"pane\":\"{{pane_id}}\"}"],
			},
			"my-custom-agent": {
				"label": "Custom Agent",
				"command": ["my-agent", "--interactive"],
			},
		},
	}`)

	cfg, err := harness.ParseConfig(jsonc)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if cfg.Default != "claude" {
		t.Fatalf("default = %q, want claude", cfg.Default)
	}
	if got := cfg.Harnesses["claude"].Command; !slices.Equal(got, []string{"claude", "--dangerously-skip-permissions"}) {
		t.Fatalf("claude command = %v", got)
	}
	if cfg.Harnesses["my-custom-agent"].Name != "my-custom-agent" {
		t.Fatalf("custom name = %q", cfg.Harnesses["my-custom-agent"].Name)
	}

	path := filepath.Join(t.TempDir(), "matomeru", "harnesses.json")
	if err := harness.SaveFile(path, cfg); err != nil {
		t.Fatalf("SaveFile failed: %v", err)
	}
	loaded, err := harness.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}
	if loaded.Default != "claude" || len(loaded.Harnesses) != 2 {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}

	// Missing file returns empty config without error.
	missing, err := harness.LoadFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil || len(missing.Harnesses) != 0 {
		t.Fatalf("missing file load = (%+v, %v)", missing, err)
	}
}

func TestParseOAIJSONFormats(t *testing.T) {
	// 1. `o-ai query --kind ai.harness`
	queryJSON := []byte(`{
		"items": [
			{
				"kind": "ai.harness",
				"id": "claude",
				"fields": {
					"id": "claude",
					"installed": true,
					"configured": true,
					"executable": "/Users/u/.local/bin/claude",
					"processStatus": "2 running"
				}
			},
			{
				"kind": "ai.harness",
				"id": "gemini",
				"fields": {
					"id": "gemini",
					"installed": false,
					"configured": true
				}
			},
			{
				"kind": "ai.model",
				"id": "ignore-me",
				"fields": {"id": "ignore-me"}
			}
		],
		"total": 3
	}`)
	got, err := harness.ParseOAIJSON(queryJSON)
	if err != nil {
		t.Fatalf("ParseOAIJSON(query) error: %v", err)
	}
	if len(got) != 2 || got[0].ID != "claude" || !got[0].Installed || got[0].Executable != "/Users/u/.local/bin/claude" {
		t.Fatalf("unexpected query rows: %+v", got)
	}
	if got[1].ID != "gemini" || got[1].Installed {
		t.Fatalf("unexpected second row: %+v", got[1])
	}

	// 2. `o-ai harness list --json`
	listJSON := []byte(`[
		{"id":"harness/omp","label":"omp","data":{"id":"omp","installed":true,"configured":true,"executable":"/Users/u/.bun/bin/omp"}},
		{"id":"harness/clawed","label":"clawed","data":{"id":"clawed","installed":false,"configured":false}}
	]`)
	gotList, err := harness.ParseOAIJSON(listJSON)
	if err != nil {
		t.Fatalf("ParseOAIJSON(list) error: %v", err)
	}
	if len(gotList) != 2 || gotList[0].ID != "omp" || !gotList[0].Installed || gotList[1].ID != "clawed" || gotList[1].Installed {
		t.Fatalf("unexpected list rows: %+v", gotList)
	}
}

func TestMergeInstalledAndBuildLaunchCommand(t *testing.T) {
	installed := []harness.InstalledHarness{
		{ID: "claude", Installed: true, Configured: true, Executable: "/usr/local/bin/claude"},
		{ID: "codex", Installed: true, Configured: true, Executable: "/usr/local/bin/codex"},
		{ID: "gemini", Installed: false, Configured: true}, // not installed -> excluded
		{ID: "omp", Installed: true, Configured: true, Executable: "/usr/local/bin/omp"},
	}

	cfg := harness.FileConfig{
		Default: "omp",
		Harnesses: map[string]harness.HarnessConfig{
			"claude": {
				Command:  []string{"claude", "--model", "opus"},
				Env:      map[string]string{"CLAUDE_ENV": "1"},
				HookArgs: []string{"--settings", "/tmp/hooks.json"},
				MCPFlag:  []string{"--mcp-config", "http={{mcp_url}}&pane={{pane_id}}"},
			},
			"codex": {
				Disabled: true, // explicitly hidden by user
			},
			"local-llm": {
				Label:   "Local LLM",
				Command: []string{"llama-cli", "--chat"},
			},
		},
	}

	entries := harness.MergeInstalled(installed, cfg)

	var ids []string
	byID := map[string]harness.Entry{}
	for _, e := range entries {
		ids = append(ids, e.ID)
		byID[e.ID] = e
	}

	// omp promoted to default first, claude installed, local-llm custom, zsh shell;
	// gemini (uninstalled) and codex (disabled) excluded.
	wantIDs := []string{"omp", "claude", "local-llm", "zsh"}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("merged ids = %v, want %v", ids, wantIDs)
	}
	if !byID["local-llm"].Custom || byID["local-llm"].Source != harness.SourceCustom {
		t.Errorf("local-llm should be marked custom: %+v", byID["local-llm"])
	}
	if !byID["zsh"].Shell || byID["zsh"].Source != harness.SourceShell {
		t.Errorf("zsh should be marked shell: %+v", byID["zsh"])
	}

	argv, env := harness.BuildLaunchCommand(byID["claude"], "http://127.0.0.1:9999/mcp/p-1", "p-1")
	wantArgv := []string{
		"claude", "--model", "opus",
		"--settings", "/tmp/hooks.json",
		"--mcp-config", "http=http://127.0.0.1:9999/mcp/p-1&pane=p-1",
	}
	if !slices.Equal(argv, wantArgv) {
		t.Fatalf("argv = %v, want %v", argv, wantArgv)
	}
	if env["CLAUDE_ENV"] != "1" {
		t.Fatalf("env = %v", env)
	}
}
