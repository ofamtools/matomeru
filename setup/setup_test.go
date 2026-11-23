package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 11, 23, 14, 30, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func TestConfigureAgents_CreatePreserveAndIdempotent(t *testing.T) {
	home := t.TempDir()
	opts := Options{Home: home, Now: fixedClock()}

	// 1. Create all four targets from scratch.
	actions, err := ConfigureAgents(opts)
	if err != nil {
		t.Fatalf("ConfigureAgents create: %v", err)
	}
	if len(actions) != len(DefaultAgentTargets) {
		t.Fatalf("got %d actions, want %d", len(actions), len(DefaultAgentTargets))
	}
	for _, a := range actions {
		if a.Status != StatusCreated {
			t.Errorf("target %s status = %q, want %q", a.Target, a.Status, StatusCreated)
		}
		raw, rerr := os.ReadFile(a.Path)
		if rerr != nil {
			t.Fatalf("read %s: %v", a.Path, rerr)
		}
		content := string(raw)
		for _, want := range []string{
			OFAMBeginMarker,
			OFAMEndMarker,
			"o-lane",
			"TestDomainFilenames",
			"mtm mcp serve",
			"ofam-wave-release",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("%s missing %q", a.Path, want)
			}
		}
	}

	// 2. Re-run is completely idempotent.
	again, err := ConfigureAgents(opts)
	if err != nil {
		t.Fatalf("ConfigureAgents idempotent: %v", err)
	}
	for _, a := range again {
		if a.Status != StatusUnchanged {
			t.Errorf("second run %s status = %q, want %q", a.Target, a.Status, StatusUnchanged)
		}
		if a.Backup != "" {
			t.Errorf("second run %s unexpectedly created backup %s", a.Target, a.Backup)
		}
	}

	// 3. Preserve user prose before and after an outdated managed block, creating a backup.
	codexPath := filepath.Join(home, ".codex", "AGENTS.md")
	prefix := "# My Custom Header\nKeep every single byte here!\n\n"
	suffix := "\n\n## Trailing Personal Notes\nDo not touch.\n"
	stale := prefix + OFAMBeginMarker + "\nold policy line\n" + OFAMEndMarker + suffix
	if err := os.WriteFile(codexPath, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, err := ConfigureAgents(opts)
	if err != nil {
		t.Fatalf("ConfigureAgents update: %v", err)
	}
	var codexAct Action
	for _, a := range updated {
		if a.Target == "codex" {
			codexAct = a
		} else if a.Status != StatusUnchanged {
			t.Errorf("untouched target %s got status %q", a.Target, a.Status)
		}
	}
	if codexAct.Status != StatusUpdated {
		t.Fatalf("codex status = %q, want %q", codexAct.Status, StatusUpdated)
	}
	if !strings.HasSuffix(codexAct.Backup, ".bak-20261123") {
		t.Errorf("codex backup = %q, want .bak-20261123 suffix", codexAct.Backup)
	}
	bakBytes, err := os.ReadFile(codexAct.Backup)
	if err != nil || string(bakBytes) != stale {
		t.Fatalf("backup mismatch: err=%v", err)
	}
	newBytes, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(newBytes)
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("surrounding user prose was altered:\n%s", got)
	}
	if strings.Contains(got, "old policy line") || !strings.Contains(got, OFAMRulesBlock()) {
		t.Fatalf("managed block was not replaced cleanly:\n%s", got)
	}

	// 4. --source mode manages symlinks and requires --force to replace regular files.
	srcHome := t.TempDir()
	srcFile := filepath.Join(srcHome, "canonical-AGENTS.md")
	if err := os.WriteFile(srcFile, []byte("# Source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	existingRegular := filepath.Join(srcHome, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(existingRegular), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingRegular, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcActs, err := ConfigureAgents(Options{Home: srcHome, Source: srcFile, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureAgents --source: %v", err)
	}
	for _, a := range srcActs {
		if a.Target == "codex" {
			if a.Status != StatusConflict {
				t.Errorf("--source regular file without --force: status=%q, want conflict", a.Status)
			}
		} else if a.Status != StatusLinked {
			t.Errorf("--source %s: status=%q, want linked", a.Target, a.Status)
		}
	}
	// With --force, backs up regular file and links it.
	forceActs, err := ConfigureAgents(Options{Home: srcHome, Source: srcFile, Force: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureAgents --source --force: %v", err)
	}
	for _, a := range forceActs {
		if a.Target == "codex" {
			if a.Status != StatusLinked || a.Backup == "" {
				t.Errorf("--source --force codex: %+v", a)
			}
			fi, lerr := os.Lstat(existingRegular)
			if lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("expected symlink at %s, got %v %v", existingRegular, fi, lerr)
			}
		} else if a.Status != StatusUnchanged {
			t.Errorf("--source --force %s: status=%q, want unchanged", a.Target, a.Status)
		}
	}
}

func TestConfigureAgents_ClaudeWrapperAndSymlinkDedup(t *testing.T) {
	home := t.TempDir()
	canonicalDir := filepath.Join(home, "hub", "ai", "instructions")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(canonicalDir, "AGENTS.md")
	if err := os.WriteFile(canonical, []byte("# Shared Global Instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Claude Code wrapper file referencing @AGENTS.md alongside symlinked AGENTS.md.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeWrapper := filepath.Join(claudeDir, "CLAUDE.md")
	if err := os.WriteFile(claudeWrapper, []byte("@AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	links := []string{
		filepath.Join(home, ".gemini", "GEMINI.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, ".claude", "AGENTS.md"),
		filepath.Join(home, ".agents", "AGENTS.md"),
	}
	for _, link := range links {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(canonical, link); err != nil {
			t.Fatal(err)
		}
	}

	actions, err := ConfigureAgents(Options{Home: home, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureAgents: %v", err)
	}

	updatedCount := 0
	dedupCount := 0
	for _, a := range actions {
		switch a.Status {
		case StatusUpdated:
			updatedCount++
		case StatusUnchanged:
			if strings.HasPrefix(a.Detail, "same target as ") {
				dedupCount++
			}
		default:
			t.Errorf("unexpected action status: %+v", a)
		}
	}
	if updatedCount != 1 || dedupCount != 3 {
		t.Fatalf("updated=%d dedup=%d, want 1 and 3; actions=%+v", updatedCount, dedupCount, actions)
	}

	// Wrapper file must remain untouched (@AGENTS.md).
	wrapperRaw, err := os.ReadFile(claudeWrapper)
	if err != nil || string(wrapperRaw) != "@AGENTS.md\n" {
		t.Fatalf("claude wrapper modified: %q (err=%v)", string(wrapperRaw), err)
	}

	// Every symlink must remain a symlink (never overwritten with a regular file).
	for _, link := range links {
		fi, lerr := os.Lstat(link)
		if lerr != nil {
			t.Fatalf("lstat %s: %v", link, lerr)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s was overwritten with a non-symlink", link)
		}
	}

	// Canonical target has both original header and managed OFAM block.
	canonRaw, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(canonRaw), "# Shared Global Instructions\n") || !strings.Contains(string(canonRaw), OFAMBeginMarker) {
		t.Fatalf("canonical content unexpected:\n%s", string(canonRaw))
	}
}

func TestConfigureAgents_MalformedMarkersRefused(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"orphan_begin", "# Header\n" + OFAMBeginMarker + "\nunclosed block\n"},
		{"orphan_end", "# Header\n" + OFAMEndMarker + "\n"},
		{"reversed", "# Header\n" + OFAMEndMarker + "\nmiddle\n" + OFAMBeginMarker + "\n"},
		{"duplicate", OFAMBeginMarker + "\na\n" + OFAMEndMarker + "\n" + OFAMBeginMarker + "\nb\n" + OFAMEndMarker + "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			target := filepath.Join(home, ".gemini", "GEMINI.md")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ConfigureAgents(Options{Home: home, Now: fixedClock()}); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			after, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.content {
				t.Fatalf("file was modified despite malformed markers")
			}
		})
	}
}

func TestSyncSkills_IdempotentSharedDirAndRealDirProtection(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"ofam-wave-release", true},
		{"ofam-custom", true},
		{"mtm-triage", true},
		{"matomeru-ops", true},
		{"ochazuke-ui", true},
		{"oden-cli", true},
		{"o-lane", true},
		{"o-lane-extra", true},
		{"o-df", true},
		{"o-cmds", true},
		{"o-questions", true},
		{"o-md", true},
		{"o-git", true},
		{"o-json", true},
		{"o-ai", true},
		{"japanese-tutor", false},
		{"custom-skill", false},
		{"", false},
	} {
		if got := IsOFAMSkill(tc.name); got != tc.want {
			t.Errorf("IsOFAMSkill(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}

	home := t.TempDir()
	hubDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-existing OFAM skill and unrelated personal skill in ~/.agents/skills.
	for _, s := range []string{"o-lane", "japanese-tutor"} {
		dir := filepath.Join(hubDir, s)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+s+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Antigravity shares the canonical hub directory via symlink.
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	geminiSkills := filepath.Join(home, ".gemini", "skills")
	if err := os.Symlink(hubDir, geminiSkills); err != nil {
		t.Fatal(err)
	}

	// Extra skills source with one valid skill and one invalid directory (missing SKILL.md).
	extraSrc := filepath.Join(home, "extra-skills")
	customSkillSrc := filepath.Join(extraSrc, "custom-skill")
	if err := os.MkdirAll(customSkillSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customSkillSrc, "SKILL.md"), []byte("---\nname: custom-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(extraSrc, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Real non-symlink skill directory in Claude skills that must NEVER be clobbered without --force.
	claudeCustom := filepath.Join(home, ".claude", "skills", "custom-skill")
	if err := os.MkdirAll(claudeCustom, 0o755); err != nil {
		t.Fatal(err)
	}
	userNote := filepath.Join(claudeCustom, "local-note.txt")
	if err := os.WriteFile(userNote, []byte("precious local skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mismatched symlink in Codex skills.
	codexCustom := filepath.Join(home, ".codex", "skills", "custom-skill")
	if err := os.MkdirAll(filepath.Dir(codexCustom), 0o755); err != nil {
		t.Fatal(err)
	}
	wrongTarget := filepath.Join(home, "wrong-skill")
	if err := os.MkdirAll(wrongTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wrongTarget, codexCustom); err != nil {
		t.Fatal(err)
	}

	opts := Options{Home: home, SkillsSrc: []string{extraSrc}, Now: fixedClock()}
	actions, err := SyncSkills(opts)
	if err != nil {
		t.Fatalf("SyncSkills: %v", err)
	}

	byTarget := map[string]Action{}
	for _, a := range actions {
		byTarget[a.Target] = a
	}

	if got := byTarget["hub/ofam-wave-release"]; got.Status != StatusCreated {
		t.Errorf("hub/ofam-wave-release = %+v, want created", got)
	}
	if got := byTarget["antigravity"]; got.Status != StatusUnchanged || got.Detail != "shared dir" {
		t.Errorf("antigravity shared dir = %+v", got)
	}
	if _, hasPerEntry := byTarget["antigravity/ofam-wave-release"]; hasPerEntry {
		t.Errorf("shared dir should skip per-entry linking")
	}
	if got := byTarget["claude/ofam-wave-release"]; got.Status != StatusLinked {
		t.Errorf("claude/ofam-wave-release = %+v, want linked", got)
	}
	if got := byTarget["claude/o-lane"]; got.Status != StatusLinked {
		t.Errorf("claude/o-lane = %+v, want linked", got)
	}
	if got := byTarget["codex/o-lane"]; got.Status != StatusLinked {
		t.Errorf("codex/o-lane = %+v, want linked", got)
	}
	// Unrelated personal skill in ~/.agents/skills MUST be ignored by default.
	if _, leaked := byTarget["claude/japanese-tutor"]; leaked {
		t.Errorf("personal skill japanese-tutor should not be synced by default")
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "japanese-tutor")); !os.IsNotExist(err) {
		t.Errorf("personal skill symlink should not exist in .claude/skills: err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "japanese-tutor")); !os.IsNotExist(err) {
		t.Errorf("personal skill symlink should not exist in .codex/skills: err=%v", err)
	}
	if got := byTarget["claude/custom-skill"]; got.Status != StatusSkipped {
		t.Errorf("claude/custom-skill real dir = %+v, want skipped", got)
	}
	if got := byTarget["codex/custom-skill"]; got.Status != StatusConflict {
		t.Errorf("codex/custom-skill mismatched symlink = %+v, want conflict", got)
	}
	if _, bad := byTarget["claude/not-a-skill"]; bad {
		t.Errorf("invalid skill dir without SKILL.md should not be linked")
	}
	if raw, rerr := os.ReadFile(userNote); rerr != nil || string(raw) != "precious local skill" {
		t.Fatalf("real directory content was damaged: err=%v raw=%q", rerr, string(raw))
	}

	// Second run without --force is idempotent on already-converged links.
	again, err := SyncSkills(opts)
	if err != nil {
		t.Fatalf("SyncSkills second run: %v", err)
	}
	for _, a := range again {
		switch a.Target {
		case "claude/custom-skill":
			if a.Status != StatusSkipped {
				t.Errorf("second run %s = %q, want skipped", a.Target, a.Status)
			}
		case "codex/custom-skill":
			if a.Status != StatusConflict {
				t.Errorf("second run %s = %q, want conflict", a.Target, a.Status)
			}
		default:
			if a.Status != StatusUnchanged {
				t.Errorf("second run %s = %q, want unchanged", a.Target, a.Status)
			}
		}
	}

	// With --all-skills: unrelated personal skill in ~/.agents/skills IS synced.
	allOpts := opts
	allOpts.AllSkills = true
	allActs, err := SyncSkills(allOpts)
	if err != nil {
		t.Fatalf("SyncSkills --all-skills: %v", err)
	}
	allByTarget := map[string]Action{}
	for _, a := range allActs {
		allByTarget[a.Target] = a
	}
	if got := allByTarget["claude/japanese-tutor"]; got.Status != StatusLinked {
		t.Errorf("--all-skills claude/japanese-tutor = %+v, want linked", got)
	}
	if got := allByTarget["codex/japanese-tutor"]; got.Status != StatusLinked {
		t.Errorf("--all-skills codex/japanese-tutor = %+v, want linked", got)
	}

	// With --force: real dir is backed up before linking, mismatched symlink is relinked.
	opts.Force = true
	forced, err := SyncSkills(opts)
	if err != nil {
		t.Fatalf("SyncSkills --force: %v", err)
	}
	forcedByTarget := map[string]Action{}
	for _, a := range forced {
		forcedByTarget[a.Target] = a
	}
	if got := forcedByTarget["claude/custom-skill"]; got.Status != StatusLinked || got.Backup == "" {
		t.Fatalf("--force claude/custom-skill = %+v, want linked with backup", got)
	} else {
		bakNote := filepath.Join(got.Backup, "local-note.txt")
		if raw, rerr := os.ReadFile(bakNote); rerr != nil || string(raw) != "precious local skill" {
			t.Fatalf("backed-up real dir missing user file: err=%v raw=%q", rerr, string(raw))
		}
	}
	if got := forcedByTarget["codex/custom-skill"]; got.Status != StatusLinked {
		t.Fatalf("--force codex/custom-skill = %+v, want linked", got)
	}
}

func TestConfigureClaudeHooks_MergePreservesUserSettingsAndIdempotent(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	initial := `{
  "model": "opus",
  "permissions": {
    "allow": ["Read", "Grep"]
  },
  "hooks": {
    "CustomEvent": [
      {
        "hooks": [{"type": "command", "command": "echo custom"}]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "python3 pgrep-guard.py"}]
      }
    ],
    "SessionStart": [
      {
        "hooks": [{"type": "command", "command": "o-lane agent-hook"}]
      }
    ],
    "Stop": [
      {
        "hooks": [{"type": "command", "command": "/usr/local/bin/mtm-hook claude-code --integration 3", "timeout": 5}]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [{"type": "command", "command": "/Users/me/bin/o-lane agent-hook claude-code", "timeout": 5}]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := Options{Home: home, Now: fixedClock()}
	actions, err := ConfigureClaudeHooks(opts)
	if err != nil {
		t.Fatalf("ConfigureClaudeHooks: %v", err)
	}
	if len(actions) != len(ClaudeHookEvents)+len(OLaneClaudeHookEvents) {
		t.Fatalf("got %d hook actions, want %d", len(actions), len(ClaudeHookEvents)+len(OLaneClaudeHookEvents))
	}

	byEvent := map[string]Action{}
	for _, a := range actions {
		byEvent[a.Target] = a
	}
	if byEvent["claude/Stop"].Status != StatusUnchanged {
		t.Errorf("existing legacy mtm-hook Stop should be unchanged, got %+v", byEvent["claude/Stop"])
	}
	if byEvent["claude/PermissionRequest"].Status != StatusUpdated || byEvent["claude/PermissionRequest"].Backup == "" {
		t.Errorf("PermissionRequest = %+v, want updated with backup", byEvent["claude/PermissionRequest"])
	}
	if byEvent["claude/o-lane/SubagentStart"].Status != StatusUpdated {
		t.Errorf("o-lane SubagentStart = %+v, want updated", byEvent["claude/o-lane/SubagentStart"])
	}
	if byEvent["claude/o-lane/SubagentStop"].Status != StatusUnchanged {
		t.Errorf("existing o-lane SubagentStop should be unchanged, got %+v", byEvent["claude/o-lane/SubagentStop"])
	}

	// Verify file permissions are 0600.
	fi, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("settings.json mode = %o, want 0600", fi.Mode().Perm())
	}

	// Inspect merged JSON structure thoroughly.
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("merged settings.json invalid JSON: %v", err)
	}
	if parsed["model"] != "opus" {
		t.Errorf("top-level model lost: %v", parsed["model"])
	}
	hooks, _ := parsed["hooks"].(map[string]any)
	if _, ok := hooks["CustomEvent"]; !ok {
		t.Errorf("unrelated CustomEvent lost")
	}

	// PreToolUse must still have Bash -> pgrep-guard.py PLUS mtm-hook.
	preGroups, _ := hooks["PreToolUse"].([]any)
	if len(preGroups) != 2 {
		t.Fatalf("PreToolUse groups = %d, want 2: %v", len(preGroups), preGroups)
	}
	if first, _ := preGroups[0].(map[string]any); first["matcher"] != "Bash" {
		t.Errorf("Bash matcher group altered: %v", first)
	}

	// SessionStart must keep o-lane agent-hook PLUS mtm-hook.
	ssGroups, _ := hooks["SessionStart"].([]any)
	if len(ssGroups) != 2 {
		t.Fatalf("SessionStart groups = %d, want 2", len(ssGroups))
	}

	// Stop must NOT have a duplicate mtm-hook group.
	stopGroups, _ := hooks["Stop"].([]any)
	if len(stopGroups) != 1 {
		t.Fatalf("Stop was duplicated: %d groups", len(stopGroups))
	}

	// SubagentStart and SubagentStop must each have exactly 1 mtm-hook and 1 o-lane entry.
	for _, ev := range []string{"SubagentStart", "SubagentStop"} {
		groups, _ := hooks[ev].([]any)
		mtmCount, olaneCount := 0, 0
		for _, g := range groups {
			gm, _ := g.(map[string]any)
			hl, _ := gm["hooks"].([]any)
			for _, h := range hl {
				hm, _ := h.(map[string]any)
				cmd, _ := hm["command"].(string)
				if isMTMClaudeHookCommand(cmd, DefaultClaudeHookCmd) {
					mtmCount++
				}
				if isOLaneHookCommand(cmd, DefaultOLaneHookCmd) {
					olaneCount++
					if got, ok := numberToInt(hm["timeout"]); !ok || got != 5 {
						t.Errorf("%s o-lane timeout = %v, want 5", ev, hm["timeout"])
					}
				}
			}
		}
		if mtmCount != 1 || olaneCount != 1 {
			t.Errorf("%s counts: mtm=%d olane=%d, want 1 and 1", ev, mtmCount, olaneCount)
		}
	}

	// Verify timeouts for all 14 events.
	for _, spec := range ClaudeHookEvents {
		groups, ok := hooks[spec.Event].([]any)
		if !ok || len(groups) == 0 {
			t.Fatalf("missing hook event %s", spec.Event)
		}
		foundMTM := false
		for _, g := range groups {
			gm, _ := g.(map[string]any)
			hl, _ := gm["hooks"].([]any)
			for _, h := range hl {
				hm, _ := h.(map[string]any)
				cmd, _ := hm["command"].(string)
				if isMTMClaudeHookCommand(cmd, DefaultClaudeHookCmd) {
					foundMTM = true
					if got, ok := numberToInt(hm["timeout"]); !ok || got != spec.Timeout {
						t.Errorf("%s timeout = %v, want %d", spec.Event, hm["timeout"], spec.Timeout)
					}
				}
			}
		}
		if !foundMTM {
			t.Errorf("%s missing mtm-hook entry", spec.Event)
		}
	}

	// Re-run must be 100% idempotent with zero writes.
	again, err := ConfigureClaudeHooks(opts)
	if err != nil {
		t.Fatalf("second ConfigureClaudeHooks: %v", err)
	}
	for _, a := range again {
		if a.Status != StatusUnchanged {
			t.Errorf("second run %s = %q, want unchanged", a.Target, a.Status)
		}
		if a.Backup != "" {
			t.Errorf("second run unexpectedly created backup %s", a.Backup)
		}
	}

	// SkipOLaneHook omits o-lane entries cleanly.
	skipHome := t.TempDir()
	skipActs, err := ConfigureClaudeHooks(Options{Home: skipHome, SkipOLaneHook: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureClaudeHooks SkipOLaneHook: %v", err)
	}
	if len(skipActs) != len(ClaudeHookEvents) {
		t.Fatalf("SkipOLaneHook actions = %d, want %d", len(skipActs), len(ClaudeHookEvents))
	}

	// Invalid JSON must fail cleanly without touching the file.
	badHome := t.TempDir()
	badPath := filepath.Join(badHome, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigureClaudeHooks(Options{Home: badHome, Now: fixedClock()}); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if after, _ := os.ReadFile(badPath); string(after) != "{not valid json" {
		t.Fatalf("invalid JSON file was modified")
	}
}

func TestConfigureMCP_ClaudeCodexAntigravityIdempotent(t *testing.T) {
	home := t.TempDir()
	claudePath := filepath.Join(home, ".claude.json")
	codexPath := filepath.Join(home, ".codex", "config.toml")
	geminiPath := filepath.Join(home, ".gemini", "settings.json")

	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(geminiPath), 0o755); err != nil {
		t.Fatal(err)
	}

	initialClaude := `{
  "theme": "dark",
  "mcpServers": {
    "other": {"type": "stdio", "command": "other-mcp", "args": ["serve"]}
  }
}
`
	initialCodex := `# Personal Codex settings
model = "o3"
# [mcp_servers.mtm]

[mcp_servers.other]
command = "other-mcp"
args = ["serve"]
`
	initialGemini := `{
  "selectedAuthType": "oauth",
  "mcpServers": {
    "sibling": {"command": "sibling-mcp", "args": ["--stdio"]}
  }
}
`
	if err := os.WriteFile(claudePath, []byte(initialClaude), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(initialCodex), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(geminiPath, []byte(initialGemini), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Check mode detects missing mtm MCP server without writing.
	chk, err := ConfigureMCP(Options{Home: home, Check: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureMCP check: %v", err)
	}
	if len(chk) != 3 {
		t.Fatalf("got %d actions, want 3", len(chk))
	}
	for _, a := range chk {
		if a.Status != StatusMissing {
			t.Errorf("check %s = %q, want missing", a.Target, a.Status)
		}
	}

	// 2. Dry-run previews updates without modifying files.
	dry, err := ConfigureMCP(Options{Home: home, DryRun: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureMCP dry-run: %v", err)
	}
	for _, a := range dry {
		if a.Status != StatusWouldUpdate {
			t.Errorf("dry-run %s = %q, want would_update", a.Target, a.Status)
		}
	}
	if got, _ := os.ReadFile(codexPath); string(got) != initialCodex {
		t.Fatalf("dry-run modified config.toml")
	}

	// 3. Apply writes all 3 atomically with .bak-YYYYMMDD backup (mode 0600).
	applied, err := ConfigureMCP(Options{Home: home, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureMCP apply: %v", err)
	}
	for _, a := range applied {
		if a.Status != StatusUpdated {
			t.Errorf("apply %s = %+v, want updated", a.Target, a)
		}
		if !strings.HasSuffix(a.Backup, ".bak-20261123") {
			t.Errorf("apply %s backup = %q, want .bak-20261123 suffix", a.Target, a.Backup)
		}
		fi, serr := os.Stat(a.Path)
		if serr != nil {
			t.Fatalf("stat %s: %v", a.Path, serr)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 0600", a.Path, fi.Mode().Perm())
		}
	}

	// Inspect Claude Code .claude.json.
	var claudeObj map[string]any
	if raw, err := os.ReadFile(claudePath); err != nil || json.Unmarshal(raw, &claudeObj) != nil {
		t.Fatalf("parse .claude.json: err=%v", err)
	}
	if claudeObj["theme"] != "dark" {
		t.Errorf(".claude.json lost top-level theme: %v", claudeObj["theme"])
	}
	cServers, _ := claudeObj["mcpServers"].(map[string]any)
	if _, ok := cServers["other"]; !ok {
		t.Errorf(".claude.json lost sibling mcpServers.other")
	}
	if !equalMCPServerEntry(cServers["mtm"], "stdio", "mtm", []string{"mcp", "serve"}) {
		t.Errorf(".claude.json mcpServers.mtm = %#v", cServers["mtm"])
	}

	// Inspect Codex config.toml: original bytes preserved verbatim + new section appended.
	codexRaw, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	wantCodex := initialCodex + "\n[mcp_servers.mtm]\ncommand = \"mtm\"\nargs = [\"mcp\", \"serve\"]\n"
	if string(codexRaw) != wantCodex {
		t.Fatalf("config.toml mismatch:\ngot:\n%s\nwant:\n%s", string(codexRaw), wantCodex)
	}

	// Inspect Antigravity/Gemini .gemini/settings.json.
	var geminiObj map[string]any
	if raw, err := os.ReadFile(geminiPath); err != nil || json.Unmarshal(raw, &geminiObj) != nil {
		t.Fatalf("parse .gemini/settings.json: err=%v", err)
	}
	if geminiObj["selectedAuthType"] != "oauth" {
		t.Errorf(".gemini/settings.json lost selectedAuthType: %v", geminiObj["selectedAuthType"])
	}
	gServers, _ := geminiObj["mcpServers"].(map[string]any)
	if _, ok := gServers["sibling"]; !ok {
		t.Errorf(".gemini/settings.json lost sibling mcpServers.sibling")
	}
	if !equalMCPServerEntry(gServers["mtm"], "", "mtm", []string{"mcp", "serve"}) {
		t.Errorf(".gemini/settings.json mcpServers.mtm = %#v", gServers["mtm"])
	}

	// 4. Re-run is 100% idempotent with zero writes and zero backups.
	again, err := ConfigureMCP(Options{Home: home, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureMCP second run: %v", err)
	}
	for _, a := range again {
		if a.Status != StatusUnchanged || a.Backup != "" {
			t.Errorf("second run %s = %+v, want unchanged with no backup", a.Target, a)
		}
	}

	// 5. Different MCPCmd without --force leaves existing entries untouched.
	noForce, err := ConfigureMCP(Options{Home: home, MCPCmd: "/opt/bin/mtm", Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureMCP no-force custom cmd: %v", err)
	}
	for _, a := range noForce {
		if a.Status != StatusUnchanged {
			t.Errorf("no-force custom cmd %s = %+v, want unchanged", a.Target, a)
		}
	}

	// Append a trailing TOML section after [mcp_servers.mtm] to verify --force replaces ONLY [mcp_servers.mtm].
	trailing := "\n[mcp_servers.after]\ncommand = \"after-mcp\"\nargs = [\"run\"]\n"
	if err := os.WriteFile(codexPath, append(codexRaw, []byte(trailing)...), 0o600); err != nil {
		t.Fatal(err)
	}

	// 6. --force updates all 3 entries while preserving sibling/trailing sections.
	forced, err := ConfigureMCP(Options{Home: home, MCPCmd: "/opt/bin/mtm", Force: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureMCP --force: %v", err)
	}
	for _, a := range forced {
		if a.Status != StatusUpdated || a.Backup == "" {
			t.Errorf("--force %s = %+v, want updated with backup", a.Target, a)
		}
	}
	codexAfterForce, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	wantCodexForce := initialCodex + "\n[mcp_servers.mtm]\ncommand = \"/opt/bin/mtm\"\nargs = [\"mcp\", \"serve\"]\n" + trailing
	if string(codexAfterForce) != wantCodexForce {
		t.Fatalf("config.toml after --force mismatch:\ngot:\n%s\nwant:\n%s", string(codexAfterForce), wantCodexForce)
	}

	// Re-running --force with the same MCPCmd is idempotent (unchanged).
	forcedAgain, err := ConfigureMCP(Options{Home: home, MCPCmd: "/opt/bin/mtm", Force: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("ConfigureMCP --force idempotent: %v", err)
	}
	for _, a := range forcedAgain {
		if a.Status != StatusUnchanged || a.Backup != "" {
			t.Errorf("--force second run %s = %+v, want unchanged with no backup", a.Target, a)
		}
	}

	// 7. Invalid JSON in .claude.json or .gemini/settings.json fails cleanly without writing.
	for _, rel := range []string{".claude.json", filepath.Join(".gemini", "settings.json")} {
		badHome := t.TempDir()
		badPath := filepath.Join(badHome, rel)
		if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(badPath, []byte("{bad json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ConfigureMCP(Options{Home: badHome, Now: fixedClock()}); err == nil {
			t.Errorf("expected error for invalid JSON in %s, got nil", rel)
		}
		if after, _ := os.ReadFile(badPath); string(after) != "{bad json" {
			t.Errorf("invalid JSON file %s was modified", rel)
		}
	}
}

func TestSetupDryRunAndCheck(t *testing.T) {
	home := t.TempDir()

	// 1. Check on empty home reports drift and writes nothing.
	chk1, err := CheckMachine(Options{Home: home, Now: fixedClock()})
	if err != nil {
		t.Fatalf("CheckMachine empty: %v", err)
	}
	if chk1.OK || !chk1.Check {
		t.Fatalf("CheckMachine empty = %+v, want OK=false Check=true", chk1)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("CheckMachine wrote files into empty home: %v (err=%v)", entries, err)
	}

	// 2. DryRun reports planned actions and writes nothing.
	dry, err := RunMachine(Options{Home: home, DryRun: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("RunMachine dry-run: %v", err)
	}
	if !dry.OK || !dry.DryRun {
		t.Fatalf("RunMachine dry-run = %+v, want OK=true DryRun=true", dry)
	}
	entries, err = os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("DryRun wrote files into empty home: %v (err=%v)", entries, err)
	}
	sawWould := false
	for _, a := range dry.Actions {
		if a.Status == StatusWouldCreate || a.Status == StatusWouldLink {
			sawWould = true
		}
	}
	if !sawWould {
		t.Fatalf("DryRun reported no planned actions: %+v", dry.Actions)
	}

	// 3. Apply creates everything.
	applied, err := RunMachine(Options{Home: home, Now: fixedClock()})
	if err != nil {
		t.Fatalf("RunMachine apply: %v", err)
	}
	if !applied.OK {
		t.Fatalf("RunMachine apply not OK: %+v", applied)
	}

	// 4. Check after apply reports OK with all unchanged.
	chk2, err := CheckMachine(Options{Home: home, Now: fixedClock()})
	if err != nil {
		t.Fatalf("CheckMachine converged: %v", err)
	}
	if !chk2.OK {
		t.Fatalf("CheckMachine after apply not OK: %+v", chk2)
	}
	for _, a := range chk2.Actions {
		if a.Status != StatusUnchanged {
			t.Errorf("converged check %s/%s = %q, want unchanged", a.Component, a.Target, a.Status)
		}
	}

	// 5. Introducing drift causes CheckMachine to report OK=false.
	if err := os.WriteFile(filepath.Join(home, ".codex", "AGENTS.md"), []byte("# drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chk3, err := CheckMachine(Options{Home: home, Now: fixedClock()})
	if err != nil {
		t.Fatalf("CheckMachine drifted: %v", err)
	}
	if chk3.OK {
		t.Fatalf("expected CheckMachine to detect drift, got OK=true")
	}
}
