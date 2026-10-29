package main

import (
	"testing"

	"github.com/unixsuperhero/ochazuke/strictcheck"
)

// No code in the module, tests included, uses a deprecated ochazuke name:
// the module vets clean with the ochazuke_strict tag, which leaves them out.
func TestNoDeprecatedOchazuke(t *testing.T) { strictcheck.Check(t) }
