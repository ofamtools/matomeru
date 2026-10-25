package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// openDirectories opens the Directories page the way a key does and runs
// the background load it starts.
func openDirectories(t *testing.T, a *App) {
	t.Helper()
	if err := a.r.OpenPage(directoriesPage); err != nil {
		t.Fatal(err)
	}
	_, cmd := a.Update(nil)
	settle(a, cmd)
	if !a.directories.Ready {
		t.Fatalf("directories did not load: %+v", a.directories)
	}
}

func itemIDs(l *ochazuke.List) []string {
	var out []string
	for _, item := range l.Items() {
		out = append(out, item.ID)
	}
	return out
}

// The page loads its known directories on open without a refresh, shows
// the pinned project's directories on top, and its rows answer: enter on a
// known directory attaches it to the pin and stays; x on a project
// directory detaches it, the default passing to the next; enter makes one
// the default.
func TestDirectoriesPageLoadsAndEditsProjectDirectories(t *testing.T) {
	t.Setenv("MTM_O_PROJ", "none")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := open(t)
	root := knownRepos(t, a, "alpha", "beta")
	alpha, beta := directoryKey(filepath.Join(root, "alpha")), directoryKey(filepath.Join(root, "beta"))
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	openDirectories(t, a)
	if got := itemIDs(a.directoryList); !slices.Equal(got, []string{"directory:" + alpha, "directory:" + beta, "directory:browse"}) {
		t.Fatalf("known rows %v", got)
	}
	if a.projectDirList.Len() != 0 || a.r.Focus != "directory:"+alpha {
		t.Fatalf("focus %s, project rows %v", a.r.Focus, itemIDs(a.projectDirList))
	}
	press(t, a, "enter", "j", "enter")
	if a.r.Page != directoriesPage {
		t.Fatalf("attach left the page for %s", a.r.Page)
	}
	p, _ := a.work.Project("matomeru")
	if len(p.Trees) != 2 || p.DefaultTree != alpha {
		t.Fatalf("attached %+v", p)
	}
	if got := itemIDs(a.projectDirList); !slices.Equal(got, []string{"project-dir:" + alpha, "project-dir:" + beta}) {
		t.Fatalf("project rows %v", got)
	}
	if err := a.projectDirList.Select("project-dir:" + alpha); err != nil {
		t.Fatal(err)
	}
	if err := a.projectDirList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "x")
	p, _ = a.work.Project("matomeru")
	if len(p.Trees) != 1 || p.DefaultTree != beta || !strings.Contains(strings.Join(a.m.Notices, "|"), "default is now") {
		t.Fatalf("remove %+v notices %q", p, a.m.Notices)
	}
	if _, err := os.Stat(alpha); err != nil {
		t.Fatalf("remove touched disk: %v", err)
	}
	// Attach alpha again, then make it the default with enter.
	if err := a.directoryList.Select("directory:" + alpha); err != nil {
		t.Fatal(err)
	}
	if err := a.directoryList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	if err := a.projectDirList.Select("project-dir:" + alpha); err != nil {
		t.Fatal(err)
	}
	if err := a.projectDirList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	if p, _ = a.work.Project("matomeru"); p.DefaultTree != alpha {
		t.Fatalf("default %q", p.DefaultTree)
	}
}

// / focuses the search in insert mode; typing filters, enter returns to
// the first match with the filter kept; } never lands in the search.
func TestDirectoriesSearchIsReachable(t *testing.T) {
	t.Setenv("MTM_O_PROJ", "none")
	a := open(t)
	root := knownRepos(t, a, "alpha", "beta")
	openDirectories(t, a)
	press(t, a, "/")
	if a.r.Focus != directoryQueryID || a.r.Mode.Name != "insert" {
		t.Fatalf("search focus %s mode %s", a.r.Focus, a.r.Mode.Name)
	}
	typeText(t, a, "bet")
	press(t, a, "enter")
	beta := "directory:" + directoryKey(filepath.Join(root, "beta"))
	if got := itemIDs(a.directoryList); !slices.Equal(got, []string{beta, "directory:browse"}) || a.r.Focus != beta || a.r.Mode.Name != "normal" {
		t.Fatalf("filtered %v focus %s mode %s", got, a.r.Focus, a.r.Mode.Name)
	}
	for range 4 {
		press(t, a, "}")
		if a.r.Focus == directoryQueryID {
			t.Fatal("} landed in the search")
		}
	}
}

// With no pin, enter on a known directory asks which project; a new name
// creates one.
func TestKnownDirectoryWithoutPinAsksForProject(t *testing.T) {
	t.Setenv("MTM_O_PROJ", "none")
	a := open(t)
	root := knownRepos(t, a, "alpha")
	executeA(t, a, "project", "unpin", "work", nil)
	openDirectories(t, a)
	press(t, a, "enter")
	if a.r.TopModal() != attachProjectChooser {
		t.Fatalf("modal %q", a.r.TopModal())
	}
	typeText(t, a, "brand new")
	press(t, a, "enter")
	var found bool
	for _, p := range a.projects {
		if p.Name == "brand new" {
			found = p.DefaultTree == directoryKey(filepath.Join(root, "alpha"))
		}
	}
	if !found {
		t.Fatalf("projects %+v", a.projects)
	}
}

// a on the bases list offers the folders holding known directories; the
// chosen one is written to dirs.json and scanned at once, so its repos
// join the known directories; x on a base row stops scanning it.
func TestBaseAddScansAndRemoveFromThePage(t *testing.T) {
	t.Setenv("MTM_O_PROJ", "none")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir()) // no default bases from the real home
	a := open(t)
	first := knownRepos(t, a, "alpha")
	second := knownRepos(t, a, "gamma", "delta")
	t.Setenv("MTM_PROJECT_BASE_DIRS", "")
	os.Unsetenv("MTM_PROJECT_BASE_DIRS")
	if _, err := a.changeBase(directoryKey(first), 1, true); err != nil {
		t.Fatal(err)
	}
	// A known directory outside every base points at its parent.
	if _, err := a.work.AddTree("ochazuke", inspectDirectory(filepath.Join(second, "gamma"))); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	openDirectories(t, a)
	if err := a.baseList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "a")
	c := a.choosers.Get(baseChooser)
	if c == nil {
		t.Fatalf("modal %q", a.r.TopModal())
	}
	if got := labels(c.Matches()); len(got) < 2 || !strings.HasPrefix(got[0], tildePath(directoryKey(second), "/home/test")) {
		t.Fatalf("base choices %q", got)
	}
	_, cmd := a.Update(keyMsg("enter"))
	settle(a, cmd)
	delta := "directory:" + directoryKey(filepath.Join(second, "delta"))
	if !slices.Contains(itemIDs(a.directoryList), delta) {
		t.Fatalf("new base not scanned: %v", itemIDs(a.directoryList))
	}
	if got := itemIDs(a.baseList); len(got) < 2 {
		t.Fatalf("bases %v", got)
	}
	if err := a.baseList.Select("base:" + directoryKey(second)); err != nil {
		t.Fatal(err)
	}
	if err := a.baseList.Focus(); err != nil {
		t.Fatal(err)
	}
	_, cmd = a.Update(keyMsg("x"))
	settle(a, cmd)
	if slices.Contains(itemIDs(a.directoryList), delta) || slices.Contains(itemIDs(a.baseList), "base:"+directoryKey(second)) {
		t.Fatalf("removed base still scanned: %v %v", itemIDs(a.directoryList), itemIDs(a.baseList))
	}
	if _, err := os.Stat(filepath.Join(second, "delta")); err != nil {
		t.Fatal(err)
	}
}

// i on a project directory opens the directory chooser; the chosen path
// takes its place and keeps the default mark.
func TestProjectDirectoryEditFromThePage(t *testing.T) {
	t.Setenv("MTM_O_PROJ", "none")
	a := open(t)
	root := knownRepos(t, a, "alpha", "beta")
	alpha, beta := directoryKey(filepath.Join(root, "alpha")), directoryKey(filepath.Join(root, "beta"))
	executeA(t, a, "project", "directory", "matomeru", ochazuke.Arguments{"path": alpha})
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	openDirectories(t, a)
	if err := a.projectDirList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "i")
	if a.choosers.Get(dirChooser) == nil {
		t.Fatalf("modal %q", a.r.TopModal())
	}
	typeText(t, a, "beta")
	press(t, a, "enter")
	p, _ := a.work.Project("matomeru")
	if len(p.Trees) != 1 || p.Trees[0].Path != beta || p.DefaultTree != beta {
		t.Fatalf("edited %+v", p)
	}
}

func TestPathCellKeepsTheTail(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/home/u/proj/matomeru", "~/proj/matomeru"},
		{"/private/tmp/claude-501/-Users-unixsuperhero-proj/scratchpad/lane-a/e2e/other", "/private/…/e2e/other"},
		{"/home/u/a-very-long-folder-name-for-testing/another-long-one/x", "~/…/another-long-one/x"},
	} {
		if got := pathCell(c.in, "/home/u"); got != c.want {
			t.Errorf("pathCell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
