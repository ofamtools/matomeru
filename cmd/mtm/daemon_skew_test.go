package main

import "testing"

// Skew compares what matters: the build's revision, not the version label.
// A daemon built from bcd23d5 before the v0.4.0 tag labels itself
// v0.3.4-13-gbcd23d5; the client labelled v0.4.0 is the same code.
func TestDaemonSkewComparesRevisionNotLabel(t *testing.T) {
	t.Parallel()
	const full = "bcd23d5aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name                                 string
		runner, daemon, client, clientCommit string
		wantCommit                           string
		skew                                 bool
	}{
		{"same commit, describe label", runnerSelf, "v0.3.4-13-gbcd23d5", "v0.4.0", full, "bcd23d5", false},
		{"same commit, stamped label", runnerSelf, "v0.4.0+bcd23d5aaaaa", "v0.4.0", full, "bcd23d5aaaaa", false},
		{"other commit", runnerSelf, "v0.4.0+1111111aaaaa", "v0.4.0", full, "1111111aaaaa", true},
		{"unknown daemon revision falls back to label", runnerSelf, "v0.3.4", "v0.4.0", full, "", true},
		{"unknown daemon revision, same label", runnerSelf, "v0.4.0", "v0.4.0", full, "", false},
		{"tuios runner keeps label comparison", runnerTuios, "v0.3.4-13-gbcd23d5", "v0.4.0", full, "", true},
	}
	for _, c := range cases {
		commit, skew := daemonSkew(c.runner, c.daemon, c.client, c.clientCommit)
		if skew != c.skew || commit != c.wantCommit {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, commit, skew, c.wantCommit, c.skew)
		}
	}
}

// The label a self daemon announces carries its revision.
func TestDaemonLabelCarriesRevision(t *testing.T) {
	t.Parallel()
	if got := daemonLabelOf("v0.4.0", "bcd23d5aaaaaaaaaaaaa"); got != "v0.4.0+bcd23d5aaaaa" {
		t.Fatalf("label = %q", got)
	}
	if got := daemonLabelOf("v0.4.0", ""); got != "v0.4.0" {
		t.Fatalf("label = %q", got)
	}
}
