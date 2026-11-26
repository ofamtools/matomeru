package setup

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetsHash_StableSHA256Hex(t *testing.T) {
	h1 := AssetsHash()
	h2 := AssetsHash()
	if h1 != h2 {
		t.Fatalf("AssetsHash not deterministic: %q != %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("AssetsHash len = %d, want 64 (%q)", len(h1), h1)
	}
	if _, err := hex.DecodeString(h1); err != nil {
		t.Fatalf("AssetsHash not valid hex %q: %v", h1, err)
	}
}

func TestSyncIfChanged_HashGatedAndForce(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	opts := Options{Home: home, Now: fixedClock()}

	// 1. First run converges all files and writes the stamp.
	rep1, ran1, err := SyncIfChanged(opts, stateDir, false)
	if err != nil {
		t.Fatalf("first SyncIfChanged: %v", err)
	}
	if !ran1 || !rep1.OK || len(rep1.Actions) == 0 {
		t.Fatalf("first SyncIfChanged: ran=%v rep=%+v", ran1, rep1)
	}
	stampPath := filepath.Join(stateDir, SyncStampFilename)
	stampBytes, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	if strings.TrimSpace(string(stampBytes)) != AssetsHash() {
		t.Fatalf("stamp = %q, want %q", strings.TrimSpace(string(stampBytes)), AssetsHash())
	}

	// Verify embedded rules include task-anchor and orchestration rules.
	codexRaw, err := os.ReadFile(filepath.Join(home, ".codex", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Tasks are the anchor",
		"pane.spawn",
		"data.relate",
		"o-release",
		"mtm daemon upgrade --json",
	} {
		if !strings.Contains(string(codexRaw), want) {
			t.Errorf("synced AGENTS.md missing %q", want)
		}
	}

	// 2. Second run is an immediate no-op (even if a file was edited, stamp gates it).
	markerPath := filepath.Join(home, ".codex", "AGENTS.md")
	beforeInfo, err := os.Stat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	rep2, ran2, err := SyncIfChanged(opts, stateDir, false)
	if err != nil {
		t.Fatalf("second SyncIfChanged: %v", err)
	}
	if ran2 || !rep2.OK || len(rep2.Actions) != 0 {
		t.Fatalf("second SyncIfChanged: ran=%v rep=%+v, want ran=false ok=true", ran2, rep2)
	}
	afterInfo, err := os.Stat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("second SyncIfChanged unexpectedly touched %s", markerPath)
	}

	// 3. Changing the stamp hash triggers clean re-sync and restores drifted block.
	if err := os.WriteFile(markerPath, []byte("# User\n\n"+OFAMBeginMarker+"\nold\n"+OFAMEndMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stampPath, []byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep3, ran3, err := SyncIfChanged(opts, stateDir, false)
	if err != nil {
		t.Fatalf("third SyncIfChanged: %v", err)
	}
	if !ran3 || !rep3.OK {
		t.Fatalf("third SyncIfChanged: ran=%v rep=%+v", ran3, rep3)
	}
	refreshed, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(refreshed), "\nold\n") || !strings.Contains(string(refreshed), OFAMRulesBlock()) {
		t.Fatalf("re-sync did not cleanly replace stale block:\n%s", string(refreshed))
	}
	stampBytes, _ = os.ReadFile(stampPath)
	if strings.TrimSpace(string(stampBytes)) != AssetsHash() {
		t.Fatalf("stamp not refreshed after re-sync: %q", string(stampBytes))
	}

	// 4. force=true runs even when stamp already matches.
	rep4, ran4, err := SyncIfChanged(opts, stateDir, true)
	if err != nil {
		t.Fatalf("forced SyncIfChanged: %v", err)
	}
	if !ran4 || !rep4.OK || len(rep4.Actions) == 0 {
		t.Fatalf("forced SyncIfChanged: ran=%v rep=%+v", ran4, rep4)
	}

	// 5. DryRun does not write the stamp.
	dryState := filepath.Join(t.TempDir(), "dry-state")
	dryHome := t.TempDir()
	dryRep, dryRan, err := SyncIfChanged(Options{Home: dryHome, DryRun: true, Now: fixedClock()}, dryState, false)
	if err != nil || !dryRan || !dryRep.OK {
		t.Fatalf("dry SyncIfChanged: ran=%v err=%v rep=%+v", dryRan, err, dryRep)
	}
	if _, err := os.Stat(filepath.Join(dryState, SyncStampFilename)); !os.IsNotExist(err) {
		t.Fatalf("dry-run unexpectedly created stamp file (err=%v)", err)
	}
}

func TestRemoveManagedBlock_CleanStripAndMalformed(t *testing.T) {
	block := OFAMRulesBlock()

	// Prose before and after with blank lines around the block collapses cleanly.
	before := "# Custom Header\nKeep every single byte here!\n"
	after := "## Trailing Personal Notes\nDo not touch.\n"
	combined := before + "\n" + block + "\n\n" + after
	cleaned, removed, err := RemoveManagedBlock(combined)
	if err != nil || !removed {
		t.Fatalf("RemoveManagedBlock middle: removed=%v err=%v", removed, err)
	}
	wantMiddle := before + "\n" + after
	if cleaned != wantMiddle {
		t.Fatalf("middle mismatch:\ngot:  %q\nwant: %q", cleaned, wantMiddle)
	}
	if strings.Contains(cleaned, OFAMBeginMarker) || strings.Contains(cleaned, OFAMEndMarker) {
		t.Fatalf("fragments left behind: %q", cleaned)
	}

	// Round-trip: user prose -> Upsert -> Remove -> Upsert -> Remove is byte-identical.
	userOnly := "# Personal Instructions\nLine 1.\nLine 2.\n"
	m1, ch1, err := UpsertManagedBlock(userOnly, block)
	if err != nil || !ch1 {
		t.Fatalf("Upsert 1: ch=%v err=%v", ch1, err)
	}
	p1, rem1, err := RemoveManagedBlock(m1)
	if err != nil || !rem1 {
		t.Fatalf("Remove 1: rem=%v err=%v", rem1, err)
	}
	if p1 != userOnly {
		t.Fatalf("round-trip 1 mismatch:\ngot:  %q\nwant: %q", p1, userOnly)
	}
	m2, ch2, err := UpsertManagedBlock(p1, block)
	if err != nil || !ch2 || m2 != m1 {
		t.Fatalf("Upsert 2 mismatch: ch=%v err=%v", ch2, err)
	}
	p2, rem2, err := RemoveManagedBlock(m2)
	if err != nil || !rem2 || p2 != userOnly {
		t.Fatalf("round-trip 2 mismatch:\ngot:  %q\nwant: %q", p2, userOnly)
	}

	// Block-only file cleans to empty string.
	onlyCleaned, onlyRemoved, err := RemoveManagedBlock(block + "\n")
	if err != nil || !onlyRemoved || onlyCleaned != "" {
		t.Fatalf("block-only: cleaned=%q removed=%v err=%v", onlyCleaned, onlyRemoved, err)
	}

	// Block at top followed by user prose leaves only user prose.
	topCleaned, topRemoved, err := RemoveManagedBlock(block + "\n\n" + after)
	if err != nil || !topRemoved || topCleaned != after {
		t.Fatalf("block-at-top: cleaned=%q removed=%v err=%v", topCleaned, topRemoved, err)
	}

	// No markers is a clean no-op.
	plainCleaned, plainRemoved, err := RemoveManagedBlock(userOnly)
	if err != nil || plainRemoved || plainCleaned != userOnly {
		t.Fatalf("no markers: cleaned=%q removed=%v err=%v", plainCleaned, plainRemoved, err)
	}

	// Malformed markers fail without modifying input.
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"orphan_begin", "# H\n" + OFAMBeginMarker + "\nunclosed\n"},
		{"orphan_end", "# H\n" + OFAMEndMarker + "\n"},
		{"reversed", OFAMEndMarker + "\nmid\n" + OFAMBeginMarker + "\n"},
		{"duplicate", OFAMBeginMarker + "\na\n" + OFAMEndMarker + "\n" + OFAMBeginMarker + "\nb\n" + OFAMEndMarker + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, rem, err := RemoveManagedBlock(tc.in)
			if err == nil || rem || out != tc.in {
				t.Fatalf("malformed %s: out=%q rem=%v err=%v", tc.name, out, rem, err)
			}
		})
	}
}

func TestPurgeAgents_DedupDryRunAndRoundTrip(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, "hub", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	userText := "# Shared Global Instructions\nKeep me intact.\n"
	if err := os.WriteFile(canonical, []byte(userText), 0o644); err != nil {
		t.Fatal(err)
	}

	// Claude wrapper + symlinked targets pointing to canonical file.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("@AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{
		filepath.Join(home, ".gemini", "GEMINI.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, ".claude", "AGENTS.md"),
		filepath.Join(home, ".agents", "AGENTS.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(canonical, link); err != nil {
			t.Fatal(err)
		}
	}

	opts := Options{Home: home, Now: fixedClock()}

	// Configure adds the managed block to canonical once.
	if _, err := ConfigureAgents(opts); err != nil {
		t.Fatalf("ConfigureAgents: %v", err)
	}

	// Dry-run reports would_purge once + 3 symlink dedups and does NOT modify file.
	dryActs, err := PurgeAgents(Options{Home: home, DryRun: true, Now: fixedClock()})
	if err != nil {
		t.Fatalf("PurgeAgents dry-run: %v", err)
	}
	wouldCount, dedupCount := 0, 0
	for _, a := range dryActs {
		switch a.Status {
		case StatusWouldPurge:
			wouldCount++
		case StatusUnchanged:
			if strings.HasPrefix(a.Detail, "same target as ") {
				dedupCount++
			}
		default:
			t.Errorf("unexpected dry-run status: %+v", a)
		}
	}
	if wouldCount != 1 || dedupCount != 3 {
		t.Fatalf("dry-run counts: would=%d dedup=%d, want 1 and 3 (%+v)", wouldCount, dedupCount, dryActs)
	}
	if raw, _ := os.ReadFile(canonical); !strings.Contains(string(raw), OFAMBeginMarker) {
		t.Fatalf("dry-run modified canonical file")
	}

	// Real purge strips ONLY the managed block, writes backup, preserves user text byte-for-byte.
	purgeRep, err := RunPurgeAgents(opts)
	if err != nil || !purgeRep.OK {
		t.Fatalf("RunPurgeAgents: err=%v rep=%+v", err, purgeRep)
	}
	purgedCount := 0
	for _, a := range purgeRep.Actions {
		if a.Status == StatusPurged {
			purgedCount++
			if a.Backup == "" {
				t.Errorf("expected backup on purge action: %+v", a)
			}
		}
	}
	if purgedCount != 1 {
		t.Fatalf("purgedCount = %d, want 1 (%+v)", purgedCount, purgeRep.Actions)
	}
	afterPurge, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterPurge) != userText {
		t.Fatalf("canonical after purge mismatch:\ngot:  %q\nwant: %q", string(afterPurge), userText)
	}

	// Re-configure -> re-purge leaves zero fragments and exact user text.
	if _, err := ConfigureAgents(opts); err != nil {
		t.Fatalf("second ConfigureAgents: %v", err)
	}
	if _, err := PurgeAgents(opts); err != nil {
		t.Fatalf("second PurgeAgents: %v", err)
	}
	finalRaw, _ := os.ReadFile(canonical)
	if string(finalRaw) != userText {
		t.Fatalf("canonical after second round-trip mismatch:\ngot:  %q\nwant: %q", string(finalRaw), userText)
	}

	// Missing files are skipped cleanly with StatusUnchanged.
	emptyHome := t.TempDir()
	emptyActs, err := PurgeAgents(Options{Home: emptyHome, Now: fixedClock()})
	if err != nil {
		t.Fatalf("PurgeAgents empty home: %v", err)
	}
	for _, a := range emptyActs {
		if a.Status != StatusUnchanged {
			t.Errorf("empty home %s = %+v, want unchanged", a.Target, a)
		}
	}

	// Malformed markers fail cleanly without modifying the file.
	badHome := t.TempDir()
	badFile := filepath.Join(badHome, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(badFile), 0o755); err != nil {
		t.Fatal(err)
	}
	badContent := "# Notes\n" + OFAMBeginMarker + "\nunclosed\n"
	if err := os.WriteFile(badFile, []byte(badContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PurgeAgents(Options{Home: badHome, Now: fixedClock()}); err == nil {
		t.Fatal("expected error for malformed markers during purge, got nil")
	}
	if afterBad, _ := os.ReadFile(badFile); string(afterBad) != badContent {
		t.Fatalf("malformed file was modified during purge")
	}
}
