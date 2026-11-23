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
	for _, sub := range []string{"agents", "skills", "hooks"} {
		code, out, errs = runMTM("setup", sub, "--home", home, "--check", "--json")
		if code != 0 {
			t.Fatalf("setup %s --check exit %d: %s %s", sub, code, out, errs)
		}
	}

	// 6. Unknown subcommand fails with usage.
	code, _, errs = runMTM("setup", "bogus")
	if code == 0 || !strings.Contains(errs, "usage: mtm setup") {
		t.Fatalf("setup bogus: code=%d err=%s", code, errs)
	}
}
