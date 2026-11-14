package main

import (
	"testing"

	"ofam.dev/ochazuke/refusecheck"
)

// mtm adds no refusal: an error template, Refuse, Unavailable, Lacks, a
// guard without a fallback, a Confirm without Loses or an Enum without
// Accept in the repository's non-test Go. The accepted sites are
// testdata/refusecheck.txt, which only shrinks: REFUSECHECK_UPDATE=1 removes
// rows and lowers counts, a row with a reason (rights, loses, failure,
// declaration, cancel) is a reviewed exception. See ochazuke's
// refusecheck/README.md for what to return instead.
func TestNoNewRefusals(t *testing.T) {
	t.Parallel()
	refusecheck.Check(t, "../..", "testdata/refusecheck.txt")
}
