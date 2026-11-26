package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

func TestFilesBaseDirectoryWorkflow(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "alpha")
	later := filepath.Join(base, "nested", "group", "beta")
	for _, dir := range []string{repo, later} {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	a, err := New(store, Options{FS: pathpicker.OSFS{}, Now: func() time.Time { return now }, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.userSettings[SetFilesScanDepth] = 2
	a.reloadSettings()
	d := NewDriver(a, 120, 36)
	topTabs := a.topTabs()

	d.Keys("g", "d")
	if a.Space() != SpaceFiles || a.Focus() != views.FilesListID {
		t.Fatalf("g D opened %q, focus %q", a.Space(), a.Focus())
	}
	if !slices.Equal(a.topTabs(), topTabs) {
		t.Fatal("opening Files changed the numbered top tabs")
	}
	if frame := d.Frame(); !strings.Contains(frame, "Files & Directories") || !strings.Contains(frame, "Base Dirs (0)") {
		t.Fatalf("Files page did not render its header:\n%s", frame)
	}

	d.Keys("tab", "a")
	pm := a.activePathPicker()
	if pm == nil || pm.Model().Mode != pathpicker.PickDir {
		t.Fatal("adding a base directory did not open the directory picker")
	}
	if err := pm.Model().SetDir(base); err != nil {
		t.Fatal(err)
	}
	d.Keys("k", "enter")
	dirs := a.files.baseStore.List()
	if len(dirs) != 1 || dirs[0].Path != base || dirs[0].MaxDepth != 2 || dirs[0].RepoCount != 1 {
		t.Fatalf("added base directories = %+v", dirs)
	}
	if entries := a.files.frecency.List(data.PathSortRecent, now); len(entries) != 1 || !entries[0].IsDir || entries[0].Path != base {
		t.Fatalf("path picker selection was not recorded: %+v", entries)
	}

	d.Keys("]")
	if got := filesPageFact(t, a, "selectedPath"); got != repo {
		t.Fatalf("selected repository = %v, want %s", got, repo)
	}
	d.Keys("y")
	if a.Register(core.RegClipboard) != repo {
		t.Fatalf("clipboard = %q, want %s", a.Register(core.RegClipboard), repo)
	}

	a.cfg.userSettings[SetFilesScanDepth] = 3
	a.reloadSettings()
	d.Keys("r", "j")
	if got := filesPageFact(t, a, "selectedPath"); got != later {
		t.Fatalf("selected repository after rescan and j = %v, want %s", got, later)
	}
	d.Keys("k")
	if got := filesPageFact(t, a, "selectedPath"); got != repo {
		t.Fatalf("selected repository after k = %v, want %s", got, repo)
	}
	d.Keys("/")
	d.Type("beta")
	d.Keys("enter", "y")
	if a.Register(core.RegClipboard) != later {
		t.Fatalf("yank after filtering = %q, want %s", a.Register(core.RegClipboard), later)
	}

	d.Keys("P")
	if a.Status() != "created 2 projects" {
		t.Fatalf("first import: %q", a.Status())
	}
	projects, err := store.Query(data.Query{Kind: data.KindTask})
	if err != nil || len(projects.Items) != 2 {
		t.Fatalf("project import = %+v, err = %v", projects.Items, err)
	}
	d.Keys("P")
	if a.Status() != "created 0 projects" {
		t.Fatalf("second import: %q", a.Status())
	}
	d.Keys("d")
	if len(a.files.baseStore.List()) != 1 {
		t.Fatal("d removed a base directory while Git Repos was active")
	}
	d.Keys("/", "ctrl+x", "enter", "[", "d")
	if dirs := a.files.baseStore.List(); len(dirs) != 0 || len(a.files.baseStore.Repos()) != 0 {
		t.Fatalf("remove left base directories or repositories: %+v, %+v", dirs, a.files.baseStore.Repos())
	}
}

func TestFrecencyPickerSortAndCopy(t *testing.T) {
	a, d := demo(t)
	d.Keys("g", "o")
	pm := a.activePathPicker()
	if pm == nil {
		t.Fatal("g o failed to open normal path picker")
	}
	if err := pm.Model().SetDir("/home/owner"); err != nil {
		t.Fatal(err)
	}
	d.Keys("enter")
	chosen := "/home/owner/proj"
	now := a.now()
	got := a.files.frecency.List(data.PathSortRecent, now)
	if len(got) != 1 || got[0].Path != chosen || got[0].Count != 1 {
		t.Fatalf("normal path picker usage = %+v", got)
	}
	frequent := "/alpha"
	for i := 0; i < 2; i++ {
		if err := a.files.frecency.Record(frequent, false, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	d.Keys("g", "/")
	fm := a.activePicker(frecencyPickerID)
	if fm == nil || a.Mode() != ModePicker {
		t.Fatalf("g F modal = %q, mode = %q", a.Modal(), a.Mode())
	}
	for i, step := range []struct{ mode, first string }{
		{"Recent", chosen}, {"Frequent", frequent}, {"Frecency", frequent}, {"Name", frequent}, {"Recent", chosen},
	} {
		if i > 0 {
			d.Keys("s")
		}
		items := fm.Model().Filtered()
		if len(items) != 2 || items[0].ID != step.first || !strings.Contains(d.Frame(), "Sort: "+step.mode) {
			t.Fatalf("%s picker = %+v:\n%s", step.mode, items, d.Frame())
		}
	}
	d.Keys("/", "s")
	if fm.Model().Query != "s" {
		t.Fatalf("typing s in filter mode did not update the query: %q", fm.Model().Query)
	}
	d.Keys("backspace", "esc", "enter")
	if a.Modal() != "" || a.Register(core.RegClipboard) != chosen {
		t.Fatalf("picking history path left modal %q and clipboard %q", a.Modal(), a.Register(core.RegClipboard))
	}

	d.Keys("g", "d")
	a.cfg.userSettings[SetFilesDefaultSort] = "name"
	a.reloadSettings()
	d.Keys("g", "d")
	if got := filesPageFact(t, a, "sortMode"); got != "name" || len(a.files.list.Rows) == 0 || a.files.list.Rows[0].ID != frequent {
		t.Fatalf("changed default sort = %v, rows = %+v", got, a.files.list.Rows)
	}
	for _, want := range []data.PathSortMode{data.PathSortName, data.PathSortRecent, data.PathSortFrequent, data.PathSortFrecency, data.PathSortName} {
		if got := filesPageFact(t, a, "sortMode"); got != string(want) {
			t.Fatalf("Files page sort = %v, want %q", got, want)
		}
		d.Keys("s")
	}
}

func filesPageFact(t *testing.T, a *App, key string) any {
	t.Helper()
	root := a.View(a.State())
	node := root.Find("files.page")
	if node == nil {
		t.Fatal("Files page missing from rendered view")
	}
	return node.Facts[key]
}
