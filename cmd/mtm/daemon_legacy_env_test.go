//go:build !nodaemon

package main

import (
	"testing"

	"ofam.dev/matomeru/internal/daemon"
)

// daemon.legacy-env: off (the default) the daemon exports only MTM_ names to
// children; on it exports the TUIOS_ ones beside them.
func TestDaemonLegacyEnvSettingSetsDropUpstream(t *testing.T) {
	for _, c := range []struct {
		value string
		drop  bool
	}{{"", true}, {"0", true}, {"1", false}} {
		o := daemonOptions(daemon.Layouts[0], daemon.LegacyEnvOn(c.value))
		if o.Names.DropUpstream != c.drop || len(o.Names.Env) < 50 {
			t.Fatalf("%q: DropUpstream %v, %d pairs", c.value, o.Names.DropUpstream, len(o.Names.Env))
		}
	}
}
