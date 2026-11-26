package data

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestFormatCompactTestSummary(t *testing.T) {
	failure := TestOutcome{Package: "example.org/pkg", Name: "TestBroken", Output: "wanted 7"}
	outcomes := []TestOutcome{{Passed: true}, {Passed: true, Output: "pass details"}, {Skipped: true}, failure, {Passed: true}}
	for _, tc := range []struct {
		name, strip, passRun string
		asterisk             bool
	}{
		{name: "dot", strip: "..s✗.", passRun: ".."},
		{name: "asterisk", strip: "**s✗*", passRun: "**", asterisk: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatCompactTestSummary(outcomes, tc.asterisk)
			wantRuns := []TestSymbolRun{{tc.passRun, "success"}, {"s", "muted"}, {"✗", "error"}, {tc.passRun[:1], "success"}}
			if got.Strip != tc.strip || got.Passed != 3 || got.Skipped != 1 || got.Failed != 1 ||
				!slices.Equal(got.Runs, wantRuns) || !slices.Equal(got.FailureDetails, []TestOutcome{failure}) {
				t.Fatalf("report = %+v", got)
			}
		})
	}
}

func TestParseGoTestJSON(t *testing.T) {
	stream := `{"Action":"start","Package":"alpha"}
{"Action":"start","Package":"beta"}
{"Action":"run","Package":"alpha","Test":"TestA"}
{"Action":"run","Package":"alpha","Test":"TestA/sub"}
{"Action":"output","Package":"alpha","Test":"TestA/sub","Output":"=== RUN TestA/sub\n","OutputType":"frame"}
{"Action":"output","Package":"alpha","Test":"TestA/sub","Output":"first line\n"}
{"Action":"pass","Package":"beta","Test":"TestA/sub","Elapsed":0.01}
{"Action":"skip","Package":"beta","Test":"TestSkipped"}
{"Action":"output","Package":"alpha","Test":"TestA/sub","Output":"second line\n","OutputType":"error"}
{"Action":"fail","Package":"alpha","Test":"TestA/sub","Elapsed":0.02}
{"Action":"fail","Package":"alpha","Test":"TestA","Elapsed":0.03}
{"Action":"fail","Package":"alpha","Elapsed":0.08}
{"Action":"pass","Package":"beta","Elapsed":0.04}
{"Action":"skip","Package":"no-tests"}
{"ImportPath":"dep [bad.test]","Action":"build-output","Output":"bad.go:7: undefined: input\n"}
{"ImportPath":"dep [bad.test]","Action":"build-fail"}
{"Action":"start","Package":"bad"}
{"Action":"fail","Package":"bad","FailedBuild":"dep [bad.test]"}
# legacy
legacy_test.go:10: undefined: input
{"Action":"start","Package":"legacy"}
{"Action":"fail","Package":"legacy"}
{"Action":"output","Package":"crashed","Output":"panic: setup failed\n"}
{"Action":"fail","Package":"crashed"}`
	got, err := ParseGoTestJSON(strings.NewReader(stream))
	want := []TestOutcome{
		{Package: "beta", Name: "TestA/sub", Passed: true, ElapsedSec: 0.01},
		{Package: "beta", Name: "TestSkipped", Skipped: true},
		{Package: "alpha", Name: "TestA/sub", ElapsedSec: 0.02, Output: "first line\nsecond line\n"},
		{Package: "alpha", Name: "TestA", ElapsedSec: 0.03},
		{Package: "bad", Output: "bad.go:7: undefined: input\n"},
		{Package: "legacy", Output: "# legacy\nlegacy_test.go:10: undefined: input\n"},
		{Package: "crashed", Output: "panic: setup failed\n"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("outcomes = %+v, error = %v; want %+v", got, err, want)
	}
}

func TestParseGoTestJSONRequiresTestEvents(t *testing.T) {
	stream := "some text\n{\"Action\":\"fail\",\"message\":\"unrelated JSON\"}\n"
	if result, err := ParseGoTestJSON(strings.NewReader(stream)); !errors.Is(err, ErrNoGoTestEvents) || len(result) != 0 {
		t.Fatalf("ordinary output produced %+v, %v", result, err)
	}
}
