package contextitems_test

import (
	"testing"

	"ofam.dev/matomeru/contextitems"
)

func TestExtractHighSignalAndExcludeNoise(t *testing.T) {
	input := "" +
		"Opened \x1b]8;;https://docs.example.com/guide\x07API Guide\x1b]8;;\x07 and checked (https://example.com/a_(b)).\n" +
		"Duplicate URL: https://docs.example.com/guide\n" +
		"See PR unixsuperhero/matomeru#42 and edited app/model.go:128:4 and ~/proj/matomeru/README.md\n" +
		"Also check ./cmd/mtm-hook/main.go:12 and /etc/hosts and harnesses.json.\n" +
		"Noise to ignore: commit 888c1d8 and 0123456789abcdef0123456789abcdef01234567 on branch feat/new-ui or origin/main or fix/login-bug v1.2.3 os.Exit e.g.\n"

	items := contextitems.Extract(input, contextitems.Options{Source: contextitems.SourceResponse})

	byVal := map[string]contextitems.Item{}
	for _, it := range items {
		if it.Source != contextitems.SourceResponse {
			t.Errorf("item %q source = %q, want %q", it.Value, it.Source, contextitems.SourceResponse)
		}
		if _, dup := byVal[it.Value]; dup {
			t.Errorf("duplicate item extracted: %q", it.Value)
		}
		byVal[it.Value] = it
	}

	// Expected high-signal items
	if it, ok := byVal["https://docs.example.com/guide"]; !ok || it.Kind != contextitems.KindOSC8 || it.Label != "API Guide" {
		t.Errorf("expected OSC-8 hyperlink with label, got %+v", byVal["https://docs.example.com/guide"])
	}
	if it, ok := byVal["https://example.com/a_(b)"]; !ok || it.Kind != contextitems.KindURL {
		t.Errorf("expected balanced-paren URL, got %+v", byVal["https://example.com/a_(b)"])
	}
	if it, ok := byVal["unixsuperhero/matomeru#42"]; !ok || it.Kind != contextitems.KindPR {
		t.Errorf("expected PR ref, got %+v", byVal["unixsuperhero/matomeru#42"])
	}
	if it, ok := byVal["app/model.go:128:4"]; !ok || it.Kind != contextitems.KindFile || it.Path != "app/model.go" || it.Line != 128 || it.Col != 4 {
		t.Errorf("expected file with line/col, got %+v", byVal["app/model.go:128:4"])
	}
	for _, wantFile := range []string{"~/proj/matomeru/README.md", "./cmd/mtm-hook/main.go:12", "/etc/hosts", "harnesses.json"} {
		if it, ok := byVal[wantFile]; !ok || it.Kind != contextitems.KindFile {
			t.Errorf("expected file %q, got %+v (all=%+v)", wantFile, it, items)
		}
	}

	// Excluded noisy items
	for _, noise := range []string{
		"888c1d8",
		"0123456789abcdef0123456789abcdef01234567",
		"feat/new-ui",
		"origin/main",
		"fix/login-bug",
		"v1.2.3",
		"os.Exit",
		"e.g.",
	} {
		if _, ok := byVal[noise]; ok {
			t.Errorf("noisy token %q should NOT have been extracted", noise)
		}
	}
}

func TestSoftWrapJoining(t *testing.T) {
	lines := []string{
		"Visit https://github.com/unixsuperhero/matomeru/pull/",
		"123/files for the diff",
		"And /Users/unixsuperhero/proj/matomeru/",
		"docs/V2-DAY1-ROADMAP.md",
		"go test ./...",
		"git status",
	}

	items := contextitems.ExtractLines(lines, contextitems.Options{Source: contextitems.SourceScreen})
	vals := map[string]string{}
	for _, it := range items {
		vals[it.Value] = it.Kind
	}

	if vals["https://github.com/unixsuperhero/matomeru/pull/123/files"] != contextitems.KindURL {
		t.Errorf("expected wrapped URL to be joined, got %+v", items)
	}
	if vals["/Users/unixsuperhero/proj/matomeru/docs/V2-DAY1-ROADMAP.md"] != contextitems.KindFile {
		t.Errorf("expected wrapped path to be joined, got %+v", items)
	}
}

func TestExtractVisibleMatchesCoordinates(t *testing.T) {
	// Row 0 has ANSI SGR colors before a URL and a PR ref.
	// Visual text: "See https://example.com and org/repo#9"
	//               01234                  23  28         38
	row0 := "\x1b[32mSee\x1b[0m \x1b[4mhttps://example.com\x1b[0m and org/repo#9"

	// Row 1 has an OSC-8 hyperlink using ST terminator (\x1b\\) and a file path.
	// Visual text: "Open Spec at app/view.go:10"
	//               01234567890123
	row1 := "Open \x1b]8;;https://spec.dev\x1b\\Spec\x1b]8;;\x1b\\ at app/view.go:10"

	matches := contextitems.ExtractVisibleMatches([]string{row0, row1})
	if len(matches) != 4 {
		t.Fatalf("got %d matches, want 4: %+v", len(matches), matches)
	}

	// Row 0, match 0: https://example.com at cols [4, 23)
	m0 := matches[0]
	if m0.Row != 0 || m0.StartCol != 4 || m0.EndCol != 23 || m0.Kind != contextitems.KindURL || m0.Value != "https://example.com" {
		t.Errorf("match[0] = %+v", m0)
	}
	// Row 0, match 1: org/repo#9 at cols [28, 38)
	m1 := matches[1]
	if m1.Row != 0 || m1.StartCol != 28 || m1.EndCol != 38 || m1.Kind != contextitems.KindPR || m1.Value != "org/repo#9" {
		t.Errorf("match[1] = %+v", m1)
	}
	// Row 1, match 2: OSC-8 "Spec" at cols [5, 9)
	m2 := matches[2]
	if m2.Row != 1 || m2.StartCol != 5 || m2.EndCol != 9 || m2.Kind != contextitems.KindOSC8 || m2.Value != "https://spec.dev" || m2.Label != "Spec" {
		t.Errorf("match[2] = %+v", m2)
	}
	// Row 1, match 3: app/view.go:10 at cols [13, 27)
	m3 := matches[3]
	if m3.Row != 1 || m3.StartCol != 13 || m3.EndCol != 27 || m3.Kind != contextitems.KindFile || m3.Value != "app/view.go:10" {
		t.Errorf("match[3] = %+v", m3)
	}
}

func TestExtractAtPrefixedAttachmentPathsAndIgnoreEmails(t *testing.T) {
	input := "" +
		"@/Users/alice/.local/share/matomeru/attachments/20261126-073000-a1b2c3.png\n" +
		"Inspect @~/.local/share/matomeru/attachments/shot.png and (@/tmp/diagram.webp).\n" +
		"Contact user@example.com or admin@example.org or dev@team.md for help.\n"

	items := contextitems.Extract(input, contextitems.Options{Source: contextitems.SourcePrompt})
	byVal := map[string]contextitems.Item{}
	for _, it := range items {
		byVal[it.Value] = it
	}

	for _, want := range []string{
		"/Users/alice/.local/share/matomeru/attachments/20261126-073000-a1b2c3.png",
		"~/.local/share/matomeru/attachments/shot.png",
		"/tmp/diagram.webp",
	} {
		if it, ok := byVal[want]; !ok || it.Kind != contextitems.KindFile {
			t.Errorf("expected @-prefixed file %q to be extracted, got %+v (all=%+v)", want, it, items)
		}
	}

	for _, emailDomain := range []string{"example.com", "example.org", "team.md"} {
		if _, ok := byVal[emailDomain]; ok {
			t.Errorf("email domain %q should NOT have been extracted (all=%+v)", emailDomain, items)
		}
	}

	matches := contextitems.ExtractVisibleMatches([]string{
		"See @/tmp/screen.png and user@example.org",
	})
	if len(matches) != 1 || matches[0].Kind != contextitems.KindFile || matches[0].Value != "/tmp/screen.png" {
		t.Fatalf("ExtractVisibleMatches = %+v, want single /tmp/screen.png match", matches)
	}
}
