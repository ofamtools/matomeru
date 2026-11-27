package matomeru_deps_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type hygieneReport struct {
	Repo      string           `json:"repo"`
	Commit    string           `json:"commit"`
	Generated string           `json:"generated"`
	Summary   map[string]int   `json:"summary"`
	Findings  []hygieneFinding `json:"findings"`
}

func currentGitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "unknown"
	}
	return s
}

// TestHygieneReport writes a structured JSON baseline report of all 7 reuse and
// special-case AST scanners when HYGIENE_REPORT is set to an output path.
// It is a silent no-op during normal go test runs.
func TestHygieneReport(t *testing.T) {
	outPath := strings.TrimSpace(os.Getenv("HYGIENE_REPORT"))
	if outPath == "" {
		return
	}

	findings := make([]hygieneFinding, 0)
	findings = append(findings, scanIdentityKindModal(t)...)
	findings = append(findings, scanActivePageDispatch(t)...)
	findings = append(findings, scanFocusOrRegionIDDispatch(t)...)
	findings = append(findings, scanParallelFilterReimpl(t)...)
	findings = append(findings, scanRawNounPrefixFormatting(t)...)
	findings = append(findings, scanParallelModalChrome(t)...)
	findings = append(findings, scanCyclomaticComplexity(t)...)

	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Func != b.Func {
			return a.Func < b.Func
		}
		return a.Detail < b.Detail
	})

	summary := map[string]int{
		ruleIdentityKindModal:   0,
		ruleIdentityActivePage:  0,
		ruleIdentityFocusRegion: 0,
		ruleReuseCustomFilter:   0,
		ruleReuseRawNounFormat:  0,
		ruleReuseModalChrome:    0,
		ruleComplexityCyclo:     0,
	}
	for _, f := range findings {
		summary[f.Rule]++
	}

	report := hygieneReport{
		Repo:      "matomeru",
		Commit:    currentGitCommit(),
		Generated: time.Now().UTC().Format(time.RFC3339),
		Summary:   summary,
		Findings:  findings,
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal hygiene report: %v", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", outPath, err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", outPath, err)
	}
}
