package main

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/notices"
)

// license.all lists every module of this build, daemon only when built in,
// and license.show prints tuios's MIT licence and its author when the daemon
// is in.
func TestLicenseVerbs(t *testing.T) {
	t.Parallel()
	a := open(t)
	var rows []licenseRow
	if err := json.Unmarshal([]byte(mustCLI(t, a, "license", "all")), &rows); err != nil {
		t.Fatal(err)
	}
	ix, err := notices.Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := len(ix.Linked(daemonBuilt)); len(rows) != want {
		t.Fatalf("license.all has %d rows, the build links %d modules", len(rows), want)
	}
	have := map[string]licenseRow{}
	for _, r := range rows {
		if len(r.Licenses) == 0 || len(r.Files) == 0 {
			t.Errorf("%s has no licence text: %+v", r.Module, r)
		}
		have[r.Module] = r
	}
	for _, m := range []string{"ofam.dev/matomeru", "ofam.dev/ochazuke", "Go standard library", "charm.land/bubbletea/v2"} {
		if _, ok := have[m]; !ok {
			t.Errorf("license.all lacks %s", m)
		}
	}
	_, hasDaemon := have["ofam.dev/mtmd"]
	if hasDaemon != daemonBuilt {
		t.Errorf("ofam.dev/mtmd listed = %v, daemonBuilt = %v", hasDaemon, daemonBuilt)
	}
	if daemonBuilt {
		var text string
		if err := json.Unmarshal([]byte(mustCLI(t, a, "license", "show", "ofam.dev/mtmd")), &text); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Copyright (c) 2025 Gaurav Gosain") || !strings.Contains(text, "Arimo") {
			t.Errorf("license.show ofam.dev/mtmd lacks tuios's notice:\n%.400s", text)
		}
	}
	if _, errOut, err := cli(a, "", "license", "show", "nonesuch.example/x"); err == nil {
		t.Errorf("a module that is not linked should fail (stderr %q)", errOut)
	}
}
