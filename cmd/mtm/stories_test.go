//go:build monogatari

package main

import (
	"testing"

	"ofam.dev/monogatari/host"
)

// TestStories runs the ten area walkthroughs through monogatari's host.
// Each area fixture builds the real App on a temp database with story fakes.
// Sessions share no state and run in parallel. The default story target runs
// this test once without race; the untagged release gate owns the race pass.
// Extend an area walkthrough or add an ordinary test instead of a skipped gap.
func TestStories(t *testing.T) {
	t.Parallel()
	cfg := storyConfig()
	cfg.Dir, cfg.Update = "stories", *updateGolden
	host.Test(t, cfg)
}
