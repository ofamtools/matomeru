package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// project new --dir attaches the directory and makes it the default; a
// missing path is attached too (record first). projects directories reports
// every project's directories resolved, so a link and its target compare
// equal.
func TestProjectNewDirAndProjectsDirectories(t *testing.T) {
	a, _ := cliApp(t)
	base := t.TempDir()
	real := filepath.Join(base, "repo")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var p work.Project
	if err := json.Unmarshal([]byte(mustCLI(t, a, "project", "new", "demo", "--dir", link)), &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo" || len(p.Trees) != 1 || p.DefaultTree != link {
		t.Fatalf("project %+v", p)
	}
	missing := filepath.Join(base, "not-yet")
	mustCLI(t, a, "project", "new", "later", "--dir", missing)
	mustCLI(t, a, "project", "new", "plain")

	var rows []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Data  struct {
			Directories []string `json:"directories"`
			Default     string   `json:"default"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(mustCLI(t, a, "projects", "directories")), &rows); err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, r := range rows {
		got[r.Label] = r.Data.Directories
		if r.Label == "demo" && r.Data.Default != directoryKey(real) {
			t.Fatalf("default %q", r.Data.Default)
		}
	}
	if len(got["demo"]) != 1 || got["demo"][0] != directoryKey(real) || len(got["later"]) != 1 || got["later"][0] != missing || got["plain"] == nil || len(got["plain"]) != 0 {
		t.Fatalf("directories %v", got)
	}
}
