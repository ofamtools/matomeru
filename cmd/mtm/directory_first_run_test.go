package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

func TestDirectoryFirstRunScanPromoteAndRestart(t *testing.T) {
	t.Parallel()
	opts := directoryTestOptions(t)
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	openDirectories(t, a)
	if a.directoryList.Len() != 0 || !strings.Contains(sizedFrame(a, 140, 30), "adds a directory or base") {
		t.Fatalf("first run: %s", sizedFrame(a, 140, 30))
	}
	root := knownRepos(t, a, "alpha", "beta", "gamma")
	testUnsetEnv(a, "MTM_PROJECT_BASE_DIRS")
	press(t, a, "n", "down", "enter")
	c := a.r.Chooser(dirChooser)
	if c == nil {
		t.Fatalf("base picker: %q", a.r.TopModal())
	}
	toSource(t, a, c, ochazuke.PathSourceTyped)
	if a.r.CurrentMode().Name == "normal" {
		press(t, a, "/")
	}
	typeText(t, a, root[:len(root)-1])
	pressSettle(t, a, root[len(root)-1:])
	press(t, a, "alt+enter")
	if a.r.TopModal() != "directory-edit" {
		t.Fatalf("base depth editor: %q", a.r.TopModal())
	}
	pressSettle(t, a, "q")
	if a.r.TopModal() != "" {
		t.Fatalf("base editor did not close: %q", a.r.TopModal())
	}
	_, cmd := a.Update(keyMsg("s"))
	if !a.directories.Loading || !strings.Contains(sizedFrame(a, 160, 32), "scanning") {
		t.Fatalf("scan progress: %s", sizedFrame(a, 160, 32))
	}
	settle(a, cmd)
	paths := map[string]string{}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		path := directoryKey(filepath.Join(root, name))
		paths[name] = path
		item := slices.IndexFunc(a.directoryList.Items(), func(item ochazuke.ListItem) bool { return item.ID == "directory:"+path })
		if item < 0 || !strings.Contains(strings.Join(a.directoryList.Items()[item].Cells, " "), "candidate project") {
			t.Fatalf("missing candidate %s: %+v", name, a.directoryList.Items())
		}
	}
	if projects, err := a.work.Projects(); err != nil || len(projects) != 0 {
		t.Fatalf("default scan created projects: %+v %v", projects, err)
	}
	// Marks, not the unmarked focused row, are the subjects of the same key.
	for _, name := range []string{"alpha", "gamma"} {
		if err := a.directoryList.ToggleMark("directory:" + paths[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.directoryList.Select("directory:" + paths["beta"]); err != nil {
		t.Fatal(err)
	}
	if err := a.directoryList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "m")
	projects, err := a.work.Projects()
	if err != nil || len(projects) != 2 {
		t.Fatalf("marked promotion: %+v %v", projects, err)
	}
	if a.r.TopModal() != "" || a.r.Page != directoriesPage || a.pin != "" {
		t.Fatalf("promotion changed context: modal=%q page=%q pin=%q", a.r.TopModal(), a.r.Page, a.pin)
	}
	for _, p := range projects {
		if p.Name == "beta" || p.DefaultTree != paths[p.Name] || len(p.Trees) != 1 || p.Trees[0].Path != paths[p.Name] || p.Repo != paths[p.Name] {
			t.Fatalf("promoted project lost its default: %+v", p)
		}
	}
	base, err := a.savedDirectory(root)
	if err != nil || base.Depth != 1 {
		t.Fatalf("saved base: %+v %v", base, err)
	}
	base.Label, base.Description = "My repositories", "Saved between runs"
	if err := a.putDirectoryRecord(base); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// The stored list contains candidates even before another scan completes.
	if got, err := a.userDirectories(); err != nil || len(got) != 4 {
		t.Fatalf("restart lost saved directories: %+v %v", got, err)
	}
	if got, err := a.savedDirectory(root); err != nil || got != base {
		t.Fatalf("restart lost base metadata: %+v %v", got, err)
	}
	if got, err := a.work.Projects(); err != nil || len(got) != 2 {
		t.Fatalf("restart changed projects: %+v %v", got, err)
	}
	for _, before := range projects {
		got, err := a.work.Project(before.ID)
		if err != nil || got.Name != before.Name || got.DefaultTree != before.DefaultTree || len(got.Trees) != 1 {
			t.Fatalf("restart lost project: %+v %v", got, err)
		}
	}
	openDirectories(t, a)
	if a.directoryList.Len() != 4 {
		t.Fatalf("restart page: %+v", a.directoryList.Items())
	}
	opts.Data = filepath.Join(t.TempDir(), "fresh.db")
	fresh, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	openDirectories(t, fresh)
	if fresh.directoryList.Len() != 0 || !strings.Contains(sizedFrame(fresh, 140, 30), "adds a directory or base") {
		t.Fatalf("second fresh run inherited directories: %s", sizedFrame(fresh, 140, 30))
	}
}

func TestDirectorySinglePromotionAndAutomaticScanAreIdempotent(t *testing.T) {
	t.Parallel()
	a, err := New(directoryTestOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	root := knownRepos(t, a, "one", "two")
	testUnsetEnv(a, "MTM_PROJECT_BASE_DIRS")
	if _, err := a.changeBase(root, 1, true); err != nil {
		t.Fatal(err)
	}
	openDirectories(t, a)
	one := directoryKey(filepath.Join(root, "one"))
	if err := a.directoryList.Select("directory:" + one); err != nil {
		t.Fatal(err)
	}
	if err := a.directoryList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "m")
	if projects, err := a.work.Projects(); err != nil || len(projects) != 1 || projects[0].DefaultTree != one {
		t.Fatalf("single selection promotion: %+v %v", projects, err)
	}
	if a.r.TopModal() != "" || a.r.Page != directoriesPage || a.pin != "" {
		t.Fatalf("promotion changed context: modal=%q page=%q pin=%q", a.r.TopModal(), a.r.Page, a.pin)
	}
	// A directory inside a repository is not another repository candidate.
	subdir := filepath.Join(root, "one", "nested")
	if err := os.MkdirAll(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "directory", "create", "work", ochazuke.Arguments{"path": subdir})
	if err := a.r.Settings().Set(settingDirectoryAuto, true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		pressSettle(t, a, "s")
	}
	projects, err := a.work.Projects()
	if err != nil || len(projects) != 2 {
		t.Fatalf("automatic scan duplicated projects or promoted a subdirectory: %+v %v", projects, err)
	}
	for _, p := range projects {
		if p.DefaultTree != directoryKey(filepath.Join(root, p.Name)) || len(p.Trees) != 1 {
			t.Fatalf("automatic project default: %+v", p)
		}
	}
}

func TestDirectoryBaseScanDoesNotScanOtherBases(t *testing.T) {
	t.Parallel()
	a, err := New(directoryTestOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	first, second := t.TempDir(), t.TempDir()
	for _, base := range []string{first, second} {
		if _, err := a.changeBase(base, 1, true); err != nil {
			t.Fatal(err)
		}
	}
	openDirectories(t, a)
	for _, base := range []string{first, second} {
		if err := os.MkdirAll(filepath.Join(base, "repo", ".git"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.baseList.Select("base:" + first); err != nil {
		t.Fatal(err)
	}
	if err := a.baseList.Focus(); err != nil {
		t.Fatal(err)
	}
	pressSettle(t, a, "s")
	if a.knownRow(directoryKey(filepath.Join(first, "repo"))) == nil || a.knownRow(directoryKey(filepath.Join(second, "repo"))) != nil {
		t.Fatalf("base scan crossed its scope: %+v", a.directories.Rows)
	}
	if err := a.directoryList.Focus(); err != nil {
		t.Fatal(err)
	}
	pressSettle(t, a, "s")
	for _, base := range []string{first, second} {
		if row := a.knownRow(directoryKey(filepath.Join(base, "repo"))); row == nil || !row.Candidate {
			t.Fatalf("whole-list scan missed %s: %+v", base, a.directories.Rows)
		}
	}
}
