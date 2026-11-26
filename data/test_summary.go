package data

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// TestOutcome is one completed test or a package failure with no failing test.
type TestOutcome struct {
	Package    string
	Name       string
	Passed     bool
	Skipped    bool
	ElapsedSec float64
	Output     string
}

// TestSymbolRun groups adjacent symbols with the same semantic tone.
type TestSymbolRun struct {
	Text string
	Tone string
}

type CompactTestReport struct {
	Strip          string
	Runs           []TestSymbolRun
	Passed         int
	Failed         int
	Skipped        int
	FailureDetails []TestOutcome
}

// FormatCompactTestSummary preserves outcome order in the strip and retains
// detailed output for failures.
func FormatCompactTestSummary(outcomes []TestOutcome, useAsterisk bool) CompactTestReport {
	var report CompactTestReport
	var strip strings.Builder
	passSymbol := '.'
	if useAsterisk {
		passSymbol = '*'
	}
	var tone string
	var runStart int
	for _, outcome := range outcomes {
		var symbol rune
		var nextTone string
		switch {
		case outcome.Skipped:
			report.Skipped++
			symbol, nextTone = 's', "muted"
		case outcome.Passed:
			report.Passed++
			symbol, nextTone = passSymbol, "success"
		default:
			report.Failed++
			report.FailureDetails = append(report.FailureDetails, outcome)
			symbol, nextTone = '✗', "error"
		}
		if nextTone != tone {
			if tone != "" {
				report.Runs = append(report.Runs, TestSymbolRun{Text: strip.String()[runStart:], Tone: tone})
			}
			runStart, tone = strip.Len(), nextTone
		}
		strip.WriteRune(symbol)
	}
	report.Strip = strip.String()
	if tone != "" {
		report.Runs = append(report.Runs, TestSymbolRun{Text: report.Strip[runStart:], Tone: tone})
	}
	return report
}

// ErrNoGoTestEvents identifies text that does not contain Go test JSON events.
var ErrNoGoTestEvents = errors.New("no Go test JSON events found")

type goTestEvent struct {
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	OutputType  string
	ImportPath  string
	FailedBuild string
}

type goTestPackage struct {
	output     map[string]*strings.Builder
	testFailed bool
}

func appendTestOutput(logs map[string]*strings.Builder, key, text string) {
	if text == "" {
		return
	}
	if logs[key] == nil {
		logs[key] = new(strings.Builder)
	}
	logs[key].WriteString(text)
}

func testOutput(logs map[string]*strings.Builder, key string) string {
	if log := logs[key]; log != nil {
		return log.String()
	}
	return ""
}

// ParseGoTestJSON reads newline-delimited output from go test -json. It emits
// named tests and subtests in completion order. Package-level build or test
// failures are included when no failing test represents them. Ordinary stderr
// lines may be interspersed with events when the streams were captured together.
func ParseGoTestJSON(input io.Reader) ([]TestOutcome, error) {
	reader := bufio.NewReader(input)
	packages := make(map[string]*goTestPackage)
	buildLogs := make(map[string]*strings.Builder)
	var outcomes []TestOutcome
	var sawTests bool
	var stderrPackage string
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var event goTestEvent
			if json.Unmarshal(line, &event) == nil {
				if event.Action == "build-output" && event.ImportPath != "" {
					appendTestOutput(buildLogs, event.ImportPath, event.Output)
				}
				if event.isTestEvent() {
					sawTests = true
					if outcome, ok := event.apply(packages, buildLogs); ok {
						outcomes = append(outcomes, outcome)
					}
				}
			} else {
				// Older Go versions and gotestjsonbuildtext put build diagnostics
				// on stderr under a "# package" heading.
				text := string(line)
				if pkg, ok := strings.CutPrefix(text, "# "); ok {
					stderrPackage = strings.TrimSpace(pkg)
				}
				if stderrPackage != "" {
					appendTestOutput(buildLogs, stderrPackage, text)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if !sawTests {
		return nil, ErrNoGoTestEvents
	}
	return outcomes, nil
}

func (e goTestEvent) isTestEvent() bool {
	if e.Package == "" && e.Test == "" {
		return false
	}
	switch e.Action {
	case "start", "run", "pause", "cont", "output", "pass", "fail", "skip", "bench":
		return true
	default:
		return false
	}
}

func (e goTestEvent) apply(packages map[string]*goTestPackage, buildLogs map[string]*strings.Builder) (TestOutcome, bool) {
	pkg := packages[e.Package]
	if pkg == nil || e.Action == "start" {
		pkg = &goTestPackage{output: make(map[string]*strings.Builder)}
		packages[e.Package] = pkg
	}
	switch e.Action {
	case "output":
		if e.OutputType != "frame" {
			appendTestOutput(pkg.output, e.Test, e.Output)
		}
	case "pass", "fail", "skip":
		outcome := TestOutcome{Package: e.Package, Name: e.Test, Passed: e.Action == "pass", Skipped: e.Action == "skip", ElapsedSec: e.Elapsed}
		outcome.Output = testOutput(pkg.output, e.Test)
		if e.Test == "" {
			delete(packages, e.Package)
			if e.Action != "fail" || pkg.testFailed {
				return TestOutcome{}, false
			}
			buildLog := testOutput(buildLogs, e.FailedBuild)
			if buildLog == "" {
				buildLog = testOutput(buildLogs, e.Package)
			}
			outcome.Output = buildLog + outcome.Output
			return outcome, true
		}
		delete(pkg.output, e.Test)
		pkg.testFailed = pkg.testFailed || e.Action == "fail"
		return outcome, true
	case "bench":
		delete(pkg.output, e.Test)
	}
	return TestOutcome{}, false
}
