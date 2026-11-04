package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// A mounted extension with path parameters receives recent directories in its
// path chooser via generic argument-form interception and recentSource:
// - file picks navigate recent directories (Opens: true, NoPick: true) and select contained files;
// - directory picks retain direct selection.
func TestMountedExtensionPathPickerRecentDirs(t *testing.T) {
	t.Parallel()
	const extCatalog = `{"schema":1,"name":"o-ext","version":"1.0","nouns":[
 {"name":"item","columns":[{"title":"item"}],"verbs":[
  {"name":"list","kind":"application"},
  {"name":"open","kind":"application","params":{"file":{"type":"path","required":true}}},
  {"name":"clone","kind":"application","params":{"dir":{"type":"path","path":"dir","required":true}}}]}],
 "pages":[]}`
	const extArms = `"item list") echo '[{"id":"1","label":"one"}]' ;;
"item open"|"item clone") echo '{"ok":true}' ;;`

	bin, log := fakeExtension(t, extCatalog, extArms)
	a := registered(t, seeded(t), "ext", bin)
	recentDir := t.TempDir()
	subDir := filepath.Join(recentDir, "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(subDir, "sample.json")
	if err := os.WriteFile(targetFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.work.SeedDirs([]work.DirRecent{{Path: recentDir, LastUsed: 42}}); err != nil {
		t.Fatal(err)
	}
	openExtPage(t, a, "ext/ext")

	// 1. File pick flow: open into recent directory, navigate nested, and pick contained file.
	press(t, a, ":")
	typeText(t, a, "ext.item open")
	press(t, a, "enter")
	settle(a, a.r.PendingCommands())
	c := a.r.Chooser(ochazuke.PathChooserKind)
	if c == nil {
		t.Fatalf("expected path chooser to open for missing required file, modal is %q", a.r.TopModal())
	}
	toSource(t, a, c, ochazuke.PathSourceRecent)
	if got := chooserData(c); !slices.Contains(got, recentDir) {
		t.Fatalf("recent source %v lacks recent dir %s", got, recentDir)
	}
	t.Logf("Smoke Frame 1 (Recent list in file pick):\n%s", sizedFrame(a, 100, 24))
	// Enter on the directory row opens into the directory (does not pick directory as file).
	_, cmd := a.Update(keyMsg("enter"))
	settle(a, cmd)
	settle(a, a.r.PendingCommands())
	if a.r.TopModal() == "" {
		t.Fatal("chooser closed on directory enter during file pick; expected to open into directory")
	}
	t.Logf("Smoke Frame 2 (Opened recent directory):\n%s", sizedFrame(a, 100, 24))
	// Navigate into nested subfolder.
	_, cmd = a.Update(keyMsg("enter"))
	settle(a, cmd)
	settle(a, a.r.PendingCommands())
	t.Logf("Smoke Frame 3 (Opened nested subfolder):\n%s", sizedFrame(a, 100, 24))
	if got := chooserData(c); !slices.Contains(got, targetFile) {
		t.Fatalf("opened nested directory rows %v lack %s", got, targetFile)
	}
	// Select and pick the contained file.
	typeText(t, a, "sample")
	_, cmd = a.Update(keyMsg("enter"))
	settle(a, cmd)
	settle(a, a.r.PendingCommands())
	if a.r.TopModal() != "" {
		t.Fatalf("chooser did not close after picking file: %q", a.r.TopModal())
	}
	wantFileCall := fmt.Sprintf(`item open --target i1 --json {"file":%q} --yes`, targetFile)
	var foundFileCall bool
	for _, call := range calls(t, log) {
		if strings.Contains(call, wantFileCall) {
			foundFileCall = true
			break
		}
	}
	if !foundFileCall {
		t.Fatalf("expected call %q, got %v", wantFileCall, calls(t, log))
	}

	// 2. Directory pick flow: directory pick directly selects the recent directory.
	press(t, a, ":")
	typeText(t, a, "ext.item clone")
	press(t, a, "enter")
	settle(a, a.r.PendingCommands())
	c = a.r.Chooser(ochazuke.PathChooserKind)
	if c == nil {
		t.Fatalf("expected path chooser to open for missing required dir, modal is %q", a.r.TopModal())
	}
	toSource(t, a, c, ochazuke.PathSourceRecent)
	t.Logf("Smoke Frame 4 (Recent list in directory pick):\n%s", sizedFrame(a, 100, 24))
	_, cmd = a.Update(keyMsg("enter"))
	settle(a, cmd)
	settle(a, a.r.PendingCommands())
	if a.r.TopModal() != "" {
		t.Fatalf("chooser did not close after directory pick: %q", a.r.TopModal())
	}
	wantDirCall := fmt.Sprintf(`item clone --target i1 --json {"dir":%q} --yes`, recentDir)
	var foundDirCall bool
	for _, call := range calls(t, log) {
		if strings.Contains(call, wantDirCall) {
			foundDirCall = true
			break
		}
	}
	if !foundDirCall {
		t.Fatalf("expected call %q, got %v", wantDirCall, calls(t, log))
	}

	// 3. Opt-out flow: path.recent-dirs=false omits recent directory from file pick.
	if err := a.r.Settings().Set("path.recent-dirs", false); err != nil {
		t.Fatal(err)
	}
	press(t, a, ":")
	typeText(t, a, "ext.item open")
	press(t, a, "enter")
	settle(a, a.r.PendingCommands())
	c = a.r.Chooser(ochazuke.PathChooserKind)
	toSource(t, a, c, ochazuke.PathSourceRecent)
	t.Logf("Smoke Frame 5 (Recent list with path.recent-dirs=false):\n%s", sizedFrame(a, 100, 24))
	if got := chooserData(c); slices.Contains(got, recentDir) {
		t.Fatalf("recent source %v should not contain recent dir when path.recent-dirs=false", got)
	}
	press(t, a, "esc")
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
	if c = reopen(); c.Source() != knownChooserSource || !slices.Contains(labels(c.Matches()), "browse…") {
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

// openKnownChooser opens the known-directory chooser (directory-chooser
// known) over the known repos; use records the path it is given.
func openKnownChooser(t *testing.T, a *App, used *string) *ochazuke.Chooser {
	t.Helper()
	knownDirectoryChooser(t, a)
	if err := a.chooseDirectoryFor("Directory for matomeru", "matomeru", browseAttach, func(path string) ochazuke.Result {
		*used = path
		return ochazuke.Done(path)
	}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	c := a.r.Chooser(dirChooser)
	if c == nil {
		t.Fatalf("no chooser: modal %q", a.r.TopModal())
	}
	return c
}

// A path typed into the known-directory chooser is answered at once by
// the typed source (ChooserSource.Claims, chooser.claim-source), as in the
// path chooser: it lists the folder and its matches, enter takes the path
// as typed, a path that is not there says so and keeps the chooser open,
// and a plain word still fuzzy-matches the known directories. With
// chooser.claim-source off nothing is claimed.
func TestKnownDirectoryChooserAnswersTypedPaths(t *testing.T) {
	t.Parallel()
	a := open(t)
	knownRepos(t, a, "alpha", "beta")
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"tmpfoo", "tmpbar", "other"} {
		if err := os.Mkdir(filepath.Join(tmp, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var used string
	c := openKnownChooser(t, a, &used)
	if c.Source() != knownChooserSource {
		t.Fatalf("opened on %s", c.Source())
	}
	// A plain word fuzzy-matches the known directories and stays there.
	typeText(t, a, "alp")
	if c.Source() != knownChooserSource || len(c.Matches()) != 1 {
		t.Fatalf("plain word: source %s, matches %q", c.Source(), labels(c.Matches()))
	}
	press(t, a, "esc")
	c = openKnownChooser(t, a, &used)
	// A path claims the typed source at once; its rows are the folders matching.
	typeText(t, a, filepath.Join(tmp, "tm"))
	settle(a, a.r.PendingCommands())
	if c.Source() != ochazuke.PathSourceTyped {
		t.Fatalf("typed path: source %s", c.Source())
	}
	got := chooserData(c)
	want := []string{filepath.Join(tmp, "tmpbar"), filepath.Join(tmp, "tmpfoo")}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("typed rows %q, want %q", got, want)
	}
	// Enter on a whole typed folder takes that folder.
	typeText(t, a, "pfoo")
	settle(a, a.r.PendingCommands())
	press(t, a, "enter")
	settle(a, a.r.PendingCommands())
	if used != filepath.Join(tmp, "tmpfoo") || a.r.TopModal() != "" {
		t.Fatalf("used %q, modal %q", used, a.r.TopModal())
	}
	// A path that is not there: nothing is used and a message says so (the
	// framework closes the chooser on a create, as it does for every kind).
	used = ""
	c = openKnownChooser(t, a, &used)
	typeText(t, a, filepath.Join(tmp, "nonexistent"))
	settle(a, a.r.PendingCommands())
	_, cmd := a.Update(keyMsg("enter"))
	settle(a, cmd)
	if used != "" {
		t.Fatalf("missing path used %q", used)
	}
	if fr := frameWH(a, 120, 30); !strings.Contains(a.m.Status, "no such directory") && !strings.Contains(fr, "no such directory") {
		t.Fatalf("no message (status %q):\n%s", a.m.Status, fr)
	}
	a.m.Status = ""
	// Claim off: a path is not claimed, the known source keeps the query.
	if err := a.r.Settings().Set(ochazuke.ChooserClaimSourceSetting, false); err != nil {
		t.Fatal(err)
	}
	c = openKnownChooser(t, a, &used)
	typeText(t, a, filepath.Join(tmp, "tm"))
	settle(a, a.r.PendingCommands())
	if c.Source() != knownChooserSource {
		t.Fatalf("claim off: source %s", c.Source())
	}
}
