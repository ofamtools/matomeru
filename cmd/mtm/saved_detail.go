package main

import (
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// The Saved detail is about the selected saved pane: its name, the
// command it relaunches (with its harness, model, flags and environment),
// where it starts, the session and window it was saved from and whether
// that window still runs; then links to its project (Work), its live pane
// (Panes) and its directory (Directories).

const savedDetailID = "saved/detail"

// savedSubject is the detail's saved pane (its id), text and links.
func (a *App) savedSubject() (string, string, []link) {
	item, ok := a.savedList.Selected()
	if !ok {
		return "", "", nil
	}
	s, ok := item.Data.(work.SavedPane)
	if !ok {
		return "", "", nil
	}
	return s.ID, a.savedFacts(s), a.savedLinks(s)
}

// savedFacts is the detail's text for a saved pane.
func (a *App) savedFacts(s work.SavedPane) string {
	home, _ := a.home()
	command := strings.Join(s.Command, " ")
	if command == "" {
		command = "daemon shell"
	}
	window := s.Window
	if window != "" {
		if _, live := a.paneLink(s.Window); live {
			window += " (running)"
		} else {
			window += " (not running)"
		}
	}
	var env []string
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range s.Env {
			if !yield(k) {
				return
			}
		}
	}) {
		env = append(env, k+"="+s.Env[k])
	}
	text := savedName(s) + "\n\n" + facts(
		"project", a.ownerLabel(s.ProjectID),
		"command", command,
		"cwd", pathCell(s.Cwd, home),
		"harness", s.Harness,
		"model", s.Model,
		"flags", strings.Join(s.Flags, " "),
		"session", s.Session,
		"window", window,
		"saved", stamp(s.Created),
		"updated", stamp(s.Updated),
	)
	if len(env) > 0 {
		text += "\n\nenvironment\n" + strings.Join(env, "\n")
	}
	return text
}

// stamp is a stored time for a detail, "" when unset.
func stamp(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04")
}

// savedLinks are a saved pane's project, live pane and directory.
func (a *App) savedLinks(s work.SavedPane) []link {
	var links []link
	if l, ok := a.projectLink(s.ProjectID); ok {
		links = append(links, l)
	}
	if l, ok := a.paneLink(s.Window); ok {
		links = append(links, l)
	}
	if a.dirKnown(s.Cwd) {
		links = append(links, a.dirLink(a.dirKey(s.Cwd)))
	}
	return links
}

// dirKnown reports whether path can be opened on Directories: a known or
// a project directory, or any while the scan has not landed (the scan
// selects it then).
func (a *App) dirKnown(path string) bool {
	if path == "" {
		return false
	}
	key := a.dirKey(path)
	if !a.directories.Ready || a.knownRow(key) != nil {
		return true
	}
	for _, item := range a.projectDirList.Items() {
		if d, ok := item.Data.(dirRow); ok && a.sameDir(d.Path, key) {
			return true
		}
	}
	return false
}

// renderSaved draws the saved list with the selected row's detail.
func (a *App) renderSaved(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	return a.withLinkedDetail(a.detail.saved, width, height, func(w, h int) ochazuke.Block {
		return ochazuke.Text(a.view(a.savedList, w, h))
	})
}
