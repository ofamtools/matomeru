package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The Directories detail is about the directory last selected in the
// project's directories or the known directories (it stays while the
// focus is on the bases or the links): its path, what kind of checkout it
// is, the repository and branch, its last use; then links to the
// projects that have it as a project directory, the other known
// directories of the same repository (its worktrees), the panes running
// in it now, and the saved panes and Vim sessions started there.

// directorySubject is the detail's directory (a directoryKey), its text
// and its links.
func (a *App) directorySubject() (string, string, []link) {
	path := a.directoryDetailPath()
	if path == "" {
		return "", "", nil
	}
	return path, a.directoryFacts(path), a.directoryLinks(path)
}

// directoryDetailPath follows the selection of the directory list holding
// the focus and keeps the last one otherwise; before any, the selected
// known directory.
func (a *App) directoryDetailPath() string {
	selectedPath := func(l *ochazuke.List) (string, bool) {
		item, ok := l.Selected()
		if !ok {
			return "", false
		}
		switch d := item.Data.(type) {
		case string:
			return a.dirKey(d), true
		case dirRow:
			return a.dirKey(d.Path), true
		}
		return "", true // browse…: no directory
	}
	switch {
	case a.projectDirList.Focused():
		a.directories.Subject, _ = selectedPath(a.projectDirList)
	case a.directoryList.Focused() || a.r.Focus == directoryQueryID:
		a.directories.Subject, _ = selectedPath(a.directoryList)
	case a.directories.Subject == "":
		path, _ := selectedPath(a.directoryList)
		return path
	}
	return a.directories.Subject
}

// directoryFacts is the detail's text for the directory at key.
func (a *App) directoryFacts(key string) string {
	home, _ := a.home()
	tree := work.Tree{Kind: "directory", Path: key}
	var last int64
	common := ""
	if row := a.knownRow(key); row != nil {
		tree, last, common = row.Tree, row.LastUsed, row.Common
	} else {
		for _, p := range a.projects {
			for _, t := range p.Trees {
				if a.sameDir(t.Path, key) {
					tree = t
				}
			}
		}
	}
	kind, repo := "plain directory", ""
	if tree.Kind == "repo" || tree.Repo != "" {
		kind, repo = "repository", tree.Repo
		main := repoRoot(common)
		switch {
		case main != "" && !a.sameDir(main, tree.Repo):
			kind, repo = "worktree", main
		case tree.Repo != "" && !a.sameDir(tree.Repo, key):
			kind = "inside a repository"
		}
	}
	if a.sameDir(repo, key) {
		repo = ""
	}
	onDisk := ""
	if st, err := os.Stat(key); err != nil || !st.IsDir() {
		onDisk = "missing"
	}
	used := ""
	if a.knownRow(key) != nil {
		used = sinceUse(last, time.Now())
	}
	return tildePath(key, home) + "\n\n" + facts(
		"kind", kind,
		"repo", tildePath(repo, home),
		"branch", tree.Branch,
		"last used", used,
		"on disk", onDisk,
	)
}

// repoRoot is the main checkout of the repository whose shared git
// directory is common ("" for none): the folder holding .git.
func repoRoot(common string) string {
	if common == "" {
		return ""
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// directoryLinks are the records tied to the directory at key: projects,
// the same repository's other known directories, panes in it now, saved
// panes and Vim sessions started there.
func (a *App) directoryLinks(key string) []link {
	var links []link
	for _, p := range a.projects {
		for _, t := range p.Trees {
			if a.sameDir(t.Path, key) {
				if l, ok := a.projectLink(p.ID); ok {
					links = append(links, l)
				}
				break
			}
		}
	}
	if row := a.knownRow(key); row != nil && row.Common != "" {
		for _, other := range a.directories.Rows {
			if other.Common == row.Common && other.Path != key {
				links = append(links, a.dirLink(other.Path))
			}
		}
	}
	for _, p := range a.panes {
		if a.sameDir(p.Cwd, key) {
			if l, ok := a.paneLink(p.ID); ok {
				links = append(links, l)
			}
		}
	}
	for _, s := range a.savedAll {
		if a.sameDir(s.Cwd, key) {
			links = append(links, link{Text: savedName(s), To: linkTarget{linkSaved, s.ID}})
		}
	}
	for _, v := range a.vimAll {
		if a.sameDir(v.Cwd, key) {
			links = append(links, link{Text: vimName(v), To: linkTarget{linkVim, v.ID}})
		}
	}
	return links
}

// savedName is a saved pane's row label: its name, else its id.
func savedName(s work.SavedPane) string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID
}

// vimName is a Vim session's row label.
func vimName(v work.VimSession) string {
	if v.Name != "" {
		return v.Name
	}
	return "untitled"
}
