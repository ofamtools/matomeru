package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

func directoryTestOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(home, "proj"), 0700); err != nil {
		t.Fatal(err)
	}
	return Options{Data: filepath.Join(root, "mtm.db"), Config: filepath.Join(root, "config"), Socket: filepath.Join(root, "dead.sock"), lookupEnv: func(key string) (string, bool) {
		value, ok := map[string]string{"HOME": home, "MTM_O_PROJ": "none"}[key]
		return value, ok
	}}
}

func TestDirectoryFreshListDoesNotProbeHome(t *testing.T) {
	t.Parallel()
	opts := directoryTestOptions(t)
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	bases, err := a.projectBases()
	if err != nil || len(bases) != 0 {
		t.Fatalf("fresh bases %v: %v", bases, err)
	}
	loaded, err := a.scanKnownDirectories(context.Background(), a.directoryScanOptions())
	if err != nil || len(loaded.Rows) != 0 || len(loaded.Unavailable) != 0 {
		t.Fatalf("fresh scan %+v: %v", loaded, err)
	}
	openDirectories(t, a)
	frame := sizedFrame(a, 140, 30)
	if a.directoryList.Len() != 0 || !strings.Contains(frame, "adds a directory or base") || !strings.Contains(frame, "n") {
		t.Fatalf("empty page:\n%s", frame)
	}
	// Opting in probes the same temp-home path the migration uses.
	if err := a.r.Settings().Set(settingDirectoryProbe, true); err != nil {
		t.Fatal(err)
	}
	loaded, err = a.scanKnownDirectories(context.Background(), a.directoryScanOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Unavailable[filepath.Join(opts.getenv("HOME"), "proj")]; ok {
		t.Fatalf("existing base unavailable: %+v", loaded)
	}
}

func TestDirectoryLegacyProbeImportsOnce(t *testing.T) {
	t.Parallel()
	opts := directoryTestOptions(t)
	db, err := store.Open(opts.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work.Open(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	loaded, err := a.scanKnownDirectories(context.Background(), a.directoryScanOptions())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(opts.getenv("HOME"), "proj")
	bases, err := a.projectBases()
	if err != nil || len(bases) != 1 || bases[0].Path != want || bases[0].Source != "migrated" || !strings.Contains(loaded.Notice, "Imported legacy directory bases once") {
		t.Fatalf("migration %v notice=%q err=%v", bases, loaded.Notice, err)
	}
	if _, err := a.changeBase(want, 0, false); err != nil {
		t.Fatal(err)
	}
	loaded, err = a.scanKnownDirectories(context.Background(), a.directoryScanOptions())
	if err != nil {
		t.Fatal(err)
	}
	bases, err = a.projectBases()
	if err != nil || len(bases) != 0 || loaded.Notice != "" {
		t.Fatalf("reseeded %v notice=%q err=%v", bases, loaded.Notice, err)
	}
}

func TestDirectoryNewKeyPicksKindAndUnconfinedPath(t *testing.T) {
	t.Parallel()
	for _, style := range []string{"list", "hybrid", "columns"} {
		t.Run(style, func(t *testing.T) {
			t.Parallel()
			a := open(t)
			if err := a.r.Settings().Set("pickers.directory.style", style); err != nil {
				t.Fatal(err)
			}
			openDirectories(t, a)
			press(t, a, "n")
			if a.r.TopModal() != "add-kind" {
				t.Fatalf("new modal %q", a.r.TopModal())
			}
			press(t, a, "enter")
			c := a.r.Chooser(dirChooser)
			if c == nil {
				t.Fatalf("directory picker missing: %q", a.r.TopModal())
			}
			toSource(t, a, c, ochazuke.PathSourceTyped)
			if a.r.CurrentMode().Name == "normal" {
				press(t, a, "/")
			}
			path := filepath.Join(t.TempDir(), "not-created")
			typeText(t, a, path[:len(path)-1])
			pressSettle(t, a, path[len(path)-1:])
			settleChooser(a, c)
			pressSettle(t, a, "alt+enter")
			var record directoryRecord
			err := a.db.Get("directories", path, &record)
			if err != nil || record.Path != path || record.Depth != 0 {
				rows, _ := a.userDirectories()
				t.Fatalf("saved %+v err=%v rows=%+v modal=%q\\n%s", record, err, rows, a.r.TopModal(), sizedFrame(a, 140, 30))
			}
			selected, _ := a.directoryList.Selected()
			if selected.ID != "directory:"+path {
				t.Fatalf("selected %q modal=%q frame=\\n%s", selected.ID, a.r.TopModal(), sizedFrame(a, 140, 30))
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("add changed disk: %v", err)
			}
		})
	}
}

func TestDirectoryStoreEditsArchiveAndOverrideReachSources(t *testing.T) {
	t.Parallel()
	a := open(t)
	path := filepath.Join(t.TempDir(), "base")
	executeA(t, a, "directory", "create", "work", ochazuke.Arguments{"path": path, "depth": 2, "label": "Repositories", "description": "Local source"})
	bases, err := a.projectBases()
	if err != nil || len(bases) != 1 || bases[0].Path != path || bases[0].Depth != 2 {
		t.Fatalf("bases %v: %v", bases, err)
	}
	walk := a.walkRoots(ochazuke.PathPick{Name: "directory", Parameter: dirParam("", true)})
	if !slices.ContainsFunc(walk.Roots, func(root ochazuke.PathRoot) bool { return root.Path == path }) {
		t.Fatalf("walk roots %v", walk.Roots)
	}
	executeA(t, a, "directory", "archive", "work", ochazuke.Arguments{"path": path})
	bases, err = a.projectBases()
	if err != nil || len(bases) != 0 {
		t.Fatalf("archived scan base %v: %v", bases, err)
	}
	if len(a.knownDirectoryChoices()) != 0 {
		t.Fatal("archive remained in picker")
	}
	executeA(t, a, "directory", "restore", "work", ochazuke.Arguments{"path": path})
	testEnv(a, "MTM_PROJECT_BASE_DIRS", "/override")
	bases, err = a.projectBases()
	if err != nil || len(bases) != 1 || bases[0].Path != "/override" || bases[0].Source != "variable" {
		t.Fatalf("override %v: %v", bases, err)
	}
	testUnsetEnv(a, "MTM_PROJECT_BASE_DIRS")
	thing, err := a.directoryThing(path)
	if err != nil || thing["label"] != "Repositories" || thing["description"] != "Local source" {
		t.Fatalf("metadata %v: %v", thing, err)
	}
}

func TestDirectoryUnavailableWorkersDoNotHoldThePage(t *testing.T) {
	t.Parallel()
	a := open(t)
	path := filepath.Join(t.TempDir(), "slow")
	executeA(t, a, "directory", "create", "work", ochazuke.Arguments{"path": path, "depth": 1})
	release := make(chan struct{})
	defer close(release)
	for range cap(a.directoryIO.slots) {
		_ = a.directoryIO.run(context.Background(), time.Millisecond, func() { <-release })
	}
	loaded, err := a.scanKnownDirectories(context.Background(), a.directoryScanOptions())
	if err != nil || loaded.Unavailable[path] != "filesystem workers busy" {
		t.Fatalf("unavailable %+v: %v", loaded, err)
	}
	a.directories.Unavailable = loaded.Unavailable
	a.refreshDirectoryRows()
	if !strings.Contains(strings.Join(a.directoryList.Items()[0].Cells, " "), "unavailable") {
		t.Fatalf("row %v", a.directoryList.Items())
	}
	if err := a.r.Settings().Set(settingDirectoryStatus, false); err != nil {
		t.Fatal(err)
	}
	a.refreshDirectoryRows()
	if strings.Contains(strings.Join(a.directoryList.Items()[0].Cells, " "), "unavailable") {
		t.Fatal("status opt-out ignored")
	}
}
