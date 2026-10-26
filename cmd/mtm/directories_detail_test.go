package main

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// linked is a fixture for the linked details: a repository alpha with a
// worktree alpha-wt under a scanned base, alpha a directory of matomeru,
// pane w1 running in it, a saved pane (builder, live as w1) and a Vim
// session (notes, live as w2) started there.
type linked struct {
	a                  *App
	alpha, worktree    string
	savedID, vimID     string
	paneRow, vimPaneRw string
}

func linkedFixture(t *testing.T) linked {
	t.Helper()
	t.Setenv("MTM_O_PROJ", "none")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := open(t)
	root := knownRepos(t, a, "alpha")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", filepath.Join(root, "alpha"), "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("commit", "-q", "--allow-empty", "-m", "start")
	git("worktree", "add", "-q", "-b", "wt", filepath.Join(root, "alpha-wt"))
	l := linked{a: a, alpha: directoryKey(filepath.Join(root, "alpha")), worktree: directoryKey(filepath.Join(root, "alpha-wt"))}
	p, err := a.work.Project("matomeru")
	if err != nil {
		t.Fatal(err)
	}
	p.Trees, p.DefaultTree = []work.Tree{{Kind: "repo", Path: l.alpha, Repo: l.alpha}}, l.alpha
	if err := a.work.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	f := a.panesd.(*fakePanes)
	f.panes[0].Cwd = l.alpha
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	l.paneRow, l.vimPaneRw = f.panes[0].rowID(), f.panes[1].rowID()
	saved, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Window: "w1", Session: "matomeru", Name: "builder", Command: []string{"go", "test", "./..."}, Cwd: l.alpha, ProjectID: "matomeru"}})
	if err != nil {
		t.Fatal(err)
	}
	l.savedID = saved.ID
	l.vimID = "v1"
	if err := a.work.PutVimSession(work.VimSession{ID: l.vimID, Name: "notes", Path: "/home/test/.local/v1.vim", Cwd: l.alpha, ProjectID: "matomeru", WindowID: "w2", Created: 1}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshSaved(); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshVim(); err != nil {
		t.Fatal(err)
	}
	return l
}

// sizedFrame is the frame at width by height, styles stripped.
func sizedFrame(a *App, width, height int) string {
	a.m.Width, a.m.Height = width, height
	return ansi.Strip(a.m.View().Content)
}

// toLinks presses } until the focus is on the links of ld.
func toLinks(t *testing.T, a *App, ld *linkedDetail) {
	t.Helper()
	for range 6 {
		if ld.links.Focused() {
			return
		}
		press(t, a, "}")
	}
	t.Fatalf("} never reached the links; focus %s", a.r.Focus)
}

// followLinkTo selects the link to target on ld's list and presses enter.
func followLinkTo(t *testing.T, a *App, ld *linkedDetail, kind, id string) {
	t.Helper()
	toLinks(t, a, ld)
	row := "link:" + kind + ":" + id
	ids := itemIDs(ld.links)
	at, want := slices.Index(ids, a.r.Focus), slices.Index(ids, row)
	if at < 0 || want < 0 {
		t.Fatalf("focus %s, no link %s among %v", a.r.Focus, row, ids)
	}
	for ; at < want; at++ {
		press(t, a, "j")
	}
	for ; at > want; at-- {
		press(t, a, "k")
	}
	if a.r.Focus != row {
		t.Fatalf("focus %s, want %s", a.r.Focus, row)
	}
	press(t, a, "enter")
}

// The detail of a directory names its kind, branch and repository and
// links its project, its worktree, the pane running in it and the saved
// pane and Vim session started there; it follows the selection, and the
// worktree's says what it is a worktree of.
func TestDirectoriesDetailShowsWhatUsesTheDirectory(t *testing.T) {
	l := linkedFixture(t)
	a := l.a
	openDirectories(t, a)
	if err := a.directoryList.Select("directory:" + l.alpha); err != nil {
		t.Fatal(err)
	}
	if err := a.directoryList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	fr := sizedFrame(a, 140, 30)
	t.Logf("Directories:\n%s", fr)
	for _, want := range []string{"kind       repository", "branch", "links (5)", "project ◆ matomeru", "dir     " + pathCell(l.worktree, "/home/test"), "pane    matomeru/claude", "saved   builder", "vim     notes"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("frame lacks %q:\n%s", want, fr)
		}
	}
	press(t, a, "k")
	if err := a.directoryList.Select("directory:" + l.worktree); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	fr = sizedFrame(a, 140, 30)
	if !strings.Contains(fr, "kind       worktree") || !strings.Contains(fr, "branch     wt") || !strings.Contains(fr, "dir     "+pathCell(l.alpha, "/home/test")) || strings.Contains(fr, "project ◆ matomeru") {
		t.Fatalf("worktree detail:\n%s", fr)
	}
}

// enter on a link opens its page with the record's row selected and
// focused: the project on Work, the pane on Panes, the worktree back on
// Directories, the saved pane on Saved, the session on Vim.
func TestDirectoriesLinksJumpToTheirRows(t *testing.T) {
	l := linkedFixture(t)
	a := l.a
	back := func() {
		t.Helper()
		if err := a.r.OpenPage(directoriesPage); err != nil {
			t.Fatal(err)
		}
		a.Update(nil)
		if err := a.directoryList.Select("directory:" + l.alpha); err != nil {
			t.Fatal(err)
		}
		if err := a.directoryList.Focus(); err != nil {
			t.Fatal(err)
		}
		a.Update(nil)
		frame(a)
	}
	openDirectories(t, a)
	for _, c := range []struct{ kind, id, page, focus string }{
		{linkProject, "matomeru", "work", "matomeru"},
		{linkPane, l.paneRow, panesPage, l.paneRow},
		{linkDirectory, l.worktree, directoriesPage, "directory:" + l.worktree},
		{linkSaved, l.savedID, savedPage, l.savedID},
		{linkVim, l.vimID, vimPage, l.vimID},
	} {
		back()
		followLinkTo(t, a, a.detail.dirs, c.kind, c.id)
		if a.r.Page != c.page || a.r.Focus != c.focus {
			t.Fatalf("%s link: page %s focus %s, want %s on %s", c.kind, a.r.Page, a.r.Focus, c.focus, c.page)
		}
	}
	// The project landed as the selected Work row, its tasks shown.
	if sel, _ := a.projectList.Selected(); sel.ID != "matomeru" {
		t.Fatalf("work selection %s", sel.ID)
	}
}

// J and K scroll the detail's text from the list while it overflows; the
// focus stays on the directory row.
func TestDirectoriesDetailScrollsWithJK(t *testing.T) {
	l := linkedFixture(t)
	a := l.a
	openDirectories(t, a)
	if err := a.directoryList.Select("directory:" + l.alpha); err != nil {
		t.Fatal(err)
	}
	if err := a.directoryList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	sizedFrame(a, 140, 14)
	d := a.detail.dirs.d
	if !d.Overflows() {
		t.Fatalf("the detail fits:\n%s", sizedFrame(a, 140, 14))
	}
	focus := a.r.Focus
	press(t, a, "J")
	if fr := sizedFrame(a, 140, 14); d.Offset() != 1 || a.r.Focus != focus || !strings.Contains(fr, "J/K detail") {
		t.Fatalf("J: offset %d focus %s\n%s", d.Offset(), a.r.Focus, fr)
	}
	press(t, a, "K")
	if sizedFrame(a, 140, 14); d.Offset() != 0 {
		t.Fatalf("K: offset %d", d.Offset())
	}
}
