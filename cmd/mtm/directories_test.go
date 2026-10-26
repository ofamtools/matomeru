package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// The project editor writes as it goes (commit on change): leaving it with
// esc q keeps every change, a field change is written at once, and the
// directories and glyph survive the writes.
func TestProjectEditCommitsOnChangeAndKeepsDirectories(t *testing.T) {
	path := seeded(t)
	a := openAt(t, path)
	if _, err := a.work.AddTree("matomeru", work.Tree{Path: "/shared"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "edit", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, " renamed")
	press(t, a, "esc") // leaving insert mode writes the name
	if p, _ := a.work.Project("matomeru"); p.Name != "matomeru renamed" {
		t.Fatalf("name not written on leaving insert: %+v", p)
	}
	a.m.Width, a.m.Height = 100, 30
	a.View()
	press(t, a, "}", "i")
	typeText(t, a, "blue")
	pauseRecord(t, a)
	if p, _ := a.work.Project("matomeru"); p.Theme.Color != "blue" {
		t.Fatalf("field not written after the pause: %+v", p.Theme)
	}
	press(t, a, "esc", "q")
	if a.editor != nil {
		t.Fatal("q left the editor open")
	}
	p, err := a.work.Project("matomeru")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "matomeru renamed" || p.Theme.Color != "blue" || p.DefaultTree != "/shared" || len(p.Trees) != 1 || p.Theme.Glyph != "◆" {
		t.Fatalf("leave lost changes or metadata: %+v", p)
	}
}
func TestTaskViewDoesNotEditAndStatusDraft(t *testing.T) {
	a := open(t)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "open", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	if a.editor != nil {
		t.Fatal("view opened editor")
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, " changed")
	press(t, a, "enter", "enter", "j", "enter", "q")
	task := stored(t, a, "t1")
	if task.Title != "write the brief changed" || task.Status != "active" || task.ProjectID != "matomeru" {
		t.Fatalf("task save: %+v", task)
	}
}
func TestKnownDirectoryBasesDepthLinksAndOrdering(t *testing.T) {
	root := t.TempDir()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	one, two, deep := filepath.Join(root, "one"), filepath.Join(root, "two"), filepath.Join(root, "nested", "deep")
	for _, dir := range []string{one, two, deep} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	if err := os.Symlink(one, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MTM_PROJECT_BASE_DIRS", root+string(os.PathListSeparator)+filepath.Join(root, "missing"))
	a := open(t)
	a.daemon = Options{Socket: filepath.Join(t.TempDir(), "missing.sock")}.client()
	if _, err := a.work.AddTree("ochazuke", work.Tree{Path: two}); err != nil {
		t.Fatal(err)
	}
	// Seed independent launch history, preserving attached-directory recency.
	if err := a.work.SeedDirs([]work.DirRecent{{Path: one, LastUsed: 10}}); err != nil {
		t.Fatal(err)
	}
	rows, footer, err := a.knownDirs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Path != directoryKey(two) || rows[1].Path != directoryKey(one) || !reflect.DeepEqual(rows[0].Projects, []string{"ochazuke"}) || !strings.Contains(footer, "missing bases:") {
		t.Fatalf("known directories: %+v / %s", rows, footer)
	}
	bases, err := projectBases(a.dirsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if bases[0].Source != "variable" {
		t.Fatalf("source: %+v", bases)
	}
	paths, _ := scanBases([]projectBase{{Path: root, Depth: 2}})
	if len(paths) != 3 {
		t.Fatalf("depth 2: %v", paths)
	}
}
func TestBaseEditingWritesFileAndEnvironmentWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MTM_PROJECT_BASE_DIRS", "/override")
	a := open(t)
	ctx := ochazuke.Ctx{Args: ochazuke.Arguments{"path": "/test-base", "depth": 2}}
	_, err := a.r.Execute(ochazuke.Request{Noun: "dirs", Verb: "base.add", Target: "directories", Args: ctx.Args})
	if err != nil {
		t.Fatal(err)
	}
	config, err := readDirectoryConfig(a.dirsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, base := range config.Bases {
		if base.Path == "/test-base" && base.Depth == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("file: %+v", config)
	}
	bases, err := projectBases(a.dirsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 1 || bases[0].Path != "/override" {
		t.Fatalf("override: %+v", bases)
	}
	_, err = a.r.Execute(ochazuke.Request{Noun: "dirs", Verb: "base.remove", Target: "directories", Args: ochazuke.Arguments{"path": "/test-base"}})
	if err != nil {
		t.Fatal(err)
	}
	config, err = readDirectoryConfig(a.dirsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range config.Bases {
		if base.Path == "/test-base" {
			t.Fatal("remove retained base")
		}
	}
}

func TestKnownDirectoriesOrderRecordedUsageBeforeNeverUsed(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"yesterday", "last-week", "never"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "init", "-q", path).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	t.Setenv("MTM_PROJECT_BASE_DIRS", root)
	a := open(t)
	a.daemon = Options{Socket: filepath.Join(t.TempDir(), "missing.sock")}.client()
	if err := a.work.SeedDirs([]work.DirRecent{
		{Path: filepath.Join(root, "yesterday"), LastUsed: 172800000},
		{Path: filepath.Join(root, "last-week"), LastUsed: 86400000},
	}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := a.knownDirs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, row := range rows {
		names = append(names, filepath.Base(row.Path))
	}
	if !reflect.DeepEqual(names, []string{"yesterday", "last-week", "never"}) {
		t.Fatalf("usage order: %v", names)
	}
}

// The task editor's fields are reached with } and written as they change:
// the status from its chooser, where moving the highlight writes nothing
// and enter writes the row; q leaves the editor.
func TestTaskFieldKeyboardFocusAndSave(t *testing.T) {
	a := open(t)
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "task", Verb: "edit", Target: "t1"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, a, " keyboard")
	press(t, a, "esc")
	a.m.Width, a.m.Height = 100, 30
	a.View()
	press(t, a, "}")
	if a.r.Focus != a.editor.fieldID("status") {
		t.Fatalf("status field unreachable: %s", a.r.Focus)
	}
	press(t, a, "i", "j", "j")
	if got := stored(t, a, "t1"); got.Title != "write the brief keyboard" || got.Status != work.StatusTodo {
		t.Fatalf("a highlighted status was written: %+v", got)
	}
	press(t, a, "j", "enter")
	if got := stored(t, a, "t1"); got.Status != "done" {
		t.Fatalf("chosen status not written: %+v", got)
	}
	press(t, a, "q")
	task := stored(t, a, "t1")
	if task.Title != "write the brief keyboard" || task.Status != "done" {
		t.Fatalf("keyboard save: %+v", task)
	}
	for _, id := range []string{recordEditorID, recordEditorID + "/title", recordEditorID + "/status"} {
		if a.r.Instances[id] != nil {
			t.Fatalf("saved input remains: %s", id)
		}
	}
}

// o proj's manifest is a known-directory source through o-proj list; a
// failing o-proj is a footer note, and its absence is no source at all.
func TestKnownDirectoriesIncludeOProjList(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "from-manifest")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "o-proj")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$1\" = list ] && echo '[{\"name\":\"m\",\"path\":\"~/x\",\"dir\":\""+repo+"\"}]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MTM_PROJECT_BASE_DIRS", filepath.Join(base, "none"))
	t.Setenv("MTM_O_PROJ", bin)
	a := open(t)
	a.daemon = Options{Socket: filepath.Join(t.TempDir(), "missing.sock")}.client()
	rows, _, err := a.knownDirs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		found = found || r.Path == directoryKey(repo)
	}
	if !found {
		t.Fatalf("manifest directory missing: %+v", rows)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, footer, err := a.knownDirs(context.Background()); err != nil || !strings.Contains(footer, "o-proj list:") {
		t.Fatalf("failing o-proj: %v %q", err, footer)
	}
}

// TestConfigDirFollowsData pins where config files go: an explicit Config,
// then MTM_CONFIG_DIR, then beside a non-default -data, and the user's
// config directory only for the default database. Adding a base on a copy
// writes dirs.json beside the copy, never under XDG_CONFIG_HOME.
func TestConfigDirFollowsData(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("MTM_PROJECT_BASE_DIRS", "")
	os.Unsetenv("MTM_PROJECT_BASE_DIRS")
	data := filepath.Join(t.TempDir(), "copy.db")
	if got := (Options{Data: data}).ConfigDir(); got != filepath.Dir(data) {
		t.Fatalf("beside data: %s", got)
	}
	if got := (Options{Data: DefaultData()}).ConfigDir(); got != filepath.Join(xdg, "matomeru") {
		t.Fatalf("default data: %s", got)
	}
	if got := (Options{Data: data, Config: "/explicit"}).ConfigDir(); got != "/explicit" {
		t.Fatalf("explicit: %s", got)
	}
	t.Setenv("MTM_CONFIG_DIR", "/from-env")
	if got := (Options{Data: DefaultData()}).ConfigDir(); got != "/from-env" {
		t.Fatalf("env: %s", got)
	}
	os.Unsetenv("MTM_CONFIG_DIR")

	a := openAt(t, seeded(t))
	if _, err := a.r.Execute(ochazuke.Request{Noun: "dirs", Verb: "base.add", Target: "directories", Args: ochazuke.Arguments{"path": "/test-base", "depth": 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.configDir, "dirs.json")); err != nil {
		t.Fatalf("dirs.json beside the data: %v", err)
	}
	if entries, _ := os.ReadDir(xdg); len(entries) != 0 {
		t.Fatalf("wrote under XDG_CONFIG_HOME: %v", entries)
	}
}
