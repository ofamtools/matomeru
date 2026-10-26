package main

import (
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// The Vim detail is about the selected Neovim session: its session file,
// project, where it runs, whether its window still runs (and the RPC
// socket it listens on), when it was saved; then links to its project
// (Work), its live pane (Panes; a side Vim links the pane it sits
// beside) and its directory (Directories).

const vimDetailID = "vim-sessions/detail"

// vimSubject is the detail's session (its id), text and links.
func (a *App) vimSubject() (string, string, []link) {
	item, ok := a.vimList.Selected()
	if !ok {
		return "", "", nil
	}
	v, ok := item.Data.(work.VimSession)
	if !ok {
		return "", "", nil
	}
	project, err := a.work.VimProject(v)
	if err != nil {
		project = v.ProjectID
	}
	return v.ID, a.vimFacts(v, project), a.vimLinks(v, project)
}

// vimFacts is the detail's text for a session owned by project.
func (a *App) vimFacts(v work.VimSession, project string) string {
	home, _ := a.home()
	window := v.WindowID
	if window != "" {
		if _, live := a.paneLink(v.WindowID); live {
			window += " (running)"
		} else {
			window += " (not running)"
		}
	}
	owned := ""
	if v.OwnedPath != "" && v.OwnedPath != v.Path {
		owned = pathCell(v.OwnedPath, home)
	}
	return vimName(v) + "\n\n" + facts(
		"session file", pathCell(v.Path, home),
		"mtm's copy", owned,
		"project", a.ownerLabel(project),
		"cwd", pathCell(v.Cwd, home),
		"state", v.State,
		"window", window,
		"listens on", v.Listen,
		"tuios session", v.Session,
		"saved", stamp(v.Created),
		"updated", stamp(v.Updated),
	)
}

// vimLinks are a session's project, live pane and directory.
func (a *App) vimLinks(v work.VimSession, project string) []link {
	var links []link
	if l, ok := a.projectLink(project); ok {
		links = append(links, l)
	}
	if l, ok := a.paneLink(v.WindowID); ok {
		links = append(links, l)
	}
	if a.dirKnown(v.Cwd) {
		links = append(links, a.dirLink(a.dirKey(v.Cwd)))
	}
	return links
}

// renderVim draws the sessions with the selected one's detail.
func (a *App) renderVim(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	return a.withLinkedDetail(a.detail.vim, width, height, func(w, h int) ochazuke.Block {
		return ochazuke.Text(a.view(a.vimList, w, h))
	})
}
