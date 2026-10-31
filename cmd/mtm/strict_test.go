package main

import (
	"testing"

	"ofam.dev/ochazuke/strictcheck"
)

// No code in the module, tests included, uses a deprecated ochazuke name:
// the module vets clean with the ochazuke_strict tag, which leaves them out.
func TestNoDeprecatedOchazuke(t *testing.T) {
	t.Parallel()
	strictcheck.Check(t)
}
