package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// Linked details: the Directories, Saved and Vim pages draw a detail
// beside their list about the selected row, its facts as scrollable text
// (an ochazuke Detail: J and K scroll it from the list) and under them the
// records it is tied to that have a page of their own as a links list, a
// focus stop (} or tab reaches it, j/k spill into it as on any page).
// Each link is a row of the detail-link noun; enter on one opens the page
// that shows the record with its row selected and focused: a project on
// Work, a pane on Panes, a directory on Directories, a saved pane on
// Saved, a Vim session on Vim. The links follow the selection after every
// update (syncLinks), never while drawing.

// linkNoun is the noun of every link row.
const linkNoun = "detail-link"

// The kinds of record a link opens.
const (
	linkProject   = "project"
	linkPane      = "pane"
	linkDirectory = "dir"
	linkSaved     = "saved"
	linkVim       = "vim"
)

// linkTarget is a link row's data: what it opens. ID is the project id,
// the pane's row id, the directory's path, the saved pane's or the Vim
// session's id.
type linkTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// link is one row of a detail's links: its kind word, the text after it
// and what it opens.
type link struct {
	Text string
	To   linkTarget
}

// linkedDetail is one page's detail with its links list.
type linkedDetail struct {
	*pageDetail
	links   *ochazuke.List
	linksID string
	// main is the page's list the focus returns to when the links empty
	// under it.
	main func() *ochazuke.List
	// subject reports the record the detail is about now ("" for none),
	// its text and its links.
	subject func() (id, text string, links []link)
	sig     string // the links last pushed, so an unchanged set is not pushed again
}

func (a *App) defineLinks() {
	a.r.Define(linkNoun).
		Verb("follow", a.followLink, "Open the linked record on its page, its row selected").
		Mode("normal").Bind("enter", ochazuke.Do(linkNoun+".follow"))
}

// addLinkedDetail adds page's detail (id) and its links list (id+"/links")
// beneath page, after the page's own lists, so the links are the last
// stop in drawing order. subject names what the detail shows; main is the
// list the focus returns to.
func (a *App) addLinkedDetail(id, page string, main func() *ochazuke.List, subject func() (string, string, []link)) (*linkedDetail, error) {
	ld := &linkedDetail{main: main, subject: subject, linksID: id + "/links"}
	d, err := a.addDetail(id, page, func() string { s, _, _ := ld.subject(); return s }, nil)
	if err != nil {
		return nil, err
	}
	ld.pageDetail = d
	if ld.links, err = a.r.AddList(ld.linksID, nil, ochazuke.Parent(page), ochazuke.NoMarks()); err != nil {
		return nil, err
	}
	a.r.Instances[ld.linksID].Unfocusable = true // until it has a link
	return ld, nil
}

// linkedDetails are the pages' linked details, nil before their pages exist.
func (a *App) linkedDetails() map[string]*linkedDetail {
	m := map[string]*linkedDetail{directoriesPage: a.detail.dirs, savedPage: a.detail.saved, vimPage: a.detail.vim}
	if a.inbox != nil {
		m[inboxPage] = a.inbox.detail
	}
	return m
}

// linkRow is the row id of the link to t; each page's links have their
// own ids, as a row id names one instance.
func (ld *linkedDetail) linkRow(t linkTarget) string {
	return ld.linksID + "/" + t.Kind + ":" + t.ID
}

// syncLinks pushes the current page's links when they changed; an empty
// set is no stop, and a focus left on it goes back to the page's list.
func (a *App) syncLinks() {
	ld := a.linkedDetails()[a.r.Page]
	if ld == nil {
		return
	}
	_, _, links := ld.subject()
	items := make([]ochazuke.ListItem, 0, len(links))
	var sig strings.Builder
	for _, l := range links {
		id := ld.linkRow(l.To)
		label := fmt.Sprintf("%-7s %s", l.To.Kind, l.Text)
		items = append(items, ochazuke.ListItem{ID: id, Noun: linkNoun, Label: label, Data: l.To})
		sig.WriteString(id + "\x00" + label + "\x00")
	}
	if sig.String() == ld.sig {
		return
	}
	ld.sig = sig.String()
	held := ld.links.Focused()
	if err := ld.links.SetItems(items); err != nil {
		a.r.UI().Status(err.Error())
		return
	}
	a.r.Instances[ld.linksID].Unfocusable = len(items) == 0
	if held && len(items) == 0 {
		if err := ld.main().Focus(); err != nil {
			a.r.UI().Status(err.Error())
		}
	}
}

// linkedPanel draws a linked detail in width by height: the subject's text
// (scrolled by J and K, its position on the last line while it
// overflows), then the links under a heading. No subject draws nothing.
func (a *App) linkedPanel(ld *linkedDetail, width, height int) string {
	id, text, _ := ld.subject()
	if id == "" || width < 1 || height < 1 {
		ld.hide()
		return fillLines("", height, width)
	}
	n := ld.links.Len()
	if n == 0 {
		return ld.lines(text, width, width, height)
	}
	// The links sit under the text; while both do not fit, the links take
	// at most half and the text scrolls in the rest.
	wrapped := strings.Count(ansi.Wrap(text, width, ""), "\n") + 1
	rows := min(n, max(1, (height-2)/2))
	if wrapped+2+n <= height {
		rows = n
	}
	textH := min(wrapped, height-rows-2)
	rows = min(n, height-textH-2)
	if textH < 1 || rows < 1 {
		ld.hide()
		return fillLines(a.linksBlock(ld, width, max(1, height-1)), height, width)
	}
	return fillLines(ld.lines(text, width, width, textH)+"\n\n"+a.linksBlock(ld, width, rows), height, width)
}

// linksBlock is the links heading and rows rows of the list.
func (a *App) linksBlock(ld *linkedDetail, width, rows int) string {
	s := ochazuke.NewStyles(*a.m.Appearance)
	return s.Muted.Render(ansi.Truncate(fmt.Sprintf("links (%d)", ld.links.Len()), width, "…")) + "\n" + a.view(ld.links, width, rows)
}

// withLinkedDetail lays a page out with its linked detail: beside the
// page's lists (left, 3/5 of the width) when the page is wide, under them
// when it is narrow. left draws the lists in the size it is given; its
// cursor keeps its place.
func (a *App) withLinkedDetail(ld *linkedDetail, width, height int, left func(width, height int) ochazuke.Block) ochazuke.Block {
	if width >= sideBySide {
		leftW := width * 3 / 5
		rightW := width - leftW - 2
		body := left(leftW, height)
		panel := a.linkedPanel(ld, rightW, height)
		return withText(body, joinColumns(body.Text, leftW+2, panel, height))
	}
	topH := max(3, height*3/5)
	if topH >= height {
		ld.hide()
		return left(width, height)
	}
	body := left(width, topH)
	text := fillLines(body.Text, topH, width) + "\n" + a.linkedPanel(ld, width, height-topH-1)
	return withText(body, text)
}

// withText is b with its text replaced, its cursor kept.
func withText(b ochazuke.Block, text string) ochazuke.Block {
	if b.Cursor != nil {
		return b.WithText(text)
	}
	return ochazuke.Text(text)
}

// joinColumns puts right beside left, left's lines cut and padded to
// leftW cells, height lines in all.
func joinColumns(left string, leftW int, right string, height int) string {
	l, r := strings.Split(left, "\n"), strings.Split(right, "\n")
	out := make([]string, height)
	for i := range out {
		var a, b string
		if i < len(l) {
			a = ansi.Truncate(l[i], leftW, "")
		}
		if i < len(r) {
			b = r[i]
		}
		out[i] = a + strings.Repeat(" ", max(0, leftW-ansi.StringWidth(a))) + b
	}
	return strings.Join(out, "\n")
}

// followLink is detail-link.follow (enter on a link row).
func (a *App) followLink(ctx ochazuke.Ctx) ochazuke.Result {
	t, ok := ctx.Instance.Data.(linkTarget)
	if !ok {
		return ochazuke.Failed(fmt.Errorf("%s is not a link", ctx.Instance.ID))
	}
	switch t.Kind {
	case linkProject:
		return resultError(a.jumpProject(t.ID))
	case linkPane:
		return resultError(a.jumpPane(t.ID))
	case linkDirectory:
		return resultError(a.jumpDirectory(t.ID))
	case linkSaved:
		return resultError(a.jumpRow(savedPage, a.savedList, t.ID, a.refreshSaved))
	case linkVim:
		return resultError(a.jumpRow(vimPage, a.vimList, t.ID, a.refreshVim))
	}
	return ochazuke.Failed(fmt.Errorf("unknown link kind %q", t.Kind))
}

// openPageNamed opens a page through page.open.
func (a *App) openPageNamed(page string) error {
	_, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "page", Args: ochazuke.Arguments{"id": page}})
	return err
}

// jumpProject opens Work on the project's row.
func (a *App) jumpProject(id string) error {
	if _, ok := a.project(id); !ok {
		return fmt.Errorf("no project %s", id)
	}
	if err := a.openPageNamed("work"); err != nil {
		return err
	}
	if err := a.projectList.Select(id); err != nil {
		return err
	}
	return a.r.SetFocus(id)
}

// jumpPane opens Panes on the pane's row.
func (a *App) jumpPane(row string) error {
	if err := a.openPageNamed(panesPage); err != nil {
		return err
	}
	if a.r.Instances[row] == nil {
		return fmt.Errorf("the pane list has no %s", row)
	}
	if err := a.pv.tree.Select(row); err != nil {
		return err
	}
	return a.r.SetFocus(row)
}

// jumpRow opens page on the row id of list, read again first (refresh).
func (a *App) jumpRow(page string, list *ochazuke.List, id string, refresh func() error) error {
	if err := refresh(); err != nil {
		return err
	}
	if err := a.openPageNamed(page); err != nil {
		return err
	}
	if err := list.Select(id); err != nil {
		return fmt.Errorf("%s is not on the %s page (outside the pin?)", id, page)
	}
	return a.r.SetFocus(id)
}

// jumpDirectory opens Directories on the directory's known row (the
// search cleared when it hides it), else on the project's directory row.
func (a *App) jumpDirectory(path string) error {
	key := directoryKey(path)
	// The jump chooses the row; the first visit's landing must not.
	a.dirsVisited = true
	if err := a.openPageNamed(directoriesPage); err != nil {
		return err
	}
	row := "directory:" + key
	if a.r.Instances[row] == nil && len(a.directoryQuery.Text) > 0 && a.knownRow(key) != nil {
		if err := a.directoryQuery.Replace(0, len(a.directoryQuery.Text), ""); err != nil {
			return err
		}
		a.filterDirectories()
	}
	if a.directoryList.Select(row) == nil {
		return a.r.SetFocus(row)
	}
	for _, item := range a.projectDirList.Items() {
		if d, ok := item.Data.(dirRow); ok && a.sameDir(d.Path, key) {
			if err := a.projectDirList.Select(item.ID); err != nil {
				return err
			}
			return a.r.SetFocus(item.ID)
		}
	}
	if a.directories.Loading || !a.directories.Ready {
		// The scan lands the row: it is selected then (refreshDirectories).
		a.directories.Want = key
		return nil
	}
	return fmt.Errorf("%s is not a known directory", path)
}

// knownRow is the cached known directory at key, or nil.
func (a *App) knownRow(key string) *knownDirectory {
	for i := range a.directories.Rows {
		if a.directories.Rows[i].Path == key {
			return &a.directories.Rows[i]
		}
	}
	return nil
}

// sameDir reports whether two paths name one directory.
func (a *App) sameDir(x, y string) bool {
	if x == "" || y == "" {
		return false
	}
	if filepath.Clean(x) == filepath.Clean(y) {
		return true
	}
	return a.dirKey(x) == a.dirKey(y)
}

// dirKey is directoryKey remembered: the details compare many paths every
// update, and a symlink lookup each time would touch the disk per pane.
func (a *App) dirKey(path string) string {
	if a.dirKeys == nil {
		a.dirKeys = map[string]string{}
	}
	if key, ok := a.dirKeys[path]; ok {
		return key
	}
	key := directoryKey(path)
	a.dirKeys[path] = key
	return key
}

// paneLink is the link to the tree row showing window: its own row, else
// the pane it is a side pane of. ok is false when no row shows it.
func (a *App) paneLink(window string) (link, bool) {
	if window == "" {
		return link{}, false
	}
	for _, p := range a.panes {
		if p.ID == window && a.r.Instances[p.rowID()] != nil {
			return link{Text: p.label(), To: linkTarget{linkPane, p.rowID()}}, true
		}
	}
	for _, p := range a.panes {
		if (p.SideVim == window || p.SideTerm == window) && a.r.Instances[p.rowID()] != nil {
			return link{Text: p.label() + " (side pane)", To: linkTarget{linkPane, p.rowID()}}, true
		}
	}
	return link{}, false
}

// projectLink is the link to a project's Work row, ok false for none.
func (a *App) projectLink(id string) (link, bool) {
	p, ok := a.project(id)
	if !ok {
		return link{}, false
	}
	return link{Text: projectLabel(p), To: linkTarget{linkProject, p.ID}}, true
}

// dirLink is the link to a directory's row on Directories.
func (a *App) dirLink(path string) link {
	home, _ := a.home()
	return link{Text: pathCell(path, home), To: linkTarget{linkDirectory, path}}
}

// facts are "label  value" lines, the labels padded to one width; an
// empty value drops its line.
func facts(pairs ...string) string {
	w := 0
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			w = max(w, len(pairs[i]))
		}
	}
	var lines []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			lines = append(lines, fmt.Sprintf("%-*s  %s", w, pairs[i], pairs[i+1]))
		}
	}
	return strings.Join(lines, "\n")
}
