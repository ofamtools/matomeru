package main

import (
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestRepoCRUDAndSyncCLI(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "mtm.db")
	c := cli{t: t, db: db}

	// 1. mtm repo create / list
	created := decode[record](t, c.ok("repo", "create", "--title", "my-service", "--field", "defaultBranch=main"))
	if created.Kind != data.KindRepo || created.Fields["name"] != "my-service" {
		t.Fatalf("repo create = %+v", created)
	}

	// 2. Seed a fake git repo on disk and reference it from a task so --harvest discovers it.
	fakeRepo := filepath.Join(t.TempDir(), "harvested-repo")
	if err := os.MkdirAll(filepath.Join(fakeRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeRepo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.ok("task", "create", "--title", "Build thing", "--field", "defaultDir="+fakeRepo)

	// 3. mtm repo sync (disable o-proj so test does not depend on host manifest)
	rep := decode[data.RepoSyncReport](t, c.ok("repo", "sync", "--base-dirs=false", "--o-proj=false", "--harvest=true", "--format", "json"))
	if rep.Created != 1 || len(rep.Repos) != 2 {
		t.Fatalf("repo sync report = %+v", rep)
	}

	// 4. Table output works cleanly
	out := c.ok("repo", "sync", "--base-dirs=false", "--o-proj=false", "--harvest=true", "--format", "table")
	if len(out) == 0 {
		t.Fatal("expected table output from mtm repo sync")
	}
}
