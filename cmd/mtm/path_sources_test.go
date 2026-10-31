package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// knownDirectoryChooser sets directory-chooser to known: the classic
// known-directory chooser with browse….
func knownDirectoryChooser(t *testing.T, a *App) {
	t.Helper()
	if err := a.r.Settings().Set(settingDirectoryChooser, dirChooserKnown); err != nil {
		t.Fatal(err)
	}
}

// chooserData are the matches' Data paths.
func chooserData(c *ochazuke.Chooser) []string {
	var out []string
	for _, row := range c.Matches() {
		if s, ok := row.Data.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// toSource steps the open directory chooser to the source name.
func toSource(t *testing.T, a *App, c *ochazuke.Chooser, name string) {
	t.Helper()
	for range c.Sources() {
		if c.Source() == name {
			return
		}
		press(t, a, "ctrl+o") // chooser.next-source; tab cycles the match mode since ochazuke v0.59.0
	}
	t.Fatalf("no source %s in %q (at %s)", name, c.Sources(), c.Source())
}

// project.directory with no path opens the path chooser (directory-chooser
// path, the default) on mtm's known source once the scan lands: the known
// directories, no browse… row (the chooser's browse source replaces it),
// and the pick attaches as the default.
func TestDirectoryPathChooserOpensOnKnown(t *testing.T) {
	t.Parallel()
	a := open(t)
	root := knownRepos(t, a, "alpha", "beta")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "directory", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	c := a.r.Chooser(dirChooser)
	if c == nil {
		t.Fatalf("no chooser: modal %q", a.r.TopModal())
	}
	for _, want := range []string{ochazuke.PathSourceBrowse, ochazuke.PathSourceRecent, ochazuke.PathSourceTyped, pathSourceKnown} {
		if !slices.Contains(c.Sources(), want) {
			t.Fatalf("sources %q lack %s", c.Sources(), want)
		}
	}
	if c.Source() != pathSourceKnown {
		t.Fatalf("opened on %s", c.Source())
	}
	alpha, beta := directoryKey(filepath.Join(root, "alpha")), directoryKey(filepath.Join(root, "beta"))
	if got := chooserData(c); !slices.Equal(got, []string{alpha, beta}) || len(c.Matches()) != 2 {
		t.Fatalf("known rows %q (%d matches)", got, len(c.Matches()))
	}
	typeText(t, a, "beta")
	press(t, a, "enter")
	if p, _ := a.project("matomeru"); p.DefaultTree != beta || a.r.TopModal() != "" {
		t.Fatalf("default %q modal %q", p.DefaultTree, a.r.TopModal())
	}
}

// The project source lists the project's own directories, the default
// first, each row's Data its path; the typed source attaches a typed
// folder.
func TestDirectoryPathChooserProjectAndTypedSources(t *testing.T) {
	t.Parallel()
	a := open(t)
	knownRepos(t, a)
	one, two := t.TempDir(), t.TempDir()
	for _, path := range []string{one, two} {
		if _, err := a.work.AddTree("matomeru", inspectDirectory(path)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.work.SetDefaultTree("matomeru", two); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	moved := ""
	if err := a.chooseDirectoryFor("Move to", "matomeru", browseAttach, func(path string) ochazuke.Result {
		moved = path
		return ochazuke.Done(path)
	}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	c := a.r.Chooser(dirChooser)
	toSource(t, a, c, pathSourceProject)
	if got := chooserData(c); !slices.Equal(got, []string{two, one}) {
		t.Fatalf("project rows %q", got)
	}
	toSource(t, a, c, ochazuke.PathSourceTyped)
	sub := filepath.Join(one, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, sub[:len(sub)-1])
	pressSettle(t, a, sub[len(sub)-1:]) // the typed source completes off the loop
	press(t, a, "alt+enter")
	if moved != sub {
		t.Fatalf("typed pick %q, want %q (modal %q)", moved, sub, a.r.TopModal())
	}
}

// The recent source is mtm's dir_recent, newest first.
func TestDirectoryPathChooserRecentIsDirRecent(t *testing.T) {
	t.Parallel()
	a := open(t)
	knownRepos(t, a)
	if err := a.work.SeedDirs([]work.DirRecent{{Path: "/old/one", LastUsed: 1}, {Path: "/new/two", LastUsed: 2}}); err != nil {
		t.Fatal(err)
	}
	if got := a.recentDirectories(); len(got) < 2 || got[0] != "/new/two" || got[1] != "/old/one" {
		t.Fatalf("recent %q", got)
	}
	if err := a.chooseDirectoryFor("Dir", "", browseAttach, func(string) ochazuke.Result { return ochazuke.Done(nil) }); err != nil {
		t.Fatal(err)
	}
	c := a.r.Chooser(dirChooser)
	toSource(t, a, c, ochazuke.PathSourceRecent)
	if got := chooserData(c); len(got) < 2 || got[0] != "/new/two" || got[1] != "/old/one" {
		t.Fatalf("recent rows %q", got)
	}
}

// directory-chooser path-remembered opens on the source last used; path
// opens on known whatever was used last.
func TestDirectoryChooserSettingStartSource(t *testing.T) {
	t.Parallel()
	a := open(t)
	knownRepos(t, a)
	reopen := func() *ochazuke.Chooser {
		t.Helper()
		if c := a.r.Chooser(dirChooser); c != nil {
			press(t, a, "esc")
		}
		if err := a.chooseDirectoryFor("Dir", "", browseAttach, func(string) ochazuke.Result { return ochazuke.Done(nil) }); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
		return a.r.Chooser(dirChooser)
	}
	c := reopen()
	toSource(t, a, c, ochazuke.PathSourceBrowse)
	if c = reopen(); c.Source() != pathSourceKnown {
		t.Fatalf("path: opened on %s", c.Source())
	}
	if err := a.r.Settings().Set(settingDirectoryChooser, dirChooserRemembered); err != nil {
		t.Fatal(err)
	}
	toSource(t, a, c, ochazuke.PathSourceBrowse)
	if c = reopen(); c.Source() != ochazuke.PathSourceBrowse {
		t.Fatalf("path-remembered: opened on %s", c.Source())
	}
	knownDirectoryChooser(t, a)
	if c = reopen(); c.Source() != "" || !slices.Contains(labels(c.Matches()), "browse…") {
		t.Fatalf("known: source %q rows %q", c.Source(), labels(c.Matches()))
	}
}

// mtm's directory fields are folder path parameters.
func TestDirectoryFieldsArePathParameters(t *testing.T) {
	t.Parallel()
	a := open(t)
	for key, name := range map[string]string{
		"project.directory":      "path",
		"project.edit-directory": "to",
		"project.new":            "dir",
		"dirs.add-base":          "path",
		scheduleNoun + ".edit":   "dir",
		scheduleNoun + ".add":    "dir",
	} {
		p := a.r.Operations[a.r.CanonicalKey(key)].Parameters[name]
		if p.Type != ochazuke.PathParameterType || p.Path != ochazuke.PathDir {
			t.Errorf("%s %s: %+v", key, name, p)
		}
	}
}

// walkProject gives the matomeru project one directory holding nested
// folders (cmd/mtm, internal/work, a .git and a .worktrees checkout) and
// returns it.
func walkProject(t *testing.T, a *App) string {
	t.Helper()
	knownRepos(t, a)
	tree, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"cmd/mtm", "internal/work", ".git/objects", ".worktrees/lane/cmd"} {
		if err := os.MkdirAll(filepath.Join(tree, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.work.AddTree("matomeru", inspectDirectory(tree)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.work.SetDefaultTree("matomeru", tree); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	return tree
}

// A project's directory pick opens on the walk source, which lists every
// folder below the project's directories (not only the roots, as known and
// project do): typing a fragment of a nested path finds the deep folder,
// and .git is skipped while the .worktrees checkout is walked.
func TestDirectoryPathChooserWalksBelowTheProjectRoot(t *testing.T) {
	t.Parallel()
	a := open(t)
	tree := walkProject(t, a)
	picked := ""
	if err := a.chooseDirectoryFor("Dir", "matomeru", browseAttach, func(path string) ochazuke.Result {
		picked = path
		return ochazuke.Done(path)
	}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	c := a.r.Chooser(dirChooser)
	if c == nil || c.Source() != ochazuke.PathSourceWalk || c.Sources()[0] != ochazuke.PathSourceWalk {
		t.Fatalf("chooser %v", c)
	}
	got := chooserData(c)
	for _, want := range []string{tree, tree + "/cmd/mtm", tree + "/internal/work", tree + "/.worktrees/lane/cmd"} {
		if !slices.Contains(got, want) {
			t.Fatalf("walk lacks %s: %q", want, got)
		}
	}
	if slices.ContainsFunc(got, func(p string) bool { return filepath.Base(p) == ".git" }) {
		t.Fatalf("walk lists .git: %q", got)
	}
	typeText(t, a, "cmd mtm")
	press(t, a, "enter")
	if picked != tree+"/cmd/mtm" {
		t.Fatalf("picked %q", picked)
	}
}

// Browse in a project's directory pick starts in the project's default
// directory (SetPathBase), not in mtm's working directory.
func TestDirectoryPathChooserBrowseStartsInTheProject(t *testing.T) {
	t.Parallel()
	a := open(t)
	tree := walkProject(t, a)
	if err := a.chooseDirectoryFor("Dir", "matomeru", browseAttach, func(string) ochazuke.Result { return ochazuke.Done(nil) }); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	c := a.r.Chooser(dirChooser)
	toSource(t, a, c, ochazuke.PathSourceBrowse)
	settle(a, a.r.PendingCommands())
	if got := chooserData(c); !slices.Contains(got, tree+"/cmd") || !slices.Contains(got, tree) {
		t.Fatalf("browse rows %q", got)
	}
}

// path.walk off restores the chooser as it was: no walk source, opened on
// known; path-remembered with nothing remembered opens on walk, the first
// source path.first names.
func TestDirectoryPathChooserWalkOffAndRemembered(t *testing.T) {
	t.Parallel()
	a := open(t)
	walkProject(t, a)
	reopen := func() *ochazuke.Chooser {
		t.Helper()
		if c := a.r.Chooser(dirChooser); c != nil {
			press(t, a, "esc")
		}
		if err := a.chooseDirectoryFor("Dir", "matomeru", browseAttach, func(string) ochazuke.Result { return ochazuke.Done(nil) }); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
		return a.r.Chooser(dirChooser)
	}
	if err := a.r.Settings().Set(settingDirectoryChooser, dirChooserRemembered); err != nil {
		t.Fatal(err)
	}
	if c := reopen(); c.Source() != ochazuke.PathSourceWalk {
		t.Fatalf("path-remembered, nothing remembered: %s", c.Source())
	}
	if err := a.r.Settings().Set(settingDirectoryChooser, dirChooserPath); err != nil {
		t.Fatal(err)
	}
	if err := a.r.Settings().Set(ochazuke.PathWalkSetting, false); err != nil {
		t.Fatal(err)
	}
	if c := reopen(); c.Source() != pathSourceKnown || slices.Contains(c.Sources(), ochazuke.PathSourceWalk) {
		t.Fatalf("walk off: on %s of %q", c.Source(), c.Sources())
	}
}
