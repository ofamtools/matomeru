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

	// omp promoted to default first, then zsh shell, claude installed, local-llm custom;
	// gemini (uninstalled) and codex (disabled) excluded.
	wantIDs := []string{"omp", "zsh", "claude", "local-llm"}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("merged ids = %v, want %v", ids, wantIDs)
	}
	// With no explicit cfg.Default, plain shell (zsh) leads as the default option.
	noDef := harness.MergeInstalled(installed, harness.FileConfig{})
	if len(noDef) == 0 || noDef[0].ID != "zsh" {
		t.Fatalf("default harness without cfg.Default = %+v, want zsh first", noDef)
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

func TestAntigravityAgyAliasRecognitionAndLaunch(t *testing.T) {
	defs := harness.DefaultConfigs()
	if defs["agy"].Label != "Antigravity" || !slices.Equal(defs["agy"].Command, []string{"agy"}) {
		t.Fatalf("agy default = %+v", defs["agy"])
	}
	if defs["antigravity"].Label != "Antigravity" || !slices.Equal(defs["antigravity"].Command, []string{"agy"}) {
		t.Fatalf("antigravity default = %+v", defs["antigravity"])
	}

	// o-ai reports id:"agy" with installed:true -> MergeInstalled resolves Antigravity and launches agy.
	entries := harness.MergeInstalled([]harness.InstalledHarness{
		{ID: "agy", Installed: true, Configured: true, Executable: "/usr/local/bin/agy"},
	}, harness.FileConfig{
		Harnesses: map[string]harness.HarnessConfig{
			"antigravity": {Env: map[string]string{"AGY_MODE": "fast"}},
		},
	})
	idx := slices.IndexFunc(entries, func(e harness.Entry) bool { return e.ID == "agy" })
	if idx < 0 {
		t.Fatalf("agy missing from merged entries: %+v", entries)
	}
	got := entries[idx]
	if got.Label != "Antigravity" || !slices.Equal(got.Command, []string{"agy"}) || got.Env["AGY_MODE"] != "fast" {
		t.Fatalf("unexpected agy entry: %+v", got)
	}
	argv, env := harness.BuildLaunchCommand(got, "", "p-1")
	if !slices.Equal(argv, []string{"agy"}) || env["AGY_MODE"] != "fast" {
		t.Fatalf("BuildLaunchCommand(agy) = (%v, %v)", argv, env)
	}
}

func TestBuildLaunchCommandWithPromptAllHarnesses(t *testing.T) {
	cases := []struct {
		id            string
		prompt        string
		wantArgv      []string
		wantDelivered bool
		wantShellCmd  string
	}{
		{
			id:            "claude",
			prompt:        "test",
			wantArgv:      []string{"claude", "test"},
			wantDelivered: true,
			wantShellCmd:  "claude test",
		},
		{
			id:            "codex",
			prompt:        "test",
			wantArgv:      []string{"codex", "test"},
			wantDelivered: true,
			wantShellCmd:  "codex test",
		},
		{
			id:            "agy",
			prompt:        "test",
			wantArgv:      []string{"agy", "-i", "test"},
			wantDelivered: true,
			wantShellCmd:  "agy -i test",
		},
		{
			id:            "antigravity",
			prompt:        "test",
			wantArgv:      []string{"agy", "-i", "test"},
			wantDelivered: true,
			wantShellCmd:  "agy -i test",
		},
		{
			id:            "omp",
			prompt:        "test",
			wantArgv:      []string{"omp", "test"},
			wantDelivered: true,
			wantShellCmd:  "omp test",
		},
		{
			id:            "gemini",
			prompt:        "test",
			wantArgv:      []string{"gemini", "-i", "test"},
			wantDelivered: true,
			wantShellCmd:  "gemini -i test",
		},
		{
			id:            "opencode",
			prompt:        "test",
			wantArgv:      []string{"opencode", "--prompt", "test"},
			wantDelivered: true,
			wantShellCmd:  "opencode --prompt test",
		},
		{
			id:            "pi",
			prompt:        "test",
			wantArgv:      []string{"pi", "test"},
			wantDelivered: true,
			wantShellCmd:  "pi test",
		},
		{
			id:            "zsh",
			prompt:        "test",
			wantArgv:      []string{"zsh"},
			wantDelivered: false,
			wantShellCmd:  "zsh",
		},
		{
			id:            "claude",
			prompt:        "",
			wantArgv:      []string{"claude"},
			wantDelivered: false,
			wantShellCmd:  "claude",
		},
		{
			id:            "claude",
			prompt:        "- fix bug",
			wantArgv:      []string{"claude", "--", "- fix bug"},
			wantDelivered: true,
			wantShellCmd:  "claude -- '- fix bug'",
		},
		{
			id:            "agy",
			prompt:        "- fix bug",
			wantArgv:      []string{"agy", "-i=- fix bug"},
			wantDelivered: true,
			wantShellCmd:  "agy '-i=- fix bug'",
		},
		{
			id:            "codex",
			prompt:        "Fix 'quoted' $VAR\nline 2 && $(echo nope)",
			wantArgv:      []string{"codex", "Fix 'quoted' $VAR\nline 2 && $(echo nope)"},
			wantDelivered: true,
			wantShellCmd:  "codex 'Fix '\\''quoted'\\'' $VAR\nline 2 && $(echo nope)'",
		},
	}

	for _, tc := range cases {
		entry, ok := harness.ResolveEntry(tc.id, harness.FileConfig{})
		if !ok {
			t.Fatalf("ResolveEntry(%q) = false, want true", tc.id)
		}
		argv, _, delivered := harness.BuildLaunchCommandWithPrompt(entry, "", "p-1", tc.prompt)
		if delivered != tc.wantDelivered {
			t.Errorf("%s prompt=%q: delivered=%v, want %v", tc.id, tc.prompt, delivered, tc.wantDelivered)
		}
		if !slices.Equal(argv, tc.wantArgv) {
			t.Errorf("%s prompt=%q: argv=%q, want %q", tc.id, tc.prompt, argv, tc.wantArgv)
		}
		if gotCmd := harness.ShellJoin(argv); gotCmd != tc.wantShellCmd {
			t.Errorf("%s prompt=%q: ShellJoin=%q, want %q", tc.id, tc.prompt, gotCmd, tc.wantShellCmd)
		}
	}

	// Custom harnesses.json promptMode/promptFlag overrides work declaratively.
	cfg, err := harness.ParseConfig([]byte(`{
		"harnesses": {
			"my-cli": {
				"command": ["my-cli", "--chat"],
				"promptMode": "flag",
				"promptFlag": "--initial"
			},
			"claude": {
				"promptMode": "stdin"
			}
		}
	}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	custom, ok := harness.ResolveEntry("my-cli", cfg)
	if !ok {
		t.Fatal("ResolveEntry(my-cli) = false")
	}
	argv, _, delivered := harness.BuildLaunchCommandWithPrompt(custom, "", "p-1", "test")
	if !delivered || !slices.Equal(argv, []string{"my-cli", "--chat", "--initial", "test"}) {
		t.Fatalf("custom flag harness = (%v, delivered=%v)", argv, delivered)
	}
	claudeOverride, ok := harness.ResolveEntry("claude", cfg)
	if !ok {
		t.Fatal("ResolveEntry(claude override) = false")
	}
	argv, _, delivered = harness.BuildLaunchCommandWithPrompt(claudeOverride, "", "p-1", "test")
	if delivered || !slices.Equal(argv, []string{"claude"}) {
		t.Fatalf("claude stdin override = (%v, delivered=%v)", argv, delivered)
	}
}
