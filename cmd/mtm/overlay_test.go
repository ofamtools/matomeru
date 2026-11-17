package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// TestOverlayStoreInMtm pins where a write the overlay keeps (a store
// with no write of its own, here a setting of the user's own made with
// ochazuke.setting create) lands, for each value of overlay.store: mtm-db
// (mtm's default, run plan 5.4a) a row of the changes table and no
// overlay.json; app overlay.json in mtm's config directory and nothing in
// the database.
func TestOverlayStoreInMtm(t *testing.T) {
	t.Parallel()
	for _, value := range []string{work.ChangesBackendName, ochazuke.OverlayStoreApp} {
		dir := t.TempDir()
		data, config := filepath.Join(dir, "data", "mtm.db"), filepath.Join(dir, "config")
		for _, d := range []string{filepath.Dir(data), config} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		seed, err := os.ReadFile(seeded(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(data, seed, 0o600); err != nil {
			t.Fatal(err)
		}
		if value != work.ChangesBackendName {
			if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"overlay.store": "`+value+`"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		a := openWith(t, Options{Data: data, Config: config})
		const marker = "overlay-probe.marker"
		mustCLI(t, a, "ochazuke.setting", "create", "--name", marker)
		kept, _ := os.ReadFile(filepath.Join(config, ochazuke.OverlayFile))
		inDB := a.r.OverlayRow("ochazuke.setting", marker) != nil
		if value == work.ChangesBackendName {
			rows, err := a.work.ChangesBackend().Rows()
			inDB = err == nil && rows["ochazuke.setting"][marker] != nil
		}
		if inFile := bytes.Contains(kept, []byte(marker)); inFile != (value == ochazuke.OverlayStoreApp) || inDB != true {
			t.Fatalf("%s: in %s %v (%q), held %v", value, ochazuke.OverlayFile, inFile, kept, inDB)
		}
	}
}
