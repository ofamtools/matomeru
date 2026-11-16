package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/ochazuke"
)

// TestOverlayWritesStayInTheConfigDir pins where a write the overlay keeps
// (ochazuke v0.146.3: a store with no write of its own, here a setting of
// the user's own made with ochazuke.setting create) lands: overlay.json in
// mtm's config directory, and nowhere in the database.
func TestOverlayWritesStayInTheConfigDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data, config := filepath.Join(dir, "data", "mtm.db"), filepath.Join(dir, "config")
	if err := os.MkdirAll(filepath.Dir(data), 0o700); err != nil {
		t.Fatal(err)
	}
	seed, err := os.ReadFile(seeded(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	a := openWith(t, Options{Data: data, Config: config})
	const marker = "overlay-probe.marker"
	mustCLI(t, a, "ochazuke.setting", "create", "--name", marker)
	kept, err := os.ReadFile(filepath.Join(config, ochazuke.OverlayFile))
	if err != nil || !bytes.Contains(kept, []byte(marker)) {
		t.Fatalf("%s in the config dir: %q, %v", ochazuke.OverlayFile, kept, err)
	}
	for _, path := range []string{data, data + "-wal"} {
		raw, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatalf("the overlay write reached %s", path)
		}
	}
}
