//go:build monogatari

package main

import (
	"testing"

	"github.com/unixsuperhero/monogatari/host"
)

// mtm's stories (cmd/mtm/stories) through monogatari's host: each story's
// fixture (seeded, empty) builds the real App on a temp database with the
// story fakes (storyfakes.go). A story with a skip ("gap: ...") runs but
// cannot fail. Stories run in parallel: a session shares nothing with
// another (the race detector checks it under -race).
func TestStories(t *testing.T) {
	t.Parallel()
	cfg := storyConfig()
	cfg.Dir, cfg.Update = "stories", *updateGolden // -update (turn_wrap_test.go) rewrites these goldens too
	host.Test(t, cfg)
}
