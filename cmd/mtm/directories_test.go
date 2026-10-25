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

func TestProjectEditPreservesDirectoriesAndCancel(t *testing.T) {
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
	a.editor.Fields[0].Input.Text = []rune("blue")
	if err := a.autosave(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "open", Target: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openAt(t, path)
	if _, err := b.r.Execute(ochazuke.Request{Noun: "project", Verb: "edit", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	if string(b.editor.Input.Text) != "matomeru renamed" || string(b.editor.Fields[0].Input.Text) != "blue" {
		t.Fatal("project draft fields lost")
	}
	press(t, b, "esc", "q")
	p, err := b.work.Project("matomeru")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "matomeru" || p.Theme.Color != "" || p.DefaultTree != "/shared" {
		t.Fatalf("cancel changed project: %+v", p)
	}
	if _, err := b.r.Execute(ochazuke.Request{Noun: "project", Verb: "edit", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	typeText(t, b, " saved")
	press(t, b, "enter")
	p, err = b.work.Project("matomeru")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "matomeru saved" || p.DefaultTree != "/shared" || len(p.Trees) != 1 || p.Theme.Glyph != "◆" {
		t.Fatalf("save lost metadata: %+v", p)
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
	a.editor.Fields[0].Input.Text = []rune("active")
	typeText(t, a, " changed")
	press(t, a, "enter")
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
	bases, err := projectBases()
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
	config, err := readDirectoryConfig()
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
	bases, err := projectBases()
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
	config, err = readDirectoryConfig()
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
	if a.r.Focus != recordInputID+"/status" {
		t.Fatalf("status field unreachable: %s", a.r.Focus)
	}
	press(t, a, "i", "ctrl+u")
	typeText(t, a, "done")
	press(t, a, "ctrl+s")
	task := stored(t, a, "t1")
	if task.Title != "write the brief keyboard" || task.Status != "done" {
		t.Fatalf("keyboard save: %+v", task)
	}
	for _, id := range []string{recordInputID, recordInputID + "/status"} {
		if a.r.Instances[id] != nil {
			t.Fatalf("saved input remains: %s", id)
		}
	}
}
