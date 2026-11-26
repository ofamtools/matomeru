package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/setup"
)

func TestCLISetup(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(t.TempDir(), "mtm.db")
	// Ensure real host CLAUDE_CONFIG_DIR cannot interfere.
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	runMTM := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := run(append([]string{"--db", db}, args...), strings.NewReader(""), &out, &errs)
		return code, out.String(), errs.String()
	}

	// 1. `mtm setup check --home <tmp> --json` exits non-zero when drift exists.
	code, out, errs := runMTM("setup", "check", "--home", home, "--json")
	if code == 0 {
		t.Fatalf("mtm setup check on empty home exited 0; stdout=%s stderr=%s", out, errs)
	}
	var chkBefore setup.Report
	if err := json.Unmarshal([]byte(out), &chkBefore); err != nil {
		t.Fatalf("setup check JSON: %v\n%s", err, out)
	}
	if chkBefore.OK || !chkBefore.Check || len(chkBefore.Actions) == 0 {
		t.Fatalf("unexpected check report before apply: %+v", chkBefore)
	}

	// Bare `mtm setup --home <tmp>` prints a safe read-only summary and exits 0.
	code, out, errs = runMTM("setup", "--home", home)
	if code != 0 || !strings.Contains(out, "COMPONENT") || !strings.Contains(out, "drift detected") {
		t.Fatalf("bare mtm setup: code=%d\nout=%s\nerr=%s", code, out, errs)
	}

	// 2. `mtm setup machine --home <tmp> --dry-run --json` succeeds and writes nothing.
	code, out, errs = runMTM("setup", "machine", "--home", home, "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("setup machine --dry-run exit %d: %s", code, errs)
	}
	var dryRep setup.Report
	if err := json.Unmarshal([]byte(out), &dryRep); err != nil {
		t.Fatalf("dry-run JSON: %v\n%s", err, out)
	}
	if !dryRep.OK || !dryRep.DryRun {
		t.Fatalf("dry-run report = %+v", dryRep)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("dry-run modified home: %v (err=%v)", entries, err)
	}

	// 3. `mtm setup machine --home <tmp> --json` creates all targets cleanly.
	code, out, errs = runMTM("setup", "machine", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("setup machine exit %d: %s", code, errs)
	}
	var applyRep setup.Report
	if err := json.Unmarshal([]byte(out), &applyRep); err != nil {
		t.Fatalf("apply JSON: %v\n%s", err, out)
	}
	if !applyRep.OK || applyRep.DryRun || applyRep.Check {
		t.Fatalf("apply report = %+v", applyRep)
	}
	for _, rel := range []string{
		filepath.Join(".gemini", "GEMINI.md"),
		filepath.Join(".codex", "AGENTS.md"),
		filepath.Join(".claude", "CLAUDE.md"),
		filepath.Join(".agents", "AGENTS.md"),
		filepath.Join(".agents", "skills", "ofam-wave-release", "SKILL.md"),
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".codex", "hooks.json"),
		filepath.Join(".codex", "config.toml"),
		filepath.Join(".gemini", "config", "hooks.json"),
		filepath.Join(".gemini", "config", "mcp_config.json"),
		filepath.Join(".gemini", "settings.json"),
		filepath.Join(".omp", "agent", "extensions", setup.OMPExtensionFilename),
		".claude.json",
	} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("expected target %s to exist: %v", rel, err)
		}
	}

	// 4. `mtm setup check --home <tmp> --json` now exits 0 with OK=true.
	code, out, errs = runMTM("setup", "check", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("setup check after apply exit %d: stdout=%s stderr=%s", code, out, errs)
	}
	var chkAfter setup.Report
	if err := json.Unmarshal([]byte(out), &chkAfter); err != nil {
		t.Fatalf("check after JSON: %v\n%s", err, out)
	}
	if !chkAfter.OK || !chkAfter.Check {
		t.Fatalf("check after apply = %+v", chkAfter)
	}

	// 5. Individual component subcommands also succeed on converged home.
	for _, sub := range []string{"agents", "skills", "hooks", "mcp"} {
		code, out, errs = runMTM("setup", sub, "--home", home, "--check", "--json")
		if code != 0 {
			t.Fatalf("setup %s --check exit %d: %s %s", sub, code, out, errs)
		}
	}

	// 6. Standalone `mtm setup mcp` applies cleanly to all 4 harnesses with custom --mcp-cmd and passes --check.
	mcpHome := t.TempDir()
	code, out, errs = runMTM("setup", "mcp", "--home", mcpHome, "--mcp-cmd", "/custom/bin/mtm", "--json")
	if code != 0 {
		t.Fatalf("setup mcp exit %d: %s %s", code, out, errs)
	}
	var mcpRep setup.Report
	if err := json.Unmarshal([]byte(out), &mcpRep); err != nil {
		t.Fatalf("setup mcp JSON: %v\n%s", err, out)
	}
	if !mcpRep.OK || len(mcpRep.Actions) != 4 {
		t.Fatalf("setup mcp report = %+v", mcpRep)
	}
	for _, rel := range []string{
		".claude.json",
		filepath.Join(".codex", "config.toml"),
		filepath.Join(".gemini", "config", "mcp_config.json"),
		filepath.Join(".gemini", "settings.json"),
	} {
		if _, err := os.Stat(filepath.Join(mcpHome, rel)); err != nil {
			t.Errorf("expected MCP target %s to exist: %v", rel, err)
		}
	}
	code, out, errs = runMTM("setup", "mcp", "--home", mcpHome, "--check", "--json")
	if code != 0 {
		t.Fatalf("setup mcp --check exit %d: %s %s", code, out, errs)
	}

	// 7. Standalone `mtm setup hooks` configures all 5 harnesses and passes --check.
	hooksHome := t.TempDir()
	code, out, errs = runMTM("setup", "hooks", "--home", hooksHome, "--json")
	if code != 0 {
		t.Fatalf("setup hooks exit %d: %s %s", code, out, errs)
	}
	var hooksRep setup.Report
	if err := json.Unmarshal([]byte(out), &hooksRep); err != nil {
		t.Fatalf("setup hooks JSON: %v\n%s", err, out)
	}
	if !hooksRep.OK {
		t.Fatalf("setup hooks report = %+v", hooksRep)
	}
	for _, rel := range []string{
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".codex", "hooks.json"),
		filepath.Join(".codex", "config.toml"),
		filepath.Join(".gemini", "config", "hooks.json"),
		filepath.Join(".gemini", "settings.json"),
		filepath.Join(".omp", "agent", "extensions", setup.OMPExtensionFilename),
	} {
		if _, err := os.Stat(filepath.Join(hooksHome, rel)); err != nil {
			t.Errorf("expected hooks target %s to exist: %v", rel, err)
		}
	}
	code, out, errs = runMTM("setup", "hooks", "--home", hooksHome, "--check", "--json")
	if code != 0 {
		t.Fatalf("setup hooks --check exit %d: %s %s", code, out, errs)
	}

	// 8. Unknown subcommand fails with usage.
	code, _, errs = runMTM("setup", "bogus")
	if code == 0 || !strings.Contains(errs, "usage: mtm setup") {
		t.Fatalf("setup bogus: code=%d err=%s", code, errs)
	}
}

func TestCLISetupSyncPurgeAndInitShell(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(t.TempDir(), "mtm.db")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	// Seed a user instruction file with personal prose so we verify purge preserves it cleanly.
	codexPath := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	userProse := "# My Personal Codex Rules\nNever delete this line.\n"
	if err := os.WriteFile(codexPath, []byte(userProse), 0o644); err != nil {
		t.Fatal(err)
	}

	runMTM := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := run(append([]string{"--db", db}, args...), strings.NewReader(""), &out, &errs)
		return code, out.String(), errs.String()
	}

	type syncResp struct {
		OK     bool         `json:"ok"`
		Synced bool         `json:"synced"`
		Hash   string       `json:"hash"`
		Report setup.Report `json:"report"`
	}

	// 1. First `mtm setup sync --home <tmp> --json` runs full sync and writes stamp.
	code, out, errs := runMTM("setup", "sync", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("first setup sync exit %d: %s %s", code, out, errs)
	}
	var sr1 syncResp
	if err := json.Unmarshal([]byte(out), &sr1); err != nil {
		t.Fatalf("sync 1 JSON: %v\n%s", err, out)
	}
	if !sr1.OK || !sr1.Synced || sr1.Hash != setup.AssetsHash() || len(sr1.Report.Actions) == 0 {
		t.Fatalf("sync 1 resp = %+v", sr1)
	}
	stampPath := filepath.Join(home, ".local", "state", "matomeru", setup.SyncStampFilename)
	if _, err := os.Stat(stampPath); err != nil {
		t.Fatalf("expected sync stamp at %s: %v", stampPath, err)
	}

	// 2. Second `mtm setup sync --home <tmp> --json` is a fast no-op (synced=false).
	code, out, errs = runMTM("setup", "sync", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("second setup sync exit %d: %s %s", code, out, errs)
	}
	var sr2 syncResp
	if err := json.Unmarshal([]byte(out), &sr2); err != nil {
		t.Fatalf("sync 2 JSON: %v\n%s", err, out)
	}
	if !sr2.OK || sr2.Synced || sr2.Hash != setup.AssetsHash() {
		t.Fatalf("sync 2 resp = %+v, want synced=false", sr2)
	}

	// 3. Text output when up to date prints short hash; --quiet prints nothing.
	code, out, errs = runMTM("setup", "sync", "--home", home)
	if code != 0 || !strings.Contains(out, "setup sync: up to date ("+setup.AssetsHash()[:12]+")") {
		t.Fatalf("sync up-to-date text: code=%d out=%q err=%q", code, out, errs)
	}
	code, out, errs = runMTM("setup", "sync", "--home", home, "--quiet")
	if code != 0 || out != "" || errs != "" {
		t.Fatalf("sync --quiet up-to-date: code=%d out=%q err=%q", code, out, errs)
	}

	// 4. --force re-runs even when stamp matches.
	code, out, errs = runMTM("setup", "sync", "--home", home, "--force", "--json")
	if code != 0 {
		t.Fatalf("sync --force exit %d: %s %s", code, out, errs)
	}
	var srForce syncResp
	if err := json.Unmarshal([]byte(out), &srForce); err != nil {
		t.Fatalf("sync --force JSON: %v\n%s", err, out)
	}
	if !srForce.OK || !srForce.Synced {
		t.Fatalf("sync --force resp = %+v, want synced=true", srForce)
	}

	// 5. `mtm setup purge --home <tmp> --dry-run --json` previews purge without clearing stamp or file.
	code, out, errs = runMTM("setup", "purge", "--home", home, "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("purge --dry-run exit %d: %s %s", code, out, errs)
	}
	var dryPurge setup.Report
	if err := json.Unmarshal([]byte(out), &dryPurge); err != nil {
		t.Fatalf("purge dry-run JSON: %v\n%s", err, out)
	}
	if !dryPurge.OK || !dryPurge.DryRun {
		t.Fatalf("purge dry-run = %+v", dryPurge)
	}
	if _, err := os.Stat(stampPath); err != nil {
		t.Fatalf("purge --dry-run unexpectedly deleted stamp: %v", err)
	}

	// 6. `mtm setup purge --home <tmp> --json` strips ONLY the managed block and deletes the sync stamp.
	code, out, errs = runMTM("setup", "purge", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("purge exit %d: %s %s", code, out, errs)
	}
	var purgeRep setup.Report
	if err := json.Unmarshal([]byte(out), &purgeRep); err != nil {
		t.Fatalf("purge JSON: %v\n%s", err, out)
	}
	if !purgeRep.OK {
		t.Fatalf("purge report = %+v", purgeRep)
	}
	if _, err := os.Stat(stampPath); !os.IsNotExist(err) {
		t.Fatalf("expected purge to remove stamp %s (err=%v)", stampPath, err)
	}
	afterPurge, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPurge) != userProse {
		t.Fatalf("codex AGENTS.md after purge:\ngot:  %q\nwant: %q", string(afterPurge), userProse)
	}

	// 7. Subsequent `mtm setup sync` re-applies because purge removed the stamp.
	code, out, errs = runMTM("setup", "sync", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("sync after purge exit %d: %s %s", code, out, errs)
	}
	var srAfterPurge syncResp
	if err := json.Unmarshal([]byte(out), &srAfterPurge); err != nil {
		t.Fatalf("sync after purge JSON: %v\n%s", err, out)
	}
	if !srAfterPurge.OK || !srAfterPurge.Synced {
		t.Fatalf("sync after purge = %+v, want synced=true", srAfterPurge)
	}

	// 8. `mtm setup init-shell` outputs lightweight background hook snippet.
	code, out, errs = runMTM("setup", "init-shell")
	if code != 0 || errs != "" || !strings.Contains(out, "mtm setup sync --quiet") {
		t.Fatalf("init-shell: code=%d out=%q err=%q", code, out, errs)
	}

	// 9. autoSyncSetupIfChanged honors MTM_NO_AUTO_SETUP and isolates HOME/XDG_STATE_HOME.
	autoHome := t.TempDir()
	autoState := filepath.Join(t.TempDir(), "xdg-state")
	t.Setenv("HOME", autoHome)
	t.Setenv("XDG_STATE_HOME", autoState)
	t.Setenv("MTM_NO_AUTO_SETUP", "1")
	t.Setenv("MTM_SMOKE", "")
	if w := autoSyncSetupIfChanged(); w != "" {
		t.Fatalf("autoSyncSetupIfChanged disabled returned warning %q", w)
	}
	if _, err := os.Stat(filepath.Join(autoState, "matomeru", setup.SyncStampFilename)); !os.IsNotExist(err) {
		t.Fatalf("MTM_NO_AUTO_SETUP=1 still wrote stamp")
	}

	t.Setenv("MTM_NO_AUTO_SETUP", "")
	if w := autoSyncSetupIfChanged(); w != "" {
		t.Fatalf("autoSyncSetupIfChanged enabled returned warning %q", w)
	}
	if _, err := os.Stat(filepath.Join(autoState, "matomeru", setup.SyncStampFilename)); err != nil {
		t.Fatalf("autoSyncSetupIfChanged did not write stamp: %v", err)
	}
}
