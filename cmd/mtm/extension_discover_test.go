package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// toolCatalog is a valid catalog of a tool named name at version.
func toolCatalog(name, version string) string {
	return strings.NewReplacer("o-fake", "o-"+name, "VERSION", version).Replace(fakeCatalog)
}

// fakeTool writes an executable o-NAME into dir that answers catalog with
// the text in dir/NAME.catalog (any other words fail) and logs each call to
// dir/NAME.log. It returns the bin.
func fakeTool(t *testing.T, dir, name, catalog string) string {
	t.Helper()
	bin := filepath.Join(dir, "o-"+name)
	answer, log := filepath.Join(dir, name+".catalog"), filepath.Join(dir, name+".log")
	if err := os.WriteFile(answer, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
echo "$*" >> '` + log + `'
case "$1" in
catalog) cat '` + answer + `' ;;
*) echo "unknown $*" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// toolCalls is a fake tool's log, a call per line.
func toolCalls(t *testing.T, dir, name string) []string {
	t.Helper()
	return calls(t, filepath.Join(dir, name+".log"))
}

// discovered is extension available on the CLI, decoded.
func discovered(t *testing.T, a *App) map[string]extCandidate {
	t.Helper()
	var rows []extCandidate
	if err := json.Unmarshal([]byte(mustCLI(t, a, "extension", "available")), &rows); err != nil {
		t.Fatal(err)
	}
	out := map[string]extCandidate{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

// Discovery lists every o-NAME on PATH that answers catalog (the first on
// PATH for each name), merged with the registry: available, registered,
// stale after the tool's catalog changed (with what changed), missing
// after its bin is gone. A tool answering garbage, a host (o-mtm, o-tui)
// and a later o-NAME of a name already found are not listed, and nothing
// but catalog ever runs.
func TestExtensionDiscoveryStatuses(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	fakeTool(t, first, "alpha", toolCatalog("alpha", "1.0"))
	fakeTool(t, second, "alpha", toolCatalog("alpha", "9.9")) // shadowed
	beta := fakeTool(t, first, "beta", toolCatalog("beta", "1.0"))
	gamma := fakeTool(t, first, "gamma", toolCatalog("gamma", "1.0"))
	delta := fakeTool(t, second, "delta", toolCatalog("delta", "1.0"))
	fakeTool(t, first, "junk", "this is not a catalog")
	fakeTool(t, first, "mtm", toolCatalog("mtm", "1.0"))
	fakeTool(t, first, "tui", toolCatalog("tui", "1.0"))
	if err := os.WriteFile(filepath.Join(first, "o-plain"), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := openAt(t, seeded(t))
	a.getenv = func(k string) string {
		if k == "PATH" {
			return first + string(os.PathListSeparator) + second
		}
		return os.Getenv(k)
	}
	for name, bin := range map[string]string{"beta": beta, "gamma": gamma, "delta": delta} {
		mustCLI(t, a, "extension", "register", name, "--bin", bin)
	}
	if err := os.WriteFile(filepath.Join(first, "beta.catalog"), []byte(strings.Replace(toolCatalog("beta", "2.0"),
		`{"name":"done"`, `{"name":"archive","kind":"application"},{"name":"done"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gamma); err != nil {
		t.Fatal(err)
	}
	rows := discovered(t, a)
	want := map[string]string{"alpha": statusAvailable, "beta": statusStale, "gamma": statusMissing, "delta": statusRegistered}
	if len(rows) != len(want) {
		t.Fatalf("rows %+v", rows)
	}
	for name, status := range want {
		if rows[name].Status != status {
			t.Errorf("%s: status %q, want %q", name, rows[name].Status, status)
		}
	}
	if got := rows["alpha"]; got.Bin != filepath.Join(first, "o-alpha") || got.Version != "1.0" || got.Tabs != 1 || got.Nouns != 1 || got.Verbs != 3 {
		t.Errorf("alpha %+v", got)
	}
	if got := strings.Join(rows["beta"].Changes, "; "); got != "version 1.0 → 2.0; + verb item.archive" {
		t.Errorf("beta changes %q", got)
	}
	if rows["delta"].RegisteredAt.IsZero() || rows["gamma"].Bin != gamma {
		t.Errorf("registered rows %+v %+v", rows["delta"], rows["gamma"])
	}
	if got := toolCalls(t, second, "alpha"); got != nil {
		t.Errorf("the shadowed o-alpha ran: %q", got)
	}
	for _, name := range []string{"mtm", "tui"} {
		if got := toolCalls(t, first, name); got != nil {
			t.Errorf("host o-%s ran: %q", name, got)
		}
	}
	for dir, names := range map[string][]string{first: {"alpha", "beta", "junk"}, second: {"delta"}} {
		for _, name := range names {
			for _, call := range toolCalls(t, dir, name) {
				if call != "catalog" {
					t.Errorf("o-%s ran %q", name, call)
				}
			}
		}
	}
	// Refreshed, the stale row is registered again.
	mustCLI(t, a, "extension", "refresh", "beta")
	if got := discovered(t, a)["beta"]; got.Status != statusRegistered || got.Changes != nil {
		t.Errorf("after refresh: %+v", got)
	}
}
