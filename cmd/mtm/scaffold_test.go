package main

import (
	"testing"

	"ofam.dev/ochazuke/scaffoldcheck"
)

func TestScaffold(t *testing.T) {
	t.Parallel()
	scaffoldcheck.Check(t, scaffoldcheck.Config{Runtime: open(t).r, Baseline: "testdata/scaffoldcheck.txt"})
}
